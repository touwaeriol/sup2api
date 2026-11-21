package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/observation"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
)

// call is the state of one gateway request.
type call struct {
	g      *Gateway
	c      *gin.Context
	gen    core.Generation
	ep     manifest.Endpoint
	plugin core.PluginInfo // plugin declaring the endpoint's platform (zero for built-in platforms)
	// platform is the id of the platform owning the endpoint; pf is its
	// definition (zero when the generation no longer lists it).
	platform string
	pf       manifest.Platform
	// params are the path parameters of the matched endpoint pattern.
	params map[string]string
	format string
	rid    string
	// clientRID is the client's X-Request-Id (recorded only, CONTRACTS §11.6/§14.4).
	clientRID string
	start     time.Time

	gw            GatewaySettings
	stickyCfg     StickySettings
	autoDisable   AutoDisableSettings
	scheduling    SchedulingSettings
	failurePolicy *FailurePolicy
	failureState  failureState
	// env is the environment resolved for the current attempt's account
	// (CONTRACTS §74.3); metaFor hands it to the plugin.
	env core.Environment
	// paramOverride is what the account's param override decided for the
	// current attempt (CONTRACTS §84).
	paramOverride paramOverrideState

	principal *core.APIKeyPrincipal
	body      []byte
	model     string
	stream    bool

	promptCache map[int]string
	// prompt is the local token count started once the hooks released the
	// body (prompt_count.go); precharge joins it instead of counting on the
	// request path. nil when not started or already joined.
	prompt *promptCount
	// bodyVersion counts hook body replacements. scanned/scannedBodyVersion
	// remember the version whose referenced-model check passed, so the
	// second checkModel can reuse it; doc caches the parsed document of that
	// same version (body_state.go).
	bodyVersion        uint64
	scanned            bool
	scannedBodyVersion uint64
	doc                gjson.Result
	docValid           bool

	// scheduling: account types able to serve the endpoint protocol
	routes    map[core.AccountTypeKey]*typeRoute
	routeKeys []core.AccountTypeKey
	// conversion: the converting routes' refusals and the served route
	// (convert_policy.go)
	conversion conversionState

	// billing: the model's global price (nil on a free endpoint)
	price                *core.PriceRule
	modelRefs            []modelReference
	upstreamRefs         map[string]core.PricedUsage
	upstreamPrimaryModel string

	sticky       *stickySession
	availability map[int64]availabilityAdmission
	// availabilityOpen marks the plugins whose availability check already
	// failed open in this request: the guard does not ask them again.
	availabilityOpen map[string]bool
	// availabilityChecked / availabilityAdmitted are the precheck's
	// candidate and admitted counts, for a trace the guard starts.
	availabilityChecked  int
	availabilityAdmitted int
	// quotaPinned: a WebSocket turn, whose quota observations may only be
	// written under quotaPin, the generation the connection was made with
	// (0 = unknown, nothing is written).
	quotaPinned bool
	quotaPin    int64
	// ranked holds the per-request priority/weight/load the scheduler.rank
	// plugins asked for (CONTRACTS §24); rankDone marks the single call, so
	// failover attempts reuse the same result. nil = the accounts' own values.
	ranked   map[int64]rankValues
	rankDone bool
	// sched collects the accounts scheduling did not use and why
	// (usage_logs.anomalies.scheduling, CONTRACTS §24.6).
	sched schedTrace
	// trail is each attempt sent upstream, in order
	// (usage_logs.anomalies.attempts, CONTRACTS §98).
	trail trailState
	// balance is the core balancing's usage read for this request (balance.go),
	// read at the same moment as the ranking and reused by failover.
	balance     *balanceState
	balanceDone bool
	// session is the request's session for the limiter's SPM caps
	// (sessionIdentity): the sticky session key, else the request's own.
	session string
	rec     *core.UsageRecord

	// metaPathParams and metaQuery are the maps handed to plugins in
	// RequestMeta; built once per request because meta() runs for every hook,
	// ranker and upstream attempt. nil when the pattern has no parameters /
	// the endpoint declares no request.queryParams.
	metaPathParams map[string]string
	metaQuery      map[string]string
	metaMapsDone   bool
	// attemptHooksSkipped are the plugins whose gateway.request hook asked
	// not to run their gateway.attempt hooks for this request (§85).
	attemptHooksSkipped map[string]bool
	// sessionFP is RequestMeta.session_fingerprint, derived before the hooks
	// run (sessionFingerprint); matched is the stickyMatch answer it used.
	sessionFP string
	matched   *stickyMatched

	// modelResolved marks that ResolveModel already answered for the current
	// body (endpoints with request.modelSource "plugin" only). setBody clears
	// it, so the plugin is asked again exactly when a hook changed the body.
	modelResolved bool

	// usage is what the plugin reading an endpoint's usage (usage.source
	// "plugin", usageplugin.go) is shown of the answer: filled at the end of
	// forwarding and taken by the waited relay for its observation
	// (waited.go); nil for every other endpoint, which is how they pay
	// nothing for the feature.
	usage *pendingExtract
	// Context of the currently selected account's concurrency lease.
	slotCtx context.Context
	// queueWaited says the request already sat out a whole queue timeout, so it
	// does not queue again (docs/GATEWAY-CONCURRENCY-DESIGN.md §3).
	queueWaited bool
	// accountHold and keyHold are the holds of the account last admitted by
	// the limiter and of the API key, until their tokens settle (CONTRACTS §78).
	accountHold, keyHold *core.RateHold
	// sessionAt is when the account last admitted by the limiter counted the
	// request's session, until releaseSession takes it off (CONTRACTS §71.3).
	sessionAt    time.Time
	rateEstimate rateEstimate
	// precharged is the estimate the request was pre-charged for (nil before
	// precharge, or when the endpoint bills nothing); upstreamResponded says
	// an upstream answer started to be relayed. Together they decide what a
	// client that hangs up early is billed (settleCanceled).
	precharged        *prechargeEstimate
	upstreamResponded bool
	// What a client hang-up costs (disconnect.go): the policy once read, the
	// watcher of the current upstream exchange, whether that exchange was
	// read to its end after the client left (its usage is then complete and
	// billed as reported).
	disconnect               string
	drain                    *upstreamDrain
	drained                  bool
	task                     *core.TaskSnapshot
	taskPublicID             string
	executionOwnsUsage       bool
	statsCompletedAt         time.Time
	statsCanceled            bool
	observationAttemptAt     time.Time
	observationResponseAt    time.Time
	observationFirstStreamAt time.Time
	observationUpstreamCode  int // status the upstream answered the attempt with, 0 = none yet
	errorBodies              []*os.File
}

