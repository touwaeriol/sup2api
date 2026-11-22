package segment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// Whose user group a caller may choose is authz's CanActOnUsers: the users
// it manages, itself only as the super admin (C-L7, FU6). admin and masked
// both hold segment:manage; the roles decide.
func TestMembershipActsOnOthers(t *testing.T) {
	e := setup(t)
	az := authz.New(authz.Deps{DB: e.db})
	require.NoError(t, az.SyncCore(context.Background()))
	e.svc.users = az
	super := e.user("super@x.com")
	for user, role := range map[int64]string{e.admin: authz.RoleAdmin, e.masked: authz.RoleOperator, super: authz.RoleSuperAdmin} {
		testutil.AssignRoles(t, e.db, user, role)
	}
	operator, vip := e.masked, e.segment("VIP")
	operator2, supplier := e.user("op2@x.com"), e.user("supplier@x.com")
	testutil.AssignRoles(t, e.db, operator2, authz.RoleOperator)
	testutil.AssignRoles(t, e.db, supplier, authz.RoleUser, authz.RoleSupplier)
	add := func(users ...int64) response {
		return e.do(operator, "POST", path("/segments/%d/members", vip), map[string]any{"user_ids": users})
	}
	personalize := func(user int64) response {
		return e.do(operator, "POST", path("/users/%d/personal-segment", user), map[string]any{})
	}
	depersonalize := func(user int64) response {
		return e.do(operator, "DELETE", path("/users/%d/personal-segment", user), nil)
	}

	add(e.plain, operator).failed(403, "permission_denied")
	require.NotEqual(t, vip, e.segmentOf(e.plain), "nobody moves when the caller is among them")
	add(e.plain, operator2).failed(403, "permission_denied")
	add(e.plain, e.admin).failed(403, "permission_denied")
	add(e.plain, super).failed(403, "permission_denied")
	e.do(e.admin, "POST", path("/segments/%d/members", vip), map[string]any{"user_ids": []int64{super}}).failed(403, "permission_denied")
	require.NotEqual(t, vip, e.segmentOf(e.plain), "nobody moves when one may not be moved")
	require.Equal(t, float64(2), add(e.plain, supplier).ok(200).data()["moved"])

	personalize(operator).failed(403, "permission_denied")
	personalize(e.admin).failed(403, "permission_denied")
	personalize(super).failed(403, "permission_denied")
	personalize(e.plain).ok(201)

	e.personal(operator)
	e.personal(e.admin)
	depersonalize(operator).failed(403, "permission_denied")
	depersonalize(e.admin).failed(403, "permission_denied")
	depersonalize(e.plain).ok(204)
	require.Equal(t, int64(2), e.id(`SELECT count(*) FROM segments WHERE kind = 'personal'`))
}

// The super admin chooses its own user group like anyone else's, and each
// time leaves an audit row (FU6).
func TestSuperuserChoosesOwnSegment(t *testing.T) {
	e := setup(t)
	az := authz.New(authz.Deps{DB: e.db})
	require.NoError(t, az.SyncCore(context.Background()))
	e.svc.users = az
	testutil.AssignRoles(t, e.db, e.admin, authz.RoleSuperAdmin)
	vip := e.segment("VIP")
	e.do(e.admin, "POST", path("/segments/%d/members", vip), map[string]any{"user_ids": []int64{e.admin, e.plain}}).ok(200)
	require.Equal(t, vip, e.segmentOf(e.admin))
	e.do(e.admin, "POST", path("/users/%d/personal-segment", e.admin), map[string]any{}).ok(201)
	e.do(e.admin, "DELETE", path("/users/%d/personal-segment", e.admin), nil).ok(204)
	require.Equal(t, int64(3), e.id(`SELECT count(*) FROM audit_logs WHERE action = 'user.act_on_self' AND user_id = $1
		AND target_id = $1::text AND detail->>'self' = 'true' AND detail->>'via' = 'session'`, e.admin))
}

// A service built without a user guard refuses every change of membership.
func TestMembershipWithoutGuardIsRefused(t *testing.T) {
	e := setup(t)
	e.svc.users = New(Deps{}).users
	e.do(e.admin, "POST", path("/segments/%d/members", e.defaultSegment()), map[string]any{"user_ids": []int64{e.plain}}).failed(500, "internal")
	e.do(e.admin, "POST", path("/users/%d/personal-segment", e.plain), map[string]any{}).failed(500, "internal")
	e.do(e.admin, "DELETE", path("/users/%d/personal-segment", e.plain), nil).failed(500, "internal")
}

