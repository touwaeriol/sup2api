package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/stretchr/testify/require"
)

// availabilityCall is a call of the anthropic messages endpoint ready for
// filterAvailability / guardAvailability.
func availabilityCall(t *testing.T, e *env) *call {
	t.Helper()
	cl := &call{g: e.gw, gen: e.gen, rec: &core.UsageRecord{}, platform: "anthropic", ep: endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages"), model: testModel}
	cl.planRoutes()
	return cl
}

var availabilityRef1 = core.AccountRef{ID: 1, PluginKey: "anthropic", Type: "apikey"}

func failingAvailability(calls *int) func(context.Context, *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
	return func(context.Context, *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
		if calls != nil {
			*calls++
		}
		return nil, core.ErrPluginUnavailable
	}
}

// A failed-open admission keeps the snapshot the core read itself, so the
// quota observation of the attempt is written under its generation.
func TestAvailabilityFailOpenKeepsSnapshotGeneration(t *testing.T) {
	e := newEnv(t)
	p, source := enableAvailability(e, 0)
	source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7, Revision: 1}
	p.fn = failingAvailability(nil)
	cl := availabilityCall(t, e)
	out, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
	require.Nil(t, failure)
	require.Len(t, out, 1)
	adm := cl.availability[1]
	require.Equal(t, int64(7), adm.quota.GetGeneration())
	require.Equal(t, availReasonPluginError, adm.reason)
	cl.applyQuotaObservation(context.Background(), 1, &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaRejected}}})
	require.Equal(t, []int64{7}, source.generations)
	require.Len(t, source.observations, 1)
}

// Without a snapshot there is no generation to write under: nothing is.
func TestAvailabilityNilSnapshotSkipsObservation(t *testing.T) {
	for _, mode := range []string{"no_row", "missing"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			p, source := enableAvailability(e, 0)
			e.gw.availabilitySnapshotTimeout = 20 * time.Millisecond
			if mode == "missing" {
				source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7}
				source.missing[1] = true
			}
			p.fn = failingAvailability(nil)
			cl := availabilityCall(t, e)
			_, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
			require.Nil(t, failure)
			require.Nil(t, cl.availability[1].quota)
			cl.applyQuotaObservation(context.Background(), 1, &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaRejected}}})
			require.Empty(t, source.generations)
		})
	}
}

// A snapshot that is missing for some ids lets those through with
// snapshot_unavailable and passes the others' snapshots to the plugin.
func TestAvailabilityPartialSnapshotMissing(t *testing.T) {
	e := newEnv(t)
	p, source := enableAvailability(e, 0)
	e.gw.availabilitySnapshotTimeout = 20 * time.Millisecond
	source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 3}
	source.snapshots[2] = &pluginv1.AccountQuotaSnapshot{Generation: 4}
	source.missing[2] = true
	cl := availabilityCall(t, e)
	refs := []core.AccountRef{availabilityRef1, {ID: 2, PluginKey: "anthropic", Type: "apikey"}}
	out, failure := cl.filterAvailability(context.Background(), refs)
	require.Nil(t, failure)
	require.Len(t, out, 2)
	require.Len(t, p.calls, 1)
	require.Equal(t, int64(3), p.calls[0].Candidates[0].GetQuota().GetGeneration())
	require.Nil(t, p.calls[0].Candidates[1].GetQuota())
	tr := cl.rec.Availability
	require.Equal(t, core.AvailabilityFailOpen, tr.Outcome)
	require.Equal(t, map[string]int{availReasonSnapshot: 1}, tr.Counts)
	require.Equal(t, int64(2), tr.Samples[0].AccountID)
	require.False(t, tr.Samples[0].Batch)
}

// A source that fails without naming the ids it could not read (against
// the QuotaView contract) cannot say which absent snapshot means "no row":
// every id without one is treated as unread, the others keep theirs.
type unnamedFailureSource struct{ *availabilitySource }

