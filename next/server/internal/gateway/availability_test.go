package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type availabilityPlugin struct {
	core.PlatformPlugin
	mu    sync.Mutex
	calls []*pluginv1.CheckModelAvailabilityRequest
	fn    func(context.Context, *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error)
}

func (p *availabilityPlugin) CheckModelAvailability(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, proto.Clone(in).(*pluginv1.CheckModelAvailabilityRequest))
	fn := p.fn
	p.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	out := &pluginv1.CheckModelAvailabilityResponse{}
	for _, v := range in.Candidates {
		state := pluginv1.ModelAvailability_AVAILABLE
		for _, w := range v.GetQuota().GetWindows() {
			if w.Status == manifest.QuotaRejected {
				state = pluginv1.ModelAvailability_UNAVAILABLE
			}
		}
		out.Results = append(out.Results, &pluginv1.ModelAvailability{AccountId: v.AccountId, Model: v.Model, State: state})
	}
	return out, nil
}

// Execute is the wrapped plugin's: the availability methods are the only
// ones this fake adds.
func (p *availabilityPlugin) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	return p.PlatformPlugin.Execute(ctx, in, client)
}

type availabilitySource struct {
	mu           sync.Mutex
	snapshots    map[int64]*pluginv1.AccountQuotaSnapshot
	reads        [][]int64
	observations []*pluginv1.QuotaResult
	generations  []int64
	err          error
	// stale marks ids AccountQuotaView reports as stale; missing ids it
	// cannot read: it waits for the context like the mirror, then reports
	// them Missing. delay holds every view back (up to the context).
	stale   map[int64]bool
	missing map[int64]bool
	delay   time.Duration
}

func (s *availabilitySource) ObserveQuotaHeaders(int64, []manifest.QuotaHeader, int, http.Header) {}
func (s *availabilitySource) AccountQuotaSnapshots(_ context.Context, ids []int64) (map[int64]*pluginv1.AccountQuotaSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, append([]int64(nil), ids...))
	out := map[int64]*pluginv1.AccountQuotaSnapshot{}
	for _, id := range ids {
		if q := s.snapshots[id]; q != nil {
			out[id] = proto.Clone(q).(*pluginv1.AccountQuotaSnapshot)
		}
	}
	return out, s.err
}

// errFakeQuotaLag is what the fake view returns with Missing ids, as the
// mirror does: err != nil exactly when Missing is not empty.
var errFakeQuotaLag = errors.New("quota snapshot not read in time")

func (s *availabilitySource) AccountQuotaView(ctx context.Context, ids []int64) (core.QuotaView, error) {
	s.mu.Lock()
	delay, waitMissing := s.delay, false
	for _, id := range ids {
		waitMissing = waitMissing || s.missing[id]
	}
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return core.QuotaView{Missing: append([]int64(nil), ids...)}, errFakeQuotaLag
		case <-time.After(delay):
		}
	}
	if waitMissing {
		<-ctx.Done()
	}
	snapshots, err := s.AccountQuotaSnapshots(ctx, ids)
	if err != nil {
		return core.QuotaView{Missing: append([]int64(nil), ids...)}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	view := core.QuotaView{Snapshots: snapshots, Stale: map[int64]bool{}}
	for _, id := range ids {
		if s.missing[id] {
			delete(snapshots, id)
			view.Missing = append(view.Missing, id)
		} else if s.stale[id] {
			view.Stale[id] = true
		}
	}
	if len(view.Missing) > 0 {
		return view, errFakeQuotaLag
	}
	return view, nil
}
func (s *availabilitySource) ApplyAccountQuotaObservation(_ context.Context, id, generation int64, q *pluginv1.QuotaResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generations = append(s.generations, generation)
	if generation <= 0 || s.snapshots[id].GetGeneration() != generation {
		return core.ErrConflict
	}
	s.observations = append(s.observations, q)
	s.snapshots[id] = &pluginv1.AccountQuotaSnapshot{Windows: q.Windows, Revision: s.snapshots[id].GetRevision() + 1, Generation: generation}
	return nil
}
func (s *availabilitySource) deny(ids ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.snapshots[id] = &pluginv1.AccountQuotaSnapshot{Revision: s.snapshots[id].GetRevision() + 1, Generation: s.snapshots[id].GetGeneration(), Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaRejected}}}
	}
}
func enableAvailability(e *env, index int) (*availabilityPlugin, *availabilitySource) {
	b := &e.gen.accountTypes[index]
	b.Type.ModelAvailability = true
	p := &availabilityPlugin{PlatformPlugin: b.Client}
	b.Client = p
	s := &availabilitySource{snapshots: map[int64]*pluginv1.AccountQuotaSnapshot{}, stale: map[int64]bool{}, missing: map[int64]bool{}}
	e.gw.d.Quota = s
	return p, s
}

