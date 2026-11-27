package gateway

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// fakeLimiter records hits and token counts and marks the given accounts as
// exhausted.
type fakeLimiter struct {
	mu         sync.Mutex
	exhausted  map[int64]bool
	refused    map[int64]bool // Reserve refuses them though Exhausted passed them
	hits       []string       // "<id>:<model>"
	tokens     map[int64]int64
	listTokens map[string]int64 // "<id>:<list key>"
	modelTkns  map[string]int64 // "<id>:<model>" of CountModelTokens
	models     []string         // model of each Exhausted call
	sessions   []string         // session of each Exhausted and TryHit call
	released   []string         // "<id>:<session>" of each ReleaseSession call
	reserved   []int64          // tokens of each Reserve call
	settled    []string         // "<id>:<actual>" of each Settle call
	usage      map[int64]core.RateUsage
	modelUsage map[string]map[int64]core.RateUsage // model -> account -> usage
	modelErr   error
	modelAsked []string // model of each ModelUsage call
	usageErr   error
	usageCalls int
}

func newFakeLimiter() *fakeLimiter {
	return &fakeLimiter{exhausted: map[int64]bool{}, tokens: map[int64]int64{}, listTokens: map[string]int64{}, modelTkns: map[string]int64{}}
}

func (l *fakeLimiter) Exhausted(_ context.Context, refs []core.AccountRef, model, session string) (map[int64]bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.models = append(l.models, model)
	l.sessions = append(l.sessions, session)
	out := map[int64]bool{}
	for _, r := range refs {
		if l.exhausted[r.ID] {
			out[r.ID] = true
		}
	}
	return out, nil
}

func (l *fakeLimiter) TryHit(_ context.Context, ref core.AccountRef, model, session string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, itoa(ref.ID)+":"+model)
	l.sessions = append(l.sessions, session)
	return !l.refused[ref.ID], nil
}

func (l *fakeLimiter) ReleaseSession(_ context.Context, ref core.AccountRef, _ string, session string, _ time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.released = append(l.released, itoa(ref.ID)+":"+session)
}

func (l *fakeLimiter) Reserve(ctx context.Context, ref core.AccountRef, model, session string, tokens int64) (core.RateHold, bool, error) {
	ok, err := l.TryHit(ctx, ref, model, session)
	l.mu.Lock()
	l.reserved = append(l.reserved, tokens)
	l.mu.Unlock()
	hold := core.RateHold{Subject: ref.ID, Items: []core.RateHoldItem{{}}}
	if ml := ref.ModelLimitFor(model); ml != nil {
		hold.Items = append(hold.Items, core.RateHoldItem{Sub: ml.Key()})
	}
	return hold, ok, err
}

func (l *fakeLimiter) Settle(ctx context.Context, hold core.RateHold, actual int64) {
	l.mu.Lock()
	l.settled = append(l.settled, fmt.Sprintf("%d:%d", hold.Subject, actual))
	l.mu.Unlock()
	if actual <= 0 {
		return
	}
	list := ""
	if len(hold.Items) > 1 {
		list = hold.Items[1].Sub
	}
	l.AddTokens(ctx, hold.Subject, list, actual)
}

func (l *fakeLimiter) AddTokens(_ context.Context, id int64, list string, n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tokens[id] += n
	if list != "" {
		l.listTokens[itoa(id)+":"+list] += n
	}
}

func (l *fakeLimiter) Usage(context.Context, []core.AccountRef) (map[int64]core.RateUsage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.usageCalls++
	if l.usageErr != nil {
		return nil, l.usageErr
	}
	return maps.Clone(l.usage), nil
}

func (l *fakeLimiter) ModelUsage(_ context.Context, _ []core.AccountRef, model string) (map[int64]core.RateUsage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.modelAsked = append(l.modelAsked, model)
	if l.modelErr != nil {
		return nil, l.modelErr
	}
	return maps.Clone(l.modelUsage[model]), nil
}

func (l *fakeLimiter) CountModelTokens(_ context.Context, id int64, model string, n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.modelTkns[itoa(id)+":"+model] += n
}

func (l *fakeLimiter) ModelRates(context.Context, core.AccountRef, time.Time) ([]core.ModelRates, error) {
	return []core.ModelRates{{}}, nil
}

func (a *fakeAccounts) set(id int64, fn func(acc *core.Account)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(a.accounts[id])
}

