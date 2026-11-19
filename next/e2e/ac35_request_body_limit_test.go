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
)

// AC 35: the request body limit is the gateway setting max_request_body_mb
// alone: a request over it is refused with 413 (a WebSocket message over it
// closes the connection with 1009) and nothing reaches the upstream;
// raising the setting takes effect without a restart.
func TestAC35_RequestBodyLimitSetting(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	tn := e.NewTenant(admin, TenantOpts{})
	wsKey := fmt.Sprintf("sk-limit-%s-%d", e.RunID, nextSeq())
	ws := e.singleAccount(admin, "grp-limit-ws", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType, APIKey: wsKey}, wsKey, "gpt", "limit")

	prev := e.setMaxRequestBodyMB(admin, 1)
	t.Cleanup(func() { e.setMaxRequestBodyMB(admin, prev) })
	big := strings.Repeat("x", 1536<<10) // 1.5 MiB of prompt
	body := MessagesBody(tn.Model, big, false)

	// Every node applies the new value (cached for up to 10 s otherwise);
	// until then a node serves the request, so the upstream is watched only
	// from here on.
	Eventually(t, 20*time.Second, time.Second, "413 on every node", func() bool {
		for _, base := range e.NodeURLs {
			if g := e.Gateway(t, base, "/v1/messages", tn.APIKey, body, nil); g.Status != http.StatusRequestEntityTooLarge {
				return false
			}
		}
		return true
	})
	mark := m.Mark(t)
	g := e.Messages(tn.APIKey, body, nil)
	if g.Status != http.StatusRequestEntityTooLarge || g.JSON().Get("error.type").String() == "" {
		t.Fatalf("over the limit: HTTP %d %s", g.Status, g.Body)
	}
	for _, k := range KeysUsed(m.Since(t, mark), "") {
		if tn.AccountKeys()[k] {
			t.Fatal("a request over the body limit reached the upstream")
		}
	}

	// A WebSocket message over the limit closes the connection with 1009.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := gatewayWS(ctx, t, e.NodeURLs[0], ws.APIKey)
	raw, _ := json.Marshal(map[string]any{"type": "response.create", "model": ws.Model, "input": big})
	_ = conn.Write(ctx, websocket.MessageText, raw)
	_, _, err := conn.Read(ctx)
	if status := websocket.CloseStatus(err); status != websocket.StatusMessageTooBig {
		t.Fatalf("websocket message over the limit: read error %v (close status %d), want 1009", err, status)
	}
	conn.CloseNow()

	// Raised to 2 MB: the same request is served.
	e.setMaxRequestBodyMB(admin, 2)
	Eventually(t, 20*time.Second, time.Second, "served on every node after raising the limit", func() bool {
		for _, base := range e.NodeURLs {
			if g := e.Gateway(t, base, "/v1/messages", tn.APIKey, body, nil); g.Status != 200 {
				return false
			}
		}
		return true
	})
	conn = gatewayWS(ctx, t, e.NodeURLs[0], ws.APIKey)
	defer conn.CloseNow()
	events := wsTurn(ctx, t, conn, map[string]any{"type": "response.create", "model": ws.Model, "input": big})
	if events[len(events)-1].Get("type").String() != "response.completed" {
		t.Fatalf("websocket message under the raised limit: %v", events)
	}
}
