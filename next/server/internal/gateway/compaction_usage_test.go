package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func TestCompactionUsageAndActualServiceFacts(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			e := newEnv(t)
			e.accounts.set(1, func(a *core.Account) { a.ModelMapping = map[string]string{testModel: "mapped-main"} })
			usage := map[string]any{"input_tokens": 23, "output_tokens": 1, "speed": "fast", "service_tier": "priority", "inference_geo": "us",
				"server_tool_use": map[string]any{"web_search_requests": 2, "web_fetch_requests": 1, "code_execution_requests": 3},
				"iterations":      []any{map[string]any{"type": "compaction", "input_tokens": 180, "output_tokens": 35}, map[string]any{"type": "message", "input_tokens": 23, "output_tokens": 1}}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"type": "message", "model": "mapped-main", "role": "assistant", "content": []any{}, "usage": usage})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"mapped-main\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"speed\":\"standard\"}}}\n\n")
				data, _ := json.Marshal(map[string]any{"type": "message_delta", "usage": usage})
				for range 2 {
					fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", data)
				}
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			t.Cleanup(srv.Close)
			e.plat.base = srv.URL
			request := body(testModel, stream)
			request["context_management"] = map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}}
			res := e.messages(request)
			rec := e.record()
			if res.status != 200 || rec.Tokens != (core.UsageTokens{Input: 23, Output: 1}) || len(rec.Additional) != 1 {
				t.Fatalf("compaction was merged, lost or repeated: %+v", rec)
			}
			part := rec.Additional[0]
			if part.Kind != "compaction" || part.Model != testModel || part.Price.ID != 9 || part.Tokens != (core.UsageTokens{Input: 180, Output: 35}) {
				t.Fatalf("compaction model/cost attribution changed: %+v", part)
			}
			for key, want := range map[string]any{"speed": "fast", "service_tier": "priority", "inference_geo": "us", "web_search_requests": float64(2), "web_fetch_requests": float64(1), "code_execution_requests": float64(3)} {
				if rec.Metrics[key] != want {
					t.Fatalf("actual %s fact = %v, want %v", key, rec.Metrics[key], want)
				}
			}
		})
	}
}
