package volcengine

import (
	"bytes"
	"context"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// ---------------------------------------------------------------- estimate
//
// The estimate itself is tested in videospec_test.go, against Ark's published
// pixel table and formula. What used to be here - a check that the model's
// highest tier is assumed and that 10 seconds is the assumed duration - tested
// a guess that no longer exists.

// ---------------------------------------------------------------- BuildUpstream (video)

func TestBuildUpstreamVideo(t *testing.T) {
	p := New()
	ctx := context.Background()
	submitBody := []byte(`{"model":"doubao-seedance-1-0-pro-250528","content":[{"type":"text","text":"a cat"}],"ratio":"16:9"}`)
	build := func(meta *pluginv1.RequestMeta, body []byte) (*pluginv1.UpstreamRequest, error) {
		return p.BuildUpstream(ctx, &pluginv1.ExecuteRequest{Meta: meta, Account: account(testKey, ""), Body: body})
	}

	// Submit: POST to Ark's own /api/v3 task collection.
	r, err := build(&pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "doubao-seedance-1-0-pro-250528"}, submitBody)
	if err != nil || r.GetMethod() != "POST" ||
		r.GetUrl() != DefaultBaseURL+"/api/v3/contents/generations/tasks" {
		t.Fatalf("submit = %s %s (%v)", r.GetMethod(), r.GetUrl(), err)
	}
	if r.GetHeaders()["authorization"] != "Bearer 11111111-2222-3333-4444-555555555555" {
		t.Fatalf("submit auth = %v", r.GetHeaders())
	}
	// The body goes up byte for byte: no stream_options edit, that is a chat
	// field, and the model is the submitted one.
	if !bytes.Equal(r.GetBody(), submitBody) || r.GetUpstreamModel() != "doubao-seedance-1-0-pro-250528" {
		t.Fatalf("submit body = %s model = %q", r.GetBody(), r.GetUpstreamModel())
	}
}

// ---------------------------------------------------------------- videoUsage (no DB)

// TestVideoUsageReservation checks the reservation videoUsage returns for a
// submit response without a database. The host owns persistence.
func TestVideoUsageReservation(t *testing.T) {
	p := New() // no host: estimation is pure parsing

	rep := p.videoUsage(&pluginv1.ExtractUsageRequest{
		Meta:    &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "doubao-seedance-2-0-260128", UserId: 42},
		Account: &pluginv1.Account{Id: 7},
		Status:  200,
		Body:    []byte(`{"id":"cgt-777"}`),
		// The request the submit carried, as the host delivers it: the raw JSON
		// of each declared path.
		Fields: map[string]string{
			PathResolution: `"1080p"`, PathRatio: `"16:9"`, PathDuration: `5`,
			PathContentCount: `1`, "content.0.text": `"a cat yawning"`,
		},
	})
	rv := rep.GetReserve()
	if rv == nil {
		t.Fatal("no reservation")
	}
	if rv.GetRefId() != "cgt-777" {
		t.Fatalf("ref_id = %q", rv.GetRefId())
	}
	// The deadline is Ark's 7 days, which is also the core's default since
	// CONTRACTS §25.6, so it passes through unchanged.
	if rv.GetDeadlineSec() != 7*24*60*60 {
		t.Fatalf("deadline_sec = %d, want 7 days", rv.GetDeadlineSec())
	}
	if rv.GetNextCheckAfterSec() != firstCheckSec {
		t.Fatalf("next_check_after_sec = %d", rv.GetNextCheckAfterSec())
	}
	// The estimate is what the REQUEST asked for - 5 seconds of 1080p 16:9 -
	// not the model's most expensive tier. This is the whole point of
	// usageRequestFields: the 4k-capable model above used to be reserved at 4k
	// for a guessed ten seconds, 1,944,000 tokens against this request's
	// 243,000 - eight times the charge, for the same video.
	if want := int64(5*videoFPS+1) * 1920 * 1080 / 1024; rv.GetTokens().GetOutputTokens() != want {
		t.Fatalf("estimated output tokens = %d, want %d (5s of 1080p 16:9)",
			rv.GetTokens().GetOutputTokens(), want)
	}
	if rv.GetFacts()[FactResolution] != Res1080 {
		t.Fatalf("estimate resolution fact = %q, want the requested tier", rv.GetFacts()[FactResolution])
	}
	// And with no request fields at all it falls back to the model's bounds,
	// which must be STRICTLY more expensive - never less.
	bare := p.videoUsage(&pluginv1.ExtractUsageRequest{
		Meta:   &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "doubao-seedance-2-0-260128"},
		Status: 200, Body: []byte(`{"id":"cgt-778"}`),
	})
	if bare.GetReserve().GetTokens().GetOutputTokens() <= rv.GetTokens().GetOutputTokens() {
		t.Fatalf("a request that stated nothing reserved %d, one that stated 1080p/5s reserved %d",
			bare.GetReserve().GetTokens().GetOutputTokens(), rv.GetTokens().GetOutputTokens())
	}
	// The report repeats the estimate outside the reservation.
	if rep.GetTokens().GetOutputTokens() != rv.GetTokens().GetOutputTokens() ||
		rep.GetFacts()[FactResolution] != rv.GetFacts()[FactResolution] {
		t.Fatalf("report must repeat the estimate: tokens=%v facts=%v", rep.GetTokens(), rep.GetFacts())
	}

	// A submit with no task id does not reserve (it could never be reconciled)
	// and does not panic on a malformed body.
	for _, body := range []string{`{}`, `{"id":""}`, `not json`, ``, `[1,2,3]`} {
		rep := p.videoUsage(&pluginv1.ExtractUsageRequest{
			Meta: &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "m"}, Status: 200, Body: []byte(body),
		})
		if rep.GetReserve() != nil {
			t.Fatalf("body %q reserved unexpectedly", body)
		}
	}

	// A relay that wraps the id is still read.
	rep = p.videoUsage(&pluginv1.ExtractUsageRequest{
		Meta: &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "m"}, Status: 200, Body: []byte(`{"data":{"id":"cgt-wrapped"}}`),
	})
	if rep.GetReserve().GetRefId() != "cgt-wrapped" {
		t.Fatalf("wrapped id: %v", rep.GetReserve())
	}
}

