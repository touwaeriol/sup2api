package usage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Pre-charged usage and the reconcile loop (CONTRACTS §25.4).

// ---------------------------------------------------------------- doubles

// recPlugin is the PlatformService the loop talks to. Its Monitor fetches
// the upstream double once and answers what parse makes of it (PENDING when
// parse is unset). asks counts the Monitor calls, parses the answers it
// gave after a fetch, which is how "the plugin was never asked" and "only
// one node asked" are asserted.
type recPlugin struct {
	url    string
	asks   atomic.Int64
	parses atomic.Int64
	parse  func(*monitorCall) (*pluginv1.ReconcileResult, error)
	// lastAccount records what the plugin was handed, for the credential
	// boundary assertions.
	mu          sync.Mutex
	lastAccount *pluginv1.Account
	lastEntry   *pluginv1.ReconcileEntry
}

// monitorCall is what recPlugin's Monitor saw: the call and its one fetch.
type monitorCall struct {
	Entry   *pluginv1.ReconcileEntry
	Account *pluginv1.Account
	Fetch   *pluginv1.ExecutionHTTPResponse
}

func (c *monitorCall) GetEntry() *pluginv1.ReconcileEntry { return c.Entry }

func (p *recPlugin) Monitor(ctx context.Context, in *pluginv1.PollRequest, fetch core.ExecutionHTTP) (*pluginv1.ReconcileResult, error) {
	p.asks.Add(1)
	p.mu.Lock()
	p.lastAccount, p.lastEntry = in.GetAccount(), in.GetEntry()
	p.mu.Unlock()
	got, err := fetch(ctx, &pluginv1.ExecutionHTTPRequest{Method: "GET", Url: p.url})
	if err != nil {
		return nil, err
	}
	p.parses.Add(1)
	res := &pluginv1.ReconcileResult{}
	if p.parse != nil {
		if res, err = p.parse(&monitorCall{Entry: in.GetEntry(), Account: in.GetAccount(), Fetch: got}); err != nil {
			return nil, err
		}
	}
	return checkedResult(res, got)
}

// checkedResult is the runtime's rule on a Monitor result (W5): a terminal
// state needs a complete, successful fetch of the call.
func checkedResult(res *pluginv1.ReconcileResult, fetch *pluginv1.ExecutionHTTPResponse) (*pluginv1.ReconcileResult, error) {
	state := res.GetState()
	complete := fetch.GetTransportError() == "" && fetch.GetStatus() >= 200 && fetch.GetStatus() < 300
	if res != nil && state != pluginv1.ReconcileResult_PENDING && state != pluginv1.ReconcileResult_POLL_FAILED && !complete {
		return nil, errors.New("a terminal result needs a complete fetch")
	}
	return res, nil
}

func (p *recPlugin) ValidateCredentials(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) {
	return nil, nil
}

func (p *recPlugin) ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return nil, nil
}
func (p *recPlugin) BuildTestRequest(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) {
	return nil, nil
}
func (p *recPlugin) BuildModelsRequest(context.Context, *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) {
	return nil, nil
}
func (p *recPlugin) ResolveModel(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error) {
	return nil, nil
}
func (p *recPlugin) BuildQuotaRequest(context.Context, *pluginv1.BuildQuotaRequestRequest) (*pluginv1.BuildQuotaRequestResponse, error) {
	return nil, nil
}

func (p *recPlugin) ParseQuotaResponse(context.Context, *pluginv1.ParseQuotaResponseRequest) (*pluginv1.QuotaResult, error) {
	return nil, nil
}

func (p *recPlugin) BuildBalanceRequest(context.Context, *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	return nil, nil
}

func (p *recPlugin) ParseBalanceResponse(context.Context, *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	return nil, nil
}

// recGen is a generation holding exactly one plugin platform.
type recGen struct {
	info   core.PluginInfo
	pf     manifest.Platform
	client core.PlatformPlugin
}

func (g *recGen) Number() uint64             { return 1 }
func (g *recGen) Plugins() []core.PluginInfo { return []core.PluginInfo{g.info} }
func (g *recGen) Plugin(key string) (core.PluginInfo, bool) {
	return g.info, key == g.info.Key
}
func (g *recGen) Endpoints() []core.EndpointBinding {
	var out []core.EndpointBinding
	for _, e := range g.pf.Endpoints {
		out = append(out, core.EndpointBinding{Plugin: g.info, Platform: g.pf.ID, Endpoint: e})
	}
	return out
}
func (g *recGen) Platforms() []core.PlatformBinding {
	return []core.PlatformBinding{{Plugin: g.info, Platform: g.pf, Client: g.client}}
}
func (g *recGen) Platform(id string) (core.PlatformBinding, bool) {
	if id != g.pf.ID {
		return core.PlatformBinding{}, false
	}
	return g.Platforms()[0], true
}
func (g *recGen) PlatformForProtocol(protocol string) (core.PlatformBinding, bool) {
	for _, e := range g.pf.Endpoints {
		if e.Protocol == protocol {
			return g.Platforms()[0], true
		}
	}
	return core.PlatformBinding{}, false
}
func (g *recGen) AccountTypes() []core.AccountTypeBinding { return nil }
func (g *recGen) AccountType(string, string) (core.AccountTypeBinding, bool) {
	return core.AccountTypeBinding{}, false
}
func (g *recGen) AccountTypesForPlatform(string) []core.AccountTypeBinding { return nil }
func (g *recGen) Hooks(string) []core.HookBinding                          { return nil }
func (g *recGen) Scheduler(string) (core.SchedulerPlugin, bool)            { return nil, false }
func (g *recGen) AccountRankers() []core.AccountRankerBinding              { return nil }
func (g *recGen) Routes(string) []core.RouteBinding                        { return nil }
func (g *recGen) Jobs() []core.JobBinding                                  { return nil }
func (g *recGen) JobRunner(string) (core.AppPlugin, bool)                  { return nil, false }
func (g *recGen) Subscriptions() []core.SubscriptionBinding                { return nil }
func (g *recGen) ReadAsset(string, string) ([]byte, string, error)         { return nil, "", nil }

