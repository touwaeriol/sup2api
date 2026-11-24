package account

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/netguard"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usagerules"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// TestResult is returned by POST /accounts/:id/test.
type TestResult struct {
	OK              bool              `json:"ok"`
	Status          int               `json:"status"`
	LatencyMs       int64             `json:"latency_ms"`
	Message         string            `json:"message"`
	ErrorCode       string            `json:"error_code,omitempty"`
	ErrorSource     string            `json:"error_source,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	// Model actually requested: the model the plugin reported, else the
	// requested model after the account's mapping.
	Model string `json:"model,omitempty"`
	// RequestedModel is the model the caller chose, before the account's
	// mapping; empty when none was given (the plugin's default).
	RequestedModel string `json:"requested_model"`
	// Protocol is the protocol the plugin tested (its usage_protocol); empty
	// when it named none or failed to build the request.
	Protocol string `json:"protocol,omitempty"`
	// Upstream is the address the test reached, without query or fragment:
	// they may carry the credential (Gemini style "?key=...").
	Upstream string `json:"upstream,omitempty"`
	// Body is a prefix of the upstream response, on success and on failure.
	Body string `json:"body,omitempty"`
	// Usage holds the token counts the platform's usage rules found in the
	// response; nil when the plugin named no usage protocol, the rules do not
	// cover JSON responses or nothing was found.
	Usage *TestUsage `json:"usage,omitempty"`
	// Reason is the plugin's explanation of a local/transport failure.
	Reason string `json:"reason,omitempty"`
	// Effect is the account effect the plugin would apply in the gateway:
	// "cooldown", "disable" or empty. Shown to the operator only: a test
	// never changes the account (see classifyTest).
	Effect string `json:"effect,omitempty"`
	// Warnings do not fail the test but tell that client calls would still
	// fail, such as a model without a price (CONTRACTS §122.5).
	Warnings []TestWarning `json:"warnings,omitempty"`
}

// TestUsage is the token usage read from a test response. Fields follow the
// usage rule names (manifest.Usage*); cache_creation_tokens is the total
// cache write (5 minute + 1 hour entries).
type TestUsage struct {
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

const (
	// maxTestBody is read from the upstream test response, maxTestBodyOut is
	// reported back (the whole prefix is used for usage extraction).
	maxTestBody    = 8 << 10
	maxTestBodyOut = 4 << 10
	// classifyTimeout bounds the ClassifyError call of a failed test; it
	// ends with the test when the caller leaves.
	classifyTimeout = 5 * time.Second
)

func (s *Service) test(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		Model string `json:"model"`
	}
	if raw, err := c.GetRawData(); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(err.Error()))
			return
		}
	}
	a, err := s.loadIn(ctx, id, perm.AccountTest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	bt, ok := s.accountType(a.PluginKey, a.Type)
	if !ok || bt.Client == nil {
		httpapi.Fail(c, core.ErrPluginUnavailable.WithMessage(t(ctx,
			"the plugin providing this account type is not enabled", "提供该账号类型的插件未启用")))
		return
	}
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// The account's model mapping applies to test calls too (CONTRACTS §18).
	requested := strings.TrimSpace(in.Model)
	// Naming the model is what makes a test explicit (CONTRACTS §122).
	tp := s.testProtocols(a, requested != "")
	model, testable := tp.testModel(requested, a.testModels())
	if !testable {
		err := errNoTestableProtocol.WithMessage(t(ctx, "this model has no protocol to test on this account",
			"这个模型在这个账号上没有可测试的协议")).WithDetails(map[string]any{"model": model})
		s.recordTest(ctx, a.ID, TestResult{Model: model, Message: err.Message, ErrorCode: err.Code, ErrorSource: "gateway"})
		httpapi.Fail(c, err)
		return
	}
	protocols := tp.of(model)
	clientModel := model
	if model != "" {
		ref := core.AccountRef{ModelMapping: a.mapping()}
		model = ref.MapModel(model)
	}
	// The test goes out through the row's proxy; without it nothing is
	// built or sent.
	eg, err := s.proxyEgress(ctx, a.ProxyID)
	if err != nil {
		res := TestResult{Model: model, RequestedModel: requested, Message: core.AsError(err).Message, ErrorCode: core.AsError(err).Code, ErrorSource: "gateway"}
		s.recordTest(ctx, a.ID, res)
		httpapi.OK(c, res)
		return
	}
	acct := a.pluginAccount(plain, eg.Proxy)
	// A test request is not served by a client endpoint, so Account.platform
	// is empty; the declaring plugin builds a request for the account type.
	req, err := bt.Client.BuildTestRequest(ctx, &pluginv1.BuildTestRequestRequest{
		Account:   acct,
		Model:     model,
		Protocols: protocols,
	})
	if err != nil {
		s.recordTest(ctx, a.ID, TestResult{Model: model, Message: core.AsError(err).Message, ErrorCode: core.AsError(err).Code, ErrorSource: "plugin"})
		httpapi.Fail(c, err)
		return
	}
	res := s.runTest(ctx, bt, acct, eg, model, req)
	res.RequestedModel = requested
	if res.OK {
		// Without a model of its own the plugin chose one: the account has
		// no model list then, nor a mapping a client name would go through.
		res.Warnings = s.priceWarnings(ctx, a.ID, cmp.Or(clientModel, res.Model))
	}
	s.recordTest(ctx, a.ID, res)
	httpapi.OK(c, res)
}

// testProtocolSet is the protocols a test of an account may use: those of
// its own platforms it serves natively, then those it serves adapted, in
// the order of its type's platforms and their endpoints, without WebSocket and token-counting ones,
// those whose endpoint says test "never" and, unless the test is explicit
// (the request names the model), those whose endpoint says "explicit"
// (CONTRACTS §122).
type testProtocolSet struct {
	protocols []string
	rules     *core.ProtocolRules
}

// testProtocols is the set of account a; explicit when the request names
// the model.
func (s *Service) testProtocols(a *row, explicit bool) testProtocolSet {
	tp := testProtocolSet{rules: a.protocolBinding().compile()}
	g := s.gen()
	if g == nil {
		return tp
	}
	skip := map[string]bool{}
	for _, e := range g.Endpoints() {
		ep := e.Endpoint
		skip[ep.Protocol] = skip[ep.Protocol] || ep.WebSocket() || ep.Test == manifest.EndpointTestNever ||
			(ep.Test == manifest.EndpointTestExplicit && !explicit)
	}
	for _, sp := range s.accountServedProtocols(a) {
		if sp.Via != viaConverted && !skip[sp.Protocol] && core.CountTokensFamily(sp.Protocol) == "" {
			tp.protocols = append(tp.protocols, sp.Protocol)
		}
	}
	return tp
}

// of lists the protocols a test of client model model is offered, most
// preferred first (BuildTestRequestRequest.protocols): for a model, those
// the account's protocol binding serves it on, in the order of the
// model_protocols entry covering it when there is one.
func (tp testProtocolSet) of(model string) []string {
	if model == "" {
		return tp.protocols
	}
	var out []string
	for _, p := range tp.protocols {
		if _, serves := tp.rules.Serves(model, p, false); serves {
			out = append(out, p)
		}
	}
	if order := tp.rules.ModelProtocols(model); order != nil {
		slices.SortStableFunc(out, func(x, y string) int { return slices.Index(order, x) - slices.Index(order, y) })
	}
	return out
}

// testModel is the model a test asks for: the caller's, else the first of
// models left with a protocol to test, else "" when there are no models
// (the plugin picks its default, CONTRACTS §15.9). testable is false when
// that model has no protocol to test, none of models has one, or there are
// no models and the account has no protocol a test may use: the test
// is refused, never sent with the plugin's default request (CONTRACTS §122.2).
func (tp testProtocolSet) testModel(requested string, models []string) (model string, testable bool) {
	if requested != "" {
		return requested, len(tp.of(requested)) > 0
	}
	for _, m := range models {
		if len(tp.of(m)) > 0 {
			return m, true
		}
	}
	return "", len(models) == 0 && len(tp.protocols) > 0
}

// errNoTestableProtocol refuses a test of a model the account serves on no
// protocol a test may use (CONTRACTS §122.2).
var errNoTestableProtocol = core.NewError(http.StatusBadRequest, "no_testable_protocol", "no protocol to test")

// LastTest is the last console test of an account (CONTRACTS §50), shown
// as last_test in the account views.
type LastTest struct {
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	LatencyMs int64     `json:"latency_ms"`
	// Model actually requested (TestResult.model).
	Model string `json:"model"`
	// Protocol tested (TestResult.protocol); "" when unknown.
	Protocol string `json:"protocol,omitempty"`
	// Message is, on success, the warnings (§122.5), usually none; on failure
	// the plugin's reason, else the upstream message; at most
	// maxLastTestMessage bytes (lastTestMessage).
	Message     string `json:"message"`
	Status      *int   `json:"status,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	ErrorSource string `json:"error_source,omitempty"`
}