func TestAvailabilityHardFilterBeforeStickyAndRank(t *testing.T) {
	e := newEnv(t)
	e.enableSticky(onFailureFailover)
	_, source := enableAvailability(e, 0)
	sess := withSession(body(testModel, false), "availability")
	if r := e.messages(sess); r.status != 200 {
		t.Fatalf("initial %d %s", r.status, r.body)
	}
	e.record()
	source.deny(1)
	ranker := e.addRanker("zero", manifest.SchedulerRank{}, &fakeRanker{fn: func(_ context.Context, in *pluginv1.RankAccountsRequest) ([]*pluginv1.RankedAccount, error) {
		var out []*pluginv1.RankedAccount
		for _, a := range in.Candidates {
			if a.AccountId == 1 {
				t.Error("denied sticky reached ranker")
			}
			out = append(out, rank(a.AccountId, 1, 0))
		}
		return out, nil
	}})
	if r := e.messages(sess); r.status != 200 {
		t.Fatalf("filtered %d %s", r.status, r.body)
	}
	if rec := e.record(); rec.AccountID == nil || *rec.AccountID != 2 || rec.StickyHit {
		t.Fatalf("record %+v", rec)
	}
	if ranker.count() != 1 || strings.Join(e.up.keys(), ",") != "acc-1,acc-2" {
		t.Fatalf("calls %v", e.up.keys())
	}
}

