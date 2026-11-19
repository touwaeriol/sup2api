package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// AC 23: ownership of accounts and proxies (CONTRACTS §21). A "vendor" role
// (account:*:own, proxy:*:own) only sees and changes what it created, cannot
// point base_url anywhere but the official address, and gets a proxy created
// or reused from a pasted proxy_url; a "viewer" role sees everything but
// changes nothing and never sees secrets; admins filter by creator.
func TestAC23_OwnershipAndProxyURL(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "anthropic", "")

	cl := &OwnershipCleanup{}
	defer cl.Run(e, admin)

	// 1. Roles and users.
	vendorKey, viewerKey := e.RoleKey("vendor"), e.RoleKey("viewer")
	vendorRole, a := e.NewUserWithRoles(admin, vendorKey, VendorPerms)
	b := e.CreateUser(admin, UserSpec{Roles: []string{vendorKey}})
	viewerRole, v := e.NewUserWithRoles(admin, viewerKey, ViewerPerms)
	cl.Roles = append(cl.Roles, vendorRole, viewerRole)
	cl.Users = append(cl.Users, a.UserID, b.UserID, v.UserID)
	for _, u := range []*Session{a, b, v} {
		if u.UserID == 0 {
			t.Fatalf("user %s has no id", u.Email)
		}
	}

	const (
		proxyURL      = "socks5://u:p@10.0.0.1:1080"
		proxyURLAlt   = "SOCKS5H://u:p@10.0.0.1:1080/" // equivalent: scheme case, socks5h, trailing "/"
		proxyURLOther = "socks5://u:x@10.0.0.1:1080"   // different password -> another proxy
	)

	// 2. A creates accounts with proxy_url: first creates the proxy, the
	// same string (and equivalent spellings) reuse it, a different password
	// makes a new one.
	acc1 := e.CreateOwnedAccount(a, e.OwnedAccountBody("", map[string]any{"proxy_url": proxyURL}))
	acc1ID, proxyID := acc1.Get("id").Int(), acc1.Get("proxy_id").Int()
	cl.Accounts = append(cl.Accounts, acc1ID)
	if !acc1.Get("proxy_created").Bool() || proxyID == 0 {
		t.Fatalf("first proxy_url must create a proxy: %s", acc1.Raw)
	}
	cl.Proxies = append(cl.Proxies, proxyID)
	if acc1.Get("created_by").Int() != a.UserID || acc1.Get("created_by_email").String() != a.Email {
		t.Fatalf("created_by of A's account: %s", acc1.Raw)
	}
	if got := acc1.Get("credentials.base_url").String(); got != "" && got != OfficialAnthropicBaseURL {
		t.Fatalf("omitted base_url must normalise to the official address: %s", acc1.Raw)
	}

	acc2 := e.CreateOwnedAccount(a, e.OwnedAccountBody("", map[string]any{"proxy_url": proxyURL}))
	acc2ID := acc2.Get("id").Int()
	cl.Accounts = append(cl.Accounts, acc2ID)
	if acc2.Get("proxy_created").Bool() || acc2.Get("proxy_id").Int() != proxyID {
		t.Fatalf("same proxy_url must reuse proxy %d: %s", proxyID, acc2.Raw)
	}

	acc3 := e.CreateOwnedAccount(a, e.OwnedAccountBody("", map[string]any{"proxy_url": proxyURLAlt}))
	acc3ID := acc3.Get("id").Int()
	cl.Accounts = append(cl.Accounts, acc3ID)
	if acc3.Get("proxy_created").Bool() || acc3.Get("proxy_id").Int() != proxyID {
		t.Fatalf("equivalent proxy_url (%s) must reuse proxy %d: %s", proxyURLAlt, proxyID, acc3.Raw)
	}

	acc4 := e.CreateOwnedAccount(a, e.OwnedAccountBody("", map[string]any{"proxy_url": proxyURLOther}))
	acc4ID, proxyID2 := acc4.Get("id").Int(), acc4.Get("proxy_id").Int()
	cl.Accounts = append(cl.Accounts, acc4ID)
	if !acc4.Get("proxy_created").Bool() || proxyID2 == 0 || proxyID2 == proxyID {
		t.Fatalf("different password must create another proxy: %s", acc4.Raw)
	}
	cl.Proxies = append(cl.Proxies, proxyID2)

	// The proxy carries the parsed parts and A as its creator; the password
	// is never returned.
	px := a.OK(t, http.MethodGet, fmt.Sprintf("/proxies/%d", proxyID), nil)
	if px.Get("protocol").String() != "socks5" || px.Get("host").String() != "10.0.0.1" || px.Get("port").Int() != 1080 ||
		px.Get("username").String() != "u" || !px.Get("has_password").Bool() || px.Get("status").String() != "active" {
		t.Fatalf("auto-created proxy: %s", px.Raw)
	}
	if px.Get("created_by").Int() != a.UserID || px.Get("created_by_email").String() != a.Email || px.Get("password").Exists() {
		t.Fatalf("auto-created proxy ownership: %s", px.Raw)
	}
	if px.Get("account_count").Int() != 3 {
		t.Errorf("proxy %d account_count = %d, want 3: %s", proxyID, px.Get("account_count").Int(), px.Raw)
	}

	// proxy_id together with proxy_url -> conflict.
	r := a.API(t, http.MethodPost, "/accounts", e.OwnedAccountBody("", map[string]any{"proxy_url": proxyURL, "proxy_id": proxyID}))
	ExpectFieldError(t, r, "proxy_url", "conflict")

	// Invalid proxy_url -> invalid; the message never echoes the string.
	for _, bad := range []string{
		"ftp://" + e.RunID + "x",                         // scheme
		"socks5://u:p@10.0.0.1",                          // no port
		"socks5://u:p@10.0.0.1:1080/path" + e.RunID,      // path
		"socks5://u:p@10.0.0.1:1080?q=" + e.RunID,        // query
		"http://u:secret-" + e.RunID + "@10.0.0.1:99999", // port out of range
		"not a url " + e.RunID,
	} {
		r := a.API(t, http.MethodPost, "/accounts", e.OwnedAccountBody("", map[string]any{"proxy_url": bad}))
		ExpectFieldError(t, r, "proxy_url", "invalid")
		if strings.Contains(string(r.Body), e.RunID) || strings.Contains(string(r.Body), "secret-") {
			t.Fatalf("proxy_url error echoes the input (may contain a password): %s", r)
		}
	}

	// B's own account with the same proxy string: B cannot see A's proxy, so
	// a new one is created for B (visible scope = own).
	accB := e.CreateOwnedAccount(b, e.OwnedAccountBody("", map[string]any{"proxy_url": proxyURL}))
	accBID, proxyBID := accB.Get("id").Int(), accB.Get("proxy_id").Int()
	cl.Accounts = append(cl.Accounts, accBID)
	if !accB.Get("proxy_created").Bool() || proxyBID == 0 || proxyBID == proxyID {
		t.Fatalf("B must not reuse A's proxy: %s", accB.Raw)
	}
	cl.Proxies = append(cl.Proxies, proxyBID)
	if accB.Get("created_by").Int() != b.UserID {
		t.Fatalf("created_by of B's account: %s", accB.Raw)
	}

	// 3. Own scope: A sees only its own accounts and proxies; B's are 404.
	aAccounts := a.ListAll(t, "/accounts")
	if ok, row := AllCreatedBy(aAccounts, a.UserID); !ok {
		t.Fatalf("A lists an account it did not create: %s", row)
	}
	ids := IDSet(aAccounts)
	for _, id := range []int64{acc1ID, acc2ID, acc3ID, acc4ID} {
		if !ids[id] {
			t.Fatalf("A's account %d missing from A's list", id)
		}
	}
	if ids[accBID] {
		t.Fatalf("A's list contains B's account %d", accBID)
	}
	aProxies := a.ListAll(t, "/proxies")
	if ok, row := AllCreatedBy(aProxies, a.UserID); !ok {
		t.Fatalf("A lists a proxy it did not create: %s", row)
	}
	pids := IDSet(aProxies)
	if !pids[proxyID] || !pids[proxyID2] || pids[proxyBID] {
		t.Fatalf("A's proxy list: %v (want %d,%d; not %d)", pids, proxyID, proxyID2, proxyBID)
	}

	bAcc := fmt.Sprintf("/accounts/%d", accBID)
	ExpectNotFound(t, a.API(t, http.MethodGet, bAcc, nil))
	ExpectNotFound(t, a.API(t, http.MethodPatch, bAcc, map[string]any{"name": e.Name("hijack")}))
	ExpectNotFound(t, a.API(t, http.MethodPost, bAcc+"/test", map[string]any{}))
	ExpectNotFound(t, a.API(t, http.MethodPost, bAcc+"/credentials/reveal", nil))
	ExpectNotFound(t, a.API(t, http.MethodDelete, bAcc, nil))
	if r := admin.API(t, http.MethodGet, bAcc, nil); r.Status != 200 {
		t.Fatalf("B's account must survive A's attempts: %s", r)
	}

	bPx := fmt.Sprintf("/proxies/%d", proxyBID)
	ExpectNotFound(t, a.API(t, http.MethodGet, bPx, nil))
	ExpectNotFound(t, a.API(t, http.MethodPatch, bPx, map[string]any{"name": e.Name("hijack")}))
	ExpectNotFound(t, a.API(t, http.MethodDelete, bPx, nil))
	ExpectNotFound(t, a.API(t, http.MethodPost, bPx+"/test", nil))
	// Linking B's proxy by id is "not found" for A as well.
	ExpectFieldError(t, a.API(t, http.MethodPost, "/accounts", e.OwnedAccountBody("", map[string]any{"proxy_id": proxyBID})), "proxy_id", "not_found")
	// Linking its own proxy by id works and does not create anything.
	acc5 := e.CreateOwnedAccount(a, e.OwnedAccountBody("", map[string]any{"proxy_id": proxyID2}))
	cl.Accounts = append(cl.Accounts, acc5.Get("id").Int())
	if acc5.Get("proxy_id").Int() != proxyID2 || acc5.Get("proxy_created").Bool() {
		t.Fatalf("proxy_id link: %s", acc5.Raw)
	}

	// 4. Guarded base_url: a vendor may only use the official address (or
	// keep whatever the admin set); the form tells the UI the same.
	acc1Path := fmt.Sprintf("/accounts/%d", acc1ID)
	r = a.API(t, http.MethodPatch, acc1Path, map[string]any{
		"credentials": map[string]any{"api_key": "******", "base_url": "https://evil.example.com"},
	})
	ExpectFieldError(t, r, "credentials.base_url", "forbidden")
	d := a.OK(t, http.MethodPatch, acc1Path, map[string]any{
		"credentials": map[string]any{"api_key": "******", "base_url": OfficialAnthropicBaseURL + "/"},
	})
	if d.Get("credentials.base_url").String() != OfficialAnthropicBaseURL {
		t.Fatalf("official base_url with trailing slash: %s", d.Raw)
	}
	// Admin (account:settings:custom) sets a custom relay.
	const relay = "https://relay.example.com"
	d = admin.OK(t, http.MethodPatch, acc1Path, map[string]any{
		"credentials": map[string]any{"api_key": "******", "base_url": relay},
	})
	if d.Get("credentials.base_url").String() != relay {
		t.Fatalf("admin custom base_url: %s", d.Raw)
	}
	// The vendor renames and sends the credentials back unchanged: allowed.
	got := a.OK(t, http.MethodGet, acc1Path, nil)
	if got.Get("credentials.api_key").String() != "******" || got.Get("credentials.base_url").String() != relay {
		t.Fatalf("A's view of its account: %s", got.Raw)
	}
	d = a.OK(t, http.MethodPatch, acc1Path, map[string]any{
		"name":        e.Name("renamed"),
		"credentials": map[string]any{"api_key": "******", "base_url": relay},
	})
	if d.Get("credentials.base_url").String() != relay {
		t.Fatalf("unchanged custom base_url must pass: %s", d.Raw)
	}
	// ... but still not to another custom address.
	ExpectFieldError(t, a.API(t, http.MethodPatch, acc1Path, map[string]any{
		"credentials": map[string]any{"api_key": "******", "base_url": "https://relay2.example.com"},
	}), "credentials.base_url", "forbidden")
	// Creating with a custom base_url is refused for the vendor too.
	ExpectFieldError(t, a.API(t, http.MethodPost, "/accounts", e.OwnedAccountBody("", map[string]any{
		"credentials": map[string]any{"api_key": "sk-ant-mock-" + e.Name("guard"), "base_url": e.MockInternalURL},
	})), "credentials.base_url", "forbidden")

	// The native form reads the restriction from the account type
	// (guarded_settings on GET /account-types, the same for every caller);
	// whether it applies is the caller's account:settings:custom, which the
	// saves above check.
	for _, c := range []*Session{a, admin} {
		at, ok := FindAccountType(c.OK(t, http.MethodGet, "/account-types", nil).Array(), AnthropicPlugin, AnthropicAPIKey)
		allowed := at.Get(`guarded_settings.#(field=="base_url").allowed`).Array()
		if !ok || len(allowed) != 1 || allowed[0].String() != OfficialAnthropicBaseURL {
			t.Fatalf("guarded base_url of %s: %s", c.Email, at.Get("guarded_settings").Raw)
		}
	}

	// 5. Viewer: sees everything, changes nothing, no secrets.
	vAccounts := v.ListAll(t, "/accounts")
	vids := IDSet(vAccounts)
	if !vids[acc1ID] || !vids[accBID] {
		t.Fatalf("viewer must list A's and B's accounts: %v", vids)
	}
	if row, _ := Find(vAccounts, "id", accBID); row.Get("created_by").Int() != b.UserID || row.Get("created_by_email").String() != b.Email {
		t.Fatalf("viewer row lacks creator: %s", row.Raw)
	}
	ExpectPermissionDenied(t, v.API(t, http.MethodPatch, acc1Path, map[string]any{"name": e.Name("viewer")}))
	ExpectPermissionDenied(t, v.API(t, http.MethodPost, acc1Path+"/credentials/reveal", nil))
	ExpectPermissionDenied(t, v.API(t, http.MethodDelete, acc1Path, nil))
	ExpectPermissionDenied(t, v.API(t, http.MethodPost, "/accounts", e.OwnedAccountBody("", nil)))
	ExpectPermissionDenied(t, v.API(t, http.MethodPatch, bPx, map[string]any{"name": e.Name("viewer")}))
	vd := v.OK(t, http.MethodGet, acc1Path, nil)
	if vd.Get("credentials.api_key").String() != "******" {
		t.Fatalf("viewer sees the api_key: %s", vd.Raw)
	}
	vpx := v.OK(t, http.MethodGet, bPx, nil)
	if vpx.Get("created_by").Int() != b.UserID || vpx.Get("password").Exists() {
		t.Fatalf("viewer's view of B's proxy: %s", vpx.Raw)
	}
	// The vendor may reveal its own secret (own:credential:view).
	rev := a.OK(t, http.MethodPost, acc1Path+"/credentials/reveal", nil)
	if k := rev.Get("credentials.api_key").String() + rev.Get("api_key").String(); !strings.HasPrefix(k, "sk-ant-mock-") {
		t.Fatalf("own reveal: %s", rev.Raw)
	}

	// 6. Admin filters and delete semantics.
	mine := admin.ListAll(t, "/accounts", "mine", "true")
	if ok, row := AllCreatedBy(mine, admin.UserID); !ok {
		t.Fatalf("admin mine=true lists a foreign account: %s", row)
	}
	if m := IDSet(mine); m[acc1ID] || m[accBID] {
		t.Fatalf("admin mine=true contains vendor accounts")
	}
	byA := admin.ListAll(t, "/accounts", "created_by", fmt.Sprint(a.UserID))
	if ok, row := AllCreatedBy(byA, a.UserID); !ok {
		t.Fatalf("created_by=A lists a foreign account: %s", row)
	}
	if m := IDSet(byA); !m[acc1ID] || !m[acc2ID] || m[accBID] {
		t.Fatalf("created_by=A: %v", m)
	}
	if row, _ := Find(byA, "id", acc1ID); row.Get("created_by_email").String() != a.Email {
		t.Fatalf("created_by_email in admin list: %s", row.Raw)
	}
	pByA := admin.ListAll(t, "/proxies", "created_by", fmt.Sprint(a.UserID))
	if ok, row := AllCreatedBy(pByA, a.UserID); !ok {
		t.Fatalf("proxies created_by=A lists a foreign proxy: %s", row)
	}
	if m := IDSet(pByA); !m[proxyID] || !m[proxyID2] || m[proxyBID] {
		t.Fatalf("proxies created_by=A: %v", m)
	}
	// Under own scope created_by narrows within the scope (CONTRACTS §21.0):
	// B's accounts stay out of reach and none of A's match; mine=true is a
	// no-op for the vendor.
	if m := IDSet(a.ListAll(t, "/accounts", "created_by", fmt.Sprint(b.UserID))); len(m) != 0 {
		t.Fatalf("created_by=B under A's own scope: %v", m)
	}
	if m := IDSet(a.ListAll(t, "/accounts", "created_by", fmt.Sprint(a.UserID))); !m[acc1ID] || m[accBID] {
		t.Fatalf("created_by=A under own scope: %v", m)
	}
	if m := IDSet(a.ListAll(t, "/accounts", "mine", "true")); !m[acc1ID] {
		t.Fatalf("mine=true under own scope: %v", m)
	}
	// The admin's own account (default base_url = mock) shows under mine=true.
	adminAcc := e.CreateAccount(admin, AccountSpec{})
	cl.Accounts = append(cl.Accounts, adminAcc)
	if m := IDSet(admin.ListAll(t, "/accounts", "mine", "true")); !m[adminAcc] {
		t.Fatalf("admin mine=true lacks its own account %d", adminAcc)
	}

	// Delete: own:delete and account:delete both work without confirmation.
	if r := a.API(t, http.MethodDelete, fmt.Sprintf("/accounts/%d", acc4ID), nil); r.Status != 204 {
		t.Fatalf("vendor deleting its own account: %s", r)
	}
	ExpectNotFound(t, a.API(t, http.MethodGet, fmt.Sprintf("/accounts/%d", acc4ID), nil))
	if r := admin.API(t, http.MethodDelete, fmt.Sprintf("/accounts/%d", acc3ID), nil); r.Status != 204 {
		t.Fatalf("admin deleting A's account: %s", r)
	}
	// Proxy still referenced by A's accounts (acc1, acc2) -> 409 for A too,
	// with the full account_count.
	if r := a.API(t, http.MethodDelete, fmt.Sprintf("/proxies/%d", proxyID), nil); r.Status != 409 || r.ErrCode() != "conflict" || r.JSON().Get("error.details.account_count").Int() != 2 {
		t.Fatalf("deleting a referenced proxy: %s", r)
	}
	// acc5 was the only account on proxyID2; after removing the link the
	// vendor can delete its own proxy.
	a.OK(t, http.MethodPatch, fmt.Sprintf("/accounts/%d", acc5.Get("id").Int()), map[string]any{"proxy_id": nil})
	if r := a.API(t, http.MethodDelete, fmt.Sprintf("/proxies/%d", proxyID2), nil); r.Status != 204 {
		t.Fatalf("vendor deleting its own unreferenced proxy: %s", r)
	}

	// 7. Audit log. There is no console endpoint for audit_logs (CONTRACTS
	// §21.2 only defines the rows), so the table is read directly. This is a
	// subtest so that a missing E2E_DOCKER_HOST shows up as a SKIP in the test
	// output instead of silently dropping the assertions from a passing case.
	t.Run("audit log", func(t *testing.T) {
		e := e.With(t)
		e.RequireDocker()
		rows := e.SQL(fmt.Sprintf(`SELECT action, target_type, target_id, detail::text FROM audit_logs WHERE user_id=%d AND action IN ('account.create','proxy.create') ORDER BY id`, a.UserID))
		var accCreate, proxyAuto bool
		for _, row := range rows {
			f := strings.SplitN(row, "|", 4)
			if len(f) < 4 {
				continue
			}
			switch {
			case f[0] == "account.create" && f[1] == "account" && f[2] == fmt.Sprint(acc1ID):
				accCreate = true
			case f[0] == "proxy.create" && f[1] == "proxy" && f[2] == fmt.Sprint(proxyID):
				proxyAuto = strings.Contains(f[3], `"auto": true`) || strings.Contains(f[3], `"auto":true`)
				if !strings.Contains(f[3], fmt.Sprintf(`"account_id": %d`, acc1ID)) && !strings.Contains(f[3], fmt.Sprintf(`"account_id":%d`, acc1ID)) {
					t.Errorf("proxy.create detail lacks account_id %d: %s", acc1ID, f[3])
				}
			}
		}
		if !accCreate || !proxyAuto {
			t.Fatalf("audit rows for A (account.create=%v proxy.create{auto}=%v): %v", accCreate, proxyAuto, rows)
		}
	})
}