func (s unnamedFailureSource) AccountQuotaView(context.Context, []int64) (core.QuotaView, error) {
	return core.QuotaView{Snapshots: map[int64]*pluginv1.AccountQuotaSnapshot{1: {Generation: 3}}}, errors.New("partial read")
}

func TestAvailabilitySourceErrorWithoutMissingIDs(t *testing.T) {
	e := newEnv(t)
	p, source := enableAvailability(e, 0)
	e.gw.d.Quota = unnamedFailureSource{source}
	cl := availabilityCall(t, e)
	refs := []core.AccountRef{availabilityRef1, {ID: 2, PluginKey: "anthropic", Type: "apikey"}}
	out, failure := cl.filterAvailability(context.Background(), refs)
	require.Nil(t, failure)
	require.Len(t, out, 2)
	require.Equal(t, int64(3), p.calls[0].Candidates[0].GetQuota().GetGeneration())
	require.Equal(t, int64(3), cl.availability[1].quota.GetGeneration())
	require.Empty(t, cl.availability[1].reason)
	require.Equal(t, availReasonSnapshot, cl.availability[2].reason)
	require.Equal(t, map[string]int{availReasonSnapshot: 1}, cl.rec.Availability.Counts)
}

// The guard adopts the snapshot it reads when the admission's generation
// cannot be trusted (stale) or was never known (nil): no reauthorization is
// inferred from it, no 503.
func TestAvailabilityGuardAdoptsStaleOrNilAdmission(t *testing.T) {
	for _, mode := range []string{"stale", "nil"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			p, source := enableAvailability(e, 0)
			if mode == "stale" {
				source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7, Revision: 1}
				source.stale[1] = true
			}
			cl := availabilityCall(t, e)
			_, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
			require.Nil(t, failure)
			require.Equal(t, mode == "stale", cl.availability[1].stale)
			source.mu.Lock()
			source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 8, Revision: 3}
			delete(source.stale, 1)
			source.mu.Unlock()
			ref := availabilityRef1
			require.Nil(t, cl.guardAvailability(context.Background(), &ref))
			require.Equal(t, int64(8), cl.availability[1].quota.GetGeneration())
			require.False(t, cl.availability[1].stale)
			require.Len(t, p.calls, 2)
			require.Nil(t, cl.rec.Availability, "a clean guard records nothing")
		})
	}
}

// A guard that cannot read the snapshot keeps the admission's, without
// waiting for the read the precheck already gave up on.
func TestAvailabilityGuardMissingSnapshotKeepsAdmission(t *testing.T) {
	e := newEnv(t)
	_, source := enableAvailability(e, 0)
	source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7, Revision: 1}
	cl := availabilityCall(t, e)
	_, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
	require.Nil(t, failure)
	source.mu.Lock()
	source.missing[1] = true
	source.mu.Unlock()
	ref := availabilityRef1
	started := time.Now()
	require.Nil(t, cl.guardAvailability(context.Background(), &ref))
	require.Less(t, time.Since(started), availabilitySnapshotTimeout/2, "guard waited for a missing snapshot")
	require.Equal(t, int64(7), cl.availability[1].quota.GetGeneration())
}

// Once the plugin failed open in a request, the guards of its attempts do
// not ask it again.
func TestAvailabilityGuardSkipsPluginAfterFailOpen(t *testing.T) {
	e := newEnv(t)
	p, source := enableAvailability(e, 0)
	source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7, Revision: 1}
	calls := 0
	p.fn = failingAvailability(&calls)
	cl := availabilityCall(t, e)
	_, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
	require.Nil(t, failure)
	// Close the node breaker so only the request's own memory is tested.
	e.gw.availabilityBreakers.done("anthropic", e.gw.now(), false, true)
	source.mu.Lock()
	source.snapshots[1] = &pluginv1.AccountQuotaSnapshot{Generation: 7, Revision: 2}
	source.mu.Unlock()
	ref := availabilityRef1
	require.Nil(t, cl.guardAvailability(context.Background(), &ref))
	require.Equal(t, 1, calls)
	require.Nil(t, cl.rec.Availability.Guard, "the precheck already recorded the failure")
}