// Only a definite UNAVAILABLE blocks (MODEL-AVAILABILITY-PRECHECK-DESIGN
// §5.1): every check that cannot be made lets the request through, says why
// in the usage record, and the precheck never answers 503.
func TestAvailabilityPluginFailuresFailOpen(t *testing.T) {
	for _, tc := range []struct{ kind, reason string }{
		{"denied", ""},
		{"missing", availReasonBadResponse},
		{"duplicate", availReasonBadResponse},
		{"outside", availReasonBadResponse},
		{"unknown", availReasonUnknown},
		{"invalid", availReasonBadResponse},
		{"nil", availReasonBadResponse},
		{"timeout", availReasonPluginTimeout},
		{"unavailable", availReasonPluginError},
		{"unimplemented", availReasonPluginError},
		{"source", availReasonSnapshot},
		{"no_source", availReasonSnapshot},
		{"absent_checker", availReasonPluginError},
	} {
		kind := tc.kind
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			p, s := enableAvailability(e, 0)
			e.gw.availabilityPluginTimeout = 50 * time.Millisecond
			switch kind {
			case "denied":
				s.deny(1, 2, 3)
			case "source":
				s.err = errors.New("snapshot failure")
			case "no_source":
				e.gw.d.Quota = nil
			case "absent_checker":
				e.gen.accountTypes[0].Client = e.plat
			default:
				p.fn = func(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
					switch kind {
					case "timeout":
						<-ctx.Done()
						return nil, ctx.Err()
					case "unavailable":
						return nil, core.ErrPluginUnavailable
					case "unimplemented":
						return nil, status.Error(codes.Unimplemented, "old")
					case "nil":
						return nil, nil
					}
					out := &pluginv1.CheckModelAvailabilityResponse{}
					for _, v := range in.Candidates {
						out.Results = append(out.Results, &pluginv1.ModelAvailability{AccountId: v.AccountId, Model: v.Model, State: pluginv1.ModelAvailability_AVAILABLE})
					}
					switch kind {
					case "missing":
						out.Results = out.Results[1:]
					case "duplicate":
						out.Results[1] = out.Results[0]
					case "outside":
						out.Results[0].AccountId = 999
					case "unknown":
						out.Results[0].State = pluginv1.ModelAvailability_UNKNOWN
						out.Results[1].State = pluginv1.ModelAvailability_UNKNOWN
						out.Results[2].State = pluginv1.ModelAvailability_UNKNOWN
					case "invalid":
						out.Results[0].State = 99
					}
					return out, nil
				}
			}
			r := e.messages(body(testModel, false))
			rec := e.record()
			if kind == "denied" {
				if r.status != 429 || e.plat.buildCount() != 0 || len(e.up.keys()) != 0 {
					t.Fatalf("%d %s builds=%d", r.status, r.body, e.plat.buildCount())
				}
				if rec.AccountID != nil || rec.Availability == nil || rec.Availability.Outcome != core.AvailabilityBlockedAll || rec.Availability.Counts[availReasonUnavailable] != 3 {
					t.Fatalf("record %+v %+v", rec, rec.Availability)
				}
				return
			}
			if r.status != 200 || e.plat.buildCount() == 0 || len(e.up.keys()) != 1 {
				t.Fatalf("%d %s builds=%d", r.status, r.body, e.plat.buildCount())
			}
			tr := rec.Availability
			if tr == nil || tr.Outcome != core.AvailabilityFailOpen || tr.Candidates != 3 || tr.Admitted != 3 || tr.Counts[tc.reason] != 3 || len(tr.Samples) == 0 {
				t.Fatalf("trace %+v", tr)
			}
			for _, sample := range tr.Samples {
				if sample.Decision != core.AvailabilityFailOpen || !strings.HasPrefix(sample.Reason, tc.reason) {
					t.Fatalf("sample %+v", sample)
				}
			}
		})
	}
}

func TestAvailabilityOptionalCompatibility(t *testing.T) {
	e := newEnv(t)
	p, s := enableAvailability(e, 0)
	e.gen.accountTypes[0].Type.ModelAvailability = false
	s.err = errors.New("must not read")
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	e.record()
	if len(p.calls) != 0 || len(s.reads) != 0 {
		t.Fatal("old declaration invoked availability")
	}
}

