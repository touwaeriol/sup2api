package guard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

func sdkOpts() []pluginsdk.Option { return []pluginsdk.Option{pluginsdk.WithInfo("guard", "0.2.11")} }

func hookReq(prompt string) *pluginv1.GatewayRequestHookRequest {
	return &pluginv1.GatewayRequestHookRequest{
		Meta:   &pluginv1.RequestMeta{RequestId: "req-1", Protocol: "anthropic.messages", Model: "claude-opus-5", UserId: 3, GroupId: 9},
		Fields: map[string]string{"model": `"claude-opus-5"`, "prompt_text": prompt},
	}
}

func setRules(t *testing.T, p *Plugin, rules ...Rule) {
	t.Helper()
	rs, err := compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	p.rules.Store(rs)
}

func TestHookMatching(t *testing.T) {
	p := New()
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{SDK: sdkOpts()})
	if got := strings.Join(h.Info.GetCapabilities(), ","); got != "gateway.hook.v1,app.jobs.v1,app.events.v1,app.broadcast.v1,http.routes.v1" {
		t.Fatalf("capabilities = %s", got)
	}
	setRules(t, p,
		Rule{ID: 1, Name: "secret", Kind: KindKeyword, Pattern: "Top Secret", Enabled: true},
		Rule{ID: 2, Name: "card", Kind: KindRegex, Pattern: `\b\d{4}-\d{4}-\d{4}-\d{4}\b`, Enabled: true},
		Rule{ID: 3, Name: "off", Kind: KindKeyword, Pattern: "hello", Enabled: false},
	)
	ctx := context.Background()
	for _, tc := range []struct {
		prompt string
		deny   bool
		rule   string
	}{
		{"hello world", false, ""},
		{"this is TOP SECRET stuff", true, "secret"},
		{`"json encoded top secret"`, true, "secret"},
		{"pay with 1234-5678-9012-3456 please", true, "card"},
		{"", false, ""},
	} {
		r, err := h.Hook.OnGatewayRequest(ctx, hookReq(tc.prompt))
		if err != nil {
			t.Fatal(err)
		}
		denied := r.GetDecision() == pluginv1.GatewayRequestHookResponse_DECISION_DENY
		if denied != tc.deny {
			t.Fatalf("%q: decision %v", tc.prompt, r.GetDecision())
		}
		if denied && (r.GetDenyStatus() != 403 || r.GetDenyCode() != DenyCode || !strings.Contains(r.GetDenyMessage(), tc.rule)) {
			t.Fatalf("%q: deny = %v", tc.prompt, r)
		}
	}
	health, err := h.Plugin.Health(ctx, &pluginv1.HealthRequest{})
	if err != nil || health.GetMetrics()["blocked"] != 3 || health.GetMetrics()["rules"] != 2 {
		t.Fatalf("health = %v %v", health, err)
	}
}

func TestSnippetAround(t *testing.T) {
	text := strings.Repeat("a", 100) + "中文MATCH中文" + strings.Repeat("b", 100)
	off := strings.Index(text, "MATCH")
	s := snippetAround(text, off, 5)
	if !strings.Contains(s, "中文MATCH中文") || len(s) > 2*snippetRadius+5+6 {
		t.Fatalf("snippet = %q", s)
	}
	if !strings.HasPrefix(snippetAround("短", 0, 3), "短") {
		t.Fatal("short snippet")
	}
}

func TestWebhookAlert(t *testing.T) {
	var mu sync.Mutex
	var got map[string]any
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(b, &got)
		mu.Unlock()
		done <- struct{}{}
	}))
	defer srv.Close()

	p := New()
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{SDK: sdkOpts(), Config: Settings{WebhookURL: srv.URL, RecordSnippets: true}})
	setRules(t, p, Rule{ID: 7, Name: "bad", Kind: KindKeyword, Pattern: "forbidden", Enabled: true})
	if _, err := h.Hook.OnGatewayRequest(context.Background(), hookReq("a forbidden word")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook not called")
	}
	mu.Lock()
	defer mu.Unlock()
	if got["rule_name"] != "bad" || got["request_id"] != "req-1" || got["snippet"] != "a forbidden word" || got["model"] != "claude-opus-5" {
		t.Fatalf("payload = %v", got)
	}
}

func TestConfigureValidation(t *testing.T) {
	h := pluginsdktest.Start(t, New(), pluginsdktest.Options{SDK: sdkOpts()})
	if errs := h.Configure(`{"webhook_url":"ftp://x"}`, nil); len(errs) != 1 || errs[0].GetField() != "webhook_url" {
		t.Fatalf("errs = %v", errs)
	}
	if errs := h.Configure(`{"webhook_url":"","record_snippets":true}`, nil); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
}

