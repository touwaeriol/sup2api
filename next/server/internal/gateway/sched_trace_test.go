package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func skipOf(tr *core.SchedulingTrace, id int64, reason string) *core.SchedulingSkip {
	if tr == nil {
		return nil
	}
	for i := range tr.Skipped {
		if tr.Skipped[i].AccountID == id && tr.Skipped[i].Reason == reason {
			return &tr.Skipped[i]
		}
	}
	return nil
}

// A request no account could serve records why each account of the group was
// not used: the client only reads "no available account".
func TestSchedulingTraceNoAccount(t *testing.T) {
	t.Run("disabled and cooling accounts", func(t *testing.T) {
		e := newEnv(t)
		until := time.Now().Add(time.Minute)
		e.accounts.disabled[1] = "fixture"
		e.accounts.cooldown[2], e.accounts.cooldown[3] = until, until
		if r := e.messages(body(testModel, false)); r.status != 529 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		tr := e.record().Scheduling
		if tr == nil || tr.V != 1 || tr.Outcome != core.SchedulingNone || tr.Candidates != 0 || tr.Eligible != 0 || tr.Counts[core.SchedNotSchedulable] != 3 {
			t.Fatalf("trace %+v", tr)
		}
		if s := skipOf(tr, 1, core.SchedNotSchedulable); s == nil || s.Detail != "disabled" || s.Until != 0 {
			t.Fatalf("disabled account: %+v", tr.Skipped)
		}
		if s := skipOf(tr, 2, core.SchedNotSchedulable); s == nil || s.Detail != "cooldown" || s.Until != until.Unix() {
			t.Fatalf("cooling account: %+v", tr.Skipped)
		}
	})
	t.Run("model lists", func(t *testing.T) {
		e := newEnv(t)
		for _, id := range []int64{1, 2, 3} {
			e.accounts.set(id, func(a *core.Account) { a.Models = []string{"another-model"} })
		}
		if r := e.messages(body(testModel, false)); r.status != 503 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		rec := e.record()
		tr := rec.Scheduling
		if tr == nil || tr.Outcome != core.SchedulingNone || tr.Candidates != 3 || tr.Eligible != 0 || tr.Counts[core.SchedModelNotServed] != 3 {
			t.Fatalf("trace %+v", tr)
		}
		if s := skipOf(tr, 2, core.SchedModelNotServed); s == nil || s.Detail != testModel {
			t.Fatalf("skips %+v", tr.Skipped)
		}
		// The trace is stored under anomalies.scheduling as a JSON string.
		var stored core.SchedulingTrace
		if raw := rec.Anomalies()[core.AnomalyScheduling]; json.Unmarshal([]byte(raw), &stored) != nil || len(stored.Skipped) != 3 {
			t.Fatalf("anomalies %q", raw)
		}
	})
	t.Run("referenced model", func(t *testing.T) {
		e := pricedAdvisorEnv(t)
		// Every account serves the primary model; none also serves the model
		// the request falls back to.
		for _, id := range []int64{1, 2, 3} {
			e.accounts.set(id, func(a *core.Account) { a.Models = []string{testModel} })
		}
		if r := e.messages(fallbackHTTPBody(false)); r.status != 503 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		tr := e.record().Scheduling
		if tr == nil || tr.Outcome != core.SchedulingNone || tr.Candidates != 3 || tr.Eligible != 0 {
			t.Fatalf("trace %+v", tr)
		}
		if s := skipOf(tr, 1, core.SchedReferencedModelNotServed); s == nil || s.Detail != advisorModel {
			t.Fatalf("skips %+v", tr.Skipped)
		}
	})
}

// Rate-limited accounts answer 429 with outcome "busy"; an account passed
// over for its rate limit on the way to the one that served is recorded too,
// and a request nothing was passed over for records nothing.
func TestSchedulingTracePassingReasons(t *testing.T) {
	e := newEnv(t)
	lim := newFakeLimiter()
	e.gw.d.Limiter = lim
	for _, id := range []int64{1, 2, 3} {
		e.accounts.set(id, func(a *core.Account) { a.RPMLimit = 1 })
	}
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if rec := e.record(); rec.Scheduling != nil || rec.Anomalies()[core.AnomalyScheduling] != "" {
		t.Fatalf("a plain request records no trace: %+v", rec.Scheduling)
	}

	lim.exhausted[1] = true
	if r := e.messages(body(testModel, false)); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	rec := e.record()
	if tr := rec.Scheduling; tr == nil || tr.Outcome != core.SchedulingPicked || tr.Candidates != 3 || tr.Eligible != 3 ||
		skipOf(tr, 1, core.SchedRateLimited) == nil || len(tr.Skipped) != 1 || rec.AccountID == nil || *rec.AccountID != 2 {
		t.Fatalf("picked after a rate-limited account: %+v account %v", tr, rec.AccountID)
	}

	lim.exhausted[2], lim.exhausted[3] = true, true
	if r := e.messages(body(testModel, false)); r.status != 429 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if tr := e.record().Scheduling; tr == nil || tr.Outcome != core.SchedulingBusy || tr.Counts[core.SchedRateLimited] != 3 {
		t.Fatalf("busy: %+v", tr)
	}
}

// The trace is bounded: skips past the cap are counted, not listed.
func TestSchedulingTraceBounds(t *testing.T) {
	c := &call{rec: &core.UsageRecord{ErrorType: errTypeNoAccount, StatusCode: 529}}
	for id := int64(1); id <= maxSchedSkips+5; id++ {
		c.skipAccount(id, core.SchedModelNotServed, "m")
		c.skipAccount(id, core.SchedModelNotServed, "again") // once per account and reason
	}
	c.sched.unschedulable = []core.UnschedulableAccount{{ID: 999, Reason: "disabled"}}
	tr := c.schedulingTrace()
	if len(tr.Skipped) != maxSchedSkips || tr.Omitted != 6 || tr.Counts[core.SchedModelNotServed] != maxSchedSkips || tr.Skipped[0].Detail != "m" {
		t.Fatalf("trace %d skipped, %d omitted, counts %v", len(tr.Skipped), tr.Omitted, tr.Counts)
	}
}