type recRegistry struct{ gen core.Generation }

func (r recRegistry) Current() core.Generation              { return r.gen }
func (r recRegistry) OnChange(func(core.Generation)) func() { return func() {} }

// recAccounts hands out one account; its egress goes through proxies
// (recProxies when nil).
type recAccounts struct {
	acc     *core.Account
	proxies core.ProxyDirectory
}

func (a recAccounts) Candidates(context.Context, int64, []core.AccountTypeKey) ([]core.AccountRef, error) {
	return nil, nil
}
func (a recAccounts) GroupHasType(context.Context, int64, []core.AccountTypeKey) (bool, error) {
	return a.acc != nil, nil
}
func (a recAccounts) Resting(context.Context, int64, []core.AccountTypeKey) ([]core.AccountRef, error) {
	return nil, nil
}
func (a recAccounts) Unschedulable(context.Context, int64) ([]core.UnschedulableAccount, error) {
	return nil, nil
}
func (a recAccounts) Load(_ context.Context, id int64) (*core.Account, error) {
	if a.acc == nil || a.acc.ID != id {
		return nil, core.ErrNotFound
	}
	return a.acc, nil
}
func (a recAccounts) Egress(ctx context.Context, id int64) (core.Egress, error) {
	acc, err := a.Load(ctx, id)
	if err != nil {
		return core.Egress{}, err
	}
	p := a.proxies
	if p == nil {
		p = recProxies{}
	}
	pc, err := p.HTTPClient(ctx, acc.ProxyID)
	return core.Egress{Client: pc.Client, Proxied: acc.ProxyID != nil, Proxy: pc.Proxy}, err
}
func (a recAccounts) IsCoolingDown(context.Context, int64) (bool, error)          { return false, nil }
func (a recAccounts) SetCooldown(context.Context, int64, time.Time, string) error { return nil }
func (a recAccounts) AutoDisable(context.Context, int64, string) (bool, error)    { return true, nil }
func (a recAccounts) AutoDisableByPolicy(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (a recAccounts) AutoDisableUntil(context.Context, int64, string, time.Time) (bool, error) {
	return true, nil
}
func (a recAccounts) TouchLastUsed(context.Context, int64) {}

type recProxies struct{}

func (recProxies) HTTPClient(_ context.Context, id *int64) (core.ProxyClient, error) {
	pc := core.ProxyClient{Client: &http.Client{Timeout: 5 * time.Second}}
	if id != nil {
		pc.Proxy = &core.ProxyRef{ID: *id, ProxySpec: core.ProxySpec{Protocol: "http", Host: "proxy.test", Port: 3128, Username: "u", Password: "pw"}}
	}
	return pc, nil
}
func (recProxies) HTTPClientFor(context.Context, core.ProxySpec) (*http.Client, error) {
	return &http.Client{Timeout: 5 * time.Second}, nil
}

// ---------------------------------------------------------------- fixture

type recFixture struct {
	*fixture
	plugin *recPlugin
	up     *httptest.Server
	hits   atomic.Int64
}

// reconcileFixture adds a plugin "vid" declaring platform "vid" with one
// endpoint whose usage a plugin reports, an upstream that answers the status
// query, and the reconcile loop wired to all of it.
func reconcileFixture(t *testing.T) *recFixture {
	t.Helper()
	return reconcileFixtureWith(t, &core.Account{
		AccountRef:  core.AccountRef{ID: 7, Name: "vid-7", PluginKey: "vid", Type: "vid_key"},
		Credentials: json.RawMessage(`{"api_key":"sk-secret"}`),
		Settings:    json.RawMessage(`{"base_url":"https://example.invalid"}`),
	})
}

// reconcileFixtureWith is reconcileFixture with a chosen account, so the
// credential boundary can be tested with an account of another plugin's type.
func reconcileFixtureWith(t *testing.T, acc *core.Account) *recFixture {
	t.Helper()
	f := newFixture(t)
	rf := &recFixture{fixture: f}
	rf.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rf.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"succeeded","usage":{"in":2000,"out":200}}`))
	}))
	t.Cleanup(rf.up.Close)
	rf.plugin = &recPlugin{url: rf.up.URL}
	f.svc.StartReconcile(context.Background(), ReconcileDeps{
		Registry:             recRegistry{gen: reconcileGen(rf.plugin)},
		Accounts:             recAccounts{acc: acc},
		AllowPrivateUpstream: true, // the upstream double listens on 127.0.0.1
		NodeID:               "node-a",
	})
	return rf
}

func reconcileGen(p core.PlatformPlugin) core.Generation {
	return &recGen{
		info: core.PluginInfo{Key: "vid", Version: "0.1.0"},
		pf: manifest.Platform{ID: "vid", Usage: manifest.UsageRules{
			Semantics: "exclusive",
			Facts:     map[string]manifest.UsageFact{"images": {Type: "number"}},
		}, Endpoints: []manifest.Endpoint{{
			ID: "gen", Method: "POST", Path: "/vid/v1/gen", Protocol: "vid.gen", Kind: "proxy",
			UsageSource: manifest.UsageSourcePlugin,
		}}},
		client: p,
	}
}

// reserved builds a pre-charged record: the plugin's estimate, plus the
// reservation naming the work it started.
func (f *fixture) reserved(id, refID string, tokens core.UsageTokens) *core.UsageRecord {
	rec := f.record(id, true)
	rec.PluginKey, rec.Platform, rec.Protocol = "vid", "vid", "vid.gen"
	rec.UpstreamProtocol, rec.AccountType = "vid.gen", "vid_key"
	rec.Tokens = tokens
	acct := int64(7)
	rec.AccountID = &acct
	rec.UsageExtract = core.UsageExtractPlugin
	rec.Reservation = &core.UsageReservation{PluginKey: "vid", RefID: refID, NextCheckAfter: 0, Deadline: time.Hour}
	return rec
}

// due makes every pending entry due right now, which is the only thing
// waiting would have done: the first check is always at least
// minReconcileDelay out, by design.
func (f *fixture) due() {
	f.t.Helper()
	if _, err := f.db.SQL.ExecContext(context.Background(),
		`UPDATE pending_settlements SET next_check_at = now() WHERE state = 'pending'`); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) cost(requestID string) decimal.Decimal {
	var d decimal.Decimal
	if err := f.db.SQL.QueryRowContext(context.Background(),
		`SELECT total_cost FROM usage_logs WHERE request_id = $1`, requestID).Scan(&d); err != nil {
		f.t.Fatal(err)
	}
	return d
}

// ---------------------------------------------------------------- submit

// Pre-charging is the hole the pre-request balance check cannot close: it is
// a cached read that tolerates an overdraft, so a user with nothing left can
// start any number of jobs that each settle an hour later. The estimate is
// charged now, through the ordinary ledger path.
func TestReservationChargesUpFrontAndRegistersTheEntry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.balance()

	rec := f.reserved("req-res", "task-1", core.UsageTokens{Input: 1000, Output: 100})
	f.svc.process(ctx, []*core.UsageRecord{rec})

	charged := f.cost("req-res")
	if charged.Sign() <= 0 {
		t.Fatalf("nothing was charged: %s", charged)
	}
	if got := before.Sub(f.balance()); !got.Equal(charged) {
		t.Fatalf("balance moved by %s, the row says %s", got, charged)
	}
	if s := f.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-res'`); s != StatusReserved {
		t.Fatalf("billing_status = %s", s)
	}
	// The ordinary key: a reservation and a settlement of the same request
	// can never both take the base amount.
	if s := f.scalar(`SELECT kind FROM balance_ledger WHERE idempotency_key = 'usage:req-res'`); s != "usage" {
		t.Fatalf("ledger kind = %s", s)
	}
	row := f.scalar(`SELECT p.plugin_key || ' ' || p.ref_id || ' ' || p.state || ' ' || p.attempts::text
		FROM pending_settlements p JOIN usage_logs u ON u.id = p.usage_log_id WHERE u.request_id = 'req-res'`)
	if row != "vid task-1 pending 0" {
		t.Fatalf("pending_settlements row = %q", row)
	}
	// A reserved row is outside usage_logs_billing_pending_idx, so the
	// settlement retry loop cannot find it and charge it a second time.
	n, err := f.svc.RetryPending(ctx)
	if err != nil || n != 0 {
		t.Fatalf("retry loop saw %d reserved rows (err %v)", n, err)
	}
}