const (
	maxLastTestMessage = 512
	recordTestTimeout  = 5 * time.Second
)

// recordTest stores the outcome as the account's last test. Like the test
// itself it changes nothing the gateway uses (no updated_at, no account
// broadcast); a failed write is only logged. A test has no time limit of its
// own: it runs until the upstream answers or the caller leaves (CONTRACTS
// §15.9); one the caller ended says nothing about the account and is not
// recorded.
func (s *Service) recordTest(ctx context.Context, id int64, res TestResult) {
	if ctx.Err() != nil {
		return
	}
	msg := lastTestMessage(res)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTestTimeout)
	defer cancel()
	// updated_at stays.
	if err := store.G(wctx, s.d.DB.Gorm).Model(&model.Account{}).Where("id = ?", id).Updates(map[string]any{
		"last_test_at": gorm.Expr("now()"), "last_test_ok": res.OK, "last_test_latency_ms": min(res.LatencyMs, math.MaxInt32),
		"last_test_model": res.Model, "last_test_message": msg, "last_test_status": res.Status,
		"last_test_error_code": cutBytes(res.ErrorCode, 128), "last_test_error_source": res.ErrorSource,
		"last_test_protocol": res.Protocol,
	}).Error; err != nil {
		slog.WarnContext(ctx, "account: record last test", "account_id", id, "err", err)
	}
}