// The node breaker: after a failed availability call the plugin is not
// asked for 10 s, then one request probes and a success closes it.
func TestAvailabilityBreaker(t *testing.T) {
	e := newEnv(t)
	p, _ := enableAvailability(e, 0)
	now := time.Unix(10_000, 0)
	e.gw.now = func() time.Time { return now }
	calls := 0
	p.fn = failingAvailability(&calls)
	check := func() *call {
		cl := availabilityCall(t, e)
		out, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
		require.Nil(t, failure)
		require.Len(t, out, 1)
		return cl
	}
	check()
	require.Equal(t, 1, calls)
	cl := check()
	require.Equal(t, 1, calls, "breaker open: no RPC")
	require.Equal(t, availReasonPluginError, cl.availability[1].reason)
	now = now.Add(availabilityBreakerOpen + time.Second)
	p.fn = nil
	allowedCalls := len(p.calls)
	cl = check()
	require.Equal(t, allowedCalls+1, len(p.calls), "probe after the open period")
	require.Empty(t, cl.availability[1].reason)
	check()
	require.Equal(t, allowedCalls+2, len(p.calls), "closed after a successful probe")

	var b availabilityBreakers
	b.done("x", now, true, false)
	now = now.Add(availabilityBreakerOpen)
	ok, probe := b.allow("x", now)
	require.True(t, ok && probe)
	ok, _ = b.allow("x", now)
	require.False(t, ok, "one probe at a time")
	b.abandon("x", true)
	ok, probe = b.allow("x", now)
	require.True(t, ok && probe, "an abandoned probe hands over")
	b.done("x", now, true, true)
	ok, _ = b.allow("x", now.Add(time.Second))
	require.False(t, ok, "failed probe reopens")
}

// Under a failure policy a model availability refusal moves to another
// account without running account rules: a 429/503 rule must not cool down
// or disable an account whose one model window ran out.
func TestAvailabilityFailurePolicyTakesNoAccountAction(t *testing.T) {
	e := newEnv(t)
	_, source := enableAvailability(e, 0)
	p := testFailurePolicy()
	p.RetryRules = append(p.RetryRules, FailureRule{ID: "core", Name: "Core", Enabled: true,
		Match: FailureMatch{Source: "core", StatusCodes: "429,503"}, Action: failureRetry, SameAccountRetries: 2, ExhaustedAction: failureReset, Egress: "same_proxy"})
	p.AccountEnabled = true
	p.AccountRules = []FailureRule{{ID: "cool", Name: "Cool", Enabled: true, Action: "cooldown", CooldownSeconds: 60,
		Match: FailureMatch{Source: "core", StatusCodes: "429,503"}}}
	e.setFailurePolicy(p)
	denyAfterPrecheck(e, source, func(id int64) bool { return id == 1 })
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	require.Equal(t, []string{"acc-2"}, e.up.keys())
	require.Empty(t, e.accounts.cooldowns)
	require.Empty(t, e.accounts.disabled)
	rec := e.record()
	require.Contains(t, rec.FailurePolicy, `"reason":"model_availability"`)
	require.Contains(t, rec.FailurePolicy, `"account_action":"none"`)
	require.NotNil(t, rec.Availability)
	require.Len(t, rec.Availability.Guard, 1)
	require.Equal(t, int64(1), rec.Availability.Guard[0].AccountID)
	require.Equal(t, core.AvailabilityBlock, rec.Availability.Guard[0].Decision)
	require.Equal(t, availReasonUnavailable, rec.Availability.Guard[0].Reason)
	require.Equal(t, 1, rec.Availability.Guard[0].Attempt)
	require.Equal(t, core.AvailabilityPass, rec.Availability.Outcome)
}