// A plugin that reports usage without reserving must behave exactly as it did
// before this feature existed - and the loop must never hear about it.
func TestNoReservationBehavesAsBefore(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()

	rec := rf.record("req-plain", true)
	rec.UsageExtract = core.UsageExtractPlugin
	rf.svc.process(ctx, []*core.UsageRecord{rec})

	if s := rf.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-plain'`); s != StatusBilled {
		t.Fatalf("billing_status = %s", s)
	}
	if n := rf.scalar(`SELECT count(*) FROM pending_settlements`); n != "0" {
		t.Fatalf("pending_settlements rows = %s", n)
	}
	if got := rf.svc.ReconcileDue(ctx); got != 0 {
		t.Fatalf("the loop claimed %d entries", got)
	}
	if rf.plugin.asks.Load() != 0 || rf.plugin.parses.Load() != 0 {
		t.Fatalf("the plugin was asked to reconcile: %d asks, %d parses",
			rf.plugin.asks.Load(), rf.plugin.parses.Load())
	}
}

// ---------------------------------------------------------------- outcomes

// SETTLED with more usage than estimated tops the charge up; with less, the
// difference comes back. Each direction has its own idempotency key, and
// neither is the reservation's.
func TestReconcileSettledAdjustsTheDifference(t *testing.T) {
	for _, tc := range []struct {
		name           string
		estimate, real core.UsageTokens
		key            string
	}{
		{"real usage above the estimate", core.UsageTokens{Input: 1000, Output: 100},
			core.UsageTokens{Input: 4000, Output: 400}, "usage:req-set:reconcile"},
		{"real usage below the estimate", core.UsageTokens{Input: 4000, Output: 400},
			core.UsageTokens{Input: 1000, Output: 100}, "refund:req-set:reconcile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rf := reconcileFixture(t)
			ctx := context.Background()
			rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-set", "task-set", tc.estimate)})
			reserved := rf.cost("req-set")
			afterReserve := rf.balance()

			real := tc.real
			rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
				return &pluginv1.ReconcileResult{
					State: pluginv1.ReconcileResult_SETTLED,
					Tokens: &pluginv1.UsageTokens{
						InputTokens: real.Input, OutputTokens: real.Output},
					Facts: map[string]string{"images": "2"},
				}, nil
			}
			rf.due()
			if n := rf.svc.ReconcileDue(ctx); n != 1 {
				t.Fatalf("claimed %d entries", n)
			}
			if rf.plugin.asks.Load() != 1 || rf.plugin.parses.Load() != 1 || rf.hits.Load() != 1 {
				t.Fatalf("asks=%d parses=%d upstream=%d", rf.plugin.asks.Load(), rf.plugin.parses.Load(), rf.hits.Load())
			}
			final := rf.cost("req-set")
			if s := rf.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-set'`); s != StatusBilled {
				t.Fatalf("billing_status = %s", s)
			}
			// The row now carries the real usage, not the estimate.
			if s := rf.scalar(`SELECT input_tokens::text || '/' || output_tokens::text FROM usage_logs WHERE request_id = 'req-set'`); s != itoa(real.Input)+"/"+itoa(real.Output) {
				t.Fatalf("tokens = %s", s)
			}
			if s := rf.scalar(`SELECT metrics->>'images' FROM usage_logs WHERE request_id = 'req-set'`); s != "2" {
				t.Fatalf("metrics = %s", s)
			}
			// The balance moved by exactly the difference, and by a ledger
			// row keyed apart from the reservation's.
			want := reserved.Sub(final)
			if got := rf.balance().Sub(afterReserve); !got.Equal(want) {
				t.Fatalf("balance moved %s, want %s (reserved %s, final %s)", got, want, reserved, final)
			}
			if s := rf.scalar(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = $1`, tc.key); s != "1" {
				t.Fatalf("no ledger row for %s", tc.key)
			}
			if s := rf.scalar(`SELECT state FROM pending_settlements`); s != SettleStateSettled {
				t.Fatalf("entry state = %s", s)
			}
		})
	}
}

// FAILED means the upstream produced nothing, so the whole reservation goes
// back and the row records the failure instead of a cost.
func TestReconcileFailedRefundsEverything(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	before := rf.balance()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-fail", "task-fail", core.UsageTokens{Input: 3000, Output: 300})})
	if rf.balance().Equal(before) {
		t.Fatal("the reservation was not charged")
	}
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED, Reason: "content_filtered"}, nil
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)

	if got := rf.balance(); !got.Equal(before) {
		t.Fatalf("balance %s, want the whole reservation back at %s", got, before)
	}
	s := rf.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || error_type || ' ' || success::text
		FROM usage_logs WHERE request_id = 'req-fail'`)
	if s != "free 0.00000000 content_filtered false" {
		t.Fatalf("row = %q", s)
	}
	if s := rf.scalar(`SELECT state FROM pending_settlements`); s != SettleStateFailed {
		t.Fatalf("entry state = %s", s)
	}
}

