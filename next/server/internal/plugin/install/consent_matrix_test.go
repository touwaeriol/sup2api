package install

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// Whether a consenter may name a role for a new permission: role:define
// first, critical ones by a superuser only (D3), then the catalog's denial.
func TestNameableRule(t *testing.T) {
	denied := core.PluginGrantees{Denied: map[string]string{"admin": "held"}}
	super := core.PluginGrantees{Superuser: true}
	for _, c := range []struct {
		name      string
		gs        core.PluginGrantees
		canDefine bool
		role      string
		risk      string
		ok        bool
		reason    string
	}{
		{"no role:define", super, false, "operator", manifest.RiskLow, false, "role_define"},
		{"critical, no superuser", denied, true, "operator", manifest.RiskCritical, false, "critical"},
		{"denied by the catalog", denied, true, "admin", manifest.RiskSensitive, false, "held"},
		{"allowed", denied, true, "operator", manifest.RiskSensitive, true, ""},
		{"critical by a superuser", super, true, "operator", manifest.RiskCritical, true, ""},
	} {
		ok, reason := nameable(c.gs, c.canDefine, c.role, c.risk)
		assert.Equal(t, c.ok, ok, c.name)
		assert.Equal(t, c.reason, reason, c.name)
	}
}

// On an upgrade a grant needs consent unless it is of a low risk
// permission to admin, operator, supplier or supplier agent (the agent's
// share of a user default included); on a first install none does.
func TestPlanNeedsConsentByRisk(t *testing.T) {
	m := &manifest.Manifest{Publisher: manifest.OfficialPublisher, Key: "mod", UserPermissions: []manifest.UserPermission{
		newPerm("low:read", "", "admin", "user"), newPerm("flow:manage", manifest.RiskSensitive, "admin"),
	}}
	roles := roleGrants(m, map[string]map[string][]string{"low:read": {"auditor": {""}}}, nil)
	for _, upgrade := range []bool{false, true} {
		plan, _, err := (&Service{}).planRoleGrants(context.Background(), nil, m, nil, roles, nil, upgrade)
		require.NoError(t, err)
		needs := map[string]bool{}
		for _, g := range plan {
			needs[g.Permission+" "+g.Role] = g.NeedsConsent
		}
		assert.Equal(t, map[string]bool{
			"plugin.mod:low:read admin": false, "plugin.mod:low:read auditor": upgrade, "plugin.mod:low:read user": upgrade, "plugin.mod:low:read supplier_agent": false,
			"plugin.mod:flow:manage admin": upgrade,
		}, needs, "upgrade %v", upgrade)
	}
}

// flowGuard is guard at version with a scoped, sensitive flow:manage
// suggested to admin (every row) and operator (own).
func flowGuard(version string) *manifest.Manifest {
	flow := newPerm("flow:manage", manifest.RiskSensitive)
	flow.Scope = &manifest.PermissionScope{Resource: "flow", Own: true}
	all := manifest.RoleDefault{Scopes: []string{"*"}}
	flow.Defaults = map[string]manifest.RoleDefault{"admin": all, "operator": {Scopes: []string{"own"}}, "supplier": all}
	return guardWith(version, flow)
}

// denyAdmin makes the fake catalog refuse the admin role (the consenter
// holds it).
func denyAdmin(e *env) {
	e.perms.grantees = func(context.Context, *gorm.DB, int64, []string) (core.PluginGrantees, error) {
		return core.PluginGrantees{Denied: map[string]string{"admin": "held"}}, nil
	}
}