// serve runs the proxy pipeline (ARCHITECTURE 6.1) for one request.
func (g *Gateway) serve(c *gin.Context, gen core.Generation, b core.EndpointBinding, params map[string]string) {
	// The request id is always generated here: it is the usage_logs key and
	// the ledger idempotency key, so a client-chosen id could dodge billing.
	rid := httpapi.NewRequestID()
	clientRID := c.GetHeader("X-Request-Id")
	c.Header("X-Request-Id", rid)
	ctx := core.WithRequestID(c.Request.Context(), rid)
	c.Request = c.Request.WithContext(ctx)

	cl := &call{
		g: g, c: c, gen: gen, ep: b.Endpoint, plugin: b.Plugin, platform: b.Platform, params: params,
		format: b.Endpoint.ErrorFormat, rid: rid, clientRID: clientRequestID(clientRID), start: g.now(),
	}
	if pb, ok := gen.Platform(b.Platform); ok {
		cl.pf = pb.Platform
	}
	// A node that lost Redis/PG beyond the self-fencing window stops serving.
	if !g.healthy() {
		writeError(c, cl.format, fromCore(core.ErrUnavailable.WithMessage("node is fenced: not serving requests"), ""))
		return
	}
	set := g.settings.get(ctx)
	if set.failurePolicyErr != nil {
		writeError(c, cl.format, fromCore(core.ErrUnavailable.WithMessage("failure policy unavailable"), errTypeInternal))
		return
	}
	cl.gw, cl.stickyCfg, cl.autoDisable, cl.scheduling = set.gateway, set.sticky, set.autoDisable, set.scheduling
	if !cl.tokenCounting() {
		cl.failurePolicy = set.failurePolicy
	}
	if b.Endpoint.WebSocket() {
		g.serveWebSocket(ctx, cl)
		return
	}
	if kind := b.Endpoint.Kind; kind != "" && kind != manifest.EndpointKindProxy {
		writeError(c, cl.format, &gwError{Status: http.StatusNotImplemented, Code: "not_implemented",
			Message: "endpoint kind " + kind + " is not supported"})
		return
	}
	cl.run(ctx)
}