func TestValidateRules(t *testing.T) {
	f := false
	rules, errs := validateRules([]ruleInput{
		{Type: "regex", Pattern: "a+b"},
		{Name: "  kw ", Pattern: "  word ", Enabled: &f},
	})
	if len(errs) != 0 || rules[0].Kind != KindRegex || rules[0].Name != "a+b" || !rules[0].Enabled ||
		rules[1].Kind != KindKeyword || rules[1].Pattern != "word" || rules[1].Name != "kw" || rules[1].Enabled {
		t.Fatalf("rules = %+v errs = %v", rules, errs)
	}
	_, errs = validateRules([]ruleInput{
		{Kind: "regex", Pattern: "("},
		{Kind: "glob", Pattern: "x"},
		{Kind: "keyword", Pattern: "  "},
		{ID: 5, Pattern: "x"}, {ID: 5, Pattern: "y"},
	})
	fields := []string{}
	for _, e := range errs {
		fields = append(fields, e.GetField())
	}
	if strings.Join(fields, ",") != "rules[0].pattern,rules[1].kind,rules[2].pattern,rules[4].id" {
		t.Fatalf("fields = %v", fields)
	}
}

func TestUnknownJobAndNoDB(t *testing.T) {
	h := pluginsdktest.Start(t, New(), pluginsdktest.Options{SDK: sdkOpts()})
	if _, err := h.App.RunJob(context.Background(), &pluginv1.RunJobRequest{JobId: "rollup"}); err == nil {
		t.Fatal("expected error without database")
	}
	if resp := h.Do("GET", "/stats", nil, nil); resp.GetStatus() != 503 {
		t.Fatalf("stats = %d", resp.GetStatus())
	}
}

// ---------------------------------------------------------------- database

type statsResp struct {
	Data Stats `json:"data"`
}

func startDB(t *testing.T) (*Plugin, *pluginsdktest.Harness) {
	t.Helper()
	dsn, schema := pluginsdktest.NewSchema(t, "plg_guard_t")
	pluginsdktest.ApplyMigrations(t, dsn, schema, filepath.Join("..", "..", "migrations"))
	fh := pluginsdktest.NewFakeHost()
	fh.SetDSN(dsn, schema)
	p := New()
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, SDK: sdkOpts(), Config: Settings{RecordSnippets: true}})
	return p, h
}

func getStats(t *testing.T, h *pluginsdktest.Harness, q map[string]string) Stats {
	t.Helper()
	resp := h.Do("GET", "/stats", q, nil)
	if resp.GetStatus() != 200 {
		t.Fatalf("stats = %d %s", resp.GetStatus(), resp.GetBody())
	}
	var s statsResp
	if err := json.Unmarshal(resp.GetBody(), &s); err != nil {
		t.Fatal(err)
	}
	return s.Data
}