// PENDING reschedules and counts the attempt; the plugin's own delay is what
// decides when, because it is the only party that knows the upstream.
func TestReconcilePendingReschedules(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-pend", "task-pend", core.UsageTokens{Input: 10, Output: 1})})
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{NextCheckAfterSec: 600}, nil
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)

	if s := rf.scalar(`SELECT state || ' ' || attempts::text FROM pending_settlements`); s != "pending 1" {
		t.Fatalf("entry = %q", s)
	}
	if s := rf.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-pend'`); s != StatusReserved {
		t.Fatalf("billing_status = %s", s)
	}
	// ~10 minutes out, so the next sweep leaves it alone.
	if s := rf.scalar(`SELECT (next_check_at > now() + interval '9 minutes')::text FROM pending_settlements`); s != "true" {
		t.Fatalf("next_check_at was not honoured: %s", s)
	}
	if n := rf.svc.ReconcileDue(ctx); n != 0 {
		t.Fatalf("a rescheduled entry was claimed again: %d", n)
	}
}

// ---------------------------------------------------------------- settled at the estimate

// SETTLED_ESTIMATE (CONTRACTS §25.5 gap 2): the upstream confirms the work
// but reports no usage. The estimate stands as the charge - the same ledger
// outcome as abandon - and the row says it was THIS reason and not that one:
// an operator reading "abandoned" would look for an upstream that never
// answered, and there was none.
func TestReconcileSettledEstimateKeepsTheChargeAndSaysWhy(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	before := rf.balance()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-est", "task-est", core.UsageTokens{Input: 2000, Output: 200})})
	charged := rf.cost("req-est")
	afterReserve := rf.balance()
	if charged.Sign() <= 0 || afterReserve.Equal(before) {
		t.Fatalf("the reservation was not charged: %s", charged)
	}

	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{
			State:  pluginv1.ReconcileResult_SETTLED_ESTIMATE,
			Reason: "status document carries no usage",
			// Sent by mistake: ignored, and the estimate still stands.
			Tokens: &pluginv1.UsageTokens{InputTokens: 1},
		}, nil
	}
	rf.due()
	if n := rf.svc.ReconcileDue(ctx); n != 1 {
		t.Fatalf("claimed %d entries", n)
	}
	if rf.plugin.asks.Load() != 1 || rf.plugin.parses.Load() != 1 {
		t.Fatalf("asks=%d parses=%d", rf.plugin.asks.Load(), rf.plugin.parses.Load())
	}
	// 1. Billed, at the estimate, with the estimate's tokens - not repriced
	//    at the zero a SETTLED answer would have carried, not refunded.
	if s := rf.scalar(`SELECT billing_status || ' ' || input_tokens::text || '/' || output_tokens::text || ' ' || success::text
		FROM usage_logs WHERE request_id = 'req-est'`); s != "billed 2000/200 true" {
		t.Fatalf("row = %q", s)
	}
	if got := rf.cost("req-est"); !got.Equal(charged) {
		t.Fatalf("cost changed from %s to %s", charged, got)
	}
	if got := rf.balance(); !got.Equal(afterReserve) {
		t.Fatalf("balance changed from %s to %s", afterReserve, got)
	}
	if s := rf.scalar(`SELECT count(*) FROM balance_ledger WHERE ref_id = 'req-est'`); s != "1" {
		t.Fatalf("ledger rows for the request = %s, want only the reservation", s)
	}
	// 2. The row says which of the two reasons kept the estimate.
	an := rf.scalar(`SELECT anomalies::text FROM usage_logs WHERE request_id = 'req-est'`)
	var m map[string]string
	if err := json.Unmarshal([]byte(an), &m); err != nil {
		t.Fatal(err)
	}
	if m[core.AnomalyReconcile] != core.ReconcileEstimated || m[core.AnomalyReconcile] == core.ReconcileAbandoned ||
		m[core.AnomalyReconcileError] != "status document carries no usage" || m[core.AnomalyReconcileAttempts] != "1" {
		t.Fatalf("anomalies = %s", an)
	}
	// 3. The entry is closed under its own state, and counts the check.
	if s := rf.scalar(`SELECT state || ' ' || attempts::text FROM pending_settlements`); s != SettleStateEstimated+" 1" {
		t.Fatalf("entry = %q", s)
	}
	if n := rf.svc.ReconcileDue(ctx); n != 0 {
		t.Fatalf("a closed entry was claimed again: %d", n)
	}
	// 4. Like abandon, it is out of the settlement retry loop's index.
	if n, err := rf.svc.RetryPending(ctx); err != nil || n != 0 {
		t.Fatalf("RetryPending saw %d rows (err %v); that is a second charge", n, err)
	}
	if s := rf.scalar(`SELECT count(*) FROM usage_logs WHERE billing_status IN ('pending','failed')`); s != "0" {
		t.Fatalf("%s rows are in the retry index", s)
	}
	// 5. The manual way out applies to it: an estimate is an estimate.
	id := rf.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'req-est'`)
	rf.post(rf.user, "/usage/"+id+"/refund", 200)
	if got := rf.balance(); !got.Equal(before) {
		t.Fatalf("balance after the manual refund %s, want %s", got, before)
	}
	rf.post(rf.user, "/usage/"+id+"/refund", 409)
}

// The answer SETTLED_ESTIMATE exists to replace: SETTLED with nothing in it.
// That reprices the row at zero and refunds the whole reservation - a
// delivered job for free - and nothing on the row says an estimate was ever
// involved. Pinned so the contrast stays visible.
func TestReconcileSettledWithZeroTokensRefundsEverything(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	before := rf.balance()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-zero", "task-zero", core.UsageTokens{Input: 2000, Output: 200})})
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED}, nil
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)
	if got := rf.balance(); !got.Equal(before) {
		t.Fatalf("balance %s, want the whole reservation back at %s", got, before)
	}
	if s := rf.scalar(`SELECT total_cost::text || ' ' || (anomalies ? 'reconcile')::text FROM usage_logs WHERE request_id = 'req-zero'`); s != "0.00000000 false" {
		t.Fatalf("row = %q", s)
	}
}

// The summary is where the estimates have to show (CONTRACTS §25.4, the
// first of the two open items): a row kept at its estimate - abandoned or
// estimated - is counted, and so is what it contributes to the revenue, next
// to a count of every row with any marker at all.
func TestSummaryCountsAnomaliesAndEstimatedRevenue(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{
		rf.reserved("req-s-est", "task-s-est", core.UsageTokens{Input: 2000, Output: 200}),
		rf.reserved("req-s-aband", "task-s-aband", core.UsageTokens{Input: 3000, Output: 300}),
		rf.reserved("req-s-real", "task-s-real", core.UsageTokens{Input: 1000, Output: 100}),
		rf.record("req-s-plain", true),
	})
	// A marker that is not an estimate: counted as an anomaly, not as revenue
	// that is a guess. And a plugin that could not answer at all (fallback)
	// counts too - while "the plugin answered" (usage_extract=plugin, on the
	// three reserved rows above) is a fact, not an anomaly, and must not.
	mism := rf.record("req-s-mism", true)
	mism.ResponseMismatch = core.ResponseMismatchSSENotDeclared
	fb := rf.record("req-s-fallback", true)
	fb.UsageExtract = core.UsageExtractFallback
	rf.svc.process(ctx, []*core.UsageRecord{mism, fb})

	answers := map[string]*pluginv1.ReconcileResult{
		"task-s-est":  {State: pluginv1.ReconcileResult_SETTLED_ESTIMATE},
		"task-s-real": {State: pluginv1.ReconcileResult_SETTLED, Tokens: &pluginv1.UsageTokens{InputTokens: 1000, OutputTokens: 100}},
	}
	rf.plugin.parse = func(in *monitorCall) (*pluginv1.ReconcileResult, error) {
		return answers[in.GetEntry().GetRefId()], nil
	}
	if _, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET deadline_at = now() - interval '1 minute' WHERE ref_id = 'task-s-aband'`); err != nil {
		t.Fatal(err)
	}
	rf.due()
	if n := rf.svc.ReconcileDue(ctx); n != 3 {
		t.Fatalf("claimed %d entries", n)
	}
	estCost := rf.cost("req-s-est").Add(rf.cost("req-s-aband"))
	if estCost.Sign() <= 0 {
		t.Fatalf("estimated rows carry no cost: %s", estCost)
	}

	rows := rf.get(rf.user, "/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	var vid map[string]any
	for _, r := range rows {
		if m := r.(map[string]any); m["key"] == "claude-sonnet-x" {
			vid = m
		}
	}
	if vid == nil {
		t.Fatalf("no summary row: %v", rows)
	}
	// Six rows: two kept at an estimate, one reconciled to a real figure
	// (its only marker says the plugin answered - not an anomaly), one plain,
	// one with a shape mismatch, one billed from the fallback. Four carry a
	// marker that counts.
	if vid["requests"].(float64) != 6 || vid["estimated"].(float64) != 2 || vid["anomalies"].(float64) != 4 {
		t.Fatalf("summary row: %v", vid)
	}
	if got, _ := decimal.NewFromString(vid["estimated_cost"].(string)); !got.Equal(estCost) {
		t.Fatalf("estimated_cost = %v, want %s", vid["estimated_cost"], estCost)
	}
	total, _ := decimal.NewFromString(vid["total_cost"].(string))
	if !total.GreaterThan(estCost) {
		t.Fatalf("total_cost %s should exceed the estimated share %s", total, estCost)
	}
	// After a manual refund the row is free: it leaves the estimate count and
	// its cost leaves the estimated share.
	id := rf.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'req-s-aband'`)
	rf.post(rf.user, "/usage/"+id+"/refund", 200)
	rows = rf.get(rf.user, "/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	for _, r := range rows {
		if m := r.(map[string]any); m["key"] == "claude-sonnet-x" {
			if m["estimated"].(float64) != 1 || m["anomalies"].(float64) != 4 {
				t.Fatalf("summary after refund: %v", m)
			}
			if got, _ := decimal.NewFromString(m["estimated_cost"].(string)); !got.Equal(rf.cost("req-s-est")) {
				t.Fatalf("estimated_cost after refund = %v", m["estimated_cost"])
			}
		}
	}
}

// A reservation the gateway dropped (CONTRACTS §25.5 gap 3) reaches the
// database as a marker: billing "free" on a reserving endpoint used to leave
// no trace at all - no charge, no entry, no row saying a job was started.
func TestDroppedReservationIsRecordedInAnomalies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rec := f.record("req-dropped", false)
	rec.UsageExtract = core.UsageExtractPlugin
	rec.ReservationDropped = `endpoint billing is "free"`
	f.svc.process(ctx, []*core.UsageRecord{rec})

	s := f.scalar(`SELECT billing_status || ' ' || (anomalies->>'reservation') || ' ' || (anomalies->>'reservation_error')
		FROM usage_logs WHERE request_id = 'req-dropped'`)
	if s != `free dropped endpoint billing is "free"` {
		t.Fatalf("row = %q", s)
	}
	if n := f.scalar(`SELECT count(*) FROM pending_settlements`); n != "0" {
		t.Fatalf("pending_settlements rows = %s", n)
	}
	// And it is what the summary counts as an anomaly.
	rows := f.get(f.user, "/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["anomalies"].(float64) != 1 || rows[0].(map[string]any)["estimated"].(float64) != 0 {
		t.Fatalf("summary: %v", rows)
	}
}

// ---------------------------------------------------------------- abandon

// The one that has to be right.
//
// Giving up keeps the money - the call really was made - and therefore MUST
// leave the row in a status the settlement retry loop cannot see. Writing
// 'failed' would read more naturally and would put the row straight back into
// usage_logs_billing_pending_idx, where RetryPending would price it again and
// charge the user a second time.
func TestAbandonKeepsTheChargeAndStaysOutOfTheRetryLoop(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-give", "task-give", core.UsageTokens{Input: 2000, Output: 200})})
	charged := rf.cost("req-give")
	afterReserve := rf.balance()

	// Past its deadline: the upstream cannot be asked any more.
	if _, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET deadline_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	rf.due()
	if n := rf.svc.ReconcileDue(ctx); n != 1 {
		t.Fatalf("claimed %d entries", n)
	}
	// Nothing was asked: there was no point.
	if rf.plugin.asks.Load() != 0 {
		t.Fatalf("the plugin was asked after the deadline")
	}
	if s := rf.scalar(`SELECT state FROM pending_settlements`); s != SettleStateAbandoned {
		t.Fatalf("entry state = %s", s)
	}
	// 1. The status is 'billed', not 'failed'.
	if s := rf.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-give'`); s != StatusBilled {
		t.Fatalf("billing_status = %s; 'failed' here would double charge", s)
	}
	// 2. The money stayed, on the estimate.
	if got := rf.cost("req-give"); !got.Equal(charged) {
		t.Fatalf("cost changed from %s to %s", charged, got)
	}
	if got := rf.balance(); !got.Equal(afterReserve) {
		t.Fatalf("balance changed from %s to %s", afterReserve, got)
	}
	// 3. It says so, in the column meant for it.
	an := rf.scalar(`SELECT anomalies::text FROM usage_logs WHERE request_id = 'req-give'`)
	var m map[string]string
	if err := json.Unmarshal([]byte(an), &m); err != nil {
		t.Fatal(err)
	}
	if m[core.AnomalyReconcile] != core.ReconcileAbandoned || m[core.AnomalyReconcileError] == "" {
		t.Fatalf("anomalies = %s", an)
	}
	if s := rf.scalar(`SELECT billing_detail ? 'reconcile' FROM usage_logs WHERE request_id = 'req-give'`); s != "false" {
		t.Fatalf("the marker landed in billing_detail: %s", s)
	}

	// 4. THE POINT: the settlement retry loop cannot see this row. Run it the
	// way the ticker does and prove nothing moved.
	ledgerRows := rf.scalar(`SELECT count(*) FROM balance_ledger WHERE ref_id = 'req-give'`)
	n, err := rf.svc.RetryPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("RetryPending picked up %d abandoned rows; that is a second charge", n)
	}
	if got := rf.scalar(`SELECT count(*) FROM balance_ledger WHERE ref_id = 'req-give'`); got != ledgerRows {
		t.Fatalf("ledger rows went from %s to %s", ledgerRows, got)
	}
	if got := rf.balance(); !got.Equal(afterReserve) {
		t.Fatalf("the retry loop moved the balance to %s", got)
	}
	// And it is not in the index's predicate at all.
	if s := rf.scalar(`SELECT count(*) FROM usage_logs WHERE billing_status IN ('pending','failed')`); s != "0" {
		t.Fatalf("%s rows are in the retry index", s)
	}
}

