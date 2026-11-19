package install

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Permissions sort in manifest order from 10 on: the first one is not 0,
// which the catalog used to take for "unset"; groups likewise. The catalog
// gets the risk, the group's label, requires as qualified keys, the scope
// with its scope_type (the values resource for a values scope) and the
// defaults as canonical suffixes; grants only what the consent decided.
func TestPermissionDefsSort(t *testing.T) {
	m := &manifest.Manifest{Key: "mod",
		PermissionGroups: []manifest.PermissionGroup{{Key: "flow", Label: manifest.LocalizedText{"en": "Flows"}}, {Key: "action"}},
		UserPermissions: []manifest.UserPermission{
			{Key: "flow:read", Group: "flow"},
			{Key: "flow:manage", Group: "flow", Risk: manifest.RiskSensitive, Requires: []string{"flow:read"},
				Scope:    &manifest.PermissionScope{Resource: "flow", Options: "/scopes/flow", Own: true},
				Defaults: map[string]manifest.RoleDefault{"admin": {Unscoped: true}, "operator": {Scopes: []string{"own"}}}},
			{Key: "input:check", Group: "action", Scope: &manifest.PermissionScope{Values: []manifest.ScopeValue{{Key: "input"}}}},
			{Key: "exempt", Risk: manifest.RiskCritical},
		}}
	defs := PermissionDefs(m, map[string]map[string][]string{"flow:manage": {"operator": {"own"}}})
	require.Len(t, defs, 4)
	for i, d := range defs {
		assert.Equal(t, (i+1)*10, d.Sort, d.Key)
		assert.Equal(t, "plugin.mod", d.Module)
	}
	read, manage, check, exempt := defs[0], defs[1], defs[2], defs[3]
	assert.Equal(t, "plugin.mod:flow:read", read.Key)
	assert.Equal(t, manifest.RiskLow, read.Risk)
	assert.Equal(t, "flow", read.Group)
	assert.Equal(t, core.LocalizedText{"en": "Flows"}, read.GroupLabel)
	assert.Equal(t, 10, read.GroupSort)
	assert.Empty(t, read.ScopeType)
	assert.Nil(t, read.Scope)
	assert.Nil(t, read.GrantRoles)

	assert.Equal(t, manifest.RiskSensitive, manage.Risk)
	assert.Equal(t, []string{"plugin.mod:flow:read"}, manage.Requires)
	assert.Equal(t, "plugin:flow", manage.ScopeType)
	assert.Equal(t, &manifest.PermissionScope{Resource: "flow", Options: "/scopes/flow", Own: true}, manage.Scope)
	assert.Equal(t, map[string][]string{"admin": {""}, "operator": {"own"}}, manage.Defaults)
	assert.Equal(t, map[string][]string{"operator": {"own"}}, manage.GrantRoles)

	assert.Equal(t, 20, check.GroupSort)
	assert.Equal(t, "plugin:value", check.ScopeType)
	assert.Equal(t, "value", check.Scope.Resource)

	assert.Equal(t, manifest.RiskCritical, exempt.Risk)
	assert.Empty(t, exempt.Group)
	assert.Zero(t, exempt.GroupSort)
	assert.Nil(t, exempt.Defaults)
}

// roleGrants: nothing for a key the catalog has (removed rows included,
// D6); none of the defaults for a rename (inherit only); else the manifest
// defaults as canonical suffixes, the roles an override names replacing
// theirs while the defaults of roles it leaves out stay for the settlement
// to drop or withhold ({} included).
func TestRoleGrantsPriority(t *testing.T) {
	all := manifest.RoleDefault{Unscoped: true}
	m := &manifest.Manifest{Key: "referral", UserPermissions: []manifest.UserPermission{
		{Key: "referral:use", Defaults: map[string]manifest.RoleDefault{"user": all, "admin": all}, InheritFrom: []string{"referral:read"}},
		{Key: "referral:stats:read"},
		{Key: "flow:manage", Scope: &manifest.PermissionScope{Resource: "flow", Own: true},
			Defaults: map[string]manifest.RoleDefault{"admin": {Scopes: []string{"*"}}, "operator": {Scopes: []string{"own"}}}},
	}}
	defaults := map[string]map[string][]string{
		"referral:use": {"admin": {""}, "supplier_agent": {""}, "user": {""}}, "referral:stats:read": {}, "flow:manage": {"admin": {""}, "operator": {"own"}},
	}
	cases := []struct {
		name      string
		overrides map[string]map[string][]string
		existing  catalogPerms
		want      map[string]map[string][]string
	}{
		{"manifest", nil, nil, defaults},
		// referral:use inherits referral:read, which the catalog has.
		{"rename", nil, catalogPerms{"plugin.referral:referral:read": {}},
			map[string]map[string][]string{"referral:use": {}, "referral:stats:read": {}, "flow:manage": {"admin": {""}, "operator": {"own"}}}},
		{"existing and revived", nil, catalogPerms{"plugin.referral:flow:manage": {}},
			map[string]map[string][]string{"referral:use": {"admin": {""}, "supplier_agent": {""}, "user": {""}}, "referral:stats:read": {}, "flow:manage": {}}},
		{"override", map[string]map[string][]string{"flow:manage": {"operator": {""}, "auditor": {"flow/3"}}, "referral:stats:read": {}}, nil,
			map[string]map[string][]string{"referral:use": {"admin": {""}, "supplier_agent": {""}, "user": {""}}, "referral:stats:read": {},
				"flow:manage": {"admin": {""}, "operator": {""}, "auditor": {"flow/3"}}}},
		{"override of a rename", map[string]map[string][]string{"referral:use": {"auditor": {""}}}, catalogPerms{"plugin.referral:referral:read": {}},
			map[string]map[string][]string{"referral:use": {"auditor": {""}}, "referral:stats:read": {}, "flow:manage": {"admin": {""}, "operator": {"own"}}}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, roleGrants(m, c.overrides, c.existing), c.name)
	}
}