func TestRulesStatsEventsJobs(t *testing.T) {
	p, h := startDB(t)
	ctx := context.Background()

	// Seeded demo rule is loaded at Init.
	resp := h.Do("GET", "/rules", nil, nil)
	var list struct {
		Data []Rule `json:"data"`
	}
	_ = json.Unmarshal(resp.GetBody(), &list)
	if len(list.Data) != 1 || list.Data[0].Pattern != "GUARD_TEST_BLOCK" {
		t.Fatalf("rules = %s", resp.GetBody())
	}
	demoID := list.Data[0].ID

	// Replace: keep the demo rule (disabled), add a regex rule and a
	// disabled one (the column defaults to true).
	resp = h.Do("PUT", "/rules", nil, map[string]any{"rules": []map[string]any{
		{"id": demoID, "name": "demo", "kind": "keyword", "pattern": "GUARD_TEST_BLOCK", "enabled": false},
		{"name": "no-ssn", "kind": "regex", "pattern": `\d{3}-\d{2}-\d{4}`, "enabled": true},
		{"name": "off", "kind": "keyword", "pattern": "GUARD_OFF", "enabled": false},
	}})
	if resp.GetStatus() != 200 {
		t.Fatalf("PUT = %d %s", resp.GetStatus(), resp.GetBody())
	}
	_ = json.Unmarshal(resp.GetBody(), &list)
	if len(list.Data) != 3 || list.Data[0].ID != demoID || list.Data[0].Enabled || list.Data[1].Kind != KindRegex || list.Data[2].Enabled {
		t.Fatalf("after PUT = %s", resp.GetBody())
	}
	ssnID := list.Data[1].ID
	if bad := h.Do("PUT", "/rules", nil, `{"rules":[{"kind":"regex","pattern":"("}]}`); bad.GetStatus() != 400 {
		t.Fatalf("bad PUT = %d", bad.GetStatus())
	}

	// Hook uses the new rules immediately.
	r, _ := h.Hook.OnGatewayRequest(ctx, hookReq("GUARD_TEST_BLOCK"))
	if r.GetDecision() != pluginv1.GatewayRequestHookResponse_DECISION_ALLOW {
		t.Fatal("disabled rule must not block")
	}
	for i := 0; i < 2; i++ {
		r, _ = h.Hook.OnGatewayRequest(ctx, hookReq("my ssn is 123-45-6789"))
		if r.GetDecision() != pluginv1.GatewayRequestHookResponse_DECISION_DENY {
			t.Fatal("ssn rule must block")
		}
	}

	// usage.recorded events, with a redelivered duplicate.
	now := time.Now().UTC()
	payload := func(group int64) string {
		b, _ := json.Marshal(map[string]any{"group_id": group, "model": "claude-opus-5", "created_at": now.Format(time.RFC3339Nano)})
		return string(b)
	}
	evs := []*pluginv1.Event{
		{Id: 101, Type: "usage.recorded", PayloadJson: payload(9)},
		{Id: 102, Type: "usage.recorded", PayloadJson: payload(9)},
		{Id: 103, Type: "user.created", PayloadJson: `{}`},
		{Id: 104, Type: "usage.recorded", PayloadJson: payload(10)},
	}
	ack, err := h.App.OnEvents(ctx, &pluginv1.OnEventsRequest{Events: evs})
	if err != nil || ack.GetAckedThroughId() != 104 {
		t.Fatalf("ack = %v %v", ack, err)
	}
	ack, err = h.App.OnEvents(ctx, &pluginv1.OnEventsRequest{Events: evs[1:3]})
	if err != nil || ack.GetAckedThroughId() != 103 {
		t.Fatalf("redelivery ack = %v %v", ack, err)
	}

	// Blocks are written asynchronously (1 s batches).
	var st Stats
	deadline := time.Now().Add(10 * time.Second)
	for {
		st = getStats(t, h, map[string]string{"range": "24h"})
		if st.BlockedTotal == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st.BlockedTotal != 2 || st.RequestsTotal != 3 || st.Bucket != "hour" || len(st.Trend) < 24 {
		t.Fatalf("stats = %+v", st)
	}
	if len(st.TopRules) != 1 || st.TopRules[0].RuleID != ssnID || st.TopRules[0].Hits != 2 || st.TopRules[0].Name != "no-ssn" {
		t.Fatalf("top rules = %+v", st.TopRules)
	}
	if len(st.Recent) != 2 || st.Recent[0].Snippet == "" || st.Recent[0].GroupID != 9 {
		t.Fatalf("recent = %+v", st.Recent)
	}
	today := getStats(t, h, map[string]string{"range": "today", "tz": "Asia/Shanghai"})
	if today.BlockedTotal != 2 {
		t.Fatalf("today = %+v", today)
	}

	// Rollup feeds the 30-day (hourly, daily buckets) view.
	job, err := h.App.RunJob(ctx, &pluginv1.RunJobRequest{JobId: JobRollup})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.App.RunJob(ctx, &pluginv1.RunJobRequest{JobId: JobRollup}); err != nil {
		t.Fatal(err)
	}
	month := getStats(t, h, map[string]string{"range": "30d"})
	if month.Bucket != "day" || month.BlockedTotal != 2 || month.RequestsTotal != 3 || len(month.Trend) < 30 {
		t.Fatalf("30d = %+v (job: %s)", month, job.GetMessage())
	}
	if _, err := h.App.RunJob(ctx, &pluginv1.RunJobRequest{JobId: JobCleanup}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.App.RunJob(ctx, &pluginv1.RunJobRequest{JobId: "nope"}); err == nil {
		t.Fatal("unknown job must fail")
	}
	if bad := h.Do("GET", "/stats", map[string]string{"range": "1y"}, nil); bad.GetStatus() != 400 {
		t.Fatalf("bad range = %d", bad.GetStatus())
	}

	// Rule changes made on another node are picked up on reload.
	db, _ := p.db(ctx)
	if err := db.Exec(`UPDATE rules SET enabled = true WHERE id = ?`, demoID).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.reloadRules(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ = h.Hook.OnGatewayRequest(ctx, hookReq("GUARD_TEST_BLOCK"))
	if r.GetDecision() != pluginv1.GatewayRequestHookResponse_DECISION_DENY {
		t.Fatal("re-enabled rule must block")
	}
}