// Running out of attempts abandons the same way running out of time does.
func TestAbandonOnAttempts(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-att", "task-att", core.UsageTokens{Input: 10, Output: 1})})
	if _, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET attempts = 100`); err != nil {
		t.Fatal(err)
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)
	if s := rf.scalar(`SELECT state FROM pending_settlements`); s != SettleStateAbandoned {
		t.Fatalf("entry state = %s", s)
	}
	if s := rf.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-att'`); s != StatusBilled {
		t.Fatalf("billing_status = %s", s)
	}
}

// ---------------------------------------------------------------- the manual way out

// The automatic policy errs towards the platform on purpose, so an
// administrator has to be able to err the other way.
func TestManualRefundAndRetryOfAnAbandonedEntry(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	before := rf.balance()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-man", "task-man", core.UsageTokens{Input: 2000, Output: 200})})
	if _, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET deadline_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)
	id := rf.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'req-man'`)

	// Reconcile again: the row goes back to "reserved" and becomes due. A
	// plain reservation (no async task) keeps the reconcile settings'
	// deadline whatever the task timeout of its group and account.
	rf.svc.opts.Environment = fakeEnvironment{}
	rf.post(rf.user, "/usage/"+id+"/reconcile", 200)
	if s := rf.scalar(`SELECT billing_status FROM usage_logs WHERE request_id = 'req-man'`); s != StatusReserved {
		t.Fatalf("billing_status = %s", s)
	}
	if s := rf.scalar(`SELECT (deadline_at > now() + interval '6 days')::text FROM pending_settlements`); s != "true" {
		t.Fatalf("deadline follows the task timeout: %s", s)
	}
	if s := rf.scalar(`SELECT state || ' ' || attempts::text FROM pending_settlements`); s != "pending 0" {
		t.Fatalf("entry = %q", s)
	}
	// It is due now and the loop really picks it up again.
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{NextCheckAfterSec: 600}, nil
	}
	if n := rf.svc.ReconcileDue(ctx); n != 1 {
		t.Fatalf("the re-queued entry was not claimed")
	}

	// Now abandon it again and refund it by hand.
	if _, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET deadline_at = now() - interval '1 minute', next_check_at = now()`); err != nil {
		t.Fatal(err)
	}
	rf.svc.ReconcileDue(ctx)
	charged := rf.cost("req-man")
	if charged.Sign() <= 0 {
		t.Fatalf("nothing is charged to refund: %s", charged)
	}
	rf.post(rf.user, "/usage/"+id+"/refund", 200)
	if got := rf.balance(); !got.Equal(before) {
		t.Fatalf("balance %s, want %s", got, before)
	}
	if s := rf.scalar(`SELECT billing_status || ' ' || total_cost::text || ' ' || (anomalies->>'reconcile')
		FROM usage_logs WHERE request_id = 'req-man'`); s != "free 0.00000000 refunded" {
		t.Fatalf("row = %q", s)
	}
	// The manual key is distinct from the loop's own refund key, so the two
	// can never silently collapse into one.
	if s := rf.scalar(`SELECT count(*) FROM balance_ledger WHERE idempotency_key = 'refund:req-man'`); s != "1" {
		t.Fatalf("manual refund ledger rows = %s", s)
	}
	// A second refund is a no-op, not a second credit.
	rf.post(rf.user, "/usage/"+id+"/refund", 409)
}

