package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/usage"
)

// The two gateways and services share only durable PG task state. The querying
// node deliberately has no eligible upstream account, ruling out hidden dispatch.
func TestTaskGatewayCrossNodeSnapshotAndAuthorization(t *testing.T) {
	db := testutil.DB(t)
	billA := billing.New(db, nil, nil, nil, nil)
	nodeA := usage.New(db, billA, billA, nil, usage.Options{})
	billB := billing.New(db, nil, nil, nil, nil)
	nodeB := usage.New(db, billB, billB, nil, usage.Options{})
	a, pa := taskDraftEnv(t, nodeA, false)
	a.gw.d.Settler = nodeA // its execution records register the task in PostgreSQL
	b, pb := taskDraftEnv(t, nodeB, true)
	res := a.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
	public := res.json().Get("id").String()
	if res.status != 200 || !strings.HasPrefix(public, "s2task_") || res.json().Get("data.id").String() != public ||
		strings.Contains(string(res.body), "raw-video-id") {
		t.Fatalf("submit did not return only public IDs: %d %s", res.status, res.body)
	}
	a.noRecord() // the commit persisted the usage with the task
	for _, key := range []string{testKey, "same-owner-other-key"} {
		got := taskDraftGet(t, b, public, key)
		if got.status != 200 || got.json().Get("id").String() != public || got.json().Get("data.id").String() != public ||
			got.json().Get("status").String() != "queued" || strings.Contains(string(got.body), "raw-video-id") ||
			!strings.Contains(string(got.body), "9007199254740993") {
			t.Fatalf("immediate query key=%s: %d %s", key, got.status, got.body)
		}
	}
	for _, key := range []string{"other-user", "other-group"} {
		if got := taskDraftGet(t, b, public, key); got.status != 404 {
			t.Fatalf("unauthorized query key=%s: %d %s", key, got.status, got.body)
		}
	}
	if got := taskDraftGet(t, b, public, "model-denied"); got.status != 404 {
		t.Fatalf("model allowlist bypassed: %d %s", got.status, got.body)
	}
	if got := taskDraftGet(t, b, "raw-video-id", testKey); got.status != 404 {
		t.Fatalf("new raw task identity was exposed: %d %s", got.status, got.body)
	}
	if len(a.up.keys()) != 1 || len(b.up.keys()) != 0 || pa.parseCalls.Load() != 1 || pb.parseCalls.Load() != 0 ||
		pa.extractCount() != 0 || pb.extractCount() != 0 || pb.buildCount() != 0 || pb.resolveCount() != 0 {
		t.Fatalf("query or async usage reached plugin/upstream: submit=%v query=%v parses=%d/%d builds=%d resolves=%d",
			a.up.keys(), b.up.keys(), pa.parseCalls.Load(), pb.parseCalls.Load(), pb.buildCount(), pb.resolveCount())
	}
}

func TestTaskGatewayNeverResubmitsAfterSending(t *testing.T) {
	db := testutil.DB(t)
	for _, mode := range []string{"parser_failure", "transport_eof", "header_timeout", "upstream_503"} {
		t.Run(mode, func(t *testing.T) {
			bill := billing.New(db, nil, nil, nil, nil)
			svc := usage.New(db, bill, bill, nil, usage.Options{})
			e, pp := taskDraftEnv(t, svc, false)
			var hits atomic.Int64
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				hits.Add(1)
				switch mode {
				case "transport_eof":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				case "header_timeout":
					<-r.Context().Done()
					return
				case "upstream_503":
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				_, _ = io.WriteString(w, `{"id":"raw-video-id","data":{"id":"raw-video-id"}}`)
			}))
			t.Cleanup(up.Close)
			pp.base = up.URL
			if mode == "parser_failure" {
				pp.parseError = errors.New("parser unavailable after task accepted")
			}
			if mode == "header_timeout" {
				e.gw.headerWait = func(bool) time.Duration { return 100 * time.Millisecond }
			}
			got := e.do("/task-draft/videos", map[string]any{"model": testModel}, nil)
			if got.status != 503 || hits.Load() != 1 || pp.buildCount() != 1 {
				t.Fatalf("task submit was retried: mode=%s status=%d hits=%d builds=%d body=%s",
					mode, got.status, hits.Load(), pp.buildCount(), got.body)
			}
			wantParses := int64(0)
			if mode == "parser_failure" {
				wantParses = 1
			}
			if pp.parseCalls.Load() != wantParses || pp.extractCount() != 0 {
				t.Fatalf("unexpected usage/parser replay: parses=%d extracts=%d", pp.parseCalls.Load(), pp.extractCount())
			}
			rec := e.record()
			if rec.Billable || rec.Reservation != nil || rec.Attempts != 1 {
				t.Fatalf("uncertain task was billed or resubmitted: %+v", rec)
			}
			var state string
			var response []byte
			if err := db.SQL.QueryRowContext(context.Background(), `SELECT state,response_body FROM task_submission_receipts WHERE request_id=$1`, rec.RequestID).
				Scan(&state, &response); err != nil || state != "uncertain" || (mode == "parser_failure" && len(response) == 0) {
				t.Fatalf("uncertain receipt lost: state=%q body=%s err=%v", state, response, err)
			}
		})
	}
}
