package account

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Key sets of the typical roles of CONTRACTS §21.1.
var (
	adminKeys = []string{"account:read", "account:create", "account:update", "account:delete", "account:test",
		"account:credential:view", "account:settings:custom", "proxy:read", "proxy:manage"}
	supplierKeys = []string{"account:read:own", "account:create:own", "account:update:own", "account:delete:own",
		"account:test:own", "account:credential:view:own", "proxy:read:own", "proxy:manage:own"}
	readerKeys = []string{"account:read", "proxy:read"}
)

func data(out map[string]any) map[string]any {
	d, _ := out["data"].(map[string]any)
	return d
}

func total(out map[string]any) float64 {
	p, _ := out["page"].(map[string]any)
	n, _ := p["total"].(float64)
	return n
}

func permission(out map[string]any) string {
	err, _ := out["error"].(map[string]any)
	det, _ := err["details"].(map[string]any)
	p, _ := det["permission"].(string)
	return p
}

func listIDs(out map[string]any) string {
	var s []string
	items, _ := out["data"].([]any)
	for _, it := range items {
		s = append(s, strconv.Itoa(int(it.(map[string]any)["id"].(float64))))
	}
	return strings.Join(s, ",")
}

// createAs posts a minimal anthropic/apikey account as uid with extra fields
// merged in and asserts 201.
func (e *env) createAs(uid int64, name string, extra map[string]any) map[string]any {
	e.t.Helper()
	body := map[string]any{"name": name, "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"}}
	for k, v := range extra {
		body[k] = v
	}
	code, out := e.doAs(uid, "POST", "/accounts", body)
	if code != 201 {
		e.t.Fatalf("create %s as %d: %d %v", name, uid, code, out)
	}
	return data(out)
}

type auditRow struct {
	Action string
	UserID *int64
	IP     string
	Detail map[string]any
}

func (e *env) audits(targetType string, targetID int64) []auditRow {
	e.t.Helper()
	rows, err := e.db.SQL.QueryContext(context.Background(), `SELECT action, user_id, ip, detail FROM audit_logs
		WHERE target_type = $1 AND target_id = $2 ORDER BY id`, targetType, strconv.FormatInt(targetID, 10))
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.Action, &r.UserID, &r.IP, &r.Detail); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func actions(rs []auditRow) string {
	var s []string
	for _, r := range rs {
		s = append(s, r.Action)
	}
	return strings.Join(s, ",")
}