// cutBytes returns at most n bytes of s, cut at a rune boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

// runTest sends the request the plugin built through eg, the egress of the
// account's row the caller just read, and reports what came back: the
// status and latency, the model and (credential-free) address it reached, a
// prefix of the response body, the token usage the platform's rules find in
// it and, for a failure, the plugin's classification.
func (s *Service) runTest(ctx context.Context, bt core.AccountTypeBinding, acct *pluginv1.Account,
	eg core.Egress, model string, tr *pluginv1.BuildTestRequestResponse) TestResult {
	res := TestResult{Model: model, Protocol: strings.TrimSpace(tr.GetUsageProtocol()), ErrorCode: "invalid_test_request", ErrorSource: "gateway"}
	if m := strings.TrimSpace(tr.GetModel()); m != "" {
		res.Model = m
	}
	u, err := url.Parse(tr.GetUrl())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		res.Message = "plugin built an invalid test URL"
		return res
	}
	res.Upstream = upstreamAddr(u)
	if err := s.guardUpstream(ctx, bt.Plugin.Key, u, eg.Proxied); err != nil {
		res.Message, res.ErrorCode = err.Error(), "upstream_validation_failed"
		return res
	}
	method := strings.ToUpper(tr.GetMethod())
	if method == "" {
		method = http.MethodPost
	}
	var body io.Reader
	if tr.GetBodyJson() != "" {
		body = strings.NewReader(tr.GetBodyJson())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		res.Message = transportError(res.Upstream, err)
		return res
	}
	for k, v := range tr.GetHeaders() {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := eg.Client.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = transportError(res.Upstream, err)
		res.ErrorCode, res.ErrorSource = "transport_error", "transport"
		var known *core.Error
		if errors.As(err, &known) {
			res.Message, res.ErrorCode, res.ErrorSource = known.Message, known.Code, "gateway"
			if reason, ok := known.Details["reason"].(string); ok && reason != "" {
				res.ErrorCode = reason
			}
		}
		res.Reason, res.Effect, _ = s.classifyTest(ctx, bt, acct, res.Model, tr.GetUsageProtocol(), 0, nil, nil, res.Message)
		return res
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxTestBody))
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	res.Status = resp.StatusCode
	res.ResponseHeaders = map[string]string{}
	for k, values := range core.ProviderHeaders(resp.Header) {
		if len(res.ResponseHeaders) >= 32 {
			break
		}
		if len(values) > 0 && len(values[0]) <= 1024 {
			res.ResponseHeaders[strings.ToLower(k)] = values[0]
		}
	}
	res.Body = truncate(strings.TrimSpace(string(snippet)), maxTestBodyOut)
	if res.OK {
		res.ErrorCode, res.ErrorSource = "", ""
		res.Usage = s.testUsage(bt, tr.GetUsageProtocol(), snippet)
		return res
	}
	res.Message = truncate(string(snippet), 1024)
	if res.Message == "" {
		res.Message = resp.Status
	}
	// HTTP status is already reported separately. Only use an error code
	// actually supplied by the response; do not invent one for an empty body.
	res.ErrorCode, res.ErrorSource = "", "response"
	for _, path := range []string{"error.code", "error.type", "code"} {
		code := gjson.GetBytes(snippet, path)
		if (code.Type == gjson.String || code.Type == gjson.Number) && code.String() != "" {
			res.ErrorCode = cutBytes(code.String(), 128)
			break
		}
	}
	reason, effect, local := s.classifyTest(ctx, bt, acct, res.Model, tr.GetUsageProtocol(), resp.StatusCode, resp.Header, snippet, "")
	if local {
		res.ErrorSource = "gateway"
		res.Reason, res.Effect = reason, effect
	}
	return res
}