// ---------------------------------------------------------------- multi node

// Two nodes sweeping at once (the task framework runs one sweep at a time;
// during an upgrade a node of the previous release still sweeps on its own):
// the lease on each claimed entry checks it once.
func TestReconcileRunsOnOneNodeOnly(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-multi", "task-multi", core.UsageTokens{Input: 10, Output: 1})})

	// Two services over the same database, which is what two nodes are.
	acc := rf.svc.rec.Accounts
	var svcs []*Service
	for _, node := range []string{"node-a", "node-b"} {
		s := New(rf.db, rf.ledger, rf.ledger, nil, Options{RetryInterval: time.Hour, RetryAfter: time.Hour})
		s.StartReconcile(ctx, ReconcileDeps{
			Registry:             recRegistry{gen: reconcileGen(rf.plugin)},
			Accounts:             acc,
			AllowPrivateUpstream: true, NodeID: node,
		})
		svcs = append(svcs, s)
	}
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{NextCheckAfterSec: 600}, nil
	}

	rf.due()
	claimed := make([]int, len(svcs))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, s := range svcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed[i] = s.ReconcileDue(ctx)
		}()
	}
	close(start)
	wg.Wait()

	if claimed[0]+claimed[1] != 1 {
		t.Fatalf("entries claimed: node-a %d, node-b %d; exactly one node must check it", claimed[0], claimed[1])
	}
	if got := rf.plugin.asks.Load(); got != 1 {
		t.Fatalf("the plugin was asked %d times for one entry", got)
	}
	if s := rf.scalar(`SELECT attempts::text FROM pending_settlements`); s != "1" {
		t.Fatalf("attempts = %s, want one check", s)
	}
}