func TestAvailabilityMappedModelsAndBatch(t *testing.T) {
	e := newEnv(t)
	p, s := enableAvailability(e, 0)
	e.accounts.set(1, func(a *core.Account) {
		a.ModelMapping = map[string]string{testModel: "up-primary", "client-extra": "up-extra"}
	})
	refs, _ := e.accounts.Candidates(context.Background(), testGroup, []core.AccountTypeKey{{PluginKey: "anthropic", Type: "apikey"}})
	cl := &call{g: e.gw, gen: e.gen, platform: "anthropic", ep: endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages"), model: testModel, modelRefs: []modelReference{{model: "client-extra"}, {model: "client-extra"}}}
	cl.planRoutes()
	out, failure := cl.filterAvailability(context.Background(), refs)
	if failure != nil || len(out) != 3 || len(p.calls) != 1 || len(s.reads) != 1 {
		t.Fatalf("out=%v failure=%v", out, failure)
	}
	in := p.calls[0]
	if len(in.Candidates) != 6 || in.Candidates[0].Model != "up-primary" || in.Candidates[0].ClientModel != testModel || in.Candidates[1].Model != "up-extra" {
		t.Fatalf("request %v", in)
	}
	s.deny(1)
	if failure := cl.guardAvailability(context.Background(), &refs[0]); failure == nil || failure.Status != 429 {
		t.Fatalf("changed revision guard: %v", failure)
	}
}

type availabilityAccounts struct {
	*fakeAccounts
	*availabilitySource
}

func TestAvailabilityBatchesByOwnerAndOptionalAccountSource(t *testing.T) {
	e := newEnv(t)
	first, source := enableAvailability(e, 0)
	second := &availabilityPlugin{PlatformPlugin: e.plat}
	var deadline time.Time
	check := func(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
		got, ok := ctx.Deadline()
		if !ok || !deadline.IsZero() && !got.Equal(deadline) {
			t.Error("plugin owner reset the availability deadline")
		}
		deadline = got
		out := &pluginv1.CheckModelAvailabilityResponse{}
		for _, candidate := range in.Candidates {
			out.Results = append(out.Results, &pluginv1.ModelAvailability{AccountId: candidate.AccountId, Model: candidate.Model, State: pluginv1.ModelAvailability_AVAILABLE})
		}
		return out, nil
	}
	first.fn, second.fn = check, check
	b := e.gen.accountTypes[0]
	b.Plugin.Key, b.Client = "second", second
	e.gen.accountTypes = append(e.gen.accountTypes, b)
	e.accounts.addTyped(testGroup, 4, 98, "second-key", "second", "apikey")
	e.gw.d.Quota = nil
	e.gw.d.Accounts = &availabilityAccounts{fakeAccounts: e.accounts, availabilitySource: source}
	cl := &call{g: e.gw, gen: e.gen, platform: "anthropic", ep: endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages"), model: testModel}
	cl.planRoutes()
	refs, _ := e.accounts.Candidates(context.Background(), testGroup, cl.routeKeys)
	out, failure := cl.filterAvailability(context.Background(), refs)
	if failure != nil || len(out) != 4 || len(first.calls) != 1 || len(second.calls) != 1 || len(source.reads) != 1 || len(source.reads[0]) != 4 {
		t.Fatalf("batch: %v %v", out, failure)
	}
	if len(first.calls[0].Candidates) != 3 || len(second.calls[0].Candidates) != 1 || second.calls[0].Candidates[0].AccountId != 4 {
		t.Fatal("candidates crossed plugin ownership")
	}
}

// The guard re-checks the chosen account right before Execute: a denial
// that arrives after the precheck (here while the accounts are ranked)
// fails the attempt over, or ends the request when every account is denied.
func TestAvailabilityExecutionGuardAndFailover(t *testing.T) {
	for _, mode := range []string{"failover", "all"} {
		t.Run(mode, func(t *testing.T) {
			all := mode == "all"
			e := newEnv(t)
			_, source := enableAvailability(e, 0)
			denyAfterPrecheck(e, source, func(id int64) bool { return all || id == 1 })
			r := e.messages(body(testModel, false))
			want := 200
			if all {
				want = 429
			}
			if r.status != want {
				t.Fatalf("guard %d %s", r.status, r.body)
			}
			e.record()
			keys := strings.Join(e.up.keys(), ",")
			if all && keys != "" || !all && keys != "acc-2" {
				t.Fatalf("sent stale admission: %s", keys)
			}
			if b := e.plat.builds; all && len(b) != 0 || !all && (len(b) != 1 || b[0].GetAccount().GetId() != 2) {
				t.Fatalf("plugin asked for a denied account: %d calls", len(b))
			}
		})
	}
}

// denyAfterPrecheck denies the candidates deny selects once the precheck
// admitted them: a ranker runs after the precheck and before the guard.
func denyAfterPrecheck(e *env, source *availabilitySource, deny func(int64) bool) {
	e.addRanker("deny", manifest.SchedulerRank{}, &fakeRanker{fn: func(_ context.Context, in *pluginv1.RankAccountsRequest) ([]*pluginv1.RankedAccount, error) {
		var out []*pluginv1.RankedAccount
		for _, a := range in.Candidates {
			if deny(a.AccountId) {
				source.deny(a.AccountId)
			}
			out = append(out, rank(a.AccountId, 1, 0))
		}
		return out, nil
	}})
}

func TestQuotaObservationUnknownUtilization(t *testing.T) {
	for _, test := range []struct {
		name   string
		window *pluginv1.QuotaWindow
		valid  bool
	}{
		{"unknown", &pluginv1.QuotaWindow{UtilizationUnknown: true}, true},
		{"legacy zero", &pluginv1.QuotaWindow{}, true},
		{"legacy numbers", &pluginv1.QuotaWindow{Utilization: 50, Used: 5, Limit: 10}, true},
		{"unknown utilization", &pluginv1.QuotaWindow{UtilizationUnknown: true, Utilization: 1}, false},
		{"unknown used", &pluginv1.QuotaWindow{UtilizationUnknown: true, Used: 1}, false},
		{"unknown limit", &pluginv1.QuotaWindow{UtilizationUnknown: true, Limit: 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.window.Key, test.window.Status = "scoped", manifest.QuotaRejected
			if got := validQuotaObservation(&pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{test.window}}); got != test.valid {
				t.Fatalf("valid=%v want %v: %v", got, test.valid, test.window)
			}
		})
	}
}

func TestAvailabilityQuotaObservationPreservesAdminPolicy(t *testing.T) {
	for _, mode := range []string{"facts", "admin", "plugin", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			_, source := enableAvailability(e, 0)
			source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7}
			cl := &call{g: e.gw, rec: &core.UsageRecord{}, autoDisable: defaultAutoDisableSettings(), availability: map[int64]availabilityAdmission{1: {quota: &pluginv1.AccountQuotaSnapshot{Generation: 7}}}}
			q := &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaRejected, Utilization: 100}}}
			cls := &pluginv1.ClassifyErrorResponse{Quota: q, Action: pluginv1.ClassifyErrorResponse_ACTION_RETURN_TO_CLIENT}
			switch mode {
			case "admin":
				cl.autoDisable = AutoDisableSettings{Enabled: true, StatusCodes: "429"}.compiled()
			case "plugin":
				cls.AccountEffect = pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN
			case "invalid":
				q.Windows[0].Key = "bad key"
			}
			cl.applyClassification(context.Background(), &typeRoute{binding: e.gen.accountTypes[0]}, &pluginv1.Account{Id: 1}, 429, nil, nil, "", cls)
			if mode == "invalid" && len(source.observations) != 0 || mode != "invalid" && len(source.observations) != 1 {
				t.Fatalf("observations: %v", source.observations)
			}
			if mode == "admin" {
				if _, ok := e.accounts.disabled[1]; !ok {
					t.Fatal("admin disable ignored")
				}
				return
			}
			if len(e.accounts.disabled) != 0 {
				t.Fatal("quota facts disabled account")
			}
			if (len(e.accounts.cooldowns) > 0) != (mode == "plugin") {
				t.Fatalf("quota facts caused global cooldown: %v", e.accounts.cooldowns)
			}
		})
	}
}