func TestOwnership(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	ctx := context.Background()
	admin := e.uid
	alice, bob, reader := e.addUser("alice@x.com"), e.addUser("bob@x.com"), e.addUser("ro@x.com")
	authz.keys[admin], authz.keys[alice], authz.keys[bob], authz.keys[reader] = adminKeys, supplierKeys, supplierKeys, readerKeys

	a1 := e.createAs(alice, "alice's", nil)
	if a1["created_by"].(float64) != float64(alice) || a1["created_by_email"] != "alice@x.com" || a1["proxy_created"] != false {
		t.Fatalf("created_by: %v", a1)
	}
	aliceID := int64(a1["id"].(float64))
	bobID := int64(e.createAs(bob, "bob's", nil)["id"].(float64))
	a3 := e.createAs(admin, "admin's", nil)
	adminID := int64(a3["id"].(float64))
	// A new account without max_concurrency is unlimited, except for an
	// own-level caller, who cannot set unlimited.
	if a1["max_concurrency"] != float64(ownDefaultConcurrency) || a3["max_concurrency"] != float64(0) {
		t.Fatalf("default max_concurrency: own %v, all %v", a1["max_concurrency"], a3["max_concurrency"])
	}
	// A row without creator (before ownership existed).
	enc, err := e.svc.d.Cipher.Encrypt([]byte(`{"api_key":"legacy-key-1"}`), aad("anthropic"))
	if err != nil {
		t.Fatal(err)
	}
	legacyID := e.exec1(`INSERT INTO accounts (name, plugin_key, type, credentials_enc) VALUES ('legacy', 'anthropic', 'apikey', $1) RETURNING id`, enc)

	// Lists: the own range sees own rows only; the all range sees everything
	// and can filter with mine / created_by (ignored under the own range).
	_, out := e.doAs(alice, "GET", "/accounts", nil)
	if total(out) != 1 || listIDs(out) != strconv.FormatInt(aliceID, 10) {
		t.Fatalf("alice list: %v", out)
	}
	if _, out = e.doAs(reader, "GET", "/accounts", nil); total(out) != 4 {
		t.Fatalf("reader list: %v", out)
	}
	if _, out = e.doAs(admin, "GET", "/accounts?mine=true", nil); total(out) != 1 || listIDs(out) != strconv.FormatInt(adminID, 10) {
		t.Fatalf("admin mine: %v", out)
	}
	if _, out = e.doAs(admin, "GET", fmt.Sprintf("/accounts?created_by=%d", bob), nil); total(out) != 1 || listIDs(out) != strconv.FormatInt(bobID, 10) {
		t.Fatalf("admin created_by: %v", out)
	}
	if code, out := e.doAs(admin, "GET", "/accounts?created_by=abc", nil); code != 400 || fmt.Sprint(fieldsOf(out)) != "[created_by:invalid]" {
		t.Fatalf("bad created_by: %d %v", code, out)
	}
	// created_by filters within the caller's range (CONTRACTS §21.0).
	if _, out = e.doAs(alice, "GET", fmt.Sprintf("/accounts?created_by=%d", bob), nil); total(out) != 0 {
		t.Fatalf("alice created_by bob: %v", out)
	}
	if _, out = e.doAs(alice, "GET", fmt.Sprintf("/accounts?created_by=%d", alice), nil); total(out) != 1 || listIDs(out) != strconv.FormatInt(aliceID, 10) {
		t.Fatalf("alice created_by alice: %v", out)
	}
	if _, out = e.doAs(alice, "GET", "/accounts?mine=true", nil); total(out) != 1 {
		t.Fatalf("alice mine: %v", out)
	}
	// The list carries the creator, never proxy_created.
	_, raw := e.doRaw(reader, "GET", "/accounts", nil)
	if strings.Contains(string(raw), "proxy_created") || !strings.Contains(string(raw), `"created_by_email":"bob@x.com"`) ||
		!strings.Contains(string(raw), `"created_by":null`) {
		t.Fatalf("list json: %s", raw)
	}

	// Everything about somebody else's account (or a legacy row) is 404
	// under the own range.
	for _, other := range []int64{bobID, adminID, legacyID} {
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/accounts/%d", nil},
			{"PATCH", "/accounts/%d", map[string]any{"name": "hijack"}},
			{"DELETE", "/accounts/%d", nil},
			{"POST", "/accounts/%d/test", nil},
			{"POST", "/accounts/%d/models/fetch", nil},
			{"POST", "/accounts/%d/credentials/reveal", nil},
		} {
			if code, out := e.doAs(alice, req.method, fmt.Sprintf(req.path, other), req.body); code != 404 {
				t.Fatalf("alice %s %d: %d %v", req.method+" "+req.path, other, code, out)
			}
		}
	}
	var name string
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT name FROM accounts WHERE id = $1`, bobID).Scan(&name)
	if name != "bob's" {
		t.Fatalf("bob's account renamed: %s", name)
	}

	// Readers see everything but hold no write or credential key.
	if code, out := e.doAs(reader, "GET", fmt.Sprintf("/accounts/%d", bobID), nil); code != 200 || data(out)["created_by_email"] != "bob@x.com" {
		t.Fatalf("reader get: %d %v", code, out)
	}
	if code, out := e.doAs(reader, "PATCH", fmt.Sprintf("/accounts/%d", bobID), map[string]any{"name": "x"}); code != 403 || permission(out) != "account:update" {
		t.Fatalf("reader patch: %d %v", code, out)
	}
	if code, out := e.doAs(reader, "POST", fmt.Sprintf("/accounts/%d/credentials/reveal", bobID), nil); code != 403 || permission(out) != "account:credential:view" {
		t.Fatalf("reader reveal: %d %v", code, out)
	}
	if code, out := e.doAs(reader, "POST", "/accounts", map[string]any{"name": "x"}); code != 403 || permission(out) != "account:create" {
		t.Fatalf("reader create: %d %v", code, out)
	}

	// Own rows: every action works.
	own := fmt.Sprintf("/accounts/%d", aliceID)
	if code, out := e.doAs(alice, "GET", own, nil); code != 200 || data(out)["created_by_email"] != "alice@x.com" || data(out)["credentials"] == nil {
		t.Fatalf("alice get own: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "PATCH", own, map[string]any{"name": "mine"}); code != 200 || data(out)["name"] != "mine" || data(out)["proxy_created"] != false {
		t.Fatalf("alice patch own: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "POST", own+"/test", nil); code != 200 || data(out)["ok"] != true {
		t.Fatalf("alice test own: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "POST", own+"/models/fetch", nil); code != 200 || len(data(out)["models"].([]any)) != 2 {
		t.Fatalf("alice models own: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "POST", own+"/credentials/reveal", nil); code != 200 ||
		data(out)["credentials"].(map[string]any)["api_key"] != "sk-good-key-123" {
		t.Fatalf("alice reveal own: %d %v", code, out)
	}

	// The all range reaches every row, including legacy ones, and shows the
	// creator email even after the user is soft-deleted.
	if code, out := e.doAs(admin, "GET", fmt.Sprintf("/accounts/%d", legacyID), nil); code != 200 || data(out)["created_by"] != nil || data(out)["created_by_email"] != nil {
		t.Fatalf("admin get legacy: %d %v", code, out)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, bob); err != nil {
		t.Fatal(err)
	}
	if code, out := e.doAs(admin, "GET", fmt.Sprintf("/accounts/%d", bobID), nil); code != 200 || data(out)["created_by_email"] != "bob@x.com" {
		t.Fatalf("deleted creator email: %d %v", code, out)
	}
	if code, _ := e.doAs(admin, "PATCH", fmt.Sprintf("/accounts/%d", bobID), map[string]any{"status": "disabled"}); code != 200 {
		t.Fatalf("admin patch bob's: %d", code)
	}
	if code, _ := e.doAs(admin, "DELETE", fmt.Sprintf("/accounts/%d", legacyID), nil); code != 204 {
		t.Fatalf("admin delete legacy: %d", code)
	}

	// Delete own; 404 afterwards.
	if code, _ := e.doAs(alice, "DELETE", own, nil); code != 204 {
		t.Fatalf("alice delete own: %d", code)
	}
	if code, _ := e.doAs(alice, "GET", own, nil); code != 404 {
		t.Fatalf("alice get deleted: %d", code)
	}

	// Audit trail of alice's account, all by alice with the client IP.
	rs := e.audits("account", aliceID)
	if actions(rs) != "account.create,account.update,account.credentials.reveal,account.delete" {
		t.Fatalf("audit actions: %s", actions(rs))
	}
	for _, r := range rs {
		if r.UserID == nil || *r.UserID != alice || r.IP == "" {
			t.Fatalf("audit row %s: user %v ip %q", r.Action, r.UserID, r.IP)
		}
	}
	if d := rs[0].Detail; d["name"] != "alice's" || d["plugin_key"] != "anthropic" || d["type"] != "apikey" {
		t.Fatalf("create detail: %v", d)
	}
	if f := rs[1].Detail["fields"].([]any); len(f) != 1 || f[0] != "name" {
		t.Fatalf("update detail: %v", rs[1].Detail)
	}
	if rs[3].Detail["name"] != "mine" {
		t.Fatalf("delete detail: %v", rs[3].Detail)
	}
	// Admin's PATCH of bob's account is audited under admin.
	if rs := e.audits("account", bobID); actions(rs) != "account.create,account.update" || *rs[1].UserID != admin ||
		fmt.Sprint(rs[1].Detail["fields"]) != "[status]" {
		t.Fatalf("bob audit: %v", rs)
	}
}

// forwardProxy is a plain HTTP forward proxy that relays absolute-URI
// requests to their target and counts them.
func forwardProxy(t *testing.T) (host string, port int, hits *atomic.Int64) {
	hits = &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !r.URL.IsAbs() {
			http.Error(w, "not a proxy request", 400)
			return
		}
		out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	h, p, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	port, _ = strconv.Atoi(p)
	return h, port, hits
}

func TestProxyURL(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	ctx := context.Background()
	admin := e.uid
	alice, bob, carol := e.addUser("alice@x.com"), e.addUser("bob@x.com"), e.addUser("carol@x.com")
	authz.keys[admin], authz.keys[alice], authz.keys[bob] = adminKeys, supplierKeys, supplierKeys
	// carol may create accounts but holds no proxy key at all.
	authz.keys[carol] = []string{"account:create:own", "account:read:own", "account:update:own"}

	proxies := func() int {
		var n int
		_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM proxies`).Scan(&n)
		return n
	}
	const url1 = "socks5h://u:pw@Proxy.Example:1080"

	// First save creates the proxy (owned by alice, audited with the account
	// id, announced on config:changed).
	sent := e.bus.count()
	a1 := e.createAs(alice, "a1", map[string]any{"proxy_url": url1})
	if a1["proxy_created"] != true || a1["proxy_id"] == nil {
		t.Fatalf("first save: %v", a1)
	}
	a1ID, p1 := int64(a1["id"].(float64)), int64(a1["proxy_id"].(float64))
	var pname, proto, host string
	var createdBy *int64
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT name, protocol, host, created_by FROM proxies WHERE id = $1`, p1).Scan(&pname, &proto, &host, &createdBy); err != nil {
		t.Fatal(err)
	}
	if pname != "socks5://proxy.example:1080" || proto != "socks5" || host != "proxy.example" || createdBy == nil || *createdBy != alice {
		t.Fatalf("auto proxy: %s %s %s %v", pname, proto, host, createdBy)
	}
	rs := e.audits("proxy", p1)
	if actions(rs) != "proxy.create" || rs[0].Detail["auto"] != true || rs[0].Detail["account_id"].(float64) != float64(a1ID) || *rs[0].UserID != alice || rs[0].IP == "" {
		t.Fatalf("proxy audit: %v", rs)
	}
	e.bus.mu.Lock()
	chans := fmt.Sprint(e.bus.sent[sent:])
	e.bus.mu.Unlock()
	if !strings.Contains(chans, core.ChannelConfigChanged) || !strings.Contains(chans, core.ChannelAccountChanged) {
		t.Fatalf("broadcasts: %s", chans)
	}
	if actions(e.audits("account", a1ID)) != "account.create" {
		t.Fatalf("account audit: %v", e.audits("account", a1ID))
	}

	// Same address again: reused, no new row, no proxy audit.
	a2 := e.createAs(alice, "a2", map[string]any{"proxy_url": "  " + url1 + " "})
	if a2["proxy_created"] != false || int64(a2["proxy_id"].(float64)) != p1 || proxies() != 1 {
		t.Fatalf("second save: %v (%d proxies)", a2, proxies())
	}
	if len(e.audits("proxy", p1)) != 1 {
		t.Fatal("reuse audited as creation")
	}
	// Bob only matches his own proxies: he gets a second row.
	b1 := e.createAs(bob, "b1", map[string]any{"proxy_url": url1})
	if b1["proxy_created"] != true || int64(b1["proxy_id"].(float64)) == p1 || proxies() != 2 {
		t.Fatalf("bob save: %v", b1)
	}
	// Admin (proxy:read) matches across owners: the smallest id wins.
	ad := e.createAs(admin, "ad", map[string]any{"proxy_url": url1})
	if ad["proxy_created"] != false || int64(ad["proxy_id"].(float64)) != p1 {
		t.Fatalf("admin save: %v", ad)
	}

	// proxy_url is resolved in the proxy write range: proxy:read widens
	// neither the rows it matches nor allow_private of the row it creates.
	dave := e.addUser("dave@x.com")
	authz.keys[dave] = append([]string{"proxy:read"}, supplierKeys...)
	d1 := e.createAs(dave, "d1", map[string]any{"proxy_url": url1})
	if d1["proxy_created"] != true {
		t.Fatalf("own-level writer with proxy:read reused another user's proxy: %v", d1)
	}
	allowPrivate := func(id any) (v bool) {
		t.Helper()
		if err := e.db.SQL.QueryRowContext(ctx, `SELECT allow_private FROM proxies WHERE id = $1`, int64(id.(float64))).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if allowPrivate(d1["proxy_id"]) {
		t.Fatal("proxy created under proxy:manage:own has allow_private")
	}
	ad2 := e.createAs(admin, "ad2", map[string]any{"proxy_url": "http://manage.example:8080"})
	if ad2["proxy_created"] != true || !allowPrivate(ad2["proxy_id"]) {
		t.Fatalf("proxy created under proxy:manage: %v", ad2)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `DELETE FROM accounts WHERE id = ANY($1)`, []int64{int64(d1["id"].(float64)), int64(ad2["id"].(float64))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `DELETE FROM proxies WHERE id = ANY($1)`, []int64{int64(d1["proxy_id"].(float64)), int64(ad2["proxy_id"].(float64))}); err != nil {
		t.Fatal(err)
	}

	// PATCH: a new address creates and relinks; the audit lists proxy_url.
	own := fmt.Sprintf("/accounts/%d", a1ID)
	code, out := e.doAs(alice, "PATCH", own, map[string]any{"proxy_url": "http://Proxy.Example:3128"})
	if code != 200 || data(out)["proxy_created"] != true || int64(data(out)["proxy_id"].(float64)) == p1 || proxies() != 3 {
		t.Fatalf("patch new url: %d %v", code, out)
	}
	p3 := int64(data(out)["proxy_id"].(float64))
	if rs := e.audits("proxy", p3); actions(rs) != "proxy.create" || rs[0].Detail["account_id"].(float64) != float64(a1ID) {
		t.Fatalf("patch proxy audit: %v", rs)
	}
	if rs := e.audits("account", a1ID); fmt.Sprint(rs[len(rs)-1].Detail["fields"]) != "[proxy_url]" {
		t.Fatalf("patch audit: %v", rs)
	}
	// Back to the first address: reused.
	if code, out = e.doAs(alice, "PATCH", own, map[string]any{"proxy_url": url1}); code != 200 || data(out)["proxy_created"] != false || int64(data(out)["proxy_id"].(float64)) != p1 {
		t.Fatalf("patch reuse: %d %v", code, out)
	}
	// Both given: conflict. Blank proxy_url counts as absent.
	if code, out = e.doAs(alice, "PATCH", own, map[string]any{"proxy_url": url1, "proxy_id": p1}); code != 400 || fmt.Sprint(fieldsOf(out)) != "[proxy_url:conflict]" {
		t.Fatalf("conflict: %d %v", code, out)
	}
	if code, out = e.doAs(alice, "PATCH", own, map[string]any{"proxy_url": "", "proxy_id": nil}); code != 200 || data(out)["proxy_id"] != nil || data(out)["proxy_created"] != false {
		t.Fatalf("blank proxy_url: %d %v", code, out)
	}
	// Invalid address: 400 without echoing the password.
	code, raw := e.doRaw(alice, "POST", "/accounts", map[string]any{"name": "bad", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"}, "proxy_url": "http://u:secretpw@host.example"})
	if code != 400 || gjson.GetBytes(raw, "error.details.fields.0.field").String() != "proxy_url" ||
		gjson.GetBytes(raw, "error.details.fields.0.code").String() != "invalid" || strings.Contains(string(raw), "secretpw") {
		t.Fatalf("invalid url: %d %s", code, raw)
	}
	if proxies() != 3 {
		t.Fatal("invalid url created a proxy")
	}
	// Detail and list never carry proxy_created.
	if _, raw = e.doRaw(alice, "GET", own, nil); strings.Contains(string(raw), "proxy_created") {
		t.Fatalf("detail json: %s", raw)
	}
	if _, raw = e.doRaw(alice, "GET", "/accounts", nil); strings.Contains(string(raw), "proxy_created") {
		t.Fatalf("list json: %s", raw)
	}

	// Without a proxy manage key proxy_url is forbidden; proxy_id must be a
	// visible proxy under the own range.
	code, out = e.doAs(carol, "POST", "/accounts", map[string]any{"name": "c", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"}, "proxy_url": url1})
	if code != 403 || permission(out) != "proxy:manage:own" {
		t.Fatalf("carol proxy_url: %d %v", code, out)
	}
	byID := func(uid, pid int64) (int, []string) {
		code, out := e.doAs(uid, "POST", "/accounts", map[string]any{"name": "x", "plugin_key": "anthropic", "type": "apikey",
			"credentials": map[string]any{"api_key": "sk-good-key-123"}, "proxy_id": pid})
		return code, fieldsOf(out)
	}
	if code, f := byID(carol, p1); code != 400 || fmt.Sprint(f) != "[proxy_id:not_found]" {
		t.Fatalf("carol by id: %d %v", code, f)
	}
	if code, f := byID(bob, p1); code != 400 || fmt.Sprint(f) != "[proxy_id:not_found]" {
		t.Fatalf("bob with alice's proxy: %d %v", code, f)
	}
	if code, _ := byID(alice, p1); code != 201 {
		t.Fatalf("alice with own proxy: %d", code)
	}
	if code, _ := byID(admin, p1); code != 201 {
		t.Fatalf("admin with alice's proxy: %d", code)
	}
	c1 := e.createAs(carol, "c1", nil)
	if code, out := e.doAs(carol, "PATCH", fmt.Sprintf("/accounts/%d", int64(c1["id"].(float64))), map[string]any{"proxy_id": p1}); code != 400 ||
		fmt.Sprint(fieldsOf(out)) != "[proxy_id:not_found]" {
		t.Fatalf("carol patch proxy_id: %d %v", code, out)
	}
	if code, out := e.doAs(bob, "PATCH", fmt.Sprintf("/accounts/%d", int64(b1["id"].(float64))), map[string]any{"proxy_id": p1}); code != 400 ||
		fmt.Sprint(fieldsOf(out)) != "[proxy_id:not_found]" {
		t.Fatalf("bob patch alice's proxy_id: %d %v", code, out)
	}

	// Type models/fetch with proxy_url goes through the address without
	// creating a proxy; it takes a proxy write key like saving one.
	host, port, hits := forwardProxy(t)
	before := proxies()
	fetch := map[string]any{"credentials": map[string]any{"api_key": "sk-good-key-123"}, "proxy_url": fmt.Sprintf("http://%s:%d", host, port)}
	code, out = e.doAs(carol, "POST", "/account-types/anthropic/apikey/models/fetch", fetch)
	if code != 403 || permission(out) != "proxy:manage:own" || hits.Load() != 0 {
		t.Fatalf("fetch through proxy_url without a proxy key: %d %v hits=%d", code, out, hits.Load())
	}
	code, out = e.doAs(alice, "POST", "/account-types/anthropic/apikey/models/fetch", fetch)
	if code != 200 || len(data(out)["models"].([]any)) != 2 || hits.Load() == 0 || proxies() != before {
		t.Fatalf("fetch through proxy_url: %d %v hits=%d proxies=%d", code, out, hits.Load(), proxies())
	}
	fetch["proxy_id"] = p1
	if code, out = e.doAs(admin, "POST", "/account-types/anthropic/apikey/models/fetch", fetch); code != 400 || fmt.Sprint(fieldsOf(out)) != "[proxy_url:conflict]" {
		t.Fatalf("fetch conflict: %d %v", code, out)
	}
	delete(fetch, "proxy_url")
	if code, out = e.doAs(carol, "POST", "/account-types/anthropic/apikey/models/fetch", fetch); code != 400 || fmt.Sprint(fieldsOf(out)) != "[proxy_id:not_found]" {
		t.Fatalf("fetch invisible proxy_id: %d %v", code, out)
	}
	fetch["proxy_url"] = "ftp://x:1"
	delete(fetch, "proxy_id")
	if code, out = e.doAs(admin, "POST", "/account-types/anthropic/apikey/models/fetch", fetch); code != 400 || fmt.Sprint(fieldsOf(out)) != "[proxy_url:invalid]" {
		t.Fatalf("fetch bad url: %d %v", code, out)
	}
}

func TestGuardedSettings(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	admin := e.uid
	alice, dave := e.addUser("alice@x.com"), e.addUser("dave@x.com")
	authz.keys[admin], authz.keys[alice] = adminKeys, supplierKeys
	authz.keys[dave] = append(append([]string{}, supplierKeys...), "account:settings:custom")

	post := func(uid int64, typ string, creds map[string]any) (int, map[string]any) {
		t.Helper()
		pk := "anthropic"
		if typ == "relay_key" {
			pk = "relay"
		}
		return e.doAs(uid, "POST", "/accounts", map[string]any{"name": "g", "plugin_key": pk, "type": typ, "credentials": creds})
	}
	key := "sk-good-key-123"
	// Forbidden for a supplier; the error names the credentials path.
	if code, out := post(alice, "apikey", map[string]any{"api_key": key, "base_url": "https://evil.example"}); code != 400 ||
		fmt.Sprint(fieldsOf(out)) != "[credentials.base_url:forbidden]" {
		t.Fatalf("custom base_url: %d %v", code, out)
	}
	// The official value and its equivalent spellings pass; so do empty and
	// absent values.
	for _, v := range []any{officialBaseURL, officialBaseURL + "/", " HTTPS://API.Anthropic.COM ", "", nil} {
		creds := map[string]any{"api_key": key}
		if v != nil {
			creds["base_url"] = v
		}
		if code, out := post(alice, "apikey", creds); code != 201 {
			t.Fatalf("base_url %v: %d %v", v, code, out)
		}
	}
	// An unguarded type (relay) accepts any address, so an own-level caller
	// needs account:relay for it (SEC-H3, CONTRACTS §21.5); with it anything
	// goes. A caller with account:settings:custom is not restricted.
	if code, out := post(alice, "relay_key", map[string]any{"api_key": key, "base_url": "https://anything.example"}); code != 403 ||
		out["error"].(map[string]any)["details"].(map[string]any)["required_permission"] != "account:relay" {
		t.Fatalf("relay without account:relay: %d %v", code, out)
	}
	authz.keys[alice] = append(append([]string{}, supplierKeys...), "account:relay")
	if code, out := post(alice, "relay_key", map[string]any{"api_key": key, "base_url": "https://anything.example"}); code != 201 {
		t.Fatalf("relay custom: %d %v", code, out)
	}
	authz.keys[alice] = supplierKeys
	if code, out := post(dave, "apikey", map[string]any{"api_key": key, "base_url": "https://custom.example"}); code != 201 {
		t.Fatalf("dave custom: %d %v", code, out)
	}
	// Fetching models with a custom address is refused the same way.
	if code, out := e.doAs(alice, "POST", "/account-types/anthropic/apikey/models/fetch", map[string]any{
		"credentials": map[string]any{"api_key": key, "base_url": "https://evil.example"}}); code != 400 || fmt.Sprint(fieldsOf(out)) != "[credentials.base_url:forbidden]" {
		t.Fatalf("fetch custom: %d %v", code, out)
	}

	// PATCH: the value the account already has stays allowed (an admin set
	// it), any other custom value is not.
	acc := e.createAs(alice, "a", map[string]any{"credentials": map[string]any{"api_key": key, "base_url": officialBaseURL}})
	own := fmt.Sprintf("/accounts/%d", int64(acc["id"].(float64)))
	if code, out := e.doAs(admin, "PATCH", own, map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": "https://custom.example"}}); code != 200 {
		t.Fatalf("admin custom patch: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "PATCH", own, map[string]any{"name": "n", "credentials": map[string]any{"api_key": Mask, "base_url": "https://CUSTOM.example/"}}); code != 200 ||
		data(out)["name"] != "n" {
		t.Fatalf("alice keeps old custom: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "PATCH", own, map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": "https://other.example"}}); code != 400 ||
		fmt.Sprint(fieldsOf(out)) != "[credentials.base_url:forbidden]" {
		t.Fatalf("alice other custom: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "POST", own+"/models/fetch", map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": "https://other.example"}}); code != 400 {
		t.Fatalf("alice fetch other custom: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "PATCH", own, map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": officialBaseURL}}); code != 200 ||
		data(out)["settings"].(map[string]any)["base_url"] != officialBaseURL {
		t.Fatalf("alice back to official: %d %v", code, out)
	}

	// The console's form reads the restriction from the type: guarded_settings
	// on GET /account-types, the same for every caller (whether it applies is
	// the caller's account:settings:custom, checked on save above).
	code, out := e.doAs(alice, "GET", "/account-types", nil)
	raw, _ := json.Marshal(out)
	if code != 200 || gjson.GetBytes(raw, `data.#(type=="apikey").guarded_settings.0.allowed`).Raw != `["`+officialBaseURL+`"]` ||
		gjson.GetBytes(raw, `data.#(type=="apikey").guarded_settings.0.field`).String() != "base_url" ||
		len(gjson.GetBytes(raw, `data.#(type=="relay_key").guarded_settings`).Array()) != 0 {
		t.Fatalf("guarded settings: %d %s", code, raw)
	}
	// Suppliers reach the type endpoints through account:create:own alone.
	authz.keys[alice] = []string{"account:create:own"}
	for _, p := range []string{"/platforms", "/account-types"} {
		if code, _ := e.doAs(alice, "GET", p, nil); code != 200 {
			t.Fatalf("own:create %s: %d", p, code)
		}
	}
	authz.keys[alice] = nil
	if code, out := e.doAs(alice, "GET", "/account-types", nil); code != 403 || permission(out) != "account:read" {
		t.Fatalf("no key: %d %v", code, out)
	}
}

