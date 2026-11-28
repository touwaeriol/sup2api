package core

import (
	"net/http"
	"testing"
)

func TestProviderHeadersExcludeSecretsAndHopHeaders(t *testing.T) {
	source := http.Header{"Request-Id": []string{"provider"}, "Retry-After": []string{"4"}, "Anthropic-Ratelimit-Tokens-Remaining": []string{"18"}, "Authorization": []string{"secret"}, "Set-Cookie": []string{"secret"}, "X-Internal-Route": []string{"private"}, "Anthropic-Fast-Input-Tokens-Limit": []string{"100"}, "Connection": []string{"Retry-After"}, "X-Should-Retry": []string{"bad\r\nvalue"}}
	out := ProviderHeaders(source)
	if out.Get("Request-Id") != "provider" || out.Get("Anthropic-Ratelimit-Tokens-Remaining") != "18" || out.Get("Anthropic-Fast-Input-Tokens-Limit") != "100" || len(out) != 3 {
		t.Fatal("unsafe or incomplete provider headers", out)
	}
	source["Request-Id"][0] = "changed"
	if out.Get("Request-Id") != "provider" {
		t.Fatal("aliased source")
	}
}

// The Codex backend's turn state routing token reaches the client, which
// replays it on the turn's later requests.
func TestProviderHeadersRelayCodexTurnState(t *testing.T) {
	out := ProviderHeaders(http.Header{"X-Codex-Turn-State": []string{"ts-1"}, "X-Codex-Other": []string{"x"}})
	if out.Get("X-Codex-Turn-State") != "ts-1" || len(out) != 1 {
		t.Fatal("turn state not relayed alone", out)
	}
}
