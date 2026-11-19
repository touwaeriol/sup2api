package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// ---------------------------------------------------------------- unit tests (no database)

func tp(t time.Time) *time.Time { return &t }

func TestQuotaView(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	decl := &manifest.AccountQuota{Windows: []manifest.QuotaWindowDefinition{{Key: "zz", Label: manifest.LocalizedText{"en": "Custom"}, ShortLabel: manifest.LocalizedText{"en": "C"}}, {Key: "7d"}, {Key: "5h"}}, Headers: []manifest.QuotaHeader{{Key: "zz", Status: "x"}, {Key: "5h", Status: "y"}}}
	if v := quotaView(decl, nil, now); !v.Supported || v.Source != "" || v.UpdatedAt != nil || v.Windows == nil || len(v.Windows) != 0 {
		t.Fatalf("no snapshot: %+v", v)
	}
	// A row created by a query claim has no data yet.
	if v := quotaView(decl, &store.QuotaSnapshot{Source: "", Error: "transient: 503", Windows: map[string]store.QuotaWindow{}}, now); v.Source != "" || v.Error != "transient: 503" {
		t.Fatalf("claim-only row: %+v", v)
	}
	snap := &store.QuotaSnapshot{Source: store.QuotaPassive, UpdatedAt: tp(now.Add(-time.Minute)), Windows: map[string]store.QuotaWindow{
		"aa":       {Utilization: 1},
		"zz":       {Utilization: 2},
		"7d_fable": {Utilization: 3, ResetsAt: tp(now.Add(time.Hour)), Status: "allowed"},
		"7d":       {Utilization: 99, ResetsAt: tp(now.Add(-time.Second)), Status: "rejected", Used: 5, Limit: 10},
		"5h":       {Utilization: 42, ResetsAt: tp(now.Add(time.Hour)), Status: "allowed_warning"},
	}}
	v := quotaView(decl, snap, now)
	var keys []string
	for _, w := range v.Windows {
		keys = append(keys, w.Key)
	}
	// Known keys first, then the declaration order, then the alphabet.
	if fmt.Sprint(keys) != "[zz 7d 5h 7d_fable aa]" {
		t.Fatalf("order: %v", keys)
	}
	if w := v.Windows[1]; w.Utilization != 99 || w.ResetsAt == nil || w.Status != "rejected" || w.Used != 5 || w.Limit != 10 || !w.Expired {
		t.Fatalf("expired window is not shown as reset: %+v", w)
	}
	if w := v.Windows[2]; w.Utilization != 42 || w.Status != "allowed_warning" || !w.ResetsAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("5h: %+v", w)
	}
	raw, _ := json.Marshal(v)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	for _, f := range []string{"supported", "source", "updated_at", "error", "windows"} {
		if _, ok := back[f]; !ok {
			t.Fatalf("field %s missing: %s", f, raw)
		}
	}
	w0 := back["windows"].([]any)[1].(map[string]any)
	if v.Windows[0].Label["en"] != "Custom" || v.Windows[0].ShortLabel["en"] != "C" {
		t.Fatalf("labels: %+v", v.Windows[0])
	}
	if _, ok := w0["resets_at"]; !ok || w0["resets_at"] == nil || w0["status"] != "rejected" || w0["expired"] != true {
		t.Fatalf("window json: %s", raw)
	}
}

func TestQuotaExpiredObservationIsNotRecovery(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	v := quotaView(&manifest.AccountQuota{Query: true}, &store.QuotaSnapshot{UpdatedAt: &past, Windows: map[string]store.QuotaWindow{
		"daily": {Utilization: 100, Status: "rejected", ResetsAt: &past, Used: 10, Limit: 10},
	}}, now)
	if len(v.Windows) != 1 || v.Windows[0].Utilization != 100 || v.Windows[0].Status != "rejected" || v.Windows[0].ResetsAt == nil || v.Windows[0].Used != 10 {
		t.Fatalf("expiry fabricated recovery: %+v", v.Windows)
	}
}