func TestAvailabilityBoundedBatches(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "duplicate", "outside", "timeout", "denied_then_bad"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			p, source := enableAvailability(e, 0)
			e.gw.availabilityPluginTimeout = 200 * time.Millisecond
			cl := &call{g: e.gw, gen: e.gen, rec: &core.UsageRecord{}, platform: "anthropic", ep: endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages"), model: testModel, modelRefs: []modelReference{{model: "client-extra"}, {model: "client-other"}}}
			cl.planRoutes()
			refs := make([]core.AccountRef, 140)
			for i := range refs {
				refs[i] = core.AccountRef{ID: int64(i + 1), PluginKey: "anthropic", Type: "apikey", ModelMapping: map[string]string{"client-extra": "up-extra"}}
			}
			var deadline time.Time
			calls := 0
			p.fn = func(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
				calls++
				if len(in.Candidates) > 128 {
					t.Errorf("unbounded batch: %d", len(in.Candidates))
				}
				got, ok := ctx.Deadline()
				if !ok || calls > 1 && !got.Equal(deadline) {
					t.Error("batch reset the shared deadline")
				}
				deadline = got
				if mode == "timeout" {
					if calls == 1 {
						time.Sleep(e.gw.availabilityPluginTimeout / 2)
					} else {
						<-ctx.Done()
						return nil, ctx.Err()
					}
				}
				out := &pluginv1.CheckModelAvailabilityResponse{}
				for _, candidate := range in.Candidates {
					if candidate.ClientModel == "client-extra" && candidate.Model != "up-extra" {
						t.Error("mapped reference lost")
					}
					state := pluginv1.ModelAvailability_AVAILABLE
					if mode == "denied_then_bad" && calls == 1 {
						state = pluginv1.ModelAvailability_UNAVAILABLE
					}
					out.Results = append(out.Results, &pluginv1.ModelAvailability{AccountId: candidate.AccountId, Model: candidate.Model, State: state})
				}
				if calls == 2 {
					switch mode {
					case "missing", "denied_then_bad":
						out.Results = out.Results[1:]
					case "duplicate":
						out.Results[1] = out.Results[0]
					case "outside":
						out.Results[0].AccountId = 1
					}
				}
				return out, nil
			}
			started := time.Now()
			out, failure := cl.filterAvailability(context.Background(), refs)
			if len(source.reads) != 1 {
				t.Fatalf("snapshot batches: %v", source.reads)
			}
			if mode == "valid" {
				if failure != nil || len(out) != len(refs) || calls != 4 {
					t.Fatalf("valid: %d %d %v", len(out), calls, failure)
				}
				return
			}
			// Three models per account: the first batch answers accounts
			// 1-43 (43 starts in it), the failed second batch and the
			// batches never asked leave 44-140 unchecked.
			firstBatch := (availabilityBatchSize + 2) / 3
			if calls != 2 || failure != nil {
				t.Fatalf("calls=%d failure=%v", calls, failure)
			}
			reason := availReasonBadResponse
			if mode == "timeout" {
				reason = availReasonPluginTimeout
				if time.Since(started) > e.gw.availabilityPluginTimeout*2 {
					t.Fatal("batch timeout was multiplied")
				}
			}
			unchecked := len(refs) - firstBatch
			wantOut := len(refs)
			if mode == "denied_then_bad" {
				// The valid first batch still blocks its accounts.
				wantOut = unchecked
			}
			if len(out) != wantOut {
				t.Fatalf("admitted %d want %d", len(out), wantOut)
			}
			tr := cl.rec.Availability
			// Account 43 straddles the batches: answered in the first, unchecked in the second.
			wantOpen := unchecked + 1
			if mode == "denied_then_bad" {
				wantOpen = unchecked
				if tr.Counts[availReasonUnavailable] != firstBatch || tr.Outcome != core.AvailabilityPartial {
					t.Fatalf("trace %+v", tr)
				}
			} else if tr.Outcome != core.AvailabilityFailOpen {
				t.Fatalf("trace %+v", tr)
			}
			if tr.Counts[reason] != wantOpen || tr.Admitted != wantOut || tr.Candidates != len(refs) {
				t.Fatalf("trace counts %+v", tr)
			}
			batchSamples := 0
			for _, s := range tr.Samples {
				if s.Batch {
					batchSamples++
					if s.AccountID != 0 || s.Reason != reason || s.Plugin != "anthropic" {
						t.Fatalf("batch sample %+v", s)
					}
				}
			}
			if batchSamples != 1 || len(tr.Samples) > availTraceSamples {
				t.Fatalf("samples %+v", tr.Samples)
			}
			if cl.availability[int64(len(refs))].reason != reason {
				t.Fatalf("admission reason %q", cl.availability[int64(len(refs))].reason)
			}
			if mode == "denied_then_bad" {
				if _, ok := cl.availability[1]; ok {
					t.Fatal("blocked account admitted")
				}
			} else if cl.availability[1].reason != "" {
				t.Fatalf("answered account marked unchecked: %q", cl.availability[1].reason)
			}
		})
	}
}

