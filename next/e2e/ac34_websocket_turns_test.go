package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// gatewayWS opens the OpenAI Responses WebSocket of the gateway (base: the
// load balancer or a node) with an API key.
func gatewayWS(ctx context.Context, t testing.TB, base, apiKey string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/v1/responses",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + apiKey}}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket handshake: HTTP %d %v", status, err)
	}
	conn.SetReadLimit(64 << 20)
	return conn
}

// wsTurn sends one response.create and reads its events up to the terminal
// one (response.completed, response.failed or error).
func wsTurn(ctx context.Context, t testing.TB, conn *websocket.Conn, msg map[string]any) []gjson.Result {
	t.Helper()
	raw, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("websocket write: %v", err)
	}
	var events []gjson.Result
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("websocket read after %d events: %v", len(events), err)
		}
		ev := gjson.ParseBytes(data)
		events = append(events, ev)
		switch ev.Get("type").String() {
		case "response.completed", "response.failed", "error":
			return events
		}
	}
}

// AC 34: the WebSocket mode of OpenAI Responses goes turn by turn through
// Execute: the plugin's first turn opens the upstream connection (one
// handshake, with the account's credential), every turn's message is the
// one the plugin built, and every turn is billed. openai/apikey and codex
// (whose plugin adds store=false and its own headers) both.
func TestAC34_ResponsesWebSocketTurns(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	for _, c := range []struct {
		name, host, path string
	}{
		{name: "openai", path: "/v1/responses"},
		{name: "codex", host: "chatgpt.com", path: "/backend-api/codex/responses"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := e.With(t)
			k := fmt.Sprintf("sk-ws-%s-%d", e.RunID, nextSeq())
			tn := e.singleAccount(admin, "grp-ws-"+c.name, platformCaseNamed(t, c.name).account(e, k), k, "gpt", "ws-"+c.name)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			mark := m.Mark(t)
			conn := gatewayWS(ctx, t, e.BaseURL, tn.APIKey)
			defer conn.CloseNow()
			prompts := []string{"turn-one-" + e.RunID, "turn-two-" + e.RunID}
			for i, p := range prompts {
				events := wsTurn(ctx, t, conn, map[string]any{"type": "response.create", "model": tn.Model, "input": p})
				last := events[len(events)-1]
				if last.Get("type").String() != "response.completed" || last.Get("response.usage.output_tokens").Int() != MockOutputTokens {
					t.Fatalf("turn %d: %v", i, events)
				}
			}
			conn.Close(websocket.StatusNormalClosure, "")
			calls := mockCallsOf(t, m, mark, k)
			handshakes, turns := wsCalls(calls)
			if len(handshakes) != 1 || len(turns) != 2 || handshakes[0].Path != c.path || (c.host != "" && handshakes[0].Host != c.host) {
				t.Fatalf("upstream: %d handshakes, %d turns, want 1 and 2 on %s%s: %+v", len(handshakes), len(turns), c.host, c.path, calls)
			}
			if handshakes[0].Header("authorization") != "Bearer "+k {
				t.Fatalf("handshake credential: %s", handshakes[0].Headers.Raw)
			}
			for i, r := range turns {
				if !strings.Contains(r.Body, prompts[i]) || gjson.Get(r.Body, "model").String() != tn.Model || gjson.Get(r.Body, "type").String() != "response.create" {
					t.Fatalf("turn %d message: %s", i, r.Body)
				}
				if c.name == "codex" && gjson.Get(r.Body, "store").Raw != "false" {
					t.Fatalf("codex turn %d message without store=false: %s", i, r.Body)
				}
			}
			// One billed usage record per turn.
			var billed []gjson.Result
			Eventually(t, 30*time.Second, time.Second, "two billed websocket turns", func() bool {
				billed = billed[:0]
				for _, u := range admin.ListAll(t, "/usage", "user_id", fmt.Sprint(tn.User.UserID)) {
					if u.Get("account_id").Int() == tn.Accounts[0].ID && u.Get("billing_status").String() == "billed" {
						billed = append(billed, u)
					}
				}
				return len(billed) == 2
			})
			for _, u := range billed {
				if u.Get("output_tokens").Int() != MockOutputTokens || Money(t, u.Get("total_cost").String()).Sign() <= 0 {
					t.Fatalf("websocket turn usage: %s", u.Raw)
				}
			}
		})
	}
}