// D2 on a first install: defaults to a role the installer may not name are
// withheld, not granted, and listed for review, where a superuser's
// confirmation grants them with their scope; the rest are granted.
func TestInstallWithholdsUnnameableDefaults(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	denyAdmin(e)
	_, err := e.svc.Upload(ctx, pkgtest.Build(flowGuard("0.1.0"), e.root), e.admin, UploadOptions{})
	require.NoError(t, err)
	res, err := e.svc.Consent(ctx, "guard", "0.1.0", ConsentRequest{Grants: explicitGuardGrants()}, e.admin)
	require.NoError(t, err)
	flow := "plugin.guard:flow:manage"
	assert.Equal(t, []RoleGrant{{Permission: flow, Role: "admin", Risk: manifest.RiskSensitive, Via: viaDefault, Withheld: true}}, res.Withheld)
	assert.Equal(t, map[string][]string{"operator": {"own"}, "supplier": {""}, "supplier_agent": {""}}, lastDef(t, e, flow).GrantRoles)
	var pending string
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT detail->>'pending' FROM audit_logs WHERE action = $1 AND target_id = 'guard'`, AuditConsent).Scan(&pending))
	assert.Contains(t, pending, `"role": "admin"`)

	addPerms(t, e, map[string]string{"flow:manage": manifest.RiskSensitive})
	gs, err := e.svc.PendingRoleGrants(ctx, e.db.Gorm, "guard", "")
	require.NoError(t, err)
	assert.Equal(t, []RoleGrant{{Version: "0.1.0", Permission: flow, Role: "admin", Risk: manifest.RiskSensitive, Via: viaDefault, Withheld: true}}, gs)
	_, err = e.svc.ReviewRoleGrants(ctx, "guard", "0.1.0", true, e.admin)
	require.NoError(t, err)
	assert.Equal(t, []string{flow + "=admin"}, e.perms.granted)
}

// An override replaces the defaults of the roles the consenter may name
// (those it leaves out get nothing); a default to a role it may not name
// still waits for review (D2).
func TestOverrideKeepsUnnameableDefaultsPending(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	denyAdmin(e)
	_, err := e.svc.Upload(ctx, pkgtest.Build(flowGuard("0.1.0"), e.root), e.admin, UploadOptions{})
	require.NoError(t, err)
	req := ConsentRequest{Grants: explicitGuardGrants(), NewPermissionRoles: map[string]map[string]manifest.RoleDefault{
		"flow:manage": {"operator": {Scopes: []string{"*"}}}}}
	res, err := e.svc.Consent(ctx, "guard", "0.1.0", req, e.admin)
	require.NoError(t, err)
	assert.Equal(t, []string{"admin"}, rolesOf(res.Withheld))
	assert.Equal(t, map[string][]string{"operator": {""}}, lastDef(t, e, "plugin.guard:flow:manage").GrantRoles)
}

// Without role:define an installer may name no role: every default waits
// for review, and the catalog is never asked.
func TestInstallWithoutRoleDefineWithholdsAllDefaults(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	delete(e.authz.perms[e.admin], "role:define")
	_, err := e.svc.Upload(ctx, pkgtest.Build(flowGuard("0.1.0"), e.root), e.admin, UploadOptions{})
	require.NoError(t, err)
	res, err := e.svc.Consent(ctx, "guard", "0.1.0", ConsentRequest{Grants: explicitGuardGrants()}, e.admin)
	require.NoError(t, err)
	assert.Equal(t, []string{"admin", "operator", "supplier", "supplier_agent"}, rolesOf(res.Withheld))
	assert.Equal(t, map[string][]string{}, lastDef(t, e, "plugin.guard:flow:manage").GrantRoles)
	assert.Empty(t, e.perms.checked)
}

// D6 on the install side: a key removed earlier and declared again is not
// new, so it gets no defaults; the review shows it revived, its cells the
// grants it comes back with.
func TestRevivedKeyGetsNoDefaults(t *testing.T) {
	e, ctx := roleGrantsEnv(t)
	_, err := e.db.SQL.ExecContext(ctx, `
INSERT INTO permissions (key, module, label, source, plugin_key, status, risk, scope_type)
VALUES ('plugin.guard:flow:manage', 'plugin.guard', '{"en":"x"}', 'plugin', 'guard', 'removed', 'sensitive', 'plugin:flow')`)
	require.NoError(t, err)
	testutil.Grant(t, e.db, "operator", "plugin.guard:flow:manage", "flow/3")
	m := flowGuard("0.1.1")
	existing, err := loadCatalogPerms(ctx, e.svc.d.Permissions, e.db.Gorm, m.Key)
	require.NoError(t, err)
	assert.True(t, existing.has("plugin.guard:flow:manage"))
	assert.True(t, existing.removed("plugin.guard:flow:manage"))
	assert.False(t, existing.removed("plugin.guard:rules:read"))

	// Nothing to grant, nothing new: the upgrade is carried over.
	up, err := e.svc.Upload(ctx, pkgtest.Build(m, e.root), e.admin, UploadOptions{})
	require.NoError(t, err)
	assert.Equal(t, ConsentApproved, up.ConsentStatus)
	assert.Nil(t, lastDef(t, e, "plugin.guard:flow:manage").GrantRoles)
	r, err := e.svc.Review(ctx, "guard", "0.1.1", e.admin)
	require.NoError(t, err)
	flow := r.UserPermissions[len(r.UserPermissions)-1]
	assert.True(t, flow.Revived)
	assert.False(t, flow.New)
	assert.Equal(t, []string{"flow/3"}, flow.Cells["operator"].Grant)
	assert.Equal(t, []string{}, flow.Cells["admin"].Grant)
	assert.False(t, flow.Cells["operator"].Withheld)
	assert.Empty(t, r.NewRoleGrants)

	// Who holds an existing key is role:read's to see (C4).
	delete(e.authz.perms[e.admin], "role:read")
	r, err = e.svc.Review(ctx, "guard", "0.1.1", e.admin)
	require.NoError(t, err)
	cell, err := json.Marshal(r.UserPermissions[len(r.UserPermissions)-1].Cells["operator"])
	require.NoError(t, err)
	assert.NotContains(t, string(cell), `"grant"`)
}

// The review's matrix is the viewer's: columns say which roles it may name
// and why not (super admins never take writes), a critical row is
// unnameable for a non-superuser, a cell's grant is withheld exactly when
// it is not empty and unnameable, and an upgrade lists the permissions it
// no longer declares with their holders.
func TestReviewMatrixCells(t *testing.T) {
	e, ctx := roleGrantsEnv(t)
	_, err := e.db.SQL.ExecContext(ctx, `
INSERT INTO roles (key, name, builtin, superuser) VALUES ('super_admin', '{"en":"Super"}', true, true);
INSERT INTO permissions (key, module, label, source, plugin_key, status, sort)
VALUES ('plugin.guard:old:gone', 'plugin.guard', '{"en":"Gone"}', 'plugin', 'guard', 'active', 40)`)
	require.NoError(t, err)
	testutil.Grant(t, e.db, "auditor", "plugin.guard:old:gone")
	testutil.Grant(t, e.db, "super_admin", "plugin.guard:old:gone")
	testutil.Grant(t, e.db, "operator", "plugin.guard:old:gone", "own")
	denyAdmin(e)
	m := flowGuard("0.1.1")
	m.UserPermissions = append(m.UserPermissions, newPerm("rules:exempt", manifest.RiskCritical))
	_, err = e.svc.Upload(ctx, pkgtest.Build(m, e.root), e.admin, UploadOptions{})
	require.NoError(t, err)
	r, err := e.svc.Review(ctx, "guard", "0.1.1", e.admin)
	require.NoError(t, err)

	cols := map[string]ReviewRoleColumn{}
	for _, c := range r.RoleColumns {
		cols[c.Key] = c
	}
	assert.Equal(t, "super_admin", r.RoleColumns[0].Key)
	assert.Equal(t, ReviewRoleColumn{Key: "super_admin", Name: core.LocalizedText{"en": "Super"}, Builtin: true, Superuser: true, Reason: "superuser_role"}, cols["super_admin"])
	assert.Equal(t, "held", cols["admin"].Reason)
	assert.True(t, cols["operator"].Nameable)
	assert.True(t, cols["auditor"].Nameable)

	byKey := map[string]ReviewUserPerm{}
	for _, up := range r.UserPermissions {
		byKey[up.Key] = up
	}
	flow := byKey["flow:manage"]
	assert.True(t, flow.New)
	// A supplier agent follows the user and the supplier (manifest.RoleDefaultsFrom).
	assert.Equal(t, map[string][]string{"admin": {""}, "operator": {"own"}, "supplier": {""}, "supplier_agent": {""}}, flow.Defaults)
	assert.Equal(t, &manifest.PermissionScope{Resource: "flow", Own: true}, flow.Scope)
	assert.Equal(t, ReviewCell{Grant: []string{""}, Reason: "held", Withheld: true}, flow.Cells["admin"])
	assert.Equal(t, ReviewCell{Grant: []string{"own"}, Nameable: true}, flow.Cells["operator"])
	assert.Equal(t, ReviewCell{Grant: []string{}, Nameable: true}, flow.Cells["auditor"])
	assert.Equal(t, ReviewCell{Grant: []string{}, Reason: "superuser_role"}, flow.Cells["super_admin"])
	for role, c := range byKey["rules:exempt"].Cells {
		if role != "super_admin" {
			assert.Equal(t, ReviewCell{Grant: []string{}, Reason: "critical"}, c, role)
		}
	}
	assert.False(t, byKey["rules:manage"].New)
	assert.Equal(t, ReviewCell{Grant: []string{}, Nameable: true}, byKey["rules:manage"].Cells["auditor"])
	assert.Equal(t, []ReviewRemovedPerm{{Key: "old:gone", Label: core.LocalizedText{"en": "Gone"}, Holders: []string{"auditor", "operator"}}}, r.RemovedUserPermissions)

	delete(e.authz.perms[e.admin], "role:read")
	r, err = e.svc.Review(ctx, "guard", "0.1.1", e.admin)
	require.NoError(t, err)
	assert.Equal(t, []ReviewRemovedPerm{{Key: "old:gone", Label: core.LocalizedText{"en": "Gone"}}}, r.RemovedUserPermissions, "no holders (C4)")
	assert.Equal(t, ReviewCell{Grant: []string{""}, Reason: "held", Withheld: true}, r.UserPermissions[len(r.UserPermissions)-2].Cells["admin"],
		"a new key's defaults are the manifest's, not holders")
}