func TestAvailabilityRetryAfterOnlyForCompleteDenial(t *testing.T) {
	for _, mode := range []string{"denied", "unknown_retry", "unknown_state", "bounded", "partial"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			p, _ := enableAvailability(e, 0)
			e.gw.now = func() time.Time { return time.Unix(1000, 0) }
			p.fn = func(_ context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
				out := &pluginv1.CheckModelAvailabilityResponse{}
				for _, candidate := range in.Candidates {
					result := &pluginv1.ModelAvailability{AccountId: candidate.AccountId, Model: candidate.Model, State: pluginv1.ModelAvailability_UNAVAILABLE, RetryAtUnix: 1000 + 60*candidate.AccountId}
					if mode == "bounded" {
						result.RetryAtUnix = 1 << 62
					}
					if candidate.AccountId == 1 {
						switch mode {
						case "unknown_retry":
							result.RetryAtUnix = 0
						case "unknown_state":
							result.State = pluginv1.ModelAvailability_UNKNOWN
						case "partial":
							result.State = pluginv1.ModelAvailability_AVAILABLE
						}
					}
					out.Results = append(out.Results, result)
				}
				return out, nil
			}
			r := e.messages(body(testModel, false))
			rec := e.record()
			want := ""
			if mode == "denied" {
				want = "60"
			}
			if mode == "bounded" {
				want = "3600"
			}
			if r.header.Get("Retry-After") != want {
				t.Fatalf("retry hint %q want %q (status %d)", r.header.Get("Retry-After"), want, r.status)
			}
			wantStatus := 429
			if mode == "unknown_state" || mode == "partial" {
				// UNKNOWN is the plugin not knowing: the account goes through.
				wantStatus = 200
			}
			if r.status != wantStatus {
				t.Fatalf("status %d want %d: %s", r.status, wantStatus, r.body)
			}
			if wantStatus == 429 && (rec.AccountID != nil || rec.Availability.Outcome != core.AvailabilityBlockedAll) {
				t.Fatalf("blocked record %+v %+v", rec, rec.Availability)
			}
			if mode == "denied" && rec.Availability.RetryAt != 1060 {
				t.Fatalf("trace retry_at %d", rec.Availability.RetryAt)
			}
		})
	}
}

