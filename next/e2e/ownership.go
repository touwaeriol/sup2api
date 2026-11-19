package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// Ownership helpers (CONTRACTS §21): own-scoped account / proxy permissions,
// guarded settings (base_url) and proxy_url auto-linking.
//
// Assumed shapes (backend implemented in parallel):
//   POST/PATCH /accounts {..., proxy_url} -> data{..., proxy_id, proxy_created, created_by, created_by_email}
//   field errors: details.fields[{field, code}] with field "proxy_url" (conflict|invalid),
//     "proxy_id" (not_found), "credentials.base_url" (forbidden)
//   GET /accounts?mine=true|created_by=<id>, GET /proxies?mine=true|created_by=<id>
//   GET /account-types -> [{..., guarded_settings: [{field, allowed}]}]

// Permission sets of the typical roles of CONTRACTS §21.1.
var (
	// VendorPerms maintains only the caller's own accounts and proxies and
	// cannot change guarded settings (no account:settings:custom).
	VendorPerms = []string{
		"account:read:own", "account:create:own", "account:update:own", "account:delete:own",
		"account:test:own", "account:credential:view:own",
		"proxy:read:own", "proxy:manage:own",
	}
	// ViewerPerms sees every account and proxy but changes nothing and never
	// sees secrets.
	ViewerPerms = []string{"account:read", "proxy:read"}
)

// OfficialAnthropicBaseURL is the only base_url a caller without
// account:settings:custom may use for anthropic/apikey (guardedSettings).
const OfficialAnthropicBaseURL = "https://api.anthropic.com"

// RoleKey returns a unique role key valid for POST /roles (^[a-z][a-z0-9_]{1,49}$).
func (e *Env) RoleKey(prefix string) string {
	return strings.ReplaceAll(strings.ToLower(e.Name(prefix)), "-", "_")
}

// NewUserWithRoles creates a role with the given permissions and a user
// holding it; returns the role id and the logged-in user.
func (e *Env) NewUserWithRoles(admin *Session, roleKey string, perms []string) (int64, *Session) {
	e.T.Helper()
	roleID := e.CreateRole(admin, roleKey, roleKey, perms)
	return roleID, e.CreateUser(admin, UserSpec{Roles: []string{roleKey}})
}

// OwnedAccountBody builds a POST /accounts body for an anthropic/apikey
// account that a caller without account:settings:custom or
// account:group:bind may create: no groups, no base_url (the plugin
// normalises it to the official address) and no proxy_id key. extra is merged on top (e.g. "proxy_url", "proxy_id",
// "credentials").
func (e *Env) OwnedAccountBody(name string, extra map[string]any) map[string]any {
	if name == "" {
		name = e.Name("own-acct")
	}
	body := map[string]any{
		"name": name, "plugin_key": AnthropicPlugin, "type": AnthropicAPIKey, "group_ids": []int64{},
		"priority": 10, "max_concurrency": 5, "schedulable": true,
		"credentials": map[string]any{"api_key": "sk-ant-mock-" + e.Name("own")},
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// CreateOwnedAccount posts body as s, requires 2xx and returns the account
// object (id, proxy_id, proxy_created, created_by, ...).
func (e *Env) CreateOwnedAccount(s *Session, body map[string]any) gjson.Result {
	e.T.Helper()
	d := s.OK(e.T, http.MethodPost, "/accounts", body)
	if d.Get("id").Int() == 0 {
		e.T.Fatalf("POST /accounts returned no id: %s", d.Raw)
	}
	if got := d.Get("credentials.api_key").String(); got != "" && got != "******" {
		e.T.Fatalf("POST /accounts leaked the api_key: %s", d.Raw)
	}
	return d
}

// FieldError returns the code of details.fields[] entry for field ("" when
// absent).
func FieldError(r *Resp, field string) string {
	for _, f := range r.JSON().Get("error.details.fields").Array() {
		if f.Get("field").String() == field {
			return f.Get("code").String()
		}
	}
	return ""
}

// ExpectFieldError requires a 400 invalid_argument response whose
// details.fields contains {field, code}.
func ExpectFieldError(t testing.TB, r *Resp, field, code string) {
	t.Helper()
	if r.Status != 400 || r.ErrCode() != "invalid_argument" {
		t.Fatalf("want 400 invalid_argument with %s/%s: %s", field, code, r)
	}
	if got := FieldError(r, field); got != code {
		t.Fatalf("want field error %s/%s, got %q: %s", field, code, got, r)
	}
}

// ExpectPermissionDenied requires 403 permission_denied.
func ExpectPermissionDenied(t testing.TB, r *Resp) {
	t.Helper()
	if r.Status != 403 || r.ErrCode() != "permission_denied" {
		t.Fatalf("want 403 permission_denied: %s", r)
	}
}

// ExpectNotFound requires 404 not_found (own scope hides other users'
// resources without telling them apart from missing ones).
func ExpectNotFound(t testing.TB, r *Resp) {
	t.Helper()
	if r.Status != 404 || r.ErrCode() != "not_found" {
		t.Fatalf("want 404 not_found: %s", r)
	}
}

// IDSet returns the "id" values of list items.
func IDSet(items []gjson.Result) map[int64]bool {
	m := map[int64]bool{}
	for _, it := range items {
		m[it.Get("id").Int()] = true
	}
	return m
}

// AllCreatedBy reports whether every item has created_by == uid; the first
// offending row is returned for the failure message.
func AllCreatedBy(items []gjson.Result, uid int64) (bool, string) {
	for _, it := range items {
		if it.Get("created_by").Int() != uid {
			return false, it.Raw
		}
	}
	return true, ""
}

// OwnershipCleanup deletes what a test created, best effort and in
// dependency order (accounts before proxies, users before roles). Failures
// are logged, not fatal: the run-unique names keep leftovers harmless.
type OwnershipCleanup struct {
	Accounts []int64
	Proxies  []int64
	Users    []int64
	Roles    []int64
}

// Run performs the cleanup as admin.
func (c *OwnershipCleanup) Run(e *Env, admin *Session) {
	e.T.Helper()
	del := func(path string) {
		if r := admin.API(e.T, http.MethodDelete, path, nil); r.Status != 204 && r.Status != 200 && r.Status != 404 {
			e.T.Logf("cleanup: %s", r)
		}
	}
	for _, id := range c.Accounts {
		del(fmt.Sprintf("/accounts/%d", id))
	}
	for _, id := range c.Proxies {
		del(fmt.Sprintf("/proxies/%d", id))
	}
	for _, id := range c.Users {
		del(fmt.Sprintf("/users/%d", id))
	}
	for _, id := range c.Roles {
		del(fmt.Sprintf("/roles/%d", id))
	}
}