func (c *call) run(ctx context.Context) {
	defer c.closeErrorBodies()
	// 1. API key.
	key := c.apiKey()
	if key == "" {
		c.fail(fromCore(core.ErrUnauthenticated.WithMessage("missing api key"), ""))
		return
	}
	p, err := c.g.d.Auth.Authenticate(core.WithClientIP(ctx, c.c.ClientIP()), key)
	if err != nil {
		c.fail(fromCore(core.AsError(err), ""))
		return
	}
	c.principal = p
	c.rec = c.newRecord()
	defer c.submit()

	// 2. Body, model, stream.
	if e := c.readBody(); e != nil {
		c.fail(e)
		return
	}
	// The body's arrival is part of every later time the row shows: a large
	// body from a distant client can be most of the first-token time.
	c.rec.UploadMs = int(c.g.now().Sub(c.start) / time.Millisecond)
	if e := c.checkModel(ctx); e != nil {
		c.fail(e)
		return
	}

	// 3. The caps of the API key. They depend on the key alone, so a capped
	// request is refused before any hook runs (as new-api rate-limits before
	// moderation); a task snapshot is read locally and is not capped.
	if !c.ep.TaskQuery() && !c.tokenCounting() {
		tokens := int64(0)
		if p.KeyLimits.TPM > 0 {
			tokens = c.rateTokens(ctx)
		}
		admitted, releaseKey, hold, e := c.g.admitKey(ctx, p, c.rid, tokens)
		if e != nil {
			c.fail(e)
			return
		}
		c.keyHold = hold
		defer releaseKey()
		ctx = admitted
	}
	// A request that never reaches countTokens gives its reservations back.
	defer c.releaseHolds(ctx)

	// 4. Hooks (may deny or patch the body).
	c.sessionFP = c.sessionFingerprint(ctx)
	if e := c.runHooks(ctx); e != nil {
		c.fail(e)
		return
	}
	if e := c.checkModel(ctx); e != nil { // hooks may have patched the model
		c.fail(e)
		return
	}
	if c.ep.TaskQuery() {
		c.serveTaskSnapshot()
		return
	}
	// The body is final from here on: the local precharge estimate is the
	// slowest part of preparing a large request, so it runs while the
	// balance check waits on the database. The defer joins it on every path,
	// so no goroutine outlives the handler.
	c.startPromptCount(ctx)
	defer c.joinPromptCount()

	// Check the final model against the plugin's billing declaration before
	// route planning, account scheduling or any upstream request.
	if e := c.prepareBilling(ctx); e != nil {
		c.fail(e)
		return
	}
	// 5. Account types serving the protocol.
	c.planRoutes()
	if len(c.routeKeys) == 0 {
		c.fail(errEndpointNotServed)
		return
	}

	// 6. Schedule, forward, fail over.
	c.dispatch(ctx)
}

// apiKey reads the key from the endpoint's auth headers or query parameter.
func (c *call) apiKey() string {
	headers := c.ep.Auth.Headers
	if len(headers) == 0 && c.ep.Auth.Query == "" {
		headers = []string{"authorization"}
	}
	for _, h := range headers {
		v := strings.TrimSpace(c.c.GetHeader(h))
		if v == "" {
			continue
		}
		if strings.EqualFold(h, "authorization") {
			if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
				v = strings.TrimSpace(v[7:])
			} else {
				continue
			}
		}
		if v != "" {
			return v
		}
	}
	if q := c.ep.Auth.Query; q != "" {
		return strings.TrimSpace(c.c.Query(q))
	}
	return ""
}

func (c *call) newRecord() *core.UsageRecord {
	p := c.principal
	return &core.UsageRecord{
		RequestID:       c.rid,
		ClientRequestID: c.clientRID,
		UserID:          p.UserID,
		APIKeyID:        p.KeyID,
		GroupID:         p.Group.ID,
		SegmentID:       p.SegmentID,
		RateSource:      p.RateSource,
		PluginKey:       c.plugin.Key,
		PluginVersion:   c.plugin.Version,
		Platform:        c.platform,
		Protocol:        c.ep.Protocol,
		Endpoint:        c.ep.Path,
		RateMultiplier:  p.Group.RateMultiplier,
		ClientIP:        c.c.ClientIP(),
		UserAgent:       truncateUTF8(c.c.Request.UserAgent(), 500),
		NodeID:          c.g.nodeID(),
		CreatedAt:       c.start,
		HookDecisions:   []core.HookDecision{},
	}
}

// priceQuery asks for model's list price as the key's group and its owner's
// segment see it.
func (c *call) priceQuery(model string) core.PriceQuery {
	return core.PriceQuery{Model: model, GroupID: c.principal.Group.ID, SegmentID: c.principal.SegmentID}
}

// bodyLimit is the largest client request body (or WebSocket message): the
// request body limit of the gateway settings and nothing else.
func (c *call) bodyLimit() int64 { return c.gw.MaxRequestBodyBytes() }

func (c *call) readBody() *gwError {
	if c.c.Request.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(c.c.Writer, c.c.Request.Body, c.bodyLimit()))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return &gwError{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large",
					Message: "request body is too large", RecordType: errTypeInvalidRequest}
			}
			if c.c.Request.Context().Err() != nil {
				return &gwError{Status: statusClientClosed, Message: "client canceled", RecordType: errTypeClientCanceled}
			}
			return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
				Message: "failed to read request body", RecordType: errTypeInvalidRequest}
		}
		c.body = body
	}
	return c.checkBodyJSON()
}

// checkBodyJSON accepts an empty body or one JSON text.
//
// JSON text is UTF-8 (RFC 8259 §8.1) and gjson's validator does not check
// that, so a body with a stray byte inside a string would pass here and
// then poison every proto string it is copied into - RequestMeta.model
// from request.modelPath, the request fields handed to plugins, price
// params - and proto.Marshal refuses invalid UTF-8,
// which makes every plugin call of the request fail: one curl, a stable
// 5xx, and a usage row PostgreSQL refuses as well (CONTRACTS §25.1). It is
// not valid JSON; say so, once, here.
//
// The shape scan runs before gjson: gjson's validator recurses, and a deeply
// nested body would overflow the stack (fatal, not a recoverable panic). A
// valid body with a repeated object key is refused too (CONTRACTS §25.9), on
// every endpoint: hooks read the first copy with gjson, the upstream and the
// protocol converters read the last. The body is never rewritten.
func (c *call) checkBodyJSON() *gwError {
	if len(c.body) == 0 {
		return nil
	}
	if !utf8.Valid(c.body) {
		return errBodyNotJSON
	}
	shape := scanJSONShape(c.body)
	if shape.tooDeep {
		return jsonShapeError(shape, "request body")
	}
	if !gjson.ValidBytes(c.body) {
		return errBodyNotJSON
	}
	if e := jsonShapeError(shape, "request body"); e != nil {
		return e
	}
	return nil
}