func TestAvailabilityFeedbackUsesAdmissionGeneration(t *testing.T) {
	for _, mode := range []string{"current", "late_denial", "late_clear"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			p, source := enableAvailability(e, 0)
			source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7, Revision: 2}
			p.fn = func(_ context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
				in.Candidates[0].Quota.Generation = 999
				return &pluginv1.CheckModelAvailabilityResponse{Results: []*pluginv1.ModelAvailability{{AccountId: 1, Model: testModel, State: pluginv1.ModelAvailability_AVAILABLE}}}, nil
			}
			cl := &call{g: e.gw, gen: e.gen, platform: "anthropic", ep: endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages"), model: testModel}
			cl.planRoutes()
			ref := core.AccountRef{ID: 1, PluginKey: "anthropic", Type: "apikey"}
			if _, failure := cl.filterAvailability(context.Background(), []core.AccountRef{ref}); failure != nil {
				t.Fatal(failure)
			}
			if cl.availability[1].quota.Generation != 7 {
				t.Fatal("plugin mutated trusted admission generation")
			}
			status := manifest.QuotaRejected
			if mode != "current" {
				source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 8, Revision: 4, Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaAllowed}}}
			}
			if mode == "late_clear" {
				status = manifest.QuotaAllowed
				source.snapshots[1].Windows[0].Status = manifest.QuotaRejected
			}
			before := proto.Clone(source.snapshots[1])
			cl.applyQuotaObservation(context.Background(), 1, &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: status}}})
			if len(source.reads) != 1 {
				t.Fatal("feedback fetched a new generation instead of using admission")
			}
			if len(source.generations) != 1 || source.generations[0] != 7 {
				t.Fatalf("feedback generation: %v", source.generations)
			}
			if mode == "current" {
				if len(source.observations) != 1 || source.snapshots[1].Windows[0].Status != status {
					t.Fatal("current feedback rejected")
				}
				return
			}
			if len(source.observations) != 0 || !proto.Equal(before, source.snapshots[1]) {
				t.Fatal("late feedback poisoned new identity")
			}
			if failure := cl.guardAvailability(context.Background(), &ref); failure == nil || failure.Status != 503 {
				t.Fatalf("execution rebound to new identity: %v", failure)
			}
			if cl.availability[1].quota.Generation != 7 {
				t.Fatal("guard replaced the admitted identity")
			}
		})
	}
}

