package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// AC 31: the operator's settings on an account apply to the request the
// platform plugin built (DESIGN-2026-11-18-PLUGIN-FORWARDING-V2 §4.5): the
// plugin's default headers can be replaced by the account's header
// override, passthrough only fills headers the request does not have yet,
// the plugin's authoritative headers (credential, signature) are not
// overridable, and an account's proxy carries the request.
func TestAC31_OperatorSettingsOnPluginRequests(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	t.Run("header override, passthrough", func(t *testing.T) {
		e := e.With(t)
		k := fmt.Sprintf("sk-ant-hdr-%s-%d", e.RunID, nextSeq())
		tn := e.singleAccount(admin, "grp-hdr", AccountSpec{APIKey: k, Extra: map[string]any{
			// anthropic-version is a default header of the plugin.
			"header_override": map[string]any{
				"anthropic-version": "2099-12-31",
				"x-e2e-set":         "fixed",
				"re:^x-e2e-pass-":   "pass",
			},
			// content-type is set by the plugin: the client's must not replace it.
			"header_passthrough": []string{"content-type", "x-e2e-trace"},
		}}, k, "claude", "hdr")
		mark := m.Mark(t)
		g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "headers", false), map[string]string{
			"Content-Type":   "application/json; charset=utf-8",
			"X-E2E-Trace":    "trace-1",
			"X-E2E-Pass-One": "pass-1",
			"X-E2E-Unlisted": "nope",
		})
		if g.Status != 200 {
			t.Fatalf("HTTP %d %s", g.Status, g.Body)
		}
		r := oneMockCall(t, m, mark, k, "header settings")
		for name, want := range map[string]string{
			"x-api-key":         k,
			"anthropic-version": "2099-12-31",
			"x-e2e-set":         "fixed",
			"x-e2e-trace":       "trace-1",
			"x-e2e-pass-one":    "pass-1",
			"content-type":      "application/json",
			"x-e2e-unlisted":    "",
		} {
			if got := r.Header(name); got != want {
				t.Errorf("upstream %s = %q, want %q (headers %s)", name, got, want, r.Headers.Raw)
			}
		}
		e.expectBilled(admin, tn.User.UserID, g.RequestID, tn.Accounts[0].ID, usageAnthropic, false)
	})

	t.Run("authoritative headers of a signed request", func(t *testing.T) {
		e := e.With(t)
		k := fmt.Sprintf("AKIASIG%s%dMOCK", strings.ToUpper(e.RunID), nextSeq())
		tn := e.singleAccount(admin, "grp-sig", AccountSpec{PluginKey: "bedrock", Type: "bedrock_aksk", Credentials: bedrockCreds(k),
			Extra: map[string]any{"header_override": map[string]any{
				"x-amz-date":   "20000101T000000Z",  // signed: authoritative
				"content-type": "text/plain",        // signed: authoritative
				"accept":       "application/x-e2e", // a default header of the plugin
			}}}, k, "claude", "sig")
		mark := m.Mark(t)
		g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "signed", false), nil)
		if g.Status != 200 {
			t.Fatalf("signed request with an override of signed headers: HTTP %d %s", g.Status, g.Body)
		}
		r := oneMockCall(t, m, mark, k, "signed request")
		if r.Header("x-amz-date") == "20000101T000000Z" || r.Header("content-type") != "application/json" ||
			!strings.HasPrefix(r.Header("authorization"), "AWS4-HMAC-SHA256 Credential="+k+"/") {
			t.Fatalf("the override changed authoritative headers: %s", r.Headers.Raw)
		}
		if r.Header("accept") != "application/x-e2e" {
			t.Fatalf("the override did not change the default header accept: %s", r.Headers.Raw)
		}
		e.expectBilled(admin, tn.User.UserID, g.RequestID, tn.Accounts[0].ID, usageAnthropic, false)
	})

	t.Run("account proxy", func(t *testing.T) {
		e := e.With(t)
		k := fmt.Sprintf("sk-proxy-%s-%d", e.RunID, nextSeq())
		// The mock also answers as an HTTP forward proxy and records it.
		tn := e.singleAccount(admin, "grp-proxy", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType, APIKey: k,
			Extra: map[string]any{"proxy_url": e.MockInternalURL}}, k, "gpt", "proxy")
		for _, stream := range []bool{false, true} {
			mark := m.Mark(t)
			g := e.OpenAI(tn.APIKey, "/v1/chat/completions", ChatBody(tn.Model, "through the proxy", stream))
			if g.Status != 200 {
				t.Fatalf("stream=%v: HTTP %d %s", stream, g.Status, g.Body)
			}
			r := oneMockCall(t, m, mark, k, "proxied request")
			if !r.Proxied || r.Path != "/v1/chat/completions" || r.Header("authorization") != "Bearer "+k {
				t.Fatalf("stream=%v: the request did not go through the account's proxy: %+v", stream, r)
			}
			e.expectBilled(admin, tn.User.UserID, g.RequestID, tn.Accounts[0].ID, usageOpenAI, stream)
		}
	})
}