var errBodyNotJSON = &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
	Message: "request body is not valid JSON", RecordType: errTypeInvalidRequest}

// checkModel (re)reads model and stream and applies the group allowlist.
// The model comes from the path parameter request.modelParam when declared
// (e.g. Gemini's /v1beta/models/:model:generateContent), from the body at
// request.modelPath, or from the platform plugin when the endpoint declares
// request.modelSource "plugin" (resolveModelFromPlugin).
// request.stream marks endpoints that always stream.
//
// It runs before hooks and again after them, and it must stay there: the
// model decides hook matching, the group allowlist, pricing and the `models`
// filter on the candidate accounts, so every one of those needs it already
// resolved.
func (c *call) checkModel(ctx context.Context) *gwError {
	req := c.ep.Request
	switch {
	case c.ep.TaskQuery():
		if e := c.resolveTask(ctx); e != nil {
			return e
		}
	case req.ModelSource == manifest.ModelSourcePlugin:
		if e := c.resolveModelFromPlugin(ctx); e != nil {
			return e
		}
	case req.ModelParam != "":
		c.model = strings.TrimSpace(c.params[req.ModelParam])
	case req.ModelPath != "":
		c.model = gjson.GetBytes(c.body, req.ModelPath).String()
	}
	if (req.ModelParam != "" || req.ModelPath != "") && c.model == "" {
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "model is required", RecordType: errTypeInvalidRequest}
	}
	// A model the client named (path, body or the plugin reading the body)
	// reaches account selection, plugins and the usage record: refuse what
	// no model id looks like before any of them sees it (CONTRACTS §15.2).
	if !c.ep.TaskQuery() && c.model != "" && !validModelName(c.model) {
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "model must be a model identifier without whitespace or control characters", RecordType: errTypeInvalidRequest}
	}
	// request.stream is an endpoint-level fact ("this endpoint only streams")
	// and always wins, including over a plugin: a plugin answering false would
	// have the host read an SSE response as JSON. Otherwise the plugin's
	// answer replaces request.streamPath, which such an endpoint may not
	// declare (manifest validation).
	switch {
	case req.Stream:
		c.stream = true
	case req.ModelSource == manifest.ModelSourcePlugin:
		// c.stream was set by resolveModelFromPlugin.
	case req.StreamPath != "":
		c.stream = gjson.GetBytes(c.body, req.StreamPath).Bool()
	}
	c.rec.Model = c.model
	c.rec.Stream = c.stream
	if !c.principal.Group.AllowsModel(c.model) {
		return fromCore(core.ErrModelNotFound.WithDetails(map[string]any{"model": c.model}), errTypeModelNotAllowed)
	}
	// The check below walks the whole body. Hooks replace the body only
	// through setBody, which bumps bodyVersion; an unchanged version
	// therefore left the input of the first pass identical, and repeating it
	// would only repeat the scan.
	if c.scanned && c.scannedBodyVersion == c.bodyVersion {
		return nil
	}
	if err := c.checkReferencedModels(); err != nil {
		return err
	}
	c.scanned, c.scannedBodyVersion = true, c.bodyVersion
	return nil
}

// resolveModelFromPlugin asks PlatformService.ResolveModel of the plugin
// declaring the endpoint's platform for the model and the stream flag
// (CONTRACTS §25.2). Endpoints that do not declare request.modelSource
// "plugin" never reach it, so nothing existing pays for this.
//
// Every failure is a 400 "model is required" — a timeout, a transport error,
// an UNIMPLEMENTED from a plugin that never wrote the method, an empty
// answer. Without a model the host cannot resolve a price, apply the group
// allowlist or filter the candidate accounts, so letting the request through
// would serve it for free.
//
// The one exception is a nil Client: the plugin declared the platform without
// declaring platform.adapter.v1 (legal per §13, and rejected at install time
// since B, but a package installed earlier or a built-in platform can still
// get here). That is a host-side misconfiguration, not a client mistake, so
// it is a 500 with a log naming the plugin.
func (c *call) resolveModelFromPlugin(ctx context.Context) *gwError {
	if c.modelResolved {
		return nil // same body as the last call: the answer cannot have changed
	}
	pb, ok := c.gen.Platform(c.platform)
	if !ok || pb.Client == nil {
		slog.ErrorContext(ctx, "gateway: endpoint declares modelSource \"plugin\" but its platform has no plugin client",
			"platform", c.platform, "plugin", c.plugin.Key, "endpoint", c.ep.Path, "protocol", c.ep.Protocol,
			"builtin", pb.Builtin, "found", ok)
		return fromCore(core.ErrInternal.WithMessage("the platform of this endpoint cannot resolve models"), errTypeInternal)
	}
	rctx, cancel := context.WithTimeout(ctx, c.gw.hotpathTimeout())
	resp, err := pb.Client.ResolveModel(rctx, &pluginv1.ResolveModelRequest{
		Meta: c.meta(), InboundHeaders: c.passHeaders(c.pf.PassHeaders)})
	cancel()
	if err != nil || resp.GetModel() == "" {
		slog.InfoContext(ctx, "gateway: resolve model failed", "plugin", pb.Plugin.Key,
			"platform", c.platform, "protocol", c.ep.Protocol, "err", err)
		return &gwError{Status: http.StatusBadRequest, Code: core.ErrInvalidArgument.Code,
			Message: "model is required", RecordType: errTypeInvalidRequest}
	}
	c.model, c.stream, c.modelResolved = resp.GetModel(), resp.GetStream(), true
	return nil
}