func TestAvailabilityFeedbackWithoutAdmissionIgnored(t *testing.T) {
	e := newEnv(t)
	_, source := enableAvailability(e, 0)
	for _, snapshot := range []*pluginv1.AccountQuotaSnapshot{nil, {}, {Generation: -1}} {
		cl := &call{g: e.gw, availability: map[int64]availabilityAdmission{1: {quota: snapshot}}}
		cl.applyQuotaObservation(context.Background(), 1, &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaRejected}}})
	}
	if len(source.observations) != 0 || len(source.generations) != 0 {
		t.Fatal("unadmitted quota feedback persisted")
	}
}

func TestAvailabilityWebSocketGenerationCannotRebind(t *testing.T) {
	e := newWSEnv(t)
	_, source := enableAvailability(e.env, 1)
	source.snapshots[11] = &pluginv1.AccountQuotaSnapshot{Generation: 7}
	c := e.mustDial()
	c.send(create(wsModel, "first"))
	c.until("response.completed")
	e.record()
	source.mu.Lock()
	source.snapshots[11] = &pluginv1.AccountQuotaSnapshot{Generation: 8}
	source.mu.Unlock()
	c.send(create(wsModel, "second"))
	ev, err := c.next()
	if err != nil || ev.Get("type").String() != "error" || ev.Get("status").Int() != 503 {
		t.Fatalf("reauthorized turn: %s %v", ev.Raw, err)
	}
	e.record()
	keys, messages := e.wsUp.snapshot()
	if len(keys) != 1 || keys[0] != "oa-11" || len(messages) != 1 {
		t.Fatalf("session rebound after reauthorization: %v %v", keys, messages)
	}
}

func TestAvailabilityWebSocketReuseKeepsContext(t *testing.T) {
	e := newWSEnv(t)
	_, source := enableAvailability(e.env, 1)
	source.deny(11)
	c := e.mustDial()
	c.send(create(wsModel, "first"))
	c.until("response.completed")
	if r := e.record(); r.AccountID == nil || *r.AccountID != 12 {
		t.Fatalf("account: %+v", r)
	}
	source.deny(12)
	c.send(create(wsModel, "second"))
	ev, err := c.next()
	if err != nil || ev.Get("type").String() != "error" || ev.Get("status").Int() != 429 {
		t.Fatalf("reuse event %s %v", ev.Raw, err)
	}
	e.record()
	keys, messages := e.wsUp.snapshot()
	if len(keys) != 1 || keys[0] != "oa-12" || len(messages) != 1 {
		t.Fatalf("silently moved/forwarded: %v %v", keys, messages)
	}
}
