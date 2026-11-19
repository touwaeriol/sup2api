package account

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// testServers is a manifest declaring the host of every httptest server.
var testServers = &manifest.Manifest{Key: "anthropic", ExternalServices: []string{"127.0.0.1"}}

// SendPluginAccountRequest is the host side of HostService.SendAccountRequest:
// the request leaves from the account's own egress, only for the plugin's own
// accounts, and every request sent is audited without its secrets.
func TestSendPluginAccountRequest(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mine := e.mkRawAccount("anthropic", "apikey", "mine", "sk-mine", false)
	other := e.mkRawAccount("relay", "relay_key", "theirs", "sk-theirs", true)

	var gotMethod, gotAuth, gotType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/reset", http.StatusFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotAuth, gotType, gotBody = r.Method, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)
		w.Header().Set("X-Reset", "1")
		_, _ = w.Write([]byte(`{"ok":true,"token":"resp-secret"}`))
	}))
	defer srv.Close()

	in := &pluginv1.SendAccountRequestRequest{AccountId: mine, Method: "post", Url: srv.URL + "/reset?code=query-secret",
		Headers: map[string]string{"Authorization": "Bearer sk-header-secret"}, Body: []byte(`{"code":"body-secret"}`)}
	out, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetStatus() != 200 || string(out.GetBody()) != `{"ok":true,"token":"resp-secret"}` || out.GetHeaders()["x-reset"] != "1" ||
		out.GetTransportError() != "" {
		t.Fatalf("answer = %v", out)
	}
	if gotMethod != http.MethodPost || gotAuth != "Bearer sk-header-secret" || gotType != "application/json" || gotBody != `{"code":"body-secret"}` {
		t.Fatalf("upstream saw %s %q %q %q", gotMethod, gotAuth, gotType, gotBody)
	}

	// A redirect is answered as it is, not followed.
	in.Url, in.Method, in.Body = srv.URL+"/moved", "", nil
	if out, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, in); err != nil || out.GetStatus() != http.StatusFound {
		t.Fatalf("redirect: %v %v", out, err)
	}

	// A transport failure is the answer, not an error.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	in.Url = dead.URL + "/reset"
	if out, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, in); err != nil || out.GetStatus() != 0 || out.GetTransportError() == "" {
		t.Fatalf("transport failure: %v %v", out, err)
	}

	// Another plugin's account is not found, like a missing id, and nothing
	// is sent for it.
	in.Url, gotMethod = srv.URL+"/reset", ""
	in.AccountId = other
	_, foreign := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, in)
	in.AccountId = other + 10_000
	_, missing := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, in)
	if core.AsError(foreign).Code != "not_found" || core.AsError(missing).Message != core.AsError(foreign).Message || gotMethod != "" {
		t.Fatalf("foreign %v, missing %v, sent %q", foreign, missing, gotMethod)
	}

	rows := e.auditRows(AuditPluginAccountRequest)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want one per request sent", len(rows))
	}
	d := rows[0]["detail"].(map[string]any)
	if rows[0]["target_id"] != itoa(mine) || d["plugin_key"] != "anthropic" || d["method"] != "POST" ||
		d["host"] != strings.TrimPrefix(srv.URL, "http://") || d["path"] != "/reset" || d["status"] != float64(200) {
		t.Fatalf("audit = %v", rows[0])
	}
	if d := rows[2]["detail"].(map[string]any); d["method"] != "GET" || d["status"] != float64(0) {
		t.Fatalf("transport failure audit = %v", d)
	}
	// The path is recorded escaped: a decoded NUL would make the jsonb row
	// fail, so the request would be sent without its audit row.
	in.AccountId, in.Url = mine, srv.URL+"/x%00y"
	if _, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, in); err != nil {
		t.Fatal(err)
	}
	if rows = e.auditRows(AuditPluginAccountRequest); len(rows) != 4 || rows[3]["detail"].(map[string]any)["path"] != "/x%00y" {
		t.Fatalf("escaped path audit = %v", rows)
	}
	raw, _ := json.Marshal(rows)
	for _, secret := range []string{"sk-header-secret", "body-secret", "query-secret", "resp-secret", "sk-mine"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("audit holds %q: %s", secret, raw)
		}
	}
}

// An answer cut short (the connection closes before the declared length) is
// a transport failure, not a shorter answer: the plugin gets neither its
// status nor its part of the body.
func TestSendPluginAccountRequestCutShort(t *testing.T) {
	e := setup(t)
	id := e.mkRawAccount("anthropic", "apikey", "mine", "sk-mine", false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte(`{"ok":`))
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()
	out, err := e.svc.SendPluginAccountRequest(context.Background(), "anthropic", testServers,
		&pluginv1.SendAccountRequestRequest{AccountId: id, Url: srv.URL + "/x"})
	if err != nil || out.GetTransportError() == "" || out.GetStatus() != 0 || len(out.GetBody()) != 0 {
		t.Fatalf("cut short: %v %v", out, err)
	}
}