// A WebSocket connection made while availability failed open has no
// generation: its later turns go through and never write observations.
func TestAvailabilityWebSocketFailOpenWritesNoObservations(t *testing.T) {
	e := newWSEnv(t)
	p, source := enableAvailability(e.env, 1)
	p.fn = failingAvailability(nil)
	c := e.mustDial()
	c.send(create(wsModel, "first"))
	c.until("response.completed")
	rec := e.record()
	require.NotNil(t, rec.Availability)
	require.Equal(t, core.AvailabilityFailOpen, rec.Availability.Outcome)
	source.mu.Lock()
	source.snapshots[11] = &pluginv1.AccountQuotaSnapshot{Generation: 8}
	source.snapshots[12] = &pluginv1.AccountQuotaSnapshot{Generation: 8}
	source.mu.Unlock()
	p.mu.Lock()
	p.fn = nil
	p.mu.Unlock()
	c.send(create(wsModel, "second"))
	c.until("response.completed")
	e.record()
	require.Empty(t, source.generations)

	// The rule itself: a pinned turn writes only under the pinned generation.
	q := &pluginv1.QuotaResult{Windows: []*pluginv1.QuotaWindow{{Key: "scoped", Status: manifest.QuotaRejected}}}
	for _, tc := range []struct {
		pin   int64
		wrote bool
	}{{0, false}, {7, false}, {8, true}} {
		cl := &call{g: e.gw, quotaPinned: true, quotaPin: tc.pin, availability: map[int64]availabilityAdmission{11: {quota: &pluginv1.AccountQuotaSnapshot{Generation: 8}}}}
		before := len(source.generations)
		cl.applyQuotaObservation(context.Background(), 11, q)
		require.Equal(t, tc.wrote, len(source.generations) > before, "pin %d", tc.pin)
	}
}

// Model names are validated before account selection.
func TestModelNameValidation(t *testing.T) {
	for name, model := range map[string]string{
		"space":   "claude bad",
		"tab":     "claude\tbad",
		"control": "claude\u0007bad",
		"nbsp":    "claude bad",
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			r := e.messages(body(model, false))
			require.Equal(t, 400, r.status, string(r.body))
			require.Contains(t, string(r.body), "invalid_request_error")
			require.Empty(t, e.up.keys())
			require.Zero(t, e.plat.buildCount())
			rec := e.record()
			require.Nil(t, rec.AccountID)
			require.Equal(t, errTypeInvalidRequest, rec.ErrorType)
		})
	}
	require.True(t, validModelName(strings.Repeat("m", 64<<10)), "a model name has no length limit")
	require.True(t, validModelName("claude-opus-4.5[1m]"))
	require.True(t, validModelName("模型-1"))
	require.False(t, validModelName("bad\xffutf8"))

	// The path parameter source (e.g. Gemini's :model) is checked as well.
	e := newEnv(t)
	ep := endpointOf(t, builtinPlatform(t, "anthropic"), "anthropic.messages")
	ep.Request.ModelParam, ep.Request.ModelPath = "model", ""
	for _, model := range []string{"bad\xffutf8", "a b", "a\x00b"} {
		cl := &call{g: e.gw, ep: ep, params: map[string]string{"model": model}, rec: &core.UsageRecord{},
			principal: &core.APIKeyPrincipal{Group: core.GroupInfo{ID: testGroup}}}
		failure := cl.checkModel(context.Background())
		require.NotNil(t, failure, "%q", model)
		require.Equal(t, http.StatusBadRequest, failure.Status)
		require.Equal(t, errTypeInvalidRequest, failure.RecordType)
	}
}