func TestModelListFiltersCandidates(t *testing.T) {
	e := newEnv(t)
	e.accounts.set(1, func(acc *core.Account) { acc.Models = []string{"another-model"} })
	e.accounts.set(2, func(acc *core.Account) { acc.Models = []string{"another-model", testModel} })
	r := e.messages(body(testModel, false))
	if r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if k := e.up.keys(); strings.Join(k, ",") != "acc-2" {
		t.Fatalf("upstream order %v, want acc-2 only", k)
	}
	e.record()

	// No account serves the model: a plain 503 no_available_account, not
	// Anthropic's overload.
	e.accounts.set(2, func(acc *core.Account) { acc.Models = []string{"another-model"} })
	e.accounts.set(3, func(acc *core.Account) { acc.Models = []string{"another-model"} })
	r = e.messages(body(testModel, false))
	if r.status != 503 || r.json().Get("error.type").String() != "api_error" {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.ErrorType != errTypeNoAccount {
		t.Fatalf("record %+v", rec)
	}
}

func TestModelMappingRewritesUpstreamModel(t *testing.T) {
	e := newEnv(t)
	e.accounts.set(1, func(acc *core.Account) { acc.ModelMapping = map[string]string{testModel: "mapped-model"} })
	r := e.messages(body(testModel, false))
	if r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	call := e.up.last()
	if m := gjson.GetBytes(call.body, "model").String(); m != "mapped-model" {
		t.Fatalf("upstream body model %q", m)
	}
	if b := e.plat.builds[0]; b.GetMeta().GetModel() != "mapped-model" || gjson.GetBytes(b.GetBody(), "model").String() != "mapped-model" {
		t.Fatalf("plugin saw meta.model=%q body=%s", b.GetMeta().GetModel(), b.GetBody())
	}
	rec := e.record()
	if rec.Model != testModel || rec.UpstreamModel != "mapped-model" || rec.Price == nil {
		t.Fatalf("record model=%q upstream=%q price=%v", rec.Model, rec.UpstreamModel, rec.Price)
	}
	// The client response is untouched apart from what the upstream sent.
	if r.json().Get("model").String() != upModel {
		t.Fatalf("response model %s", r.json().Get("model").String())
	}
}

func TestRateLimitedAccountsAreSkipped(t *testing.T) {
	lim := newFakeLimiter()
	e := newEnv(t, func(e *env) {})
	e.gw.d.Limiter = lim
	e.accounts.set(1, func(acc *core.Account) { acc.RPMLimit = 10 })
	e.accounts.set(2, func(acc *core.Account) { acc.RPMLimit = 5 })
	lim.exhausted[1] = true
	r := e.messages(body(testModel, false))
	if r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if k := e.up.keys(); strings.Join(k, ",") != "acc-2" {
		t.Fatalf("upstream order %v, want acc-2 (acc-1 exhausted)", k)
	}
	rec := e.record()
	if len(lim.hits) != 1 || lim.hits[0] != "2:"+testModel {
		t.Fatalf("hits %v", lim.hits)
	}
	if n := rec.Tokens.Throughput(rec.UsageSemantics); lim.tokens[2] != n || n == 0 {
		t.Fatalf("tokens counted %d, record %d", lim.tokens[2], n)
	}
	// Account 3 has no limits, so it was not asked about.
	if len(lim.models) != 1 || lim.models[0] != testModel {
		t.Fatalf("exhausted calls %v", lim.models)
	}
	// Without a sticky session each request is a session of its own (CONTRACTS §71).
	for _, s := range lim.sessions {
		if !core.IsRequestSession(s) {
			t.Fatalf("sessions %q", lim.sessions)
		}
	}

	// Every candidate exhausted: 429 rate_limited.
	lim.exhausted[2], lim.exhausted[3] = true, true
	e.accounts.set(3, func(acc *core.Account) { acc.TPMLimit = 1 })
	r = e.messages(body(testModel, false))
	if r.status != 429 || !strings.Contains(string(r.body), "rate limited") {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.ErrorType != errTypeNoAccount {
		t.Fatalf("record %+v", rec)
	}
}

// A model list of an account (CONTRACTS §68) takes its own concurrency slot,
// and the response's tokens are counted in the list as well as the account.
func TestModelLimitsSlotsAndTokens(t *testing.T) {
	lim := newFakeLimiter()
	e := newEnv(t)
	e.gw.d.Limiter = lim
	list := core.ModelLimit{Models: []string{"other-model", testModel}, MaxConcurrency: 1}
	e.accounts.set(1, func(acc *core.Account) { acc.ModelLimits = []core.ModelLimit{list} })
	kind := core.ModelLimitSlots(list.Key())

	// The list is full: account 1 is busy for this model, account 2 serves it.
	e.slots.inUse[kind+":1"] = 1
	r := e.messages(body(testModel, false))
	if r.status != 200 || strings.Join(e.up.keys(), ",") != "acc-2" {
		t.Fatalf("status %d upstream %v", r.status, e.up.keys())
	}
	e.record()

	// A free list slot: account 1 serves, both slots are released after.
	e.slots.inUse[kind+":1"] = 0
	r = e.messages(body(testModel, false))
	if r.status != 200 {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	rec := e.record()
	for _, k := range []string{kind, core.ModelSlots(testModel)} {
		if n, _ := e.slots.InUse(context.Background(), k, 1); n != 0 {
			t.Fatal("slot not released", k, n)
		}
	}
	if n := rec.Tokens.Throughput(rec.UsageSemantics); lim.listTokens["1:"+list.Key()] != n || lim.tokens[1] != n || lim.modelTkns["1:"+testModel] != n {
		t.Fatalf("tokens account %d list %v model %v, record %d", lim.tokens[1], lim.listTokens, lim.modelTkns, n)
	}
	if lim.hits[len(lim.hits)-1] != "1:"+testModel {
		t.Fatalf("hits %v", lim.hits)
	}
}

func TestWeightedOrder(t *testing.T) {
	refs := func() []core.AccountRef {
		return []core.AccountRef{{ID: 1, Weight: 1}, {ID: 2, Weight: 3}, {ID: 3, Weight: 6}}
	}
	ids := func(g []core.AccountRef) string {
		var s []string
		for _, r := range g {
			s = append(s, itoa(r.ID))
		}
		return strings.Join(s, ",")
	}
	// rnd=0 keeps the input order (used by the test env).
	g := refs()
	weightedOrder(g, refWeights(g), func() float64 { return 0 })
	if ids(g) != "1,2,3" {
		t.Fatalf("rnd=0: %s", ids(g))
	}
	// rnd just below 1 picks the last of the remaining accounts each time.
	g = refs()
	weightedOrder(g, refWeights(g), func() float64 { return 0.999 })
	if ids(g) != "3,1,2" {
		t.Fatalf("rnd=0.999: %s", ids(g))
	}
	// 0.5*10=5 lands in account 3's [4,10) range; then 0.5*4=2 in account 2's [1,4).
	g = refs()
	weightedOrder(g, refWeights(g), func() float64 { return 0.5 })
	if ids(g) != "3,2,1" {
		t.Fatalf("rnd=0.5: %s", ids(g))
	}
	// 0.15*10=1.5 lands in account 2's range first, then 0.15*7=1.05 in account 3's.
	g = refs()
	weightedOrder(g, refWeights(g), func() float64 { return 0.15 })
	if ids(g) != "2,3,1" {
		t.Fatalf("rnd=0.15: %s", ids(g))
	}
	// Statistically: with weights 1:3:6 account 3 comes first ~60% of the time.
	n := 20000
	first := map[int64]int{}
	seq := 0.0
	rnd := func() float64 { seq += 0.6180339887; seq -= float64(int(seq)); return seq }
	for i := 0; i < n; i++ {
		g = refs()
		weightedOrder(g, refWeights(g), rnd)
		first[g[0].ID]++
	}
	if p := float64(first[3]) / float64(n); p < 0.55 || p > 0.65 {
		t.Fatalf("account 3 first %.3f, want ~0.6 (%v)", p, first)
	}
	if p := float64(first[1]) / float64(n); p < 0.07 || p > 0.13 {
		t.Fatalf("account 1 first %.3f, want ~0.1 (%v)", p, first)
	}
}

// Sessions per minute (CONTRACTS §71) count sticky sessions: an account at
// its cap keeps serving the sessions it holds, new sessions go to other
// accounts, and a request without a session is a new session.
func TestSessionsPerMinuteFollowStickySessions(t *testing.T) {
	e := newEnv(t)
	e.gw.d.Limiter = account.NewLimiter(e.rdb)
	e.enableSticky(onFailureFailover)
	for id := int64(1); id <= 3; id++ {
		e.accounts.set(id, func(acc *core.Account) { acc.SPMLimit = 1 })
	}
	send := func(session string) (int, string) {
		t.Helper()
		b := body(testModel, false)
		if session != "" {
			b = withSession(b, "user_abc_account__session_"+session)
		}
		r := e.messages(b)
		e.record()
		if r.status != 200 {
			return r.status, string(r.body)
		}
		return r.status, e.up.last().key
	}
	for _, step := range []struct{ session, want string }{
		{"1", "acc-1"}, {"1", "acc-1"}, {"2", "acc-2"}, {"3", "acc-3"}, {"1", "acc-1"},
	} {
		if code, got := send(step.session); code != 200 || got != step.want {
			t.Fatalf("session %q: %d %s, want %s", step.session, code, got, step.want)
		}
	}
	for _, session := range []string{"4", ""} {
		if code, got := send(session); code != 429 || !strings.Contains(got, "rate limited") {
			t.Fatalf("new session %q over every cap: %d %s", session, code, got)
		}
	}
	u, err := e.gw.d.Limiter.Usage(context.Background(), []core.AccountRef{{ID: 1}, {ID: 2}, {ID: 4}})
	if err != nil || u[1].Sessions != 1 || u[2].Sessions != 1 || u[1].RPM != 3 {
		t.Fatal(u, err)
	}
}

// refWeights are the accounts' own weights, as tierWeights gives them
// without loads or balancing.
func refWeights(g []core.AccountRef) []float64 { return (&call{}).tierWeights(g) }
