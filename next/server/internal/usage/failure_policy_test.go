package usage

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func TestFailurePolicyTracePersistsWithoutAnomaly(t *testing.T) {
	f := newFixture(t)
	r := f.record("policy-trace", true)
	r.FailurePolicy = `[{"attempt":1,"action":"switch_reset","reason":"retry_scheduled"}]`
	f.svc.process(t.Context(), []*core.UsageRecord{r})
	require.Equal(t, r.FailurePolicy, f.scalar(`SELECT anomalies->>'failure_policy' FROM usage_logs WHERE request_id='policy-trace'`))
	rows := f.get(f.user, "/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	require.Len(t, rows, 1)
	require.Equal(t, float64(0), rows[0].(map[string]any)["anomalies"])
}

// traceOf decodes an anomalies value of a usage detail: a JSON string
// holding JSON.
func traceOf(t *testing.T, d map[string]any, key string, into any) {
	t.Helper()
	anomalies, ok := d["anomalies"].(map[string]any)
	require.True(t, ok, "anomalies: %v", d["anomalies"])
	raw, ok := anomalies[key].(string)
	require.True(t, ok, "%s: %v", key, anomalies[key])
	require.NoError(t, json.Unmarshal([]byte(raw), into))
}

// The availability trace is stored in anomalies, is no anomaly for the
// summary, is hidden from self-service, loses every account id (its own
// and the failure policy's) for an operator without account:read, and
// gets account names for one with it (MODEL-AVAILABILITY-PRECHECK-DESIGN
// §6.1, §6.2).
func TestAvailabilityTraceDetailAndRedaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	var a1, a2 int64
	require.NoError(t, f.db.SQL.QueryRowContext(ctx, `INSERT INTO accounts(name,plugin_key,type,credentials_enc) VALUES('Fable One','anthropic','apikey','') RETURNING id`).Scan(&a1))
	require.NoError(t, f.db.SQL.QueryRowContext(ctx, `INSERT INTO accounts(name,plugin_key,type,credentials_enc) VALUES('Fable Two','anthropic','apikey','') RETURNING id`).Scan(&a2))
	r := f.record("availability-trace", true)
	r.AccountID = &a2
	r.Availability = &core.AvailabilityTrace{V: 1, Outcome: core.AvailabilityPartial, Candidates: 3, Admitted: 2,
		Counts: map[string]int{"unavailable:quota_rejected:7d_fable": 1, "plugin_error": 1}, RetryAt: 1_900_000_000,
		Samples: []core.AvailabilitySample{
			{AccountID: a1, Plugin: "anthropic", Model: "claude-fable-5", Decision: core.AvailabilityBlock, Reason: "unavailable:quota_rejected:7d_fable", RetryAt: 1_900_000_000},
			{Plugin: "anthropic", Decision: core.AvailabilityFailOpen, Reason: "plugin_error", Batch: true},
		},
		Guard: []core.AvailabilitySample{{AccountID: a2, Plugin: "anthropic", Decision: core.AvailabilityBlock, Reason: "guard_identity_changed", Attempt: 1}},
	}
	r.FailurePolicy = `[{"revision":1,"attempt":1,"account_id":` + strconv.FormatInt(a2, 10) + `,"source":"core","status":503,"action":"switch","account_action":"none","reason":"model_availability","account_result":"unchanged"}]`
	f.svc.process(ctx, []*core.UsageRecord{r})

	var stored core.AvailabilityTrace
	require.NoError(t, json.Unmarshal([]byte(f.scalar(`SELECT anomalies->>'availability' FROM usage_logs WHERE request_id='availability-trace'`)), &stored))
	require.Equal(t, *r.Availability, stored)
	require.Empty(t, stored.Samples[0].AccountName, "names are never stored")

	rows := f.get(f.user, "/usage/summary?group_by=model&from=2026-09-01T00:00:00Z", 200)["data"].([]any)
	require.Len(t, rows, 1)
	require.Equal(t, float64(0), rows[0].(map[string]any)["anomalies"], "a trace is not an anomaly")

	id := f.scalar(`SELECT id::text FROM usage_logs WHERE request_id='availability-trace'`)

	// Self-service: no anomalies at all.
	self := f.get(f.user, "/me/usage/"+id, 200)["data"].(map[string]any)
	require.NotContains(t, self, "anomalies")

	// Operator with account:read: ids kept, names filled in.
	d := f.get(f.user, "/usage/"+id, 200)["data"].(map[string]any)
	var tr core.AvailabilityTrace
	traceOf(t, d, core.AnomalyAvailability, &tr)
	require.Equal(t, a1, tr.Samples[0].AccountID)
	require.Equal(t, "Fable One", tr.Samples[0].AccountName)
	require.True(t, tr.Samples[1].Batch)
	require.Zero(t, tr.Samples[1].AccountID)
	require.Equal(t, "Fable Two", tr.Guard[0].AccountName)
	var events []map[string]any
	traceOf(t, d, "failure_policy", &events)
	require.Equal(t, float64(a2), events[0]["account_id"])

	// Operator without account:read: samples stay, account ids go, in the
	// availability trace and in the failure policy events alike.
	f.svc.Authz = denyPerms{keys: []string{"account:read"}}
	d = f.get(f.user, "/usage/"+id, 200)["data"].(map[string]any)
	require.Nil(t, d["account_id"])
	tr = core.AvailabilityTrace{}
	traceOf(t, d, core.AnomalyAvailability, &tr)
	require.Len(t, tr.Samples, 2)
	require.Len(t, tr.Guard, 1)
	for _, s := range append(append([]core.AvailabilitySample(nil), tr.Samples...), tr.Guard...) {
		require.Zero(t, s.AccountID)
		require.Empty(t, s.AccountName)
	}
	require.False(t, tr.Samples[0].Batch, "an account sample stays distinguishable from a batch one")
	require.True(t, tr.Samples[1].Batch)
	require.Equal(t, "unavailable:quota_rejected:7d_fable", tr.Samples[0].Reason)
	require.Equal(t, "claude-fable-5", tr.Samples[0].Model)
	require.Equal(t, int64(1_900_000_000), tr.Samples[0].RetryAt)
	require.Equal(t, r.Availability.Counts, tr.Counts)
	events = nil
	traceOf(t, d, "failure_policy", &events)
	require.Len(t, events, 1)
	require.NotContains(t, events[0], "account_id")
	require.Equal(t, "model_availability", events[0]["reason"])
}