// ---------------------------------------------------------------- credentials

// Monitor runs on the account that served the request, credentials
// included, and only when that account's type is the plugin's own: the
// plugin already sees these credentials on every request it serves with the
// account. An account of another plugin's type is never handed over; the
// check is deferred without spending an attempt.
func TestReconcileCredentialBoundary(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-cred", "task-cred", core.UsageTokens{Input: 10, Output: 1})})
	rf.plugin.parse = func(*monitorCall) (*pluginv1.ReconcileResult, error) {
		return &pluginv1.ReconcileResult{NextCheckAfterSec: 600}, nil
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)

	rf.plugin.mu.Lock()
	acct, entry := rf.plugin.lastAccount, rf.plugin.lastEntry
	rf.plugin.mu.Unlock()
	if acct.GetId() != 7 || !strings.Contains(acct.GetCredentialsJson(), "sk-secret") {
		t.Fatalf("own account type must come with credentials: %+v", acct)
	}
	if entry.GetRefId() != "task-cred" || entry.GetRequestId() != "req-cred" || entry.GetDeadlineAtUnix() == 0 {
		t.Fatalf("entry = %+v", entry)
	}

	// The same account under another plugin's account type.
	rf2 := reconcileFixtureWith(t, &core.Account{
		AccountRef:  core.AccountRef{ID: 7, Name: "other", PluginKey: "someone_else", Type: "other_key"},
		Credentials: json.RawMessage(`{"api_key":"sk-not-yours"}`),
		Settings:    json.RawMessage(`{"base_url":"https://nope.invalid"}`),
	})
	rf2.svc.process(ctx, []*core.UsageRecord{rf2.reserved("req-cred2", "task-cred2", core.UsageTokens{Input: 10, Output: 1})})
	rf2.due()
	rf2.svc.ReconcileDue(ctx)
	if rf2.plugin.asks.Load() != 0 || rf2.plugin.lastAccount != nil {
		t.Fatalf("another plugin's account was handed over: %+v", rf2.plugin.lastAccount)
	}
	// An account of another plugin can never be asked: a failed query that
	// spends the failure budget (TestUnaskableTaskAccount), not a deferral.
	if s := rf2.scalar(`SELECT state || ' ' || poll_failures::text FROM pending_settlements`); s != "pending 1" {
		t.Fatalf("entry = %q", s)
	}
}

// ---------------------------------------------------------------- settings