// ownFixture: operator (an operator) is in segment own; other is a segment
// nobody is in; grp is a public group.
type ownFixture struct {
	*env
	az                   *authz.Service
	operator, own, other int64
	grp                  int64
}

func newOwnFixture(t *testing.T) ownFixture {
	e := setup(t)
	az := authz.New(authz.Deps{DB: e.db})
	require.NoError(t, az.SyncCore(context.Background()))
	e.svc.users = az
	testutil.AssignRoles(t, e.db, e.masked, authz.RoleOperator)
	f := ownFixture{env: e, az: az, operator: e.masked, own: e.segment("Own"), other: e.segment("Other"), grp: e.group("g", "public", "1")}
	e.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, f.operator, f.own)
	return f
}

// grant grants segment grp as uid, through PATCH /segments/:id.
func (f ownFixture) grant(uid, segment int64) response {
	return f.do(uid, "PATCH", path("/segments/%d", segment), map[string]any{
		"group_rates": []map[string]any{{"group_id": f.grp, "granted": true, "rate": nil}}})
}

// modelRate sets a model rate of segment in grp as uid.
func (f ownFixture) modelRate(uid, segment int64) response {
	return f.do(uid, "PATCH", path("/segments/%d", segment), map[string]any{
		"model_rates": []map[string]any{{"group_id": f.grp, "model": "m", "rate": "0.5"}}})
}

// grantInGroup grants segment grp as uid, through segment_rates of the group.
func (f ownFixture) grantInGroup(uid, segment int64) error {
	fe, err := f.groupWrite(uid, f.grp, map[string]any{"segment_id": segment, "granted": true, "rate": nil})
	require.Empty(f.t, fe)
	return err
}

// segment:manage and group:manage change what a user group charges or
// grants only when the caller is not in it, a personal segment included
// (FU2).
func TestSettingsOfOwnSegment(t *testing.T) {
	f := newOwnFixture(t)
	f.grant(f.operator, f.own).failed(409, "conflict")
	f.modelRate(f.operator, f.own).failed(409, "conflict")
	var ce *core.Error
	require.ErrorAs(t, f.grantInGroup(f.operator, f.own), &ce)
	require.Equal(t, core.ErrConflict.Code, ce.Code)
	f.do(f.operator, "DELETE", path("/segments/%d", f.own), nil).failed(409, "conflict")
	require.Equal(t, int64(0), f.id(`SELECT count(*) FROM segment_groups WHERE segment_id = $1`, f.own), "a refused save writes nothing")
	require.Equal(t, int64(0), f.id(`SELECT count(*) FROM segment_model_rates WHERE segment_id = $1`, f.own))
	// Renaming is no change of terms; reads are not either.
	f.do(f.operator, "PATCH", path("/segments/%d", f.own), map[string]any{"name": "Mine"}).ok(200)
	f.do(f.operator, "GET", path("/segments/%d/groups", f.own), nil).ok(200)

	personal := f.personal(f.operator)
	f.grant(f.operator, personal).failed(409, "conflict")
	f.modelRate(f.operator, personal).failed(409, "conflict")
}

// Segments the caller is not in are fine, and so is the default segment,
// which everyone is in.
func TestSettingsOfOtherSegments(t *testing.T) {
	f := newOwnFixture(t)
	f.grant(f.operator, f.other).ok(200)
	f.modelRate(f.operator, f.other).ok(200)
	require.NoError(t, f.grantInGroup(f.operator, f.other))
	// admin is in the default segment and no superuser: it is exempt.
	require.Equal(t, f.defaultSegment(), f.segmentOf(f.admin))
	f.modelRate(f.admin, f.defaultSegment()).ok(200)
	require.NoError(t, f.grantInGroup(f.admin, f.defaultSegment()))
}

// A superuser changes its own user group, audited.
func TestSuperuserSetsOwnSegment(t *testing.T) {
	f := newOwnFixture(t)
	testutil.AssignRoles(t, f.db, f.admin, authz.RoleSuperAdmin)
	f.exec(`UPDATE users SET segment_id = $2 WHERE id = $1`, f.admin, f.other)
	f.grant(f.admin, f.other).ok(200)
	require.NoError(t, f.grantInGroup(f.admin, f.other))
	require.Equal(t, int64(2), f.id(`SELECT count(*) FROM audit_logs WHERE action = 'user.act_on_self' AND user_id = $1`, f.admin))
}
