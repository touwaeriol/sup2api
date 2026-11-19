package volcengine

import (
	"context"
	"net/http"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	sdkvolcengine "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/volcengine"
)

// TestAnthropicSurfaceProtocolIDs checks the ids this plugin routes by are the
// anthropic plugin's (the built-in anthropic platform's protocol ids): the
// Ark Anthropic surface is the same wire protocol, and classify.go compares
// RequestMeta.protocol against the SDK provider constants. "anthropic-messages"
// used to be declared there, which made PolicyAnthropic unreachable.
func TestAnthropicSurfaceProtocolIDs(t *testing.T) {
	if ProtocolMessages != sdkvolcengine.ProtocolMessages {
		t.Fatalf("plugin protocol = %q, SDK provider = %q", ProtocolMessages, sdkvolcengine.ProtocolMessages)
	}
	if ProtocolMessages != "anthropic.messages" {
		t.Fatalf("protocol id = %q", ProtocolMessages)
	}
}

// TestClassifyErrorAnthropicSurface: an error whose RequestMeta.protocol is
// anthropic.messages is classified with the Anthropic vocabulary (no
// not_found_error folding, api_error for 5xx); an OpenAI-surface error keeps
// the OpenAI vocabulary.
func TestClassifyErrorAnthropicSurface(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	p := New()
	p.now = func() time.Time { return now }
	cls := func(protocol string, status int32) *pluginv1.ClassifyErrorResponse {
		r, err := p.ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{
			Status: status, Meta: &pluginv1.RequestMeta{Protocol: protocol},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	for _, tc := range []struct {
		status   int32
		wantType string
	}{
		{http.StatusNotFound, "not_found_error"},
		{http.StatusInternalServerError, "api_error"},
		{http.StatusServiceUnavailable, "api_error"},
		{http.StatusTooManyRequests, "rate_limit_error"},
	} {
		if got := cls(ProtocolMessages, tc.status).GetClientErrorType(); got != tc.wantType {
			t.Errorf("anthropic surface %d: client error type = %q, want %q", tc.status, got, tc.wantType)
		}
	}

	// The OpenAI surface (no protocol, or an OpenAI one) folds 404 into
	// invalid_request_error and calls 5xx server_error.
	if got := cls("", http.StatusNotFound).GetClientErrorType(); got != "invalid_request_error" {
		t.Errorf("openai surface 404: client error type = %q, want invalid_request_error", got)
	}
	if got := cls(ProtocolChat, http.StatusInternalServerError).GetClientErrorType(); got != "server_error" {
		t.Errorf("openai surface 500: client error type = %q, want server_error", got)
	}
}
