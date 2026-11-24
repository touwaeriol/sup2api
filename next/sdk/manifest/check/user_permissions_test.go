package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// inheritFrom names local keys this manifest no longer declares (a rename
// or a split): neither the permission's own key nor another declared one,
// whose holders would get the new permission besides; a critical
// permission alike, the risk of the old key is checked at consent.
func TestUserPermissionInheritFrom(t *testing.T) {
	m := minimal()
	m.UserPermissions = []manifest.UserPermission{
		{Key: "x:use", Label: label("Use"), InheritFrom: []string{"x:read"}},
		{Key: "x:manage", Label: label("Manage"), InheritFrom: []string{"x:manage", "Bad Key", "x:use", "x:edit"}},
		{Key: "x:all", Risk: manifest.RiskCritical, Label: label("All"), InheritFrom: []string{"x:exempt", "x:manage"}},
	}
	assert.Equal(t, map[string]string{
		"userPermissions[1].inheritFrom[0]": "invalid_value",
		"userPermissions[1].inheritFrom[1]": "invalid_value",
		"userPermissions[1].inheritFrom[2]": "invalid_value",
		"userPermissions[2].inheritFrom[1]": "invalid_value",
	}, fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "userPermissions"))
}

// sensitive and defaultRoles were replaced by risk and defaults; a manifest
// still declaring them is rejected rather than decoded to no risk and no
// defaults. Without manifest.json among the files there is nothing to read.
func TestRetiredUserPermissionFields(t *testing.T) {
	raw := []byte(`{"userPermissions":[
		{"key":"x:use","label":{"en":"Use"},"defaultRoles":["admin"]},
		{"key":"x:read","label":{"en":"Read"},"defaults":{"admin":true}},
		{"key":"x:manage","label":{"en":"Manage"},"sensitive":true,"defaultRoles":[]}]}`)
	m := minimal()
	m.UserPermissions = []manifest.UserPermission{{Key: "x:use", Label: label("Use")}, {Key: "x:read", Label: label("Read")}, {Key: "x:manage", Label: label("Manage")}}
	assert.Equal(t, map[string]string{
		"userPermissions[0].defaultRoles": "unsupported",
		"userPermissions[2].sensitive":    "unsupported",
		"userPermissions[2].defaultRoles": "unsupported",
	}, fieldCodes(Validate(m, map[string][]byte{"manifest.json": raw}, ValidateOptions{Tooling: true}), "userPermissions"))
	assert.Empty(t, fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "userPermissions"))
}

// A key ending in :own would read as a scope in the core (CONTRACTS §21.0).
func TestUserPermissionKeyNotAScope(t *testing.T) {
	m := minimal()
	m.UserPermissions = []manifest.UserPermission{
		{Key: "reset:own", Label: manifest.LocalizedText{"en": "Reset"}},
		{Key: "own:reset", Label: manifest.LocalizedText{"en": "Reset"}, InheritFrom: []string{"reset:own"}},
	}
	got := codes(Validate(m, nil, ValidateOptions{Tooling: true}))
	if got["userPermissions[0].key"] != "invalid_format" || got["userPermissions[1].inheritFrom[0]"] != "invalid_value" || got["userPermissions[1].key"] != "" {
		t.Fatalf("codes = %v", got)
	}
}

// fieldCodes is codes limited to the fields under the given prefixes, so a
// case states every error of the declarations it is about.
func fieldCodes(err error, prefixes ...string) map[string]string {
	out := map[string]string{}
	for f, c := range codes(err) {
		for _, p := range prefixes {
			if strings.HasPrefix(f, p) {
				out[f] = c
			}
		}
	}
	return out
}

func label(s string) manifest.LocalizedText { return manifest.LocalizedText{"en": s} }