// prepareBilling resolves the model's global price (ARCHITECTURE 7.3),
// captures the price inputs and checks the balance. Free endpoints skip it.
func (c *call) prepareBilling(ctx context.Context) *gwError {
	if strings.EqualFold(c.ep.Billing, "free") {
		return nil
	}
	rule, err := c.g.d.Pricer.Resolve(ctx, c.priceQuery(c.model))
	if err != nil {
		e := core.AsError(err)
		rt := errTypePriceNotConfigured
		if e.Code != core.ErrPriceNotConfigured.Code {
			rt = errTypeInternal
		}
		return fromCore(e, rt)
	}
	kind := rule.BillingType()
	if !c.ep.SupportsBillingType(kind) {
		return &gwError{Status: http.StatusBadRequest, Code: "billing_type_not_supported",
			Message:    "endpoint " + c.ep.Protocol + " does not support " + kind + " pricing for model " + c.model,
			RecordType: errTypeInvalidRequest}
	}
	c.price = rule
	c.rec.Price = rule
	c.capturePriceInputs(c.price)
	if e := c.prepareReferencedPrices(ctx); e != nil {
		return e
	}
	if c.g.d.Balance == nil {
		return fromCore(core.AsError(errors.New("billing precharger unavailable")), errTypeInternal)
	}
	if c.principal.KeyQuota.Exhausted() {
		return fromCore(core.ErrKeyQuotaExhausted, errTypeKeyQuotaExhausted)
	}
	if err := c.g.d.Balance.CheckBalance(ctx, c.principal.UserID); err != nil {
		e := core.AsError(err)
		rt := errTypeInsufficientBalance
		if e.Code != core.ErrInsufficientBalance.Code {
			rt = errTypeInternal
		}
		return fromCore(e, rt)
	}
	if err := c.precharge(ctx); err != nil {
		e := core.AsError(err)
		kind := errTypeInternal
		if e.Code == core.ErrInsufficientBalance.Code {
			kind = errTypeInsufficientBalance
		}
		return fromCore(e, kind)
	}
	return nil
}

// capturePriceInputs records the param()/header() values the price
// expression reads, from the final (post-hook) request.
func (c *call) capturePriceInputs(rule *core.PriceRule) {
	c.rec.PriceParams, c.rec.PriceHeaders = c.priceInputs(rule)
}

func (c *call) priceInputs(rule *core.PriceRule) (params map[string]string, values map[string]string) {
	return c.priceInputsFrom(rule, c.body)
}

func (c *call) priceInputsFrom(rule *core.PriceRule, body []byte) (params map[string]string, values map[string]string) {
	if rule == nil {
		return nil, nil
	}
	paths, headers := c.g.d.Pricer.Inputs(rule)
	if len(paths) > 0 {
		params = map[string]string{}
		for _, p := range paths {
			if r := gjson.GetBytes(body, p); r.Exists() {
				params[p] = r.Raw
			}
		}
	}
	if len(headers) > 0 {
		values = map[string]string{}
		for _, h := range headers {
			if v := c.c.GetHeader(h); v != "" {
				values[headerLower(h)] = v
			}
		}
	}
	return params, values
}

// meta describes the request to plugins. Protocol is the endpoint protocol
// and Environment has no account layer; attempts override both with the
// upstream protocol and the attempt's environment (metaFor).
func (c *call) meta() *pluginv1.RequestMeta {
	if !c.metaMapsDone {
		c.metaPathParams = validMeta(c.params)
		c.metaQuery = validMeta(c.declaredQuery())
		c.metaMapsDone = true
	}
	m := &pluginv1.RequestMeta{
		RequestId: c.rid, Protocol: c.ep.Protocol, ClientProtocol: c.ep.Protocol,
		Model: c.model, Stream: c.stream, ClientIp: c.c.ClientIP(),
		PathParams: c.metaPathParams, Query: c.metaQuery,
		Endpoint: c.ep.Path, SessionFingerprint: c.sessionFP, Environment: c.environment(nil),
	}
	if p := c.principal; p != nil {
		m.UserId, m.ApiKeyId, m.GroupId = p.UserID, p.KeyID, p.Group.ID
	}
	return m
}