func TestShouldQueryQuota(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { return tp(now.Add(-d)) }
	cases := []struct {
		name  string
		snap  *store.QuotaSnapshot
		force bool
		want  bool
	}{
		{"no snapshot", nil, false, true},
		{"fresh passive", &store.QuotaSnapshot{UpdatedAt: ago(time.Minute)}, false, false},
		{"fresh passive, forced", &store.QuotaSnapshot{UpdatedAt: ago(time.Minute)}, true, true},
		{"fresh for 30 minutes", &store.QuotaSnapshot{UpdatedAt: ago(29 * time.Minute)}, false, false},
		{"stale", &store.QuotaSnapshot{UpdatedAt: ago(30 * time.Minute)}, false, true},
		{"floor", &store.QuotaSnapshot{UpdatedAt: ago(time.Hour), LastActiveAt: ago(50 * time.Second)}, false, false},
		{"after the floor", &store.QuotaSnapshot{UpdatedAt: ago(time.Hour), LastActiveAt: ago(time.Minute)}, false, true},
		{"earlier saved schedule", &store.QuotaSnapshot{NextCheckAt: ago(time.Minute), LastActiveAt: ago(50 * time.Second)}, false, false},
		{"due after the floor", &store.QuotaSnapshot{NextCheckAt: ago(time.Second), LastActiveAt: ago(time.Minute)}, false, true},
		{"not due yet", &store.QuotaSnapshot{NextCheckAt: ago(-time.Minute), LastActiveAt: ago(time.Hour)}, false, false},
		{"floor, forced", &store.QuotaSnapshot{UpdatedAt: ago(time.Minute), LastActiveAt: ago(10 * time.Second)}, true, true},
		{"future timestamp, forced", &store.QuotaSnapshot{LastActiveAt: ago(-time.Hour)}, true, true},
		{"within automatic floor, forced", &store.QuotaSnapshot{UpdatedAt: ago(time.Minute), LastActiveAt: ago(31 * time.Second)}, true, true},
		{"negative cache", &store.QuotaSnapshot{Error: "x", UpdatedAt: ago(10 * time.Minute), LastActiveAt: ago(40 * time.Second)}, false, false},
		{"negative cache, forced", &store.QuotaSnapshot{Error: "x", UpdatedAt: ago(10 * time.Minute), LastActiveAt: ago(40 * time.Second)}, true, true},
		{"negative cache remains fresh", &store.QuotaSnapshot{Error: "x", LastActiveAt: ago(29 * time.Minute)}, false, false},
		{"negative cache expired", &store.QuotaSnapshot{Error: "x", LastActiveAt: ago(30 * time.Minute)}, false, true},
	}
	for _, c := range cases {
		if got := shouldQueryQuota(c.snap, c.force, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

type savedQuota struct {
	id int64
	w  map[string]store.QuotaWindow
}

func recorderForTest() (*quotaRecorder, *[]savedQuota, *sync.Mutex) {
	var mu sync.Mutex
	var saved []savedQuota
	r := newQuotaRecorder(func(_ context.Context, id int64, w map[string]store.QuotaWindow) error {
		mu.Lock()
		saved = append(saved, savedQuota{id, w})
		mu.Unlock()
		return nil
	})
	return r, &saved, &mu
}

func TestQuotaRecorderThrottle(t *testing.T) {
	r, saved, _ := recorderForTest()
	t0 := time.Now()
	r.observe(1, map[string]store.QuotaWindow{"5h": {Utilization: 10, Status: "allowed"}}, t0)
	// The first sample is due at once.
	if got := r.take(t0, false); len(got) != 1 || got[1]["5h"].Utilization != 10 {
		t.Fatalf("first take: %v", got)
	}
	// Samples within the interval are merged, not written...
	r.observe(1, map[string]store.QuotaWindow{"5h": {Utilization: 11, Status: "allowed"}}, t0.Add(time.Second))
	r.observe(1, map[string]store.QuotaWindow{"7d": {Utilization: 3, Status: "allowed"}}, t0.Add(2*time.Second))
	r.observe(1, map[string]store.QuotaWindow{"5h": {Utilization: 12, Status: "allowed"}}, t0.Add(3*time.Second))
	if got := r.take(t0.Add(3*time.Second), false); len(got) != 0 {
		t.Fatalf("throttled take: %v", got)
	}
	// ...until the interval has passed: the last value of each window wins.
	got := r.take(t0.Add(quotaPassiveInterval+time.Second), false)
	if len(got) != 1 || got[1]["5h"].Utilization != 12 || got[1]["7d"].Utilization != 3 {
		t.Fatalf("trailing take: %v", got)
	}
	// A status change is written at once.
	t1 := t0.Add(quotaPassiveInterval + 2*time.Second)
	r.observe(1, map[string]store.QuotaWindow{"5h": {Utilization: 100, Status: "rejected"}}, t1)
	if got := r.take(t1, false); len(got) != 1 || got[1]["5h"].Status != "rejected" {
		t.Fatalf("status change take: %v", got)
	}
	// Shutdown writes what is pending regardless of the interval.
	r.observe(1, map[string]store.QuotaWindow{"5h": {Utilization: 100, Status: "rejected"}}, t1.Add(time.Second))
	r.flush(context.Background(), true)
	if len(*saved) != 1 || (*saved)[0].w["5h"].Utilization != 100 {
		t.Fatalf("flush all: %v", *saved)
	}
	// Idle accounts are forgotten.
	r.take(t1.Add(quotaIdle+time.Minute), false)
	if len(r.entries) != 0 {
		t.Fatalf("idle entry kept: %v", r.entries)
	}
}

// The writer goroutine picks up a due sample through the kick channel.
func TestQuotaRecorderRun(t *testing.T) {
	r, saved, mu := recorderForTest()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.run(ctx); close(done) }()
	r.observe(7, map[string]store.QuotaWindow{"5h": {Utilization: 1}}, time.Now())
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(*saved)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sample not written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestObserveQuotaHeaders(t *testing.T) {
	s := New(Deps{})
	var saved *[]savedQuota
	s.quota, saved, _ = recorderForTest()
	decls := []manifest.QuotaHeader{{Key: "5h", Utilization: "anthropic-ratelimit-unified-5h-utilization",
		Reset: "anthropic-ratelimit-unified-5h-reset", Status: "anthropic-ratelimit-unified-5h-status"}}
	reset := time.Now().Add(time.Hour).Unix()
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-5h-utilization", "0.25")
	h.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset, 10))
	h.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	for id, status := range map[int64]int{1: 200, 2: 429, 3: 500, 4: 401, 5: 101} {
		s.ObserveQuotaHeaders(id, decls, status, h)
	}
	s.ObserveQuotaHeaders(6, nil, 200, h)
	s.ObserveQuotaHeaders(7, decls, 200, http.Header{})
	s.quota.flush(context.Background(), true)
	ids := map[int64]store.QuotaWindow{}
	for _, sv := range *saved {
		ids[sv.id] = sv.w["5h"]
	}
	if len(ids) != 3 || ids[1].Utilization != 25 || ids[2].ResetsAt.Unix() != reset || ids[5].Status != "allowed" {
		t.Fatalf("observed: %+v", ids)
	}
}

func TestValidQuotaKey(t *testing.T) {
	for k, want := range map[string]bool{"5h": true, "7d_fable": true, "": false, "_a": false, "A": false, "a-b": false,
		"abcdefghijabcdefghijabcdefghij12": true, "abcdefghijabcdefghijabcdefghij123": false} {
		if validQuotaKey(k) != want {
			t.Errorf("validQuotaKey(%q) = %v", k, !want)
		}
	}
}

// ---------------------------------------------------------------- API (database)

// quotaUpstream answers GET /quota with the 5h utilization in pct, or
// failStatus when set, and counts the calls.
type quotaUpstream struct {
	srv        *httptest.Server
	calls      atomic.Int32
	pct        atomic.Int64
	failStatus atomic.Int32
}

func newQuotaUpstream(t *testing.T) *quotaUpstream {
	u := &quotaUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.calls.Add(1)
		if s := u.failStatus.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		fmt.Fprintf(w, `{"5h": %d, "reset": %d}`, u.pct.Load(), time.Now().Add(time.Hour).Unix())
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// withQuota declares quota on the anthropic/apikey type of the harness.
func (e *env) withQuota(q *manifest.AccountQuota) {
	e.gen.types[0].Type.Quota = q
}

func TestAccountQuotaAPI(t *testing.T) {
	e := setup(t)
	up := newQuotaUpstream(t)
	e.plat.quotaURL = up.srv.URL + "/quota"
	up.pct.Store(40)
	id := e.mkRawAccount("anthropic", "apikey", "sub", "sk-good-key-123", true)
	relay := e.mkRawAccount("relay", "relay_key", "relay", "sk-good-key-123", true)
	path := fmt.Sprintf("/accounts/%d/quota", id)

	// A type without quota: not supported, nothing asked, quota null in lists.
	code, out := e.do("GET", path, nil)
	if d := out["data"].(map[string]any); code != 200 || d["supported"] != false || up.calls.Load() != 0 {
		t.Fatalf("unsupported: %d %v", code, out)
	}
	// Without automatic refresh a read never asks the upstream; only a
	// manual refresh does (CONTRACTS §70).
	e.withQuota(&manifest.AccountQuota{Query: true, Headers: []manifest.QuotaHeader{{Key: "5h", Status: "x-status"}}})
	code, out = e.do("GET", path, nil)
	if d := out["data"].(map[string]any); code != 200 || d["supported"] != true || up.calls.Load() != 0 {
		t.Fatalf("query-only read: %d %v (calls %d)", code, out, up.calls.Load())
	}
	e.withQuota(&manifest.AccountQuota{Query: true, AutoRefresh: true, Headers: []manifest.QuotaHeader{{Key: "5h", Status: "x-status"}}})

	// First read queries once; the fresh snapshot is served afterwards.
	code, out = e.do("GET", path, nil)
	d := out["data"].(map[string]any)
	if code != 200 || d["supported"] != true || d["source"] != "active" || up.calls.Load() != 1 {
		t.Fatalf("first: %d %v (calls %d)", code, out, up.calls.Load())
	}
	if w := d["windows"].([]any)[0].(map[string]any); w["key"] != "5h" || w["utilization"].(float64) != 40 || w["status"] != "allowed" {
		t.Fatalf("window: %v", w)
	}
	e.do("GET", path, nil)
	if up.calls.Load() != 1 {
		t.Fatalf("fresh snapshot queried again: %d", up.calls.Load())
	}
	// Completed manual refreshes can run again immediately, including when
	// the snapshot is fresh and the previous active query was seconds ago.
	up.pct.Store(50)
	_, out = e.do("GET", path+"?force=true", nil)
	if up.calls.Load() != 2 || out["data"].(map[string]any)["windows"].([]any)[0].(map[string]any)["utilization"].(float64) != 50 {
		t.Fatalf("first immediate refresh: %v calls=%d", out, up.calls.Load())
	}
	up.pct.Store(55)
	_, out = e.do("GET", path+"?force=true", nil)
	if up.calls.Load() != 3 || out["data"].(map[string]any)["windows"].([]any)[0].(map[string]any)["utilization"].(float64) != 55 {
		t.Fatalf("second immediate refresh: %v calls=%d", out, up.calls.Load())
	}
	ctx := context.Background()

	// A failure is recorded and negatively cached for 30 minutes.
	up.failStatus.Store(503)
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE account_quota_snapshots SET last_active_at = now() - interval '31 minutes',
		updated_at = now() - interval '31 minutes', next_check_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	_, out = e.do("GET", path, nil)
	d = out["data"].(map[string]any)
	if up.calls.Load() != 4 || d["error"] == "" || d["windows"].([]any)[0].(map[string]any)["utilization"].(float64) != 55 {
		t.Fatalf("failure: %v (calls %d)", out, up.calls.Load())
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE account_quota_snapshots SET last_active_at = now() - interval '29 minutes'`); err != nil {
		t.Fatal(err)
	}
	e.do("GET", path, nil)
	if up.calls.Load() != 4 {
		t.Fatalf("negative cache ignored: %d", up.calls.Load())
	}

	// The list carries the stored snapshot without asking the upstream, and
	// null for types without quota.
	code, out = e.do("GET", "/accounts?orphaned=all", nil)
	if code != 200 {
		t.Fatalf("list: %d %v", code, out)
	}
	for _, it := range out["data"].([]any) {
		a := it.(map[string]any)
		switch int64(a["id"].(float64)) {
		case id:
			if q := a["quota"].(map[string]any); q["supported"] != true || q["error"] == "" {
				t.Fatalf("list quota: %v", q)
			}
		case relay:
			if a["quota"] != nil {
				t.Fatalf("relay quota: %v", a["quota"])
			}
		}
	}
	if up.calls.Load() != 4 {
		t.Fatalf("list asked the upstream: %d", up.calls.Load())
	}

	// A passive sample clears the error and becomes the source.
	h := http.Header{}
	h.Set("x-status", "rejected")
	e.svc.ObserveQuotaHeaders(id, e.gen.types[0].Type.Quota.Headers, 429, h)
	e.svc.quota.flush(ctx, true)
	_, out = e.do("GET", path, nil)
	d = out["data"].(map[string]any)
	if d["source"] != "passive" || d["error"] != "" || d["windows"].([]any)[0].(map[string]any)["status"] != "rejected" {
		t.Fatalf("passive: %v", out)
	}
}

func TestAccountResetStatus(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.mkRawAccount("anthropic", "apikey", "a", "sk-good-key-123", true)
	if err := e.svc.SetCooldown(ctx, id, time.Now().Add(time.Hour), "429"); err != nil {
		t.Fatal(err)
	}
	statusEvents := func() int {
		n := 0
		for _, ev := range e.eventTypes() {
			if ev == "account.status_changed" {
				n++
			}
		}
		return n
	}
	before, beforeEvents := e.bus.count(), statusEvents()
	code, out := e.do("POST", fmt.Sprintf("/accounts/%d/reset-status", id), nil)
	if code != 200 || out["data"].(map[string]any)["cooldown_until"] != nil {
		t.Fatalf("reset: %d %v", code, out)
	}
	if e.mr.Exists(cooldownKey(id)) {
		t.Fatal("cooldown key kept")
	}
	if e.bus.count() == before {
		t.Fatal("account:changed not broadcast")
	}
	if statusEvents() != beforeEvents+1 {
		t.Fatalf("events: %v", e.eventTypes())
	}
	rows := e.auditRows("account.reset_status")
	if len(rows) != 1 {
		t.Fatalf("audit: %v", rows)
	}
	// Without a cooldown nothing changes status: audited, no event.
	n := len(e.eventTypes())
	if code, _ = e.do("POST", fmt.Sprintf("/accounts/%d/reset-status", id), nil); code != 200 || len(e.eventTypes()) != n {
		t.Fatalf("second reset: %d events %v", code, e.eventTypes())
	}
	// Resetting a disabled account preserves its manual scheduling switch.
	off := e.mkRawAccount("anthropic", "apikey", "off", "sk-good-key-123", false)
	code, out = e.do("POST", fmt.Sprintf("/accounts/%d/reset-status", off), nil)
	if code != 200 || data(out)["status"] != "active" || data(out)["schedulable"] != false {
		t.Fatalf("disabled: %d %v", code, out)
	}
	if code, _ = e.do("POST", "/accounts/999999/reset-status", nil); code != 404 {
		t.Fatalf("missing: %d", code)
	}
}

func TestAccountResetStatusRestoresScheduling(t *testing.T) {
	for _, state := range []string{"disabled", "auto_disabled", "error", "active"} {
		for _, cooldown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cooldown=%t", state, cooldown), func(t *testing.T) {
				e := setup(t)
				ctx := t.Context()
				id := e.mkRawAccount("anthropic", "apikey", "recover", "sk-good-key-123", true)
				group := e.exec1(`INSERT INTO groups(name) VALUES('reset') RETURNING id`)
				if _, err := e.db.SQL.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, id, group); err != nil {
					t.Fatal(err)
				}
				if _, err := e.svc.AutoDisable(ctx, id, "upstream rejected credentials (401)"); err != nil {
					t.Fatal(err)
				}
				if _, err := e.db.SQL.ExecContext(ctx, `UPDATE accounts SET status=$2 WHERE id=$1`, id, state); err != nil {
					t.Fatal(err)
				}
				if cooldown {
					if err := e.svc.SetCooldown(ctx, id, time.Now().Add(time.Hour), "429"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := e.svc.Candidates(ctx, group, nil); err != nil {
					t.Fatal(err)
				}
				var beforeAccount, afterAccount string
				const preserved = `SELECT (to_jsonb(a) - ARRAY['status','status_reason','updated_at'])::text FROM accounts a WHERE id=$1`
				if err := e.db.SQL.QueryRowContext(ctx, preserved, id).Scan(&beforeAccount); err != nil {
					t.Fatal(err)
				}
				before := len(e.eventTypes())
				path := fmt.Sprintf("/accounts/%d/reset-status", id)
				code, out := e.do("POST", path, nil)
				if code != 200 || data(out)["status"] != "active" || data(out)["status_reason"] != "" ||
					data(out)["schedulable"] != true || data(out)["cooldown_until"] != nil || data(out)["auto_disable"] != true {
					t.Fatalf("reset: %d %v", code, out)
				}
				if err := e.db.SQL.QueryRowContext(ctx, preserved, id).Scan(&afterAccount); err != nil || beforeAccount != afterAccount {
					t.Fatalf("reset changed unrelated account fields: %v", err)
				}
				candidates, err := e.svc.Candidates(ctx, group, nil)
				if err != nil || len(candidates) != 1 || candidates[0].ID != id {
					t.Fatalf("candidates after reset: %v %v", candidates, err)
				}
				var payload string
				if err := e.db.SQL.QueryRowContext(ctx, `SELECT payload::text FROM events WHERE type='account.status_changed' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				var event map[string]any
				if err := json.Unmarshal([]byte(payload), &event); err != nil || event["status"] != "active" || len(e.eventTypes()) != before+1 {
					t.Fatalf("status event: %s %v", payload, err)
				}
				audits := e.auditRows("account.reset_status")
				if len(audits) != 1 {
					t.Fatalf("audits: %v", audits)
				}
				detail := audits[0]["detail"].(map[string]any)
				if detail["previous_status"] != state || detail["status_reset"] != true || detail["cooldown_cleared"] != cooldown {
					t.Fatalf("audit detail: %v", detail)
				}
				before = len(e.eventTypes())
				if code, _ := e.do("POST", path, nil); code != 200 || len(e.eventTypes()) != before {
					t.Fatalf("repeated reset: %d events %v", code, e.eventTypes())
				}
			})
		}
	}
}