// permissionManifest declares every new permission field validly, on the
// core that honours them: GET /flows is the options route of flow:manage.
func permissionManifest() *manifest.Manifest {
	m := minimal()
	m.HostCompat = ">=0.1.290 <0.2.0"
	m.Capabilities = []manifest.Capability{{ID: manifest.CapHTTPRoutes}}
	m.HostPermissions = []manifest.HostPermission{{ID: "routes.admin"}}
	m.PermissionGroups = []manifest.PermissionGroup{{Key: "flow", Label: label("Flows")}, {Key: "record", Label: label("Records")}}
	m.UserPermissions = []manifest.UserPermission{
		{Key: "flow:read", Group: "flow", Label: label("View flows"), Defaults: map[string]manifest.RoleDefault{"admin": {Unscoped: true}, "operator": {Unscoped: true}}},
		{Key: "flow:manage", Group: "flow", Risk: manifest.RiskSensitive, Requires: []string{"flow:read"}, Label: label("Edit flows"),
			Scope:    &manifest.PermissionScope{Resource: "flow", Options: "/flows", Own: true},
			Defaults: map[string]manifest.RoleDefault{"admin": {Scopes: []string{"*"}}, "operator": {Scopes: []string{"own"}}}},
		{Key: "record:read", Group: "record", Label: label("View records"),
			Scope:    &manifest.PermissionScope{Values: []manifest.ScopeValue{{Key: "input", Label: label("Input")}, {Key: "output", Label: label("Output")}}},
			Defaults: map[string]manifest.RoleDefault{"admin": {Scopes: []string{"*"}}, "operator": {Scopes: []string{"input"}}}},
		{Key: "exempt:use", Risk: manifest.RiskCritical, Label: label("Exempt")},
	}
	m.Routes = []manifest.Route{
		{Method: "GET", Path: "/flows", Scope: "admin", Permission: "flow:read"},
		{Method: "PUT", Path: "/flows/:id", Scope: "admin", Permission: "flow:manage", ScopeFrom: "path:id"},
	}
	return m
}

func TestPermissionManifestFixtureIsValid(t *testing.T) {
	require.Empty(t, codes(Validate(permissionManifest(), nil, ValidateOptions{Tooling: true})))
}

func TestPermissionGroups(t *testing.T) {
	m := permissionManifest()
	m.PermissionGroups = append(m.PermissionGroups,
		manifest.PermissionGroup{Key: "Flow!", Label: label("Bad")},
		manifest.PermissionGroup{Key: "flow", Label: label("Again")},
		manifest.PermissionGroup{Key: "empty"},
		manifest.PermissionGroup{Key: "unused", Label: label("Unused")})
	m.UserPermissions[0].Group = "missing"
	assert.Equal(t, map[string]string{
		"permissionGroups[2].key":   "invalid_format",
		"permissionGroups[3].key":   "duplicate",
		"permissionGroups[4].label": "required",
		"userPermissions[0].group":  "unknown",
	}, fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "permissionGroups", "userPermissions"))
}

func TestUserPermissionRiskAndRequires(t *testing.T) {
	m := permissionManifest()
	m.UserPermissions[0].Risk = "high"
	m.UserPermissions[1].Requires = []string{"flow:read", "flow:manage", "flow:write"}
	assert.Equal(t, map[string]string{
		"userPermissions[0].risk":        "invalid_value",
		"userPermissions[1].requires[1]": "unknown",
		"userPermissions[1].requires[2]": "unknown",
	}, fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "userPermissions"))
}

func TestUserPermissionDefaults(t *testing.T) {
	cases := []struct {
		name     string
		perm     int
		defaults map[string]manifest.RoleDefault
		want     map[string]string
	}{
		{"not a built-in role", 0, map[string]manifest.RoleDefault{"super_admin": {Unscoped: true}, "custom": {Unscoped: true}, "admin": {Unscoped: true}},
			map[string]string{"userPermissions[0].defaults.super_admin": "invalid_value", "userPermissions[0].defaults.custom": "invalid_value"}},
		{"critical", 3, map[string]manifest.RoleDefault{"admin": {Unscoped: true}},
			map[string]string{"userPermissions[3].defaults": "unsupported"}},
		{"false and []", 1, map[string]manifest.RoleDefault{"admin": {}, "user": {Scopes: []string{}}},
			map[string]string{"userPermissions[1].defaults.admin": "invalid_value", "userPermissions[1].defaults.user": "invalid_value"}},
		{"array on an unscoped permission", 0, map[string]manifest.RoleDefault{"admin": {Scopes: []string{"*"}}},
			map[string]string{"userPermissions[0].defaults.admin": "invalid_value"}},
		{"true on a scoped permission", 1, map[string]manifest.RoleDefault{"admin": {Unscoped: true}},
			map[string]string{"userPermissions[1].defaults.admin": "invalid_value"}},
		{"an id", 1, map[string]manifest.RoleDefault{"admin": {Scopes: []string{"*", "flow/3"}}},
			map[string]string{"userPermissions[1].defaults.admin[1]": "invalid_value"}},
		{"own not allowed", 2, map[string]manifest.RoleDefault{"admin": {Scopes: []string{"own"}}},
			map[string]string{"userPermissions[2].defaults.admin[0]": "invalid_value"}},
		{"undeclared value", 2, map[string]manifest.RoleDefault{"admin": {Scopes: []string{"input", "other"}}},
			map[string]string{"userPermissions[2].defaults.admin[1]": "invalid_value"}},
	}
	for _, c := range cases {
		m := permissionManifest()
		m.UserPermissions[c.perm].Defaults = c.defaults
		assert.Equal(t, c.want, fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "userPermissions"), c.name)
	}
}