// A client that goes away during the precheck ends the request as
// canceled: nothing is admitted and the breaker is not charged.
func TestAvailabilityClientCancelEndsCanceled(t *testing.T) {
	for _, mode := range []string{"plugin", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			p, source := enableAvailability(e, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "plugin" {
				p.fn = func(ctx context.Context, _ *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
					cancel()
					<-ctx.Done()
					return nil, ctx.Err()
				}
			} else {
				source.delay = time.Second
				time.AfterFunc(20*time.Millisecond, cancel)
			}
			cl := availabilityCall(t, e)
			out, failure := cl.filterAvailability(ctx, []core.AccountRef{availabilityRef1})
			require.Empty(t, out)
			require.NotNil(t, failure)
			require.Equal(t, statusClientClosed, failure.Status)
			require.Equal(t, errTypeClientCanceled, failure.RecordType)
			ok, _ := e.gw.availabilityBreakers.allow("anthropic", e.gw.now())
			require.True(t, ok, "a canceled request charged the breaker")
		})
	}
}

// The plugin's budget starts after the snapshot read: a slow read does not
// shorten it.
func TestAvailabilitySnapshotBudgetSeparateFromPlugin(t *testing.T) {
	e := newEnv(t)
	p, source := enableAvailability(e, 0)
	source.delay = 400 * time.Millisecond
	var remaining time.Duration
	p.fn = func(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
		deadline, _ := ctx.Deadline()
		remaining = time.Until(deadline)
		out := &pluginv1.CheckModelAvailabilityResponse{}
		for _, v := range in.Candidates {
			out.Results = append(out.Results, &pluginv1.ModelAvailability{AccountId: v.AccountId, Model: v.Model, State: pluginv1.ModelAvailability_AVAILABLE})
		}
		return out, nil
	}
	cl := availabilityCall(t, e)
	_, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
	require.Nil(t, failure)
	require.Greater(t, remaining, availabilityPluginTimeout*8/10)
	require.Len(t, source.reads, 1, "the slow read still finished within its budget")
}

// The trace is bounded: samples, count keys, guard samples, and the text
// it copies from plugins and clients.
func TestAvailabilityTraceBounds(t *testing.T) {
	var entries []availabilityEntry
	for i := range 25 {
		entries = append(entries, availabilityEntry{id: int64(i + 1), plugin: "p",
			v: availabilityVerdict{blocked: true, code: availReasonUnavailable, detail: "r" + string(rune('a'+i)), model: "m", retryAt: 100}})
	}
	tr := buildAvailabilityTrace(entries, 30, 5, 100)
	require.Equal(t, core.AvailabilityPartial, tr.Outcome)
	require.Len(t, tr.Samples, availTraceSamples)
	require.Equal(t, 5, tr.Omitted)
	require.LessOrEqual(t, len(tr.Counts), availTraceCountKeys)
	total := 0
	for _, n := range tr.Counts {
		total += n
	}
	require.Equal(t, 25, total)
	require.Equal(t, 25-(availTraceCountKeys-1), tr.Counts[availCountOther])

	// Every reason keeps a sample even when another fills the list.
	entries = nil
	for i := range 30 {
		code := availReasonUnknown
		if i >= 28 {
			code = availReasonSnapshot
		}
		entries = append(entries, availabilityEntry{id: int64(i + 1), plugin: "p", v: availabilityVerdict{code: code}})
	}
	tr = buildAvailabilityTrace(entries, 30, 30, 0)
	require.Equal(t, core.AvailabilityFailOpen, tr.Outcome)
	var reasons []string
	for _, s := range tr.Samples {
		reasons = append(reasons, s.Reason)
	}
	require.Contains(t, reasons, availReasonSnapshot)
	require.Equal(t, 10, tr.Omitted)

	// Plugin reasons and models are cut and cleaned.
	long := strings.Repeat("x", 30) + "\n\x00\u0085" + strings.Repeat("é", 40)
	tr = buildAvailabilityTrace([]availabilityEntry{{id: 1, plugin: "p", v: availabilityVerdict{blocked: true, code: availReasonUnavailable, detail: long, model: strings.Repeat("m", 150)}}}, 1, 0, 0)
	require.Equal(t, core.AvailabilityBlockedAll, tr.Outcome)
	s := tr.Samples[0]
	require.LessOrEqual(t, len(s.Reason), len(availReasonUnavailable)+1+availReasonBytes)
	require.NotContains(t, s.Reason, "\n")
	require.NotContains(t, s.Reason, "\x00")
	require.True(t, strings.HasPrefix(s.Reason, "unavailable:xxx"))
	require.Len(t, s.Model, availModelBytes)

	// No entries, no trace.
	require.Nil(t, buildAvailabilityTrace(nil, 3, 3, 0))

	// Guard samples append to the precheck's trace, at most five.
	cl := &call{rec: &core.UsageRecord{Attempts: 2}}
	cl.setAvailabilityTrace(buildAvailabilityTrace(entries[:1], 3, 3, 0))
	for range 7 {
		cl.appendGuardSample(core.AvailabilitySample{AccountID: 9, Decision: core.AvailabilityBlock, Reason: availReasonUnavailable})
	}
	require.Len(t, cl.rec.Availability.Guard, availTraceGuard)
	require.Equal(t, 2, cl.rec.Availability.Guard[0].Attempt)
	require.Equal(t, core.AvailabilityFailOpen, cl.rec.Availability.Outcome)
	raw := cl.rec.Anomalies()[core.AnomalyAvailability]
	var back core.AvailabilityTrace
	require.NoError(t, json.Unmarshal([]byte(raw), &back))
	require.Equal(t, 1, back.V)
	require.Len(t, back.Guard, availTraceGuard)

	// A guard trace without precheck entries keeps its guard samples when
	// a later precheck (none here) records nothing.
	cl = &call{rec: &core.UsageRecord{}, availabilityChecked: 3, availabilityAdmitted: 3}
	cl.appendGuardSample(core.AvailabilitySample{AccountID: 9, Decision: core.AvailabilityBlock, Reason: availReasonIdentity})
	require.Equal(t, core.AvailabilityPass, cl.rec.Availability.Outcome)
	require.Equal(t, 3, cl.rec.Availability.Candidates)
	cl.setAvailabilityTrace(nil)
	require.Len(t, cl.rec.Availability.Guard, 1)
}