// overrideGrants takes the manifest spelling and the canonical suffixes
// alike: true and "*" are every row, which makes the rest redundant; own
// only where the scope takes it; an enum key or "value/<key>" of the
// declared values; a runtime id of the declared resource. Anything else is
// invalid under new_permission_roles.<local>.<role>, an undeclared key
// unknown; false and [] grant the role nothing.
func TestOverrideGrants(t *testing.T) {
	m := &manifest.Manifest{Key: "mod", UserPermissions: []manifest.UserPermission{
		{Key: "flow:manage", Scope: &manifest.PermissionScope{Resource: "flow", Own: true}},
		{Key: "flow:read", Scope: &manifest.PermissionScope{Resource: "flow"}},
		{Key: "input:check", Scope: &manifest.PermissionScope{Values: []manifest.ScopeValue{{Key: "input"}, {Key: "output"}}}},
		{Key: "plain"},
	}}
	scopes := func(vs ...string) manifest.RoleDefault { return manifest.RoleDefault{Scopes: vs} }
	got, errs := overrideGrants(m, map[string]map[string]manifest.RoleDefault{
		"flow:manage": {"admin": {Unscoped: true}, "operator": scopes("*", "own"), "support": scopes("own", "flow/3", "flow/3"), "user": {}, "quiet": scopes()},
		"input:check": {"admin": scopes("input", "value/output", "value/input")},
		"plain":       {"admin": {Unscoped: true}},
	})
	require.Empty(t, errs)
	assert.Equal(t, map[string]map[string][]string{
		"flow:manage": {"admin": {""}, "operator": {""}, "support": {"flow/3", "own"}},
		"input:check": {"admin": {"value/input", "value/output"}},
		"plain":       {"admin": {""}},
	}, got)

	_, errs = overrideGrants(m, map[string]map[string]manifest.RoleDefault{
		"flow:read":   {"own": scopes("own"), "proxy": scopes("proxy/3"), "empty": scopes("flow/")},
		"input:check": {"other": scopes("value/x"), "bare": scopes("x")},
		"plain":       {"scoped": scopes("own")},
		"nope":        {"admin": {Unscoped: true}},
	})
	codes := map[string]string{}
	for _, e := range errs {
		codes[e.Field] += e.Code
	}
	assert.Equal(t, map[string]string{
		"new_permission_roles.flow:read.own":     "invalid",
		"new_permission_roles.flow:read.proxy":   "invalid",
		"new_permission_roles.flow:read.empty":   "invalid",
		"new_permission_roles.input:check.other": "invalid",
		"new_permission_roles.input:check.bare":  "invalid",
		"new_permission_roles.plain.scoped":      "invalid",
		"new_permission_roles.nope":              "unknown",
	}, codes)
}

// On an upgrade a grant the current one covers is carried, named again or
// not; one wider than the current grant, or on a first install, is new.
func TestDecideCarriesCoveredGrants(t *testing.T) {
	m := &manifest.Manifest{Key: "demo", HostPermissions: []manifest.HostPermission{
		{ID: "ui.native"}, {ID: "users.read"},
		{ID: "events", Scope: map[string]any{"subscribe": []any{"a", "b"}}},
	}}
	current := map[string]Grant{
		"ui.native":  {Status: GrantGranted},
		"users.read": {Status: GrantGranted},
		"events":     {Status: GrantGranted, Scope: map[string]any{"subscribe": []any{"a"}}},
	}
	req := ConsentRequest{Grants: []GrantInput{{Permission: "ui.native"}, {Permission: "events"}}}
	carried := func(ds []decision) map[string]bool {
		out := map[string]bool{}
		for _, d := range ds {
			out[d.permission] = d.carried
		}
		return out
	}
	ds, errs := decide(m, req, current, nil, true)
	if len(errs) != 0 || fmt.Sprint(carried(ds)) != "map[events:false ui.native:true users.read:true]" {
		t.Fatalf("upgrade: %v %+v", carried(ds), errs)
	}
	req.Grants = append(req.Grants, GrantInput{Permission: "users.read"})
	if ds, _ := decide(m, req, current, nil, false); fmt.Sprint(carried(ds)) != "map[events:false ui.native:false users.read:false]" {
		t.Fatalf("first install: %v", carried(ds))
	}
}