// declaredQuery returns the query parameters the endpoint listed in
// request.queryParams, and nothing else. The query string can carry
// credentials — the endpoint's auth.query parameter, and whatever the next
// upstream invents — so the host never hands over the whole thing and never
// tries to guess which names are secret: an allow-list is wrong in the safe
// direction, a deny-list in the unsafe one.
//
// Matching is case-insensitive in both directions, because url.Values is a
// case-sensitive map and HTTP clients are not: "?Alt=sse" must satisfy a
// declared "alt", and "?Key=sk-..." must still be excluded by auth.query
// "key". Install-time validation guarantees no declared name equals
// auth.query under EqualFold, so the exclusion can never be overridden here.
func (c *call) declaredQuery() map[string]string {
	names := c.ep.Request.QueryParams
	if len(names) == 0 || c.c.Request == nil || c.c.Request.URL == nil {
		return nil
	}
	values := c.c.Request.URL.Query()
	if len(values) == 0 {
		return nil
	}
	// Sorted, so a client sending both "?alt=" and "?Alt=" gets the same
	// answer on every node and every attempt.
	sent := make([]string, 0, len(values))
	for k := range values {
		sent = append(sent, k)
	}
	sort.Strings(sent)
	out := make(map[string]string, len(names))
	for _, name := range names {
		if auth := c.ep.Auth.Query; auth != "" && strings.EqualFold(name, auth) {
			continue
		}
		for _, k := range sent {
			vs := values[k]
			if !strings.EqualFold(k, name) || len(vs) == 0 {
				continue
			}
			// The key is the name as the client sent it; only the first value
			// of a repeated parameter travels.
			out[k] = vs[0]
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validMeta is a RequestMeta path_params or query map as the client sent
// it, every entry of any size. It returns nil for an empty map: the field
// is optional and a nil map costs no allocation on the hot path.
//
// The map comes straight off the wire, so invalid UTF-8 is dropped: proto3
// string fields must be valid UTF-8 and marshalling a RequestMeta with a
// stray byte would fail every plugin call of the request.
func validMeta(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		name := strings.ToValidUTF8(k, "")
		if name == "" {
			continue
		}
		out[name] = strings.ToValidUTF8(v, "")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// metaFor is meta for an upstream attempt on rt.
func (c *call) metaFor(rt *typeRoute) *pluginv1.RequestMeta {
	m := c.meta()
	m.Protocol, m.Environment = rt.upstream, c.env
	if c.rec != nil && c.rec.AccountID != nil {
		m.AccountId = *c.rec.AccountID
	}
	return m
}

func (c *call) passHeaders(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v := c.c.GetHeader(n); v != "" {
			out[headerLower(n)] = v
		}
	}
	return out
}

// fail renders err (if nothing was written yet) and records it.
func (c *call) fail(e *gwError) {
	if c.rec != nil {
		c.rec.Success = false
		c.rec.StatusCode = clientError(c.format, e).Status
		if e.RecordType != "" {
			c.rec.ErrorType = e.RecordType
		} else if c.rec.ErrorType == "" {
			c.rec.ErrorType = errTypeInternal
		}
		if c.rec.ErrorMessage == "" {
			c.rec.ErrorMessage = truncateUTF8(e.Message, 1000)
		}
	}
	if e.Status == statusClientClosed || c.c.Writer.Written() {
		return
	}
	writeError(c.c, c.format, e)
}

// submit finalizes the usage record and hands it to the settler. A request
// whose usage the records of a waited relay own (executionOwnsUsage) has
// none to submit.
func (c *call) submit() {
	rec := c.rec
	if rec == nil || c.g.d.Settler == nil {
		return
	}
	rec.Conversion = c.conversionTrace()
	c.concludeScheduling(c.c.Request.Context())
	rec.Scheduling = c.schedulingTrace()
	rec.AttemptTrail = c.attemptTrail()
	c.statsCompletedAt = c.g.now()
	// A client that left once it was served did not cancel anything.
	c.statsCanceled = c.c.Request.Context().Err() != nil && !rec.Success
	c.observeRequest(observation.Completed, rec)
	if c.executionOwnsUsage {
		c.observeRequest(observation.UsageFinalized, rec)
		return
	}
	rec.LatencyMs = int(c.g.now().Sub(c.start) / time.Millisecond)
	if rec.StatusCode == 0 {
		rec.StatusCode = c.c.Writer.Status()
	}
	c.finishSubmit(context.Background(), rec, c.ep.Billing)
}

func (c *call) finishSubmit(ctx context.Context, rec *core.UsageRecord, billing string) {
	c.finalizeBillability(ctx, rec, billing)
	c.observeRequest(observation.UsageFinalized, rec)
	c.g.d.Settler.Submit(rec)
}

func (c *call) finalizeBillability(ctx context.Context, rec *core.UsageRecord, billing string) {
	// completed: the upstream did the work this record is priced for - it
	// answered, or the client left while it was answering and still owes for
	// it (a per-request price is due in both cases).
	owed, waived := c.settleCanceled(ctx, rec, billing)
	hasUsage, priced := usageOf(rec, rec.Success || owed)
	rec.Billable = !strings.EqualFold(billing, "free") && !waived && priced
	if !rec.Billable {
		c.dropReservation(ctx, rec, billing, hasUsage)
		rec.Price = nil
	}
}

// usageOf says whether rec holds usage, and whether it holds a price that is
// due: one for that usage, or a per-request one (core.PriceRule.PerRequest)
// when the upstream completed the work.
func usageOf(rec *core.UsageRecord, completed bool) (hasUsage, priced bool) {
	hasUsage = rec.Tokens != (core.UsageTokens{}) || len(rec.Metrics) > 0
	priced = rec.Price != nil && (hasUsage || (completed && rec.Price.PerRequest))
	for _, item := range append(append([]core.PricedUsage(nil), rec.Additional...), rec.Replacement...) {
		itemUsage := item.Tokens != (core.UsageTokens{}) || len(item.Metrics) > 0
		hasUsage = hasUsage || itemUsage
		priced = priced || (item.Price != nil && (itemUsage || (completed && item.Price.PerRequest)))
	}
	return hasUsage, priced
}

// errReadingEmpty is why a request is unreported whose usage is the host's
// reading - the core's own, or the one a plugin chose (hostUsageChosen) -
// when that reading holds nothing to bill.
var errReadingEmpty = errors.New("the upstream answered and the host's reading of it holds no usage")

// readingEmpty is errReadingEmpty when the request was served and the host's
// reading of the answer found nothing to bill: there are no facts and the
// core makes none up, so the missing usage shows instead of passing as a
// free request. A free endpoint, a task submission (its usage comes when
// the task ends) and a per-request price have something else to say about
// the money.
func (c *call) readingEmpty() error {
	r := c.rec
	if !r.Success || strings.EqualFold(c.ep.Billing, "free") || c.ep.TaskSubmit() {
		return nil
	}
	if hasUsage, priced := usageOf(r, true); hasUsage || priced {
		return nil
	}
	return errReadingEmpty
}

// endUnreported ends as unreported, when why says so, a request served by
// account whose answer the host read: the client keeps what it was sent and
// the record is the free usage_unreported one (submit releases the
// precharge), logged at ERROR as ExecutionRecords.CloseExecutionUnreported
// does for a relay that waited. It runs when the attempt ends, before the
// account is credited with the request: an unreported request neither
// renews its sticky binding nor touches the account's last use.
func (c *call) endUnreported(ctx context.Context, account int64, why error) {
	if why == nil {
		return
	}
	c.rec.SetUnreported(why.Error())
	slog.ErrorContext(ctx, "gateway: request unreported, not billed", "request_id", c.rid, "plugin", c.rec.PluginKey,
		"account_id", account, "account_type", c.rec.AccountType, "endpoint", c.ep.Path, "protocol", c.rec.UpstreamProtocol, "reason", why.Error())
}

// settleCanceled decides what a request pays whose client hung up while the
// upstream was answering, by the client disconnect policy (CONTRACTS
// §25.8). owed says the upstream's work is to be paid for, which is what
// makes a per-request price due; waived says the policy lets this record go
// free whatever it holds.
//
// An answer read to its end already holds the real usage and is billed as it
// stands. Any other one that is not waived pays the estimate it was
// pre-charged for instead of the nothing a cut-off stream reports: the usage
// event comes last in every streaming protocol, so hanging up just before it
// would otherwise release the hold and give the output away.
//
// Not applied when the client left before any upstream answer (nothing was
// consumed), when the endpoint bills nothing, or when a plugin accounted for
// the work itself (Replacement attempts, a Reservation).
func (c *call) settleCanceled(ctx context.Context, rec *core.UsageRecord, billing string) (owed, waived bool) {
	if rec.ErrorType != errTypeClientCanceled || !c.upstreamResponded {
		return false, false
	}
	if strings.EqualFold(billing, "free") || rec.Reservation != nil || len(rec.Replacement) > 0 {
		return false, false
	}
	if c.drained {
		return true, false
	}
	if c.disconnectPolicy(ctx) == core.DisconnectFree {
		return false, true
	}
	est := c.precharged
	if est == nil {
		return false, false
	}
	rec.Tokens = canceledTokens(rec.Tokens, est.tokens)
	if len(rec.Additional) == 0 && rec.SegmentsDropped == "" {
		// Segments that could not be read stay dropped (CONTRACTS §120.4).
		rec.Additional = est.additional
	}
	if len(rec.Metrics) == 0 && len(est.metrics) > 0 {
		rec.Metrics = est.metrics
	}
	if rec.UsageSemantics == "" {
		rec.UsageSemantics = est.semantics
	}
	rec.UsageExtract = core.UsageExtractPrecharge
	return true, false
}

// canceledTokens is the usage of a request cut off mid-answer: what the
// answer had reported, completed from the pre-charge estimate.
//
// The prompt side (input and the three cache counts) is taken from one source
// only. The estimate counts the whole prompt as input, while a response
// counts the cached part of the same prompt under the cache fields, so mixing
// the two per field bills the prompt twice wherever input excludes cache. A
// response that reported any of the four reported all of them (they arrive
// together, in the first event of the protocols that send them early), so it
// wins; the estimate stands only when the response had said nothing.
//
// Output is the larger of the two: the response's count is whatever had been
// produced when the client left, the estimate is the reserve.
func canceledTokens(reported, est core.UsageTokens) core.UsageTokens {
	out := reported
	if reported.Input == 0 && reported.CacheRead == 0 && reported.CacheCreation == 0 && reported.CacheCreation1h == 0 {
		out = est
	}
	out.Output = max(reported.Output, est.Output)
	return out
}

// dropReservation clears a Reservation from a record that turned out not to
// be billable, and says so - in the log, naming the plugin and the endpoint,
// and on the record, under usage_logs.anomalies.
//
// The settler would have dropped it anyway (initialStatus reads Billable
// first), silently: no pre-charge, no pending_settlements entry, no reconcile,
// for a job the upstream really started - the shape CONTRACTS §25.5 found in
// the first plugin to reserve, where the submit endpoint had been marked
// billing "free" because "the submit itself has no final usage". The manifest
// check now refuses that combination; this is what stands for packages
// installed before it did, and for the other way here: an estimate with no
// tokens and no facts on a per-token price, which is a plugin bug the plugin
// author has to hear about.
func (c *call) dropReservation(ctx context.Context, rec *core.UsageRecord, billing string, hasUsage bool) {
	rv := rec.Reservation
	if rv == nil {
		return
	}
	var why string
	switch {
	case strings.EqualFold(billing, "free"):
		why = "endpoint billing is \"free\""
	case !hasUsage:
		why = "the estimate has no tokens and no facts, and the price is not per-request"
	default:
		why = "record is not billable"
	}
	rec.Reservation = nil
	rec.ReservationDropped = why
	slog.WarnContext(ctx, "gateway: plugin reserved a pre-charge the request cannot carry; the reservation is DROPPED - "+
		"nothing is charged now and nothing will be reconciled",
		"request_id", c.rid, "plugin", rv.PluginKey, "platform", c.platform, "endpoint", c.ep.Path,
		"protocol", c.ep.Protocol, "ref_id", rv.RefID, "billing", billing, "reason", why)
}

// countTokens adds this request's tokens to the rate-limit windows of the
// account and of the API key.
func (c *call) countTokens(ctx context.Context, accountID int64) {
	n := c.rec.Tokens.Throughput(c.rec.UsageSemantics)
	if len(c.rec.Replacement) > 0 {
		n = 0
		for _, item := range c.rec.Replacement {
			n += item.Tokens.Throughput(item.UsageSemantics)
		}
	}
	for _, item := range c.rec.Additional {
		n += item.Tokens.Throughput(item.UsageSemantics)
	}
	// A stream cut before its usage event: count the prompt, so that hanging
	// up does not escape the caps that made this estimate necessary. A request
	// that succeeded without usage (count_tokens) used nothing to count.
	if n <= 0 && !c.rec.Success {
		n = c.rateEstimate.prompt
	}
	ctx = context.WithoutCancel(ctx)
	if lim := c.g.d.Limiter; lim != nil && accountID != 0 {
		if h := c.accountHold; h != nil && h.Subject == accountID {
			c.accountHold = nil
			lim.Settle(ctx, *h, n)
		} else if n > 0 {
			lim.AddTokens(ctx, accountID, "", n)
		}
		lim.CountModelTokens(ctx, accountID, c.model, n)
	}
	if w := c.g.d.KeyWindows; w != nil && c.principal != nil {
		if h := c.keyHold; h != nil {
			c.keyHold = nil
			w.Settle(ctx, *h, n)
		} else if n > 0 {
			w.AddTokens(ctx, c.principal.KeyID, n)
		}
	}
}

// releaseAccountHold gives back what the account's hold still reserves: its
// attempt ended with no tokens to count. The request stays counted in RPM.
func (c *call) releaseAccountHold(ctx context.Context) {
	if h := c.accountHold; h != nil {
		c.accountHold = nil
		if lim := c.g.d.Limiter; lim != nil {
			lim.Settle(context.WithoutCancel(ctx), *h, 0)
		}
	}
}

// releaseHolds gives back what the request still reserves.
func (c *call) releaseHolds(ctx context.Context) {
	c.releaseAccountHold(ctx)
	if h := c.keyHold; h != nil {
		c.keyHold = nil
		if w := c.g.d.KeyWindows; w != nil {
			w.Settle(context.WithoutCancel(ctx), *h, 0)
		}
	}
}