// ---------------------------------------------------------------- reconcile (no DB)

func TestTaskQuery(t *testing.T) {
	r, err := taskQuery(&pluginv1.ReconcileEntry{RefId: "cgt-abc"}, account(testKey, `{"base_url":"`+BytePlusBaseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.GetMethod() != "GET" || r.GetUrl() != BytePlusBaseURL+"/api/v3/contents/generations/tasks/cgt-abc" {
		t.Fatalf("reconcile request = %s %s", r.GetMethod(), r.GetUrl())
	}
	if r.GetHeaders()["authorization"] != "Bearer 11111111-2222-3333-4444-555555555555" {
		t.Fatalf("reconcile auth = %v", r.GetHeaders())
	}
	// No ref id is a hard error (the entry is unusable).
	if _, err := taskQuery(&pluginv1.ReconcileEntry{}, account(testKey, "")); err == nil {
		t.Fatal("expected an error for an entry with no ref_id")
	}
}

func TestReadTaskQuery(t *testing.T) {
	p := New() // no host at all: not one answer below may depend on the plugin's own table
	entry := &pluginv1.ReconcileEntry{RefId: "cgt-1"}

	parse := func(status int32, transportErr, body string) *pluginv1.ReconcileResult {
		return p.readTaskQuery(entry, &pluginv1.ExecutionHTTPResponse{Status: status, TransportError: transportErr, Body: []byte(body)})
	}

	// Running / queued -> PENDING, with the plugin's own cadence.
	for _, st := range []string{"queued", "running", "processing"} {
		r := parse(200, "", `{"status":"`+st+`"}`)
		if r.GetState() != pluginv1.ReconcileResult_PENDING || r.GetNextCheckAfterSec() != runningCheckSec {
			t.Fatalf("status %q -> %v", st, r)
		}
	}
	for _, st := range []string{"", "something-new"} {
		if r := parse(200, "", `{"status":"`+st+`"}`); r.GetState() != pluginv1.ReconcileResult_POLL_FAILED || r.GetTaskSnapshotJson() != "" {
			t.Fatalf("unknown status was treated as valid progress: %v", r)
		}
	}
	for _, code := range []int32{404, 410} {
		if r := parse(code, "", `{}`); r.GetState() != pluginv1.ReconcileResult_NOT_FOUND {
			t.Fatalf("missing task: %v", r)
		}
	}

	// Succeeded -> SETTLED with the real completion tokens and the real
	// resolution fact.
	r := parse(200, "", `{"status":"succeeded","usage":{"completion_tokens":123456,"total_tokens":123456},"content":{"video_url":"https://x/v.mp4","resolution":"1080p"}}`)
	if r.GetState() != pluginv1.ReconcileResult_SETTLED || r.GetTokens().GetOutputTokens() != 123456 {
		t.Fatalf("succeeded -> %v", r)
	}
	if r.GetFacts()[FactResolution] != Res1080 {
		t.Fatalf("succeeded resolution fact = %q", r.GetFacts()[FactResolution])
	}

	// completion_tokens absent but total_tokens present: total is used.
	r = parse(200, "", `{"status":"succeeded","usage":{"total_tokens":999}}`)
	if r.GetTokens().GetOutputTokens() != 999 {
		t.Fatalf("total fallback -> %v", r)
	}

	// THE MONEY RULE, with no database in reach: a succeeded task the upstream
	// reports no usage for is SETTLED_ESTIMATE, never SETTLED at zero. SETTLED
	// with zero tokens reprices the row at zero and refunds the whole
	// reservation, i.e. hands out a delivered video for free. Before
	// SETTLED_ESTIMATE existed this needed the plugin's own est_tokens column,
	// so without a host it fell through to exactly that zero; the point of the
	// change is that the answer no longer depends on the plugin's table.
	for _, body := range []string{
		`{"status":"succeeded"}`,
		`{"status":"succeeded","content":{"video_url":"https://x/v.mp4"}}`,
		`{"status":"succeeded","usage":{}}`,
		`{"status":"succeeded","usage":{"completion_tokens":0,"total_tokens":0}}`,
	} {
		r := parse(200, "", body)
		if r.GetState() != pluginv1.ReconcileResult_SETTLED_ESTIMATE {
			t.Fatalf("%s -> %v, want SETTLED_ESTIMATE", body, r.GetState())
		}
		if r.GetTokens() != nil || len(r.GetFacts()) != 0 {
			t.Fatalf("%s: SETTLED_ESTIMATE must carry no tokens or facts: %v", body, r)
		}
		if r.GetReason() == "" {
			t.Fatalf("%s: SETTLED_ESTIMATE should note why there was no usage", body)
		}
	}
	// The invariant behind that state, asserted directly: no succeeded body
	// may ever produce SETTLED with a zero (or negative) token count.
	for _, body := range []string{
		`{"status":"succeeded"}`, `{"status":"succeeded","usage":{"completion_tokens":0}}`,
		`{"status":"succeeded","usage":{"completion_tokens":-5}}`,
		`{"status":"succeeded","usage":{"completion_tokens":"nonsense"}}`,
		`{"status":"succeeded","usage":null}`,
	} {
		r := parse(200, "", body)
		if r.GetState() == pluginv1.ReconcileResult_SETTLED && r.GetTokens().GetOutputTokens() <= 0 {
			t.Fatalf("%s settled at %d tokens: that refunds the whole reservation",
				body, r.GetTokens().GetOutputTokens())
		}
	}

	// An unknown upstream resolution is not reported as a fact (the core would
	// drop it anyway).
	r = parse(200, "", `{"status":"succeeded","usage":{"completion_tokens":1},"content":{"resolution":"8k"}}`)
	if _, ok := r.GetFacts()[FactResolution]; ok {
		t.Fatalf("an unknown resolution must not be a fact: %v", r.GetFacts())
	}

	// Failed / expired / cancelled -> FAILED with a reason.
	r = parse(200, "", `{"status":"failed","error":{"message":"content moderation"}}`)
	if r.GetState() != pluginv1.ReconcileResult_FAILED || r.GetReason() != "content moderation" {
		t.Fatalf("failed -> %v", r)
	}
	r = parse(200, "", `{"status":"expired"}`)
	if r.GetState() != pluginv1.ReconcileResult_FAILED || r.GetReason() != "expired" {
		t.Fatalf("expired -> %v", r)
	}

	// A transport error or a non-2xx is not a verdict: PENDING, so the
	// deadline (not a refund) decides an upstream that really ran the task.
	if r := parse(0, "connection refused", ``); r.GetState() != pluginv1.ReconcileResult_POLL_FAILED {
		t.Fatalf("transport error -> %v", r)
	}
	if r := parse(503, "", `nope`); r.GetState() != pluginv1.ReconcileResult_POLL_FAILED {
		t.Fatalf("503 -> %v", r)
	}
	// A malformed 200 body does not panic and asks again.
	if r := parse(200, "", `not json at all`); r.GetState() != pluginv1.ReconcileResult_POLL_FAILED {
		t.Fatalf("garbage body -> %v", r)
	}
}