// upstreamAddr renders the address a test reached without the query and
// fragment: they may carry the credential (a Gemini style "?key=...") and
// this value is shown in the console.
func upstreamAddr(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// transportError renders a transport failure with the credential-free
// address instead of the request URL net/http puts in the message (it keeps
// the query, which may carry the key). The plugin classifying the failure
// sees the same text, since it may repeat it in its reason. A dial the
// private-address guard refused reads as netguard.ErrPrivate: its message
// names the address the proxy or upstream host resolved to, which the caller
// may not see (M3).
func transportError(addr string, err error) string {
	var ue *url.Error
	prefix := ""
	if errors.As(err, &ue) {
		prefix, err = strings.TrimSpace(ue.Op+" "+addr)+": ", ue.Err
	}
	if errors.Is(err, netguard.ErrPrivate) {
		err = netguard.ErrPrivate
	}
	return prefix + err.Error()
}

// testUsage reads the token counts of a successful test response. The plugin
// names the protocol whose usage rules describe its test response; the rules
// are resolved exactly like the gateway resolves them for a forwarded
// request (usagerules.For). Only non-streaming JSON responses are covered:
// anything unknown or unparsable yields nil, which never fails the test.
func (s *Service) testUsage(bt core.AccountTypeBinding, protocol string, body []byte) *TestUsage {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" || len(body) == 0 {
		return nil
	}
	g := s.gen()
	if g == nil {
		return nil
	}
	pb, ok := g.PlatformForProtocol(protocol)
	if !ok {
		return nil
	}
	// A zero AccountPlatform (the type does not serve this platform) simply
	// has no override, leaving the endpoint and platform rules.
	ap, _ := bt.Supports(pb.Platform.ID)
	var ep *manifest.Endpoint
	for i := range pb.Platform.Endpoints {
		if pb.Platform.Endpoints[i].Protocol == protocol {
			ep = &pb.Platform.Endpoints[i]
			break
		}
	}
	acc := usagerules.New(usagerules.For(ap, ep, &pb.Platform, protocol))
	if !acc.HasJSON() {
		return nil
	}
	acc.ApplyJSON(body)
	tk := acc.Tokens()
	if tk == (core.UsageTokens{}) {
		return nil
	}
	return &TestUsage{
		InputTokens:         tk.Input,
		OutputTokens:        tk.Output,
		CacheReadTokens:     tk.CacheRead,
		CacheCreationTokens: tk.CacheCreation + tk.CacheCreation1h,
	}
}

// classifyTest asks the plugin what a failed test means and returns the
// reason and the account effect it would have in the gateway ("cooldown",
// "disable" or empty), and whether the failure is local. protocol is the one
// the plugin tested (its usage_protocol): its failure rules apply.
//
// It deliberately applies NO side effect: a console test never puts the
// account into cooldown and never disables it (that is the gateway's
// scheduling decision, gateway.call.classify); the effect is reported for
// display only. Plugins that do not implement ClassifyError, or fail, leave
// both values empty.
func (s *Service) classifyTest(ctx context.Context, bt core.AccountTypeBinding, acct *pluginv1.Account,
	model, protocol string, status int, header http.Header, body []byte, transportErr string) (reason, effect string, local bool) {
	cctx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()
	cls, err := bt.Client.ClassifyError(cctx, &pluginv1.ClassifyErrorRequest{
		Meta:    &pluginv1.RequestMeta{Model: model, Protocol: protocol},
		Account: acct, Status: int32(status), Headers: execfetch.FirstHeaderValues(header),
		BodyPrefix: core.ClassifyPrefix(body), TransportError: transportErr,
	})
	if err != nil || cls == nil {
		slog.DebugContext(ctx, "account: test classification unavailable", "plugin", bt.Plugin.Key, "err", err)
		return "", "", false
	}
	switch cls.GetAccountEffect() {
	case pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN:
		effect = "cooldown"
	case pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_DISABLE:
		effect = "disable"
	}
	return truncate(cls.GetReason(), 512), effect, cls.GetLocalFailure()
}

func truncate(s string, n int) string { return x.TruncateEllipsis(s, n) }

// guardUpstream refuses the target of a request plugin pluginKey described
// and the core sends (test account, fetch models, quota query, refresh,
// SendAccountRequest): one the administrator's plugin network access does
// not let the plugin reach (core.CheckPluginNetwork, CONTRACTS §113), then a
// non-public host by the gateway's rule (netguard.CheckHostVia): a direct
// connection checks the resolved addresses; through a proxy, which receives
// the name, only a literal private address is refused and nothing is
// resolved here. SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM lifts the latter as
// it does for the gateway.
func (s *Service) guardUpstream(ctx context.Context, pluginKey string, u *url.URL, proxied bool) error {
	if ce := core.CheckPluginNetwork(ctx, s.d.NetworkAccess, pluginKey, u); ce != nil {
		return ce
	}
	if s.d.AllowPrivateUpstream {
		return nil
	}
	return netguard.CheckHostVia(ctx, u.Hostname(), proxied, netguard.DefaultLookup)
}