func TestAccountResetStatusRedisFailure(t *testing.T) {
	e := setup(t)
	id := e.mkRawAccount("anthropic", "apikey", "blocked", "sk-good-key-123", true)
	if _, err := e.svc.AutoDisable(t.Context(), id, "upstream rejected credentials (401)"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetCooldown(t.Context(), id, time.Now().Add(time.Hour), "429"); err != nil {
		t.Fatal(err)
	}
	beforeEvents, beforeBus := len(e.eventTypes()), e.bus.count()
	e.mr.SetError("ERR unavailable")
	code, _ := e.do("POST", fmt.Sprintf("/accounts/%d/reset-status", id), nil)
	e.mr.SetError("")
	var status, reason string
	if err := e.db.SQL.QueryRowContext(t.Context(), `SELECT status,status_reason FROM accounts WHERE id=$1`, id).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusServiceUnavailable || status != "auto_disabled" || reason != "upstream rejected credentials (401)" || !e.mr.Exists(cooldownKey(id)) {
		t.Fatalf("failed reset changed state: %d %s %s", code, status, reason)
	}
	if len(e.auditRows("account.reset_status")) != 0 || len(e.eventTypes()) != beforeEvents || e.bus.count() != beforeBus {
		t.Fatal("failed reset reported a successful transition")
	}
}

func TestAccountResetStatusOwnership(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	owner, stranger, reader := e.addUser("owner@x.com"), e.addUser("stranger@x.com"), e.addUser("reader@x.com")
	authz.keys[owner] = []string{"account:update:own"}
	authz.keys[stranger] = []string{"account:update:own"}
	authz.keys[reader] = []string{"account:read"}
	id := e.mkRawAccount("anthropic", "apikey", "private", "sk-good-key-123", false)
	if _, err := e.db.SQL.ExecContext(t.Context(), `UPDATE accounts SET created_by=$2,owner_id=$2,status_reason='rejected' WHERE id=$1`, id, owner); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetCooldown(t.Context(), id, time.Now().Add(time.Hour), "429"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/accounts/%d/reset-status", id)
	for _, tc := range []struct {
		uid  int64
		code int
	}{{stranger, 404}, {reader, 403}} {
		code, out := e.doAs(tc.uid, "POST", path, nil)
		if code != tc.code || !e.mr.Exists(cooldownKey(id)) || len(e.auditRows("account.reset_status")) != 0 {
			t.Fatalf("unauthorized reset: %d %v", code, out)
		}
	}
	code, out := e.doAs(owner, "POST", path, nil)
	if code != 200 || data(out)["status"] != "active" || data(out)["status_reason"] != "" || data(out)["schedulable"] != false {
		t.Fatalf("owner reset: %d %v", code, out)
	}
}

// With the account's proxy disabled the quota query asks the plugin nothing
// and sends nothing; the failure is recorded as transient (CONTRACTS §124).
func TestQuotaQueryWithoutEgress(t *testing.T) {
	e := setup(t)
	up := newQuotaUpstream(t)
	e.plat.quotaURL = up.srv.URL + "/quota"
	e.withQuota(&manifest.AccountQuota{Query: true, AutoRefresh: true, Headers: []manifest.QuotaHeader{{Key: "5h", Status: "x-status"}}})
	id := e.mkRawAccount("anthropic", "apikey", "sub", "sk-good-key-123", true)
	host, port, hits := forwardProxy(t)
	pid := e.exec1(`INSERT INTO proxies (name, protocol, host, port, status) VALUES ('off', 'http', $1, $2, 'disabled') RETURNING id`, host, port)
	if _, err := e.db.SQL.ExecContext(t.Context(), `UPDATE accounts SET proxy_id = $1 WHERE id = $2`, pid, id); err != nil {
		t.Fatal(err)
	}
	code, out := e.do("GET", fmt.Sprintf("/accounts/%d/quota?force=true", id), nil)
	d, _ := out["data"].(map[string]any)
	if code != 200 || d["error"] != "transient: proxy disabled" {
		t.Fatalf("quota without an egress: %d %v", code, out)
	}
	e.plat.mu.Lock()
	builds := e.plat.quotaCalls
	e.plat.mu.Unlock()
	if builds != 0 || e.plat.called("ParseQuotaResponse") != 0 || up.calls.Load() != 0 || hits.Load() != 0 {
		t.Fatalf("plugin or upstream reached: builds %d, parses %d, upstream %d, proxy %d", builds, e.plat.called("ParseQuotaResponse"), up.calls.Load(), hits.Load())
	}
}