// SendPluginAccountRequest reaches the hosts a relay of the account may
// (manifest.UpstreamHostAllowed): the plugin's externalServices and their
// subdomains, and the account's own upstream addresses, read from its
// settings and credentials. Any other host is refused before anything is
// sent or audited. The answer is whole, whatever its size.
func TestSendPluginAccountRequestHosts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	large := strings.Repeat("x", 256<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	local := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	m := &manifest.Manifest{Key: "relay", ExternalServices: []string{"ext.invalid"},
		AccountTypes: []manifest.AccountType{{ID: "relay_key", UpstreamFields: []string{"base_url", "asset_base_url"}}}}
	id := e.mkRawAccount("relay", "relay_key", "relay", "sk-relay", true)
	e.exec1(`UPDATE accounts SET settings = $1 WHERE id = $2 RETURNING id`, `{"base_url":"`+local+`"}`, id)
	send := func(u string) (*pluginv1.ExecutionHTTPResponse, error) {
		return e.svc.SendPluginAccountRequest(ctx, "relay", m, &pluginv1.SendAccountRequestRequest{AccountId: id, Method: "POST", Url: u, Body: []byte(large)})
	}

	// The account's own address: sent, the whole body and answer.
	out, err := send(local + "/assets/list")
	if err != nil || out.GetStatus() != 200 || string(out.GetBody()) != large {
		t.Fatalf("account address: %v status %d, %d bytes", err, out.GetStatus(), len(out.GetBody()))
	}
	// An externalServices host or a subdomain of one passes the rule (the
	// name resolves nowhere: a transport failure, not a refusal).
	for _, u := range []string{"https://ext.invalid/x", "https://api.ext.invalid/x"} {
		if out, err := send(u); err != nil || out.GetStatus() != 0 || out.GetTransportError() == "" {
			t.Fatalf("%s: %v %v", u, out, err)
		}
	}
	audited := len(e.auditRows(AuditPluginAccountRequest))
	// Neither: refused, nothing sent or audited. 127.0.0.1 is the same
	// server under a name the account does not hold.
	for _, u := range []string{srv.URL + "/x", "https://ext.invalid.evil.example/x", "https://sub." + strings.TrimPrefix(local, "http://") + "/x"} {
		if _, err := send(u); core.AsError(err).Code != "permission_denied" || !strings.Contains(err.Error(), "relay") {
			t.Fatalf("%s: %v", u, err)
		}
	}
	// An address in the credentials counts as one in the settings.
	enc, err := e.svc.d.Cipher.Encrypt([]byte(`{"api_key":"sk","asset_base_url":"`+srv.URL+`"}`), aad("relay"))
	if err != nil {
		t.Fatal(err)
	}
	e.exec1(`UPDATE accounts SET credentials_enc = $1 WHERE id = $2 RETURNING id`, enc, id)
	if out, err := send(srv.URL + "/x"); err != nil || out.GetStatus() != 200 {
		t.Fatalf("credential address: %v %v", out, err)
	}
	if n := len(e.auditRows(AuditPluginAccountRequest)); n != audited+1 {
		t.Fatalf("audit rows %d, want %d: refused requests are not audited", n, audited+1)
	}
}

// SendPluginAccountRequest sends through the proxy the account holds when
// the request is made, read with its credentials (CONTRACTS §123): a proxy
// set straight in the table carries the next request, and a disabled one is
// a transport error with nothing sent directly.
func TestSendPluginAccountRequestThroughAccountProxy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var direct atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		_, _ = w.Write([]byte(`{"via":"direct"}`))
	}))
	defer srv.Close()
	var mu sync.Mutex
	var via []string
	prx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		via = append(via, r.Method+" "+r.URL.String())
		mu.Unlock()
		_, _ = w.Write([]byte(`{"via":"proxy"}`))
	}))
	defer prx.Close()
	id := e.mkRawAccount("anthropic", "apikey", "mine", "sk-mine", false)
	port := prx.Listener.Addr().(*net.TCPAddr).Port
	on := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('p', 'http', '127.0.0.1', $1) RETURNING id`, port)
	off := e.exec1(`INSERT INTO proxies (name, protocol, host, port, status) VALUES ('off', 'http', '127.0.0.1', $1, 'disabled') RETURNING id`, port)
	send := func(proxyID int64) *pluginv1.ExecutionHTTPResponse {
		t.Helper()
		e.exec1(`UPDATE accounts SET proxy_id = $1 WHERE id = $2 RETURNING id`, proxyID, id)
		out, err := e.svc.SendPluginAccountRequest(ctx, "anthropic", testServers, &pluginv1.SendAccountRequestRequest{AccountId: id, Method: "POST", Url: srv.URL + "/reset"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if out := send(on); out.GetStatus() != 200 || string(out.GetBody()) != `{"via":"proxy"}` {
		t.Fatalf("via proxy: %v", out)
	}
	if out := send(off); out.GetStatus() != 0 || !strings.Contains(out.GetTransportError(), "disabled") {
		t.Fatalf("disabled proxy: %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(via) != 1 || via[0] != "POST "+srv.URL+"/reset" || direct.Load() != 0 {
		t.Fatalf("proxy saw %v, %d sent directly", via, direct.Load())
	}
}
