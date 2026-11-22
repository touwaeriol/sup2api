package gateway

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
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
	if e := cl.loadSettings(ctx); e != nil {
		writeError(c, cl.format, e)
		return
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

// loadSettings gives the call the gateway settings it runs under. A node
// that lost Redis/PG beyond the self-fencing window stops serving.
func (c *call) loadSettings(ctx context.Context) *gwError {
	if !c.g.healthy() {
		return fromCore(core.ErrUnavailable.WithMessage("node is fenced: not serving requests"), "")
	}
	set := c.g.settings.get(ctx)
	if set.failurePolicyErr != nil {
		return fromCore(core.ErrUnavailable.WithMessage("failure policy unavailable"), errTypeInternal)
	}
	c.gw, c.stickyCfg, c.autoDisable, c.scheduling = set.gateway, set.sticky, set.autoDisable, set.scheduling
	if !c.tokenCounting() {
		c.failurePolicy = set.failurePolicy
	}
	return nil
}

func (c *call) run(ctx context.Context) {
	defer c.closeErrorBodies()
	// 1. API key.
	if e := c.authenticate(ctx); e != nil {
		c.fail(e)
		return
	}
	defer c.submit()
	// 2. Body, model, stream.
	if e := c.readRequest(ctx); e != nil {
		c.fail(e)
		return
	}
	c.serveRequest(ctx)
}

// serveRequest serves a read request of an authenticated key: within the
// key's caps, after the hooks.
func (c *call) serveRequest(ctx context.Context) {
	ctx, releaseKey, e := c.admitCaps(ctx) // 3. The caps of the API key.
	if e != nil {
		c.fail(e)
		return
	}
	defer releaseKey()
	// A request that never reaches countTokens gives its reservations back.
	defer c.releaseHolds(ctx)
	// 4. Hooks (may deny or patch the body).
	if e := c.applyHooks(ctx); e != nil {
		c.fail(e)
		return
	}
	if c.ep.TaskQuery() {
		c.serveTaskSnapshot()
		return
	}
	c.billAndDispatch(ctx)
}

// authenticate reads and authenticates the API key, and starts the usage
// record of its principal.
func (c *call) authenticate(ctx context.Context) *gwError {
	key := c.apiKey()
	if key == "" {
		return fromCore(core.ErrUnauthenticated.WithMessage("missing api key"), "")
	}
	p, err := c.g.d.Auth.Authenticate(core.WithClientIP(ctx, c.c.ClientIP()), key)
	if err != nil {
		return fromCore(core.AsError(err), "")
	}
	c.principal = p
	c.rec = c.newRecord()
	return nil
}

// readRequest reads the body and checks its model.
func (c *call) readRequest(ctx context.Context) *gwError {
	if e := c.readBody(); e != nil {
		return e
	}
	// The body's arrival is part of every later time the row shows: a large
	// body from a distant client can be most of the first-token time.
	c.rec.UploadMs = int(c.g.now().Sub(c.start) / time.Millisecond)
	return c.checkModel(ctx)
}

// admitCaps admits the request under the caps of its API key and returns
// the release of what it holds. They depend on the key alone, so a capped
// request is refused before any hook runs (as new-api rate-limits before
// moderation); a task snapshot is read locally and is not capped.
func (c *call) admitCaps(ctx context.Context) (context.Context, func(), *gwError) {
	if c.ep.TaskQuery() || c.tokenCounting() {
		return ctx, func() {}, nil
	}
	tokens := int64(0)
	if c.principal.KeyLimits.TPM > 0 {
		tokens = c.rateTokens(ctx)
	}
	admitted, releaseKey, hold, e := c.g.admitKey(ctx, c.principal, c.rid, tokens)
	if e != nil {
		return ctx, nil, e
	}
	c.keyHold = hold
	return admitted, releaseKey, nil
}

// applyHooks runs the hooks, which may deny the request or patch its body,
// and checks the model again: hooks may have patched it.
func (c *call) applyHooks(ctx context.Context) *gwError {
	c.sessionFP = c.sessionFingerprint(ctx)
	if e := c.runHooks(ctx); e != nil {
		return e
	}
	return c.checkModel(ctx)
}

// billAndDispatch prepares billing of the final model, plans the account types
// serving the protocol and dispatches.
func (c *call) billAndDispatch(ctx context.Context) {
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

// newRecord starts the usage record at the rate of the key's group, the
// model aside: prepareBilling sets the model's.
func (c *call) newRecord() *core.UsageRecord {
	p := c.principal
	rate, source := p.Rates().Rate()
	return &core.UsageRecord{
		RequestID:       c.rid,
		ClientRequestID: c.clientRID,
		UserID:          p.UserID,
		APIKeyID:        p.KeyID,
		GroupID:         p.Group.ID,
		SegmentID:       p.SegmentID,
		RateSource:      source,
		PluginKey:       c.plugin.Key,
		PluginVersion:   c.plugin.Version,
		Platform:        c.platform,
		Protocol:        c.ep.Protocol,
		Endpoint:        c.ep.Path,
		RateMultiplier:  rate,
		ClientIP:        c.c.ClientIP(),
		UserAgent:       truncateUTF8(c.c.Request.UserAgent(), 500),
		NodeID:          c.g.nodeID(),
		CreatedAt:       c.start,
		HookDecisions:   []core.HookDecision{},
	}
}