// The model mismatch check keeps blocking and says why.
func TestAvailabilityMismatchRecordsReason(t *testing.T) {
	e := newEnv(t)
	cl := &call{g: e.gw, rec: &core.UsageRecord{Attempts: 1}}
	ref := availabilityRef1
	failure := cl.availabilityMismatch(context.Background(), &ref, "up-model", "other-model")
	require.Equal(t, http.StatusServiceUnavailable, failure.Status)
	require.Equal(t, errTypeModelAvailability, failure.RecordType)
	g := cl.rec.Availability.Guard
	require.Len(t, g, 1)
	require.Equal(t, availReasonModelMismatch, g[0].Reason)
	require.Equal(t, "other-model", g[0].Model)
}

func TestWarnSampler(t *testing.T) {
	var s warnSampler
	t0 := time.Unix(1000, 0)
	ok, n := s.allow("k", t0)
	require.True(t, ok)
	require.Zero(t, n)
	ok, _ = s.allow("k", t0.Add(time.Second))
	require.False(t, ok)
	ok, _ = s.allow("k", t0.Add(59*time.Second))
	require.False(t, ok)
	ok, _ = s.allow("other", t0.Add(time.Second))
	require.True(t, ok, "keys are independent")
	ok, n = s.allow("k", t0.Add(61*time.Second))
	require.True(t, ok)
	require.Equal(t, 2, n)
	for i := range warnSamplerKeys {
		s.allow(string(rune(0x4e00+i)), t0)
	}
	require.LessOrEqual(t, len(s.keys), warnSamplerKeys)
	ok, _ = s.allow("k", t0.Add(62*time.Second))
	require.True(t, ok, "a full sampler forgets every key")
}

type captureHandler struct {
	mu    sync.Mutex
	level slog.Level
	buf   bytes.Buffer
}