// TestGuardedSettingVersions: with versions on the guarded setting, a
// versioned official address passes for a caller without
// account:settings:custom, an escaped "/" does not make a version, PATCH
// compares with the stored value without its version, and GET
// /account-types shows the versions. Without versions nothing changes.
func TestGuardedSettingVersions(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	admin := e.uid
	alice := e.addUser("alice@x.com")
	authz.keys[admin], authz.keys[alice] = adminKeys, supplierKeys
	key := "sk-good-key-123"
	post := func(base string) (int, map[string]any) {
		t.Helper()
		return e.doAs(alice, "POST", "/accounts", map[string]any{"name": "g", "plugin_key": "anthropic", "type": "apikey",
			"credentials": map[string]any{"api_key": key, "base_url": base}})
	}
	forbidden := func(code int, out map[string]any) bool {
		return code == 400 && fmt.Sprint(fieldsOf(out)) == "[credentials.base_url:forbidden]"
	}

	if code, out := post(officialBaseURL + "/v1"); !forbidden(code, out) {
		t.Fatalf("versioned official without versions: %d %v", code, out)
	}
	e.gen.types[0].Type.GuardedSettings[0].Versions = []string{"/v1"}
	for _, v := range []string{officialBaseURL + "/v1", " HTTPS://API.Anthropic.COM/v1/ ", officialBaseURL} {
		if code, out := post(v); code != 201 {
			t.Fatalf("base_url %q: %d %v", v, code, out)
		}
	}
	for _, v := range []string{officialBaseURL + "/a%2Fv1", officialBaseURL + "/v1/v1", officialBaseURL + "/V1"} {
		if code, out := post(v); !forbidden(code, out) {
			t.Fatalf("base_url %q: %d %v", v, code, out)
		}
	}

	// PATCH: the stored custom value with or without its version is "no
	// change"; the stored value is kept as typed.
	acc := e.createAs(alice, "a", map[string]any{"credentials": map[string]any{"api_key": key, "base_url": officialBaseURL}})
	path := fmt.Sprintf("/accounts/%d", int64(acc["id"].(float64)))
	if code, out := e.doAs(admin, "PATCH", path, map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": "https://custom.example/v1"}}); code != 200 {
		t.Fatalf("admin custom patch: %d %v", code, out)
	}
	patch := func(base string) (int, map[string]any) {
		t.Helper()
		return e.doAs(alice, "PATCH", path, map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": base}})
	}
	if code, out := patch("https://custom.example"); code != 200 || data(out)["settings"].(map[string]any)["base_url"] != "https://custom.example" {
		t.Fatalf("old with version: %d %v", code, out)
	}
	if code, out := patch("https://custom.example/v1"); code != 200 {
		t.Fatalf("new with version: %d %v", code, out)
	}
	if code, out := patch("https://custom.example/a%2Fv1"); !forbidden(code, out) {
		t.Fatalf("escaped slash against old: %d %v", code, out)
	}

	code, out := e.doAs(alice, "GET", "/account-types", nil)
	raw, _ := json.Marshal(out)
	if code != 200 || gjson.GetBytes(raw, `data.#(type=="apikey").guarded_settings.0.versions`).Raw != `["/v1"]` {
		t.Fatalf("account types: %d %s", code, raw)
	}
}

// ---------------------------------------------------------------- unit (no DB)

func TestGuardNorm(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.anthropic.com":        "https://api.anthropic.com",
		" https://api.anthropic.com/ ":     "https://api.anthropic.com",
		"HTTPS://API.Anthropic.COM//":      "https://api.anthropic.com",
		"https://Api.Example.com/V1/Path/": "https://api.example.com/V1/Path",
		"not a url":                        "not a url",
		"relative/path/":                   "relative/path",
		"https://api.anthropic.com:443/":   "https://api.anthropic.com:443",
	} {
		if got := guardNorm(in, nil); got != want {
			t.Errorf("guardNorm(%q) = %q, want %q", in, got, want)
		}
	}
	// With versions: trailing slashes go first, then at most one version
	// segment of the escaped path, the longest, matched whole and
	// case-sensitively, and the "/" left before it.
	versions := []string{"/v1", "/v1beta", "/openai", "/openai/v1"}
	for in, want := range map[string]string{
		"https://API.openai.com/v1/":         "https://api.openai.com",
		"https://api.openai.com/v1":          "https://api.openai.com",
		"https://x/v1beta":                   "https://x",
		"https://x/openai/v1":                "https://x",
		"https://x/openai":                   "https://x",
		"https://x/v1/v1":                    "https://x/v1",
		"https://x/p/v1":                     "https://x/p",
		"https://x/a%2Fv1":                   "https://x/a%2Fv1",
		"https://x/a%2Fb/v1":                 "https://x/a%2Fb",
		"https://x/V1":                       "https://x/V1",
		"https://x/xv1":                      "https://x/xv1",
		"https://x/v1x":                      "https://x/v1x",
		"https://x":                          "https://x",
		"not a url/v1":                       "not a url/v1",
		"https://api.anthropic.com:443/v1//": "https://api.anthropic.com:443",
		"https://x//v1":                      "https://x",
	} {
		if got := guardNorm(in, versions); got != want {
			t.Errorf("guardNorm(%q, versions) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckGuarded(t *testing.T) {
	ctx := context.Background()
	guards := []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.anthropic.com", "https://eu.anthropic.com"}}}
	st := func(v string) fields { return fields{"base_url": json.RawMessage(v)} }
	codes := func(err error) string {
		if err == nil {
			return ""
		}
		fs, _ := core.AsError(err).Details["fields"].([]core.FieldError)
		var s []string
		for _, f := range fs {
			s = append(s, f.Field+":"+f.Code)
		}
		return strings.Join(s, ",")
	}
	cases := []struct {
		name string
		st   fields
		old  []byte
		want string
	}{
		{"absent", fields{}, nil, ""},
		{"null", st(`null`), nil, ""},
		{"empty", st(`""`), nil, ""},
		{"blank", st(`"  "`), nil, ""},
		{"official", st(`"https://api.anthropic.com"`), nil, ""},
		{"second allowed", st(`"https://eu.anthropic.com/"`), nil, ""},
		{"case and slash", st(`" HTTPS://API.ANTHROPIC.COM/ "`), nil, ""},
		{"custom", st(`"https://evil.example"`), nil, "credentials.base_url:forbidden"},
		{"not a string", st(`123`), nil, "credentials.base_url:forbidden"},
		{"old value", st(`"https://custom.example/"`), []byte(`{"api_key":"k","base_url":"https://CUSTOM.example"}`), ""},
		{"other than old", st(`"https://other.example"`), []byte(`{"base_url":"https://custom.example"}`), "credentials.base_url:forbidden"},
		{"old not a string", st(`"https://custom.example"`), []byte(`{"base_url":5}`), "credentials.base_url:forbidden"},
	}
	for _, c := range cases {
		if got := codes(checkGuarded(ctx, guards, c.st, c.old)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	if err := checkGuarded(ctx, nil, st(`"https://evil.example"`), nil); err != nil {
		t.Errorf("no guards: %v", err)
	}

	// Versions: the submitted value, the allowed ones and the old value are
	// all compared without one version segment; without versions a
	// versioned official address is a custom value.
	versioned := []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.openai.com"}, Versions: []string{"/v1"}}}
	allowedV1 := []manifest.GuardedSetting{{Field: "base_url", Allowed: []string{"https://api.openai.com/v1"}, Versions: []string{"/v1"}}}
	for _, c := range []struct {
		name   string
		guards []manifest.GuardedSetting
		st     fields
		old    []byte
		want   string
	}{
		{"versioned official", versioned, st(`"https://API.openai.com/v1/"`), nil, ""},
		{"allowed carries the version", allowedV1, st(`"https://api.openai.com"`), nil, ""},
		{"escaped slash is no version", versioned, st(`"https://api.openai.com/a%2Fv1"`), nil, "credentials.base_url:forbidden"},
		{"one version only", versioned, st(`"https://api.openai.com/v1/v1"`), nil, "credentials.base_url:forbidden"},
		{"case-sensitive", versioned, st(`"https://api.openai.com/V1"`), nil, "credentials.base_url:forbidden"},
		{"old with version", versioned, st(`"https://custom.example"`), []byte(`{"base_url":"https://custom.example/v1"}`), ""},
		{"new with version", versioned, st(`"https://custom.example/v1"`), []byte(`{"base_url":"https://custom.example"}`), ""},
		{"old with other path", versioned, st(`"https://custom.example/v1"`), []byte(`{"base_url":"https://custom.example/p"}`), "credentials.base_url:forbidden"},
		{"no versions", guards, st(`"https://api.anthropic.com/v1"`), nil, "credentials.base_url:forbidden"},
	} {
		if got := codes(checkGuarded(ctx, c.guards, c.st, c.old)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestInputProxyURL(t *testing.T) {
	ctx := context.Background()
	var in input
	_ = json.Unmarshal([]byte(`{"name":"n","plugin_key":"p","type":"t","credentials":{},"proxy_url":"http://h:1","proxy_id":3}`), &in)
	if fe := in.validate(ctx, true); len(fe) != 1 || fe[0].Field != "proxy_url" || fe[0].Code != "conflict" {
		t.Fatalf("conflict: %v", fe)
	}
	in = input{}
	_ = json.Unmarshal([]byte(`{"proxy_url":"http://h:1","proxy_id":null}`), &in)
	if fe := in.validate(ctx, false); len(fe) != 1 || fe[0].Code != "conflict" {
		t.Fatalf("conflict with null proxy_id: %v", fe)
	}
	in = input{}
	_ = json.Unmarshal([]byte(`{"proxy_url":"  ","proxy_id":3}`), &in)
	if fe := in.validate(ctx, false); len(fe) != 0 || in.ProxyURL != nil {
		t.Fatalf("blank proxy_url: %v %v", fe, in.ProxyURL)
	}
	in = input{}
	_ = json.Unmarshal([]byte(`{"proxy_url":" http://h:1 "}`), &in)
	if fe := in.validate(ctx, false); len(fe) != 0 || in.ProxyURL == nil || *in.ProxyURL != "http://h:1" {
		t.Fatalf("trim proxy_url: %v %v", fe, in.ProxyURL)
	}
	in = input{}
	_ = json.Unmarshal([]byte(`{"name":"n","proxy_id":null,"credentials":null,"models":[],"rpm_limit":1}`), &in)
	if got := fmt.Sprint(in.changedFields()); got != "[name proxy_id models rpm_limit]" {
		t.Fatalf("changedFields: %s", got)
	}
	in = input{}
	_ = json.Unmarshal([]byte(`{"credentials":{"api_key":"k"},"proxy_url":"http://h:1"}`), &in)
	if got := fmt.Sprint(in.changedFields()); got != "[proxy_url credentials]" {
		t.Fatalf("changedFields creds: %s", got)
	}
}

// TestMaskedSecretToNewAddress covers M2: a Mask value is restored from the
// stored secret only when the caller may reveal it or the upstream address
// stays; otherwise the secret must be typed again and the plugin never sees
// the request.
func TestMaskedSecretToNewAddress(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	admin := e.uid
	op, ownViewer := e.addUser("op@x.com"), e.addUser("ownview@x.com")
	authz.keys[admin] = adminKeys
	authz.keys[op] = []string{"account:read", "account:update", "account:test"}
	authz.keys[ownViewer] = []string{"account:read", "account:create", "account:update", "account:test", "account:credential:view:own"}

	key := "sk-good-key-123"
	acc := e.createAs(admin, "relay", map[string]any{"plugin_key": "relay", "type": "relay_key",
		"credentials": map[string]any{"api_key": key, "base_url": "https://a.example"}})
	path := fmt.Sprintf("/accounts/%d", int64(acc["id"].(float64)))
	moved := map[string]any{"credentials": map[string]any{"api_key": Mask, "base_url": "https://evil.example"}}
	calls := func() int {
		e.plat.mu.Lock()
		defer e.plat.mu.Unlock()
		return len(e.plat.calls)
	}

	// Without credential:view over the account (the own key does not reach an
	// account created by someone else) both endpoints refuse before the plugin.
	for _, uid := range []int64{op, ownViewer} {
		before := calls()
		for _, req := range []struct{ method, path string }{{"PATCH", path}, {"POST", path + "/models/fetch"}} {
			if code, out := e.doAs(uid, req.method, req.path, moved); code != 400 ||
				fmt.Sprint(fieldsOf(out)) != "[credentials.api_key:reenter_required]" {
				t.Fatalf("%d %s %s: %d %v", uid, req.method, req.path, code, out)
			}
		}
		if n := calls(); n != before {
			t.Fatalf("plugin validated %d times for %d", n-before, uid)
		}
	}
	// Mask with an unchanged address (after normalization) passes.
	if code, out := e.doAs(op, "PATCH", path, map[string]any{"name": "kept",
		"credentials": map[string]any{"api_key": Mask, "base_url": "https://A.example/"}}); code != 200 {
		t.Fatalf("same address: %d %v", code, out)
	}
	// A new address with the secret typed again passes.
	if code, out := e.doAs(op, "PATCH", path, map[string]any{
		"credentials": map[string]any{"api_key": key, "base_url": "https://b.example"}}); code != 200 ||
		data(out)["settings"].(map[string]any)["base_url"] != "https://b.example" {
		t.Fatalf("reentered: %d %v", code, out)
	}
	// account:credential:view may move a masked secret.
	if code, out := e.doAs(admin, "PATCH", path, moved); code != 200 ||
		data(out)["settings"].(map[string]any)["base_url"] != "https://evil.example" {
		t.Fatalf("admin: %d %v", code, out)
	}

	// The own key reveals the caller's own accounts.
	ownAcc := e.createAs(ownViewer, "mine", map[string]any{"plugin_key": "relay", "type": "relay_key",
		"credentials": map[string]any{"api_key": key, "base_url": "https://a.example"}})
	if code, out := e.doAs(ownViewer, "PATCH", fmt.Sprintf("/accounts/%d", int64(ownAcc["id"].(float64))), moved); code != 200 {
		t.Fatalf("own viewer on own account: %d %v", code, out)
	}
}

func TestAddressChanged(t *testing.T) {
	declared := manifest.AccountType{UpstreamFields: []string{"endpoint"}}
	legacy := manifest.AccountType{GuardedSettings: []manifest.GuardedSetting{{Field: "region"}}}
	versioned := manifest.AccountType{GuardedSettings: []manifest.GuardedSetting{{Field: "base_url", Versions: []string{"/v1"}}}}
	cases := []struct {
		at      manifest.AccountType
		b, old  string
		changed bool
	}{
		{declared, `{"endpoint":"https://a","base_url":"x"}`, `{"endpoint":"https://a/","base_url":"y"}`, false},
		{declared, `{"endpoint":"https://b"}`, `{"endpoint":"https://a"}`, true},
		{legacy, `{"base_url":"https://a","region":"eu"}`, `{"base_url":"https://a","region":"us"}`, true},
		{legacy, `{"base_url":null}`, `{}`, false},
		{legacy, `{"base_url":"https://a"}`, ``, true},
		// Versions are not applied: only a version segment apart is a change.
		{versioned, `{"base_url":"https://x/v1"}`, `{"base_url":"https://x"}`, true},
		{manifest.AccountType{UpstreamFields: []string{}}, `{"base_url":"https://b"}`, `{"base_url":"https://a"}`, false},
		{manifest.AccountType{UpstreamFields: []string{"upstream"}}, `{"upstream":{ "url": "u" }}`, `{"upstream":{"url":"u"}}`, false},
		{manifest.AccountType{UpstreamFields: []string{"upstream"}}, `{"upstream":{"url":"v"}}`, `{"upstream":{"url":"u"}}`, true},
	}
	for i, c := range cases {
		var old []byte
		if c.old != "" {
			old = []byte(c.old)
		}
		if got := addressChanged([]byte(c.b), old, c.at); got != c.changed {
			t.Errorf("case %d: addressChanged = %v, want %v", i, got, c.changed)
		}
	}
}

// TestOwnChangeGroupsAndCaps: an own-level write is judged on what it
// changes (G1, M10, W2-G4), so a supplier can save a form an administrator
// adjusted as is.
func TestOwnChangeGroupsAndCaps(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	admin, alice := e.uid, e.addUser("alice@x.com")
	authz.keys[admin], authz.keys[alice] = adminKeys, supplierKeys
	g1 := e.exec1(`INSERT INTO groups (name) VALUES ('g1') RETURNING id`)
	g2 := e.exec1(`INSERT INTO groups (name) VALUES ('g2') RETURNING id`)
	private := e.exec1(`INSERT INTO groups (name, visibility) VALUES ('private', 'restricted') RETURNING id`)
	path := fmt.Sprintf("/accounts/%d", int64(e.createAs(alice, "a", nil)["id"].(float64)))
	if code, out := e.doAs(admin, "PATCH", path, map[string]any{"group_ids": []int64{g1, private}, "priority": 200}); code != 200 {
		t.Fatalf("admin patch: %d %v", code, out)
	}
	// save sends the whole edit form as alice reads it, with change applied.
	save := func(change map[string]any) (int, map[string]any) {
		t.Helper()
		_, out := e.doAs(alice, "GET", path, nil)
		v := data(out)
		body := map[string]any{"name": v["name"], "group_ids": v["group_ids"], "priority": v["priority"], "weight": v["weight"],
			"max_concurrency": v["max_concurrency"], "status": v["status"], "credentials": map[string]any{"api_key": Mask}}
		maps.Copy(body, change)
		return e.doAs(alice, "PATCH", path, body)
	}
	expect := func(name string, change map[string]any, code int, fields string) {
		t.Helper()
		got, out := save(change)
		if got != code || (fields != "" && fmt.Sprint(fieldsOf(out)) != fields) {
			t.Fatalf("%s: %d %v", name, got, out)
		}
	}
	expect("unchanged form", nil, 200, "")
	expect("add without bind", map[string]any{"group_ids": []int64{g1, private, g2}}, 403, "")
	expect("unbind all without bind", map[string]any{"group_ids": []int64{}}, 403, "")
	expect("priority above the cap", map[string]any{"priority": 150}, 400, "[priority:restricted]")

	authz.keys[alice] = append(slices.Clone(supplierKeys), "account:group:bind")
	expect("add usable", map[string]any{"group_ids": []int64{g1, private, g2}}, 200, "")
	expect("remove", map[string]any{"group_ids": []int64{g2}}, 200, "")
	expect("add private", map[string]any{"group_ids": []int64{g2, private}}, 400, "[group_ids:not_bindable]")
	expect("missing group", map[string]any{"group_ids": []int64{g2, private + 1000}}, 400, "[group_ids:not_found]")

	authz.keys[alice] = append(slices.Clone(supplierKeys), "group:manage")
	expect("group:manage", map[string]any{"group_ids": []int64{g2, private}}, 200, "")
	if _, out := e.doAs(alice, "GET", path, nil); fmt.Sprint(data(out)["group_ids"]) != fmt.Sprint([]any{float64(g2), float64(private)}) ||
		data(out)["priority"] != float64(200) {
		t.Fatalf("saved: %v", data(out))
	}
}

// TestOwnCustomUpstream: account:relay is needed by an own-level caller only
// to create an account whose upstream address can be set freely, or to move
// its address (G2); the same rule covers fetching models.
func TestOwnCustomUpstream(t *testing.T) {
	authz := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, authz)
	alice := e.addUser("alice@x.com")
	authz.keys[e.uid], authz.keys[alice] = adminKeys, supplierKeys
	oauth := core.PluginInfo{Key: "oauth", Manifest: &manifest.Manifest{Name: manifest.LocalizedText{"en": "OAuth"}}}
	e.gen.plugins = append(e.gen.plugins, oauth)
	for _, id := range []string{"codex_oauth", "managed"} {
		e.gen.types = append(e.gen.types, core.AccountTypeBinding{Plugin: oauth, Client: e.plat,
			Type: manifest.AccountType{ID: id, Form: manifest.Form{Mode: "native", Component: "KeyForm"}, UpstreamFields: []string{}}})
	}
	post := func(pk, typ string, creds map[string]any) (int, map[string]any) {
		t.Helper()
		return e.doAs(alice, "POST", "/accounts", map[string]any{"name": typ, "plugin_key": pk, "type": typ, "credentials": creds})
	}
	for _, typ := range []string{"codex_oauth", "managed"} {
		if code, out := post("oauth", typ, map[string]any{}); code != 201 {
			t.Fatalf("create %s: %d %v", typ, code, out)
		}
	}
	relayCreds := map[string]any{"api_key": "sk-good-key-123", "base_url": "https://relay.example"}
	if code, out := post("relay", "relay_key", relayCreds); code != 403 {
		t.Fatalf("create custom: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "POST", "/account-types/relay/relay_key/models/fetch", map[string]any{"credentials": relayCreds}); code != 403 {
		t.Fatalf("fetch custom: %d %v", code, out)
	}

	authz.keys[alice] = append(slices.Clone(supplierKeys), "account:relay")
	code, out := post("relay", "relay_key", relayCreds)
	if code != 201 {
		t.Fatalf("create custom with relay: %d %v", code, out)
	}
	authz.keys[alice] = supplierKeys
	path := fmt.Sprintf("/accounts/%d", int64(data(out)["id"].(float64)))
	for _, change := range []map[string]any{{"name": "renamed"}, {"status": "disabled"}, {"status": "active"},
		{"credentials": map[string]any{"api_key": Mask, "base_url": "https://relay.example"}}} {
		if code, out := e.doAs(alice, "PATCH", path, change); code != 200 {
			t.Fatalf("patch %v: %d %v", change, code, out)
		}
	}
	for _, key := range []string{Mask, "sk-good-key-123"} {
		if code, out := e.doAs(alice, "PATCH", path, map[string]any{"credentials": map[string]any{"api_key": key, "base_url": "https://other.example"}}); code != 403 {
			t.Fatalf("move base_url (key %s): %d %v", key, code, out)
		}
	}
	moved := map[string]any{"credentials": map[string]any{"api_key": "sk-good-key-123", "base_url": "https://other.example"}}
	if code, out := e.doAs(alice, "POST", path+"/models/fetch", moved); code != 403 {
		t.Fatalf("fetch models at a moved base_url: %d %v", code, out)
	}
}

// TestBuiltinSupplierRole runs a supplier as the built-in roles make one
// (user + supplier, decision 7) through the account routes: an OAuth-type
// account is created, a custom-upstream one needs account:relay, groups
// cannot be bound (no account:group:bind for now), and other users' accounts
// do not exist for it. The key set the other tests call supplierKeys is the
// role's default on accounts and proxies (it also reads its own usage).
func TestBuiltinSupplierRole(t *testing.T) {
	fake := fakeAuthz{keys: map[int64][]string{}}
	e := setupWith(t, fake)
	ctx := context.Background()
	roles := authz.New(authz.Deps{DB: e.db})
	if err := roles.Start(ctx); err != nil {
		t.Fatal(err)
	}
	keysOf := func(roleKeys ...string) []string {
		t.Helper()
		set, err := roles.RolesPermissions(ctx, e.db.Gorm, roleKeys)
		if err != nil {
			t.Fatal(err)
		}
		return set.KeyList()
	}
	if got := keysOf(authz.RoleSupplier); !slices.Equal(got, slices.Sorted(slices.Values(append(slices.Clone(supplierKeys), "usage:self:read", "usage:account:read:own")))) {
		t.Fatalf("supplier role grants %v, want %v", got, supplierKeys)
	}
	alice := e.addUser("alice@x.com")
	fake.keys[e.uid], fake.keys[alice] = adminKeys, keysOf(authz.RoleUser, authz.RoleSupplier)

	oauth := core.PluginInfo{Key: "oauth", Manifest: &manifest.Manifest{Name: manifest.LocalizedText{"en": "OAuth"}}}
	e.gen.plugins = append(e.gen.plugins, oauth)
	e.gen.types = append(e.gen.types, core.AccountTypeBinding{Plugin: oauth, Client: e.plat,
		Type: manifest.AccountType{ID: "codex_oauth", Form: manifest.Form{Mode: "native", Component: "KeyForm"}, UpstreamFields: []string{}}})
	code, out := e.doAs(alice, "POST", "/accounts", map[string]any{"name": "mine", "plugin_key": "oauth", "type": "codex_oauth", "credentials": map[string]any{}})
	if code != 201 {
		t.Fatalf("create codex_oauth: %d %v", code, out)
	}
	mine := fmt.Sprintf("/accounts/%d", int64(data(out)["id"].(float64)))
	relay := map[string]any{"name": "relay", "plugin_key": "relay", "type": "relay_key",
		"credentials": map[string]any{"api_key": "sk-good-key-123", "base_url": "https://relay.example"}}
	if code, out := e.doAs(alice, "POST", "/accounts", relay); code != 403 {
		t.Fatalf("create custom upstream: %d %v", code, out)
	}
	group := e.exec1(`INSERT INTO groups (name) VALUES ('g1') RETURNING id`)
	if code, out := e.doAs(alice, "PATCH", mine, map[string]any{"group_ids": []int64{group}}); code != 403 {
		t.Fatalf("bind a group: %d %v", code, out)
	}

	theirs := int64(e.createAs(e.uid, "theirs", nil)["id"].(float64))
	if code, out := e.doAs(alice, "GET", fmt.Sprintf("/accounts/%d", theirs), nil); code != 404 {
		t.Fatalf("read another user's account: %d %v", code, out)
	}
	if code, out := e.doAs(alice, "GET", "/accounts", nil); code != 200 || total(out) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
}