// The plugin states an upstream fact (how long an answer exists); the
// operator states a policy (how long a charge may stay open). The policy
// wins, and the backoff ladder fills in whenever the plugin has no opinion.
func TestBackoffAndDeadlineClamp(t *testing.T) {
	cfg := ReconcileSettings{MaxAgeSec: 3600, Backoff: "10s,30s,1m,5m,15m", MaxAttempts: 7}.resolve()

	for i, want := range []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute} {
		if got := cfg.clampDelay(0, i); got != want {
			t.Errorf("attempt %d: delay %s, want %s", i, got, want)
		}
	}
	// Past the ladder the last rung repeats.
	if got := cfg.clampDelay(0, 99); got != 15*time.Minute {
		t.Errorf("delay past the ladder = %s", got)
	}
	// The plugin's own suggestion wins, within the bounds.
	if got := cfg.clampDelay(2*time.Hour, 0); got != 2*time.Hour {
		t.Errorf("plugin delay = %s", got)
	}
	if got := cfg.clampDelay(time.Millisecond, 0); got != minReconcileDelay {
		t.Errorf("a sub-second delay must not turn the loop into a busy wait: %s", got)
	}
	if got := cfg.clampDelay(99*time.Hour, 0); got != maxReconcileDelay {
		t.Errorf("delay ceiling = %s", got)
	}
	// Deadlines: shorter than the policy is the plugin's to choose, longer
	// is not, and no opinion means the policy.
	if got := cfg.clampDeadline(10 * time.Minute); got != 10*time.Minute {
		t.Errorf("plugin deadline = %s", got)
	}
	if got := cfg.clampDeadline(7 * 24 * time.Hour); got != time.Hour {
		t.Errorf("a plugin deadline beyond the policy = %s, want the policy", got)
	}
	if got := cfg.clampDeadline(0); got != time.Hour {
		t.Errorf("default deadline = %s", got)
	}
	if cfg.maxAttempts != 7 {
		t.Errorf("maxAttempts = %d", cfg.maxAttempts)
	}
	// A nonsense ladder falls back to the default rather than to "no delay".
	if got := (ReconcileSettings{Backoff: "nonsense,3ms"}).resolve(); len(got.backoff) != 5 || got.backoff[0] != 10*time.Second {
		t.Errorf("fallback ladder = %v", got.backoff)
	}

	// The default policy is 7 days, not 24 hours (CONTRACTS §25.4, the second
	// open item): the first real upstream keeps its answers for 7 days and
	// states so; a 24h cap would have abandoned - and billed at the estimate -
	// every job that ran past a day. With the default in force a 7 day
	// statement passes through whole, no opinion gets the 7 days, and longer
	// is still the policy's call.
	def := DefaultReconcileSettings().resolve()
	if DefaultReconcileAgeSec != 7*24*3600 || def.maxAge != 7*24*time.Hour {
		t.Fatalf("default max age = %s", def.maxAge)
	}
	if got := def.clampDeadline(7 * 24 * time.Hour); got != 7*24*time.Hour {
		t.Errorf("a 7 day deadline under the default = %s", got)
	}
	if got := def.clampDeadline(0); got != 7*24*time.Hour {
		t.Errorf("no opinion under the default = %s", got)
	}
	if got := def.clampDeadline(8 * 24 * time.Hour); got != 7*24*time.Hour {
		t.Errorf("8 days under the default = %s, want the policy", got)
	}
	// An out-of-range stored value resolves to the new default too, and the
	// default is inside the range the API accepts.
	if got := (ReconcileSettings{MaxAgeSec: 1}).resolve(); got.maxAge != 7*24*time.Hour {
		t.Errorf("out-of-range max age resolved to %s", got.maxAge)
	}
	if d := time.Duration(DefaultReconcileAgeSec) * time.Second; d < minReconcileAge || d > maxReconcileAge {
		t.Errorf("the default %s is outside [%s, %s]", d, minReconcileAge, maxReconcileAge)
	}
}

func itoa(n int64) string { return decimal.NewFromInt(n).String() }

// post drives one of the two console actions through the router, so the
// permission and the handler are exercised and not just the SQL.
func (f *fixture) post(uid int64, path string, want int) map[string]any {
	f.t.Helper()
	req := httptest.NewRequest("POST", "/api/v1"+path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+strconv.FormatInt(uid, 10))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != want {
		f.t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func (p *recPlugin) BuildRefreshRequest(context.Context, *pluginv1.BuildRefreshRequestRequest) (*pluginv1.BuildRefreshRequestResponse, error) {
	return nil, nil
}

func (p *recPlugin) ParseRefreshResponse(context.Context, *pluginv1.ParseRefreshResponseRequest) (*pluginv1.RefreshResult, error) {
	return nil, nil
}

// An operator without account:read gets the account of a pre-charged row
// neither from the row nor from its settlement entry, in the detail or in
// the answer of a manual reconcile.
func TestSettlementHidesAccountWithoutAccountRead(t *testing.T) {
	rf := reconcileFixture(t)
	ctx := context.Background()
	rf.svc.process(ctx, []*core.UsageRecord{rf.reserved("req-acc", "task-acc", core.UsageTokens{Input: 10, Output: 1})})
	id := rf.scalar(`SELECT id::text FROM usage_logs WHERE request_id = 'req-acc'`)
	d := rf.get(rf.user, "/usage/"+id, 200)["data"].(map[string]any)
	require.Equal(t, float64(7), d["account_id"])
	require.Equal(t, float64(7), d["settlement"].(map[string]any)["account_id"])

	rf.svc.Authz = denyPerms{keys: []string{"account:read"}}
	d = rf.get(rf.user, "/usage/"+id, 200)["data"].(map[string]any)
	require.Nil(t, d["account_id"])
	require.Nil(t, d["settlement"].(map[string]any)["account_id"])

	if _, err := rf.db.SQL.ExecContext(ctx, `UPDATE pending_settlements SET deadline_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	rf.due()
	rf.svc.ReconcileDue(ctx)
	rf.svc.opts.Environment = fakeEnvironment{}
	out := rf.post(rf.user, "/usage/"+id+"/reconcile", 200)["data"].(map[string]any)
	require.Nil(t, out["account_id"])
}

func (*recPlugin) Execute(context.Context, *pluginv1.ExecuteRequest, core.ExecutionClient) (core.ExecutionResult, error) {
	return core.ExecutionResult{}, errors.New("recPlugin does not execute requests")
}

func (g *recGen) SettingsValidator(string) (core.SettingsValidator, bool) { return nil, false }