func (h *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		h.buf.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	h.buf.WriteString("\n")
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }
func (h *captureHandler) lines(substr string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, l := range strings.Split(h.buf.String(), "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

// Fail-open and all-blocked WARNs are merged per key and throttled.
func TestAvailabilityWarnLogsThrottled(t *testing.T) {
	h := &captureHandler{level: slog.LevelInfo}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	e := newEnv(t)
	p, source := enableAvailability(e, 0)
	now := time.Unix(50_000, 0)
	e.gw.now = func() time.Time { return now }
	p.fn = func(context.Context, *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
		return nil, errors.New("plugin down")
	}
	for range 3 {
		_, failure := availabilityCall(t, e).filterAvailability(context.Background(), []core.AccountRef{availabilityRef1, {ID: 2, PluginKey: "anthropic", Type: "apikey"}})
		require.Nil(t, failure)
	}
	lines := h.lines("model availability check failed open")
	require.Len(t, lines, 1, "one merged line for both accounts and three requests")
	require.Contains(t, lines[0], "accounts=2")
	require.Contains(t, lines[0], "reason="+availReasonPluginError)
	now = now.Add(warnSamplerWindow + time.Second)
	_, _ = availabilityCall(t, e).filterAvailability(context.Background(), []core.AccountRef{availabilityRef1})
	lines = h.lines("model availability check failed open")
	require.Len(t, lines, 2)
	require.Contains(t, lines[1], "suppressed=2")

	p.fn = nil
	source.deny(1, 2)
	now = now.Add(availabilityBreakerOpen + time.Second) // past the breaker the failures opened
	for range 2 {
		cl := availabilityCall(t, e)
		cl.principal = &core.APIKeyPrincipal{Group: core.GroupInfo{ID: testGroup}}
		_, failure := cl.filterAvailability(context.Background(), []core.AccountRef{availabilityRef1, {ID: 2, PluginKey: "anthropic", Type: "apikey"}})
		require.NotNil(t, failure)
		require.Equal(t, http.StatusTooManyRequests, failure.Status)
	}
	blocked := h.lines("no candidate passed model availability")
	require.Len(t, blocked, 1)
	require.Contains(t, blocked[0], "account=1 reason=unavailable")
}

// A batch left unsent because earlier batches spent the shared budget is not
// a plugin failure: it fails open without opening the breaker.
func TestAvailabilityUnaskedBatchDoesNotOpenBreaker(t *testing.T) {
	e := newEnv(t)
	p, _ := enableAvailability(e, 0)
	e.gw.availabilityPluginTimeout = 50 * time.Millisecond
	slow := true
	p.fn = func(ctx context.Context, in *pluginv1.CheckModelAvailabilityRequest) (*pluginv1.CheckModelAvailabilityResponse, error) {
		if slow {
			<-ctx.Done() // spend the budget, then answer the batch anyway
		}
		out := &pluginv1.CheckModelAvailabilityResponse{}
		for _, c := range in.Candidates {
			out.Results = append(out.Results, &pluginv1.ModelAvailability{AccountId: c.AccountId, Model: c.Model, State: pluginv1.ModelAvailability_AVAILABLE})
		}
		return out, nil
	}
	refs := make([]core.AccountRef, 140)
	for i := range refs {
		refs[i] = core.AccountRef{ID: int64(i + 1), PluginKey: "anthropic", Type: "apikey"}
	}
	cl := availabilityCall(t, e)
	out, failure := cl.filterAvailability(context.Background(), refs)
	require.Nil(t, failure)
	require.Len(t, out, 140)
	require.Len(t, p.calls, 1, "the second batch was never sent")
	require.Equal(t, availReasonPluginTimeout, cl.availability[140].reason)
	slow = false
	cl = availabilityCall(t, e)
	_, failure = cl.filterAvailability(context.Background(), refs)
	require.Nil(t, failure)
	require.Len(t, p.calls, 3, "breaker still closed: both batches asked")
	require.Empty(t, cl.availability[140].reason)
}
