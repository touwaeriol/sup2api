package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// testNow is the fixed clock of the tests.
var testNow = time.Unix(1_800_000_000, 0)

// fakeJWT builds an unsigned JWT with the given claims (the plugin never
// verifies signatures).
func fakeJWT(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(b) + ".sig"
}

// idToken is an id_token with email, account id and plan.
func idToken(email, accountID, plan string) string {
	return fakeJWT(map[string]any{
		"email": email,
		authClaim: map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  plan,
		},
	})
}

// testEndpoints point every service root at base.
func testEndpoints(base string) Endpoints { return Endpoints{Auth: base, ChatGPT: base} }

// startPlugin runs p in the SDK harness with a fake host (in-memory KV)
// that knows the plugin's manifest, so it relays and fetches only to the
// hosts the core would, plus the tests' own authorization servers; it sends
// the fetches of route calls to their URL itself, as the core does through
// the proxy of their ticket (sendFetch).
func startPlugin(t *testing.T, p *Plugin) *pluginsdktest.Harness {
	t.Helper()
	h := pluginsdktest.Start(t, p, pluginsdktest.Options{Manifest: testManifest, SDK: []pluginsdk.Option{pluginsdk.WithInfo("codex", Version)}})
	h.Host.ExternalServices = []string{"auth.openai.com", "chatgpt.com", "127.0.0.1", "auth.example"}
	h.Host.ExecuteHTTPFunc = sendFetch
	return h
}

// sendFetch sends a fetch to its URL, the way the core's fetch does
// (execfetch): the headers as given, no redirects, the answer whole; a
// failed exchange is a transport error.
func sendFetch(ctx context.Context, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, in.GetMethod(), in.GetUrl(), bytes.NewReader(in.GetBody()))
	if err != nil {
		return nil, err
	}
	for k, v := range in.GetHeaders() {
		req.Header.Set(k, v)
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return &pluginv1.ExecutionHTTPResponse{TransportError: "execution HTTP request failed"}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return &pluginv1.ExecutionHTTPResponse{Status: int32(resp.StatusCode), Body: body}, nil
}

// testTicket is the proxy ticket the tests' console hands to a start route.
const testTicket = "sealed-proxy-ticket"

// startBody is the body of a start route: the account form's proxy ticket.
var startBody = map[string]string{"proxy_ticket": testTicket}

// testManifest is the plugin's own manifest.json.
var testManifest = func() []byte {
	b, err := os.ReadFile("../../manifest.json")
	if err != nil {
		panic(err)
	}
	return b
}()

// mustPlugin is the plugin with the given roots and the plugin's manifest.
func mustPlugin(ep Endpoints) *Plugin {
	p, err := NewWithEndpoints(ep, testManifest)
	if err != nil {
		panic(err)
	}
	return p
}

// newTestPlugin is mustPlugin with the fixed clock.
func newTestPlugin(ep Endpoints) *Plugin {
	p := mustPlugin(ep)
	p.now = func() time.Time { return testNow }
	return p
}

func account(typ, creds string) *pluginv1.Account {
	return &pluginv1.Account{Id: 7, Platform: "openai", Type: typ, CredentialsJson: creds}
}

func mustJSON(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

// mustData reads a route answer {"data": v} (pluginsdk.DataResponse).
func mustData(t *testing.T, raw []byte, v any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	mustJSON(t, raw, &env)
	if len(env.Data) == 0 {
		t.Fatalf("no data in %s", raw)
	}
	mustJSON(t, env.Data, v)
}