// A trace that cannot be parsed is not shown to an operator without
// account:read: it could carry account ids.
func TestShapeAnomaliesDropsUnreadableTraces(t *testing.T) {
	t.Parallel()
	s := &Service{}
	raw := json.RawMessage(`{"availability":"{not json","failure_policy":"[{\"account_id\":5","usage_extract":"plugin"}`)
	out := s.shapeAnomalies(t.Context(), raw, false)
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	require.Equal(t, map[string]any{"usage_extract": "plugin"}, m)
	require.JSONEq(t, string(raw), string(s.shapeAnomalies(t.Context(), raw, true)))
	require.Nil(t, s.shapeAnomalies(t.Context(), json.RawMessage(`[`), false))
	plain := json.RawMessage(`{"usage_extract":"fallback"}`)
	require.Equal(t, plain, s.shapeAnomalies(t.Context(), plain, false))
}

// The scheduling trace keeps its skips for an operator without account:read,
// without the accounts; one that cannot be parsed is dropped for them.
func TestShapeAnomaliesRedactsSchedulingTrace(t *testing.T) {
	t.Parallel()
	s := &Service{}
	rec := &core.UsageRecord{Scheduling: &core.SchedulingTrace{V: 1, Outcome: core.SchedulingNone, Candidates: 2,
		Counts:  map[string]int{core.SchedModelNotServed: 1, core.SchedNotSchedulable: 1},
		Skipped: []core.SchedulingSkip{{AccountID: 7, Reason: core.SchedModelNotServed, Detail: "m"}, {AccountID: 8, Reason: core.SchedNotSchedulable, Detail: "cooldown", Until: 1_900_000_000}}}}
	raw, err := json.Marshal(rec.Anomalies())
	require.NoError(t, err)
	var m map[string]string
	require.NoError(t, json.Unmarshal(s.shapeAnomalies(t.Context(), raw, false), &m))
	var tr core.SchedulingTrace
	require.NoError(t, json.Unmarshal([]byte(m[core.AnomalyScheduling]), &tr))
	require.Len(t, tr.Skipped, 2)
	require.Equal(t, core.SchedulingSkip{Reason: core.SchedModelNotServed, Detail: "m"}, tr.Skipped[0])
	require.Equal(t, core.SchedulingSkip{Reason: core.SchedNotSchedulable, Detail: "cooldown", Until: 1_900_000_000}, tr.Skipped[1])
	require.Equal(t, rec.Scheduling.Counts, tr.Counts)

	broken := json.RawMessage(`{"scheduling":"{not json"}`)
	require.JSONEq(t, `{}`, string(s.shapeAnomalies(t.Context(), broken, false)))
	require.JSONEq(t, string(broken), string(s.shapeAnomalies(t.Context(), broken, true)))
}
