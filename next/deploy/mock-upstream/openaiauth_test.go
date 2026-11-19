package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proxiedClient is a client whose proxy is srv, trusting the mock's CA in dir.
func proxiedClient(t *testing.T, srv *httptest.Server, dir string) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	proxy, _ := url.Parse(srv.URL)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

// The device code login and the token exchange, sent to https://auth.openai.com
// through the mock as the proxy (CONNECT): every request is recorded as
// proxied over TLS for that host; the first poll is pending.
func TestOpenAIAuthThroughConnect(t *testing.T) {
	s := newServer()
	s.tunnels = newTunnelListener()
	h := s.handler()
	dir := t.TempDir()
	cfg, err := tlsConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = tunnelServer(h, cfg).ServeTLS(s.tunnels, "", "") }()
	t.Cleanup(func() { _ = s.tunnels.Close() })
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := proxiedClient(t, srv, dir)

	send := func(path, contentType, body string) (int, map[string]any) {
		t.Helper()
		resp, err := c.Post("https://auth.openai.com"+path, contentType, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	st, start := send("/api/accounts/deviceauth/usercode", "application/json", `{"client_id":"app_x"}`)
	if st != 200 || start["device_auth_id"] == "" || start["user_code"] == "" || start["interval"] != "1" {
		t.Fatalf("usercode = %d %v", st, start)
	}
	poll, _ := json.Marshal(map[string]any{"device_auth_id": start["device_auth_id"], "user_code": start["user_code"]})
	if st, _ := send("/api/accounts/deviceauth/token", "application/json", string(poll)); st != http.StatusForbidden {
		t.Fatalf("first poll = %d, want 403 pending", st)
	}
	st, approval := send("/api/accounts/deviceauth/token", "application/json", string(poll))
	code, _ := approval["authorization_code"].(string)
	if st != 200 || code == "" || approval["code_verifier"] == "" {
		t.Fatalf("second poll = %d %v", st, approval)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {"v"}}
	st, tok := send("/oauth/token", "application/x-www-form-urlencoded", form.Encode())
	if st != 200 || tok["access_token"] != "at-"+code || tok["refresh_token"] == "" {
		t.Fatalf("token = %d %v", st, tok)
	}
	idt, _ := tok["id_token"].(string)
	parts := strings.Split(idt+"..", ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if strings.Count(idt, ".") != 2 || !strings.Contains(string(payload), `"chatgpt_account_id":"acct-e2e"`) {
		t.Fatalf("id_token = %s (%s)", idt, payload)
	}

	s.mu.Lock()
	log := append([]recorded(nil), s.log...)
	s.mu.Unlock()
	if len(log) != 4 {
		t.Fatalf("recorded %d requests, want 4: %+v", len(log), log)
	}
	for _, r := range log {
		if !r.Proxied || !r.TLS || r.Host != "auth.openai.com" {
			t.Fatalf("want proxied TLS to auth.openai.com: %+v", r)
		}
	}
	if log[1].Status != http.StatusForbidden || log[3].Path != "/oauth/token" {
		t.Fatalf("recorded = %+v", log)
	}
}

// Sent to the mock directly the same request is not proxied; a CONNECT to a
// mock without a TLS listener is refused.
func TestOpenAIAuthDirectAndNoTunnel(t *testing.T) {
	s, srv := newTestServer(t)
	if resp := post(t, srv, "/api/accounts/deviceauth/usercode", nil, `{"client_id":"app_x"}`); resp.StatusCode != 200 {
		t.Fatalf("usercode = %d", resp.StatusCode)
	}
	if resp := post(t, srv, "/oauth/token", nil, "grant_type=refresh_token"); resp.StatusCode != 400 {
		t.Fatalf("token without a code = %d", resp.StatusCode)
	}
	s.mu.Lock()
	r := s.log[0]
	s.mu.Unlock()
	if r.Proxied || r.TLS {
		t.Fatalf("direct request recorded as %+v", r)
	}
	req, _ := http.NewRequest(http.MethodConnect, srv.URL, nil)
	req.Host = "auth.openai.com:443"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("CONNECT without TLS = %d", resp.StatusCode)
	}
}
