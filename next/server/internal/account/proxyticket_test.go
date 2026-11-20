package account

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// A proxy ticket carries the account form's proxy choice to an
// authorization flow (CONTRACTS §117): checked when issued exactly as
// models/fetch checks it, then each fetch with it goes through that proxy -
// or directly - as long as the user of the route call is the one it was
// issued to and may still use the proxy. A pasted proxy URL is only used:
// it creates no proxy row and is never written to a log or an audit row.
func TestProxyTicket(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	az := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, az)
	ctx := context.Background()
	admin := e.uid
	alice, carol, reader := e.addUser("alice@x.com"), e.addUser("carol@x.com"), e.addUser("reader@x.com")
	az.keys[admin], az.keys[alice], az.keys[reader] = adminKeys, supplierKeys, readerKeys
	// carol may create accounts but holds no proxy key at all.
	az.keys[carol] = []string{"account:create:own", "account:read:own", "account:update:own"}

	// The authorization server: where every fetch must end up.
	var issuer atomic.Int64
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuer.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"path":%q,"body":%q,"ua":%q}`, r.URL.Path, body, r.Header.Get("User-Agent"))
	}))
	t.Cleanup(auth.Close)
	savedHost, savedPort, savedHits := forwardProxy(t)
	pastedHost, pastedPort, pastedHits := forwardProxy(t)
	const password = "s3cret-proxy-pw"
	pasted := fmt.Sprintf("http://agent:%s@%s:%d", password, pastedHost, pastedPort)

	saved := e.createAs(admin, "saved", map[string]any{"proxy_url": fmt.Sprintf("http://%s:%d", savedHost, savedPort)})
	savedID := int64(saved["proxy_id"].(float64))
	proxies := func() (n int) {
		_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM proxies`).Scan(&n)
		return n
	}
	before := proxies()

	issue := func(uid int64, body map[string]any) (int, map[string]any, string) {
		t.Helper()
		code, out := e.doAs(uid, "POST", "/account-types/anthropic/apikey/proxy-ticket", body)
		ticket, _ := data(out)["ticket"].(string)
		return code, out, ticket
	}
	fetch := func(uid int64, plugin, ticket string) (*pluginv1.ExecutionHTTPResponse, error) {
		return e.svc.FetchWithProxyTicket(core.WithUserID(ctx, uid), plugin, &pluginv1.ExecutionHTTPRequest{
			Method: "POST", Url: auth.URL + "/oauth/token", Headers: map[string]string{"User-Agent": "ua/1"},
			Body: []byte("grant_type=x"), ProxyTicket: ticket})
	}
	// via fetches with ticket as uid and reports which proxies it went through.
	via := func(uid int64, ticket string) string {
		t.Helper()
		s0, p0, i0 := savedHits.Load(), pastedHits.Load(), issuer.Load()
		out, err := fetch(uid, "anthropic", ticket)
		if err != nil || out.GetStatus() != 200 || out.GetTransportError() != "" || issuer.Load() != i0+1 ||
			!strings.Contains(string(out.GetBody()), `"body":"grant_type=x","ua":"ua/1"`) {
			t.Fatalf("fetch: %v %v (issuer %d -> %d)", out, err, i0, issuer.Load())
		}
		return fmt.Sprintf("saved=%d pasted=%d", savedHits.Load()-s0, pastedHits.Load()-p0)
	}

	// The three choices of the form.
	code, out, byID := issue(admin, map[string]any{"proxy_id": savedID})
	if code != 200 || byID == "" {
		t.Fatalf("ticket for proxy %d: %d %v", savedID, code, out)
	}
	if got := via(admin, byID); got != "saved=1 pasted=0" {
		t.Fatalf("existing proxy: %s", got)
	}
	code, out, byURL := issue(admin, map[string]any{"proxy_url": "  " + pasted + " "})
	if code != 200 || strings.Contains(byURL, password) {
		t.Fatalf("ticket for a pasted proxy: %d %v", code, out)
	}
	if got := via(admin, byURL); got != "saved=0 pasted=1" {
		t.Fatalf("pasted proxy: %s", got)
	}
	code, out, direct := issue(admin, map[string]any{"proxy_id": nil})
	if code != 200 {
		t.Fatalf("ticket without a proxy: %d %v", code, out)
	}
	if got := via(admin, direct); got != "saved=0 pasted=0" {
		t.Fatalf("no proxy: %s", got)
	}
	// A supplier (own scopes) may use a pasted proxy too.
	if code, out, ticket := issue(alice, map[string]any{"proxy_url": pasted}); code != 200 || via(alice, ticket) != "saved=0 pasted=1" {
		t.Fatalf("supplier: %d %v", code, out)
	}

	// Issuing checks as models/fetch does.
	if code, out, _ := issue(carol, map[string]any{"proxy_url": pasted}); code != 403 || permission(out) != "proxy:manage:own" {
		t.Fatalf("pasted proxy without a proxy key: %d %v", code, out)
	}
	if code, out, _ := issue(carol, map[string]any{"proxy_id": savedID}); code != 400 || fmt.Sprint(fieldsOf(out)) != "[proxy_id:not_found]" {
		t.Fatalf("someone else's proxy: %d %v", code, out)
	}
	if code, out, _ := issue(admin, map[string]any{"proxy_id": savedID, "proxy_url": pasted}); code != 400 || fmt.Sprint(fieldsOf(out)) != "[proxy_url:conflict]" {
		t.Fatalf("both: %d %v", code, out)
	}
	if code, out, _ := issue(admin, map[string]any{"proxy_url": "socks5://agent:" + password + "@h"}); code != 400 ||
		fmt.Sprint(fieldsOf(out)) != "[proxy_url:invalid]" || strings.Contains(fmt.Sprint(out), password) {
		t.Fatalf("bad pasted proxy: %d %v", code, out)
	}
	if code, out, _ := issue(reader, map[string]any{}); code != 403 {
		t.Fatalf("a user who can neither create nor update accounts: %d %v", code, out)
	}
	if code, out := e.doAs(admin, "POST", "/account-types/ghost/apikey/proxy-ticket", map[string]any{}); code != 400 || fmt.Sprint(fieldsOf(out)) != "[type:unknown]" {
		t.Fatalf("unknown type: %d %v", code, out)
	}

	// Using it: only the user it was issued to, only its plugin, only intact.
	i0 := issuer.Load()
	for name, try := range map[string]func() (*pluginv1.ExecutionHTTPResponse, error){
		"another user":   func() (*pluginv1.ExecutionHTTPResponse, error) { return fetch(alice, "anthropic", byID) },
		"no user":        func() (*pluginv1.ExecutionHTTPResponse, error) { return fetch(0, "anthropic", byID) },
		"another plugin": func() (*pluginv1.ExecutionHTTPResponse, error) { return fetch(admin, "relay", byID) },
		"tampered": func() (*pluginv1.ExecutionHTTPResponse, error) {
			return fetch(admin, "anthropic", byID[:len(byID)-2]+"AA")
		},
		"made up":          func() (*pluginv1.ExecutionHTTPResponse, error) { return fetch(admin, "anthropic", "not-a-ticket") },
		"without a ticket": func() (*pluginv1.ExecutionHTTPResponse, error) { return fetch(admin, "anthropic", "") },
	} {
		if _, err := try(); !denied(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Every use checks the user's permissions of now: without proxy:manage
	// the pasted proxy is out of reach, without its account keys the saved
	// one too.
	az.keys[admin] = []string{"account:create", "proxy:read"}
	if _, err := fetch(admin, "anthropic", byURL); !denied(err) {
		t.Errorf("pasted proxy after proxy:manage was revoked: %v", err)
	}
	az.keys[admin] = []string{"proxy:read"}
	if _, err := fetch(admin, "anthropic", byID); !denied(err) {
		t.Errorf("after the account keys were revoked: %v", err)
	}
	if issuer.Load() != i0 {
		t.Fatalf("refused fetches reached the issuer: %d", issuer.Load()-i0)
	}

	// A proxy disabled after its ticket was issued refuses the fetch like a
	// revoked permission does: an error of the call, not a transport failure
	// of a request that was never sent.
	az.keys[admin] = adminKeys
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE proxies SET status = 'disabled', updated_at = clock_timestamp() WHERE id = $1`, savedID); err != nil {
		t.Fatal(err)
	}
	e.prx.Invalidate(savedID)
	if out, err := fetch(admin, "anthropic", byID); err == nil || core.AsError(err).Code != core.ErrUnavailable.Code || issuer.Load() != i0 {
		t.Fatalf("fetch through a disabled proxy: %v %v (issuer hits %d)", out, err, issuer.Load()-i0)
	}

	// The pasted proxy was only used.
	if proxies() != before {
		t.Fatalf("a pasted proxy was stored: %d -> %d proxies", before, proxies())
	}
	var audited int
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE detail::text LIKE '%' || $1 || '%'`, password).Scan(&audited); err != nil || audited != 0 {
		t.Fatalf("audit rows with the proxy password: %d %v", audited, err)
	}
	if strings.Contains(logs.String(), password) {
		t.Fatalf("the proxy password reached the log:\n%s", logs.String())
	}
}

func TestProxyChoiceCheck(t *testing.T) {
	e := setup(t)
	ctx := core.WithUserID(context.Background(), e.uid)
	blank := "   "
	for name, c := range map[string]ProxyChoice{"none": {}, "blank url": {ProxyURL: &blank}} {
		got, err := e.svc.checkChoice(ctx, c, "account:create")
		if err != nil || got.proxied() || got.choice().ProxyURL != nil || got.choice().ProxyID != nil {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
}

// denied reports a permission_denied error.
func denied(err error) bool {
	return err != nil && core.AsError(err).Code == core.ErrPermissionDenied.Code
}