func TestUserPermissionScope(t *testing.T) {
	many := make([]manifest.ScopeValue, 51)
	for i := range many {
		many[i] = manifest.ScopeValue{Key: fmt.Sprintf("v%d", i), Label: label("V")}
	}
	cases := []struct {
		name  string
		scope manifest.PermissionScope
		want  map[string]string
	}{
		{"values and resource", manifest.PermissionScope{Resource: "flow", Values: []manifest.ScopeValue{{Key: "a", Label: label("A")}}},
			map[string]string{"userPermissions[4].scope": "mutually_exclusive"}},
		{"values and own", manifest.PermissionScope{Own: true, Values: []manifest.ScopeValue{{Key: "a", Label: label("A")}}},
			map[string]string{"userPermissions[4].scope": "mutually_exclusive"}},
		{"neither", manifest.PermissionScope{Options: "/flows"},
			map[string]string{"userPermissions[4].scope": "required"}},
		{"bad resource", manifest.PermissionScope{Resource: "Flow", Own: true},
			map[string]string{"userPermissions[4].scope.resource": "invalid_format"}},
		{"core resource", manifest.PermissionScope{Resource: "account", Own: true},
			map[string]string{"userPermissions[4].scope.resource": "reserved"}},
		{"values resource", manifest.PermissionScope{Resource: "value", Own: true},
			map[string]string{"userPermissions[4].scope.resource": "reserved"}},
		{"granted only whole", manifest.PermissionScope{Resource: "flow"},
			map[string]string{"userPermissions[4].scope": "required"}},
		{"own only", manifest.PermissionScope{Resource: "flow", Own: true}, map[string]string{}},
		{"not a route ref", manifest.PermissionScope{Resource: "flow", Options: "flows"},
			map[string]string{"userPermissions[4].scope.options": "invalid_route_ref"}},
		{"undeclared route", manifest.PermissionScope{Resource: "flow", Options: "/nope"},
			map[string]string{"userPermissions[4].scope.options": "unknown_route"}},
		{"POST route", manifest.PermissionScope{Resource: "flow", Options: "POST /flows/search"},
			map[string]string{"userPermissions[4].scope.options": "invalid"}},
		{"user route", manifest.PermissionScope{Resource: "flow", Options: "/flows/mine"},
			map[string]string{"userPermissions[4].scope.options": "invalid"}},
		{"parameterized route", manifest.PermissionScope{Resource: "flow", Options: "/flows/:id"},
			map[string]string{"userPermissions[4].scope.options": "invalid"}},
		{"too many values", manifest.PermissionScope{Values: many},
			map[string]string{"userPermissions[4].scope.values": "too_many"}},
		{"bad values", manifest.PermissionScope{Values: []manifest.ScopeValue{{Key: "a b", Label: label("A")}, {Key: "in", Label: label("In")}, {Key: "in", Label: label("In")}, {Key: "out"}}},
			map[string]string{"userPermissions[4].scope.values[0].key": "invalid_format", "userPermissions[4].scope.values[2].key": "duplicate", "userPermissions[4].scope.values[3].label": "required"}},
	}
	for _, c := range cases {
		m := permissionManifest()
		m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "routes.user"})
		m.Routes = append(m.Routes,
			manifest.Route{Method: "POST", Path: "/flows/search", Scope: "admin", Permission: "flow:read"},
			manifest.Route{Method: "GET", Path: "/flows/mine", Scope: "user", Permission: "flow:read"},
			manifest.Route{Method: "GET", Path: "/flows/:id", Scope: "admin", Permission: "flow:read"})
		m.UserPermissions = append(m.UserPermissions, manifest.UserPermission{Key: "flow:audit", Label: label("Audit"), Scope: &c.scope})
		assert.Equal(t, c.want, fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "userPermissions"), c.name)
	}
}

// A permission key's length counts the plugin id (manifest.PermissionKey of
// m.ID()), not the bare key: plugin._tester__demo:<local> may not exceed
// 150 bytes even where plugin.demo:<local> would not.
func TestUserPermissionKeyLengthByPluginID(t *testing.T) {
	m := minimal()
	local := "x:" + strings.Repeat("a", 128)
	m.UserPermissions = []manifest.UserPermission{{Key: local, Label: label("Long")}}
	require.LessOrEqual(t, len(manifest.PermissionKey(m.Key, local)), 150)
	assert.Equal(t, map[string]string{"userPermissions[0].key": "invalid_format"},
		fieldCodes(Validate(m, nil, ValidateOptions{Tooling: true}), "userPermissions"))
}
