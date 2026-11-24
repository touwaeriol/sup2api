package install

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// revokedEnv runs one plugin ("mini") through versions that ask for the
// low-risk kv (required), when withLog, log (optional), and the high-risk
// net (optional) for the domains net names for the version.
type revokedEnv struct {
	*env
	t   *testing.T
	net map[string][]any
}

func (e revokedEnv) manifest(version string, withLog bool) *manifest.Manifest {
	m := pkgtest.Minimal("mini", version, "sub2api")
	if withLog {
		m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "log", Optional: true})
	}
	if d, ok := e.net[version]; ok {
		m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "net", Scope: map[string]any{"domains": d}, Optional: true})
	}
	return m
}

func (e revokedEnv) upload(version string, withLog bool) *Review {
	e.t.Helper()
	r, err := e.svc.Upload(context.Background(), pkgtest.Build(e.manifest(version, withLog), e.root), e.admin, UploadOptions{})
	if err != nil {
		e.t.Fatalf("upload %s: %v", version, err)
	}
	return r
}

func (e revokedEnv) consent(version string, req ConsentRequest) error {
	_, err := e.svc.Consent(context.Background(), "mini", version, req, e.admin)
	return err
}

// activate is the rollout commit of version.
func (e revokedEnv) activate(version string, withLog bool) {
	e.t.Helper()
	ctx := context.Background()
	if err := e.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := grants.Promote(ctx, tx, "mini", version); err != nil {
			return err
		}
		if _, err := store.SQLTx(tx).ExecContext(ctx, `UPDATE plugins SET status = 'enabled', active_version = $1 WHERE key = 'mini'`, version); err != nil {
			return err
		}
		return NewDefaultsApplier(nil).ApplyDefaults(ctx, tx, e.manifest(version, withLog), nil)
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e revokedEnv) statuses(version string) map[string]string {
	e.t.Helper()
	g, err := LoadVersionGrants(context.Background(), e.db.Gorm, "mini", version)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for id, gr := range g {
		out[id] = gr.Status
	}
	return out
}

func revokedOf(r *Review) map[string]bool {
	out := map[string]bool{}
	for _, hp := range r.HostPermissions {
		if hp.Revoked {
			out[hp.ID] = true
		}
	}
	return out
}

// A low-risk host permission an administrator revoked is not granted again
// by default in a later consent: only an explicit grant restores it. A
// required one must then be granted explicitly; an optional one the request
// does not grant stays revoked (also when it is listed as denied, as the
// console sends it), in the staged set and after the activation, also
// across a version that no longer asks for it and an abandoned upgrade.
func TestRevokedLowRiskGrantIsNotGrantedByDefault(t *testing.T) {
	e := revokedEnv{env: newEnv(t), t: t}
	ctx := context.Background()

	e.upload("0.1.0", true)
	if err := e.consent("0.1.0", ConsentRequest{}); err != nil {
		t.Fatalf("first consent: %v", err)
	}
	if s := e.statuses("0.1.0"); s["kv"] != GrantGranted || s["log"] != GrantGranted {
		t.Fatalf("low-risk permissions are granted by default on install: %v", s)
	}
	e.activate("0.1.0", true)
	for _, p := range []string{"kv", "log"} {
		if err := e.svc.RevokeGrant(ctx, "mini", p, e.admin); err != nil {
			t.Fatalf("revoke %s: %v", p, err)
		}
	}

	// The upgrade is not approved on upload (the revoked ones count as added)
	// and its review marks them.
	r := e.upload("0.2.0", true)
	if r.ConsentStatus != ConsentAwaiting {
		t.Fatalf("upgrade after a revocation approved on upload: %s", r.ConsentStatus)
	}
	if rv := revokedOf(r); !rv["kv"] || !rv["log"] {
		t.Fatalf("review of 0.2.0 does not mark the revoked permissions: %+v", r.HostPermissions)
	}
	if rr, err := e.svc.Review(ctx, "mini", "0.2.0", e.admin); err != nil || !revokedOf(rr)["kv"] {
		t.Fatalf("stored review of 0.2.0: %v %+v", err, rr)
	}

	// kv is required: a consent not naming it fails, the version waits.
	if err := e.consent("0.2.0", ConsentRequest{}); fieldCode(err, "permission:kv") != "consent_required" {
		t.Fatalf("consent to 0.2.0 not naming the revoked kv: %v (staged %v)", err, e.statuses("0.2.0"))
	}
	// Naming kv restores it; log (optional, listed as denied as the console
	// sends an unticked one) stays revoked.
	if err := e.consent("0.2.0", ConsentRequest{Grants: []GrantInput{{Permission: "kv"}}, Denied: []string{"log"}}); err != nil {
		t.Fatalf("consent to 0.2.0 naming kv: %v", err)
	}
	if s := e.statuses("0.2.0"); s["kv"] != GrantGranted || s["log"] != GrantRevoked {
		t.Fatalf("staged grants of 0.2.0: %v", s)
	}
	var carried string
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT d->>'carried' FROM audit_logs, jsonb_array_elements(detail->'decisions') d
		WHERE action = 'plugin.consent' AND target_id = 'mini' AND detail->>'version' = '0.2.0' AND d->>'permission' = 'kv'`).Scan(&carried); err != nil || carried != "false" {
		t.Fatalf("kv decision audit: %q %v", carried, err)
	}
	e.activate("0.2.0", true)

	// A version that no longer asks for log keeps its revocation; the one
	// after asks again and does not get it by default. kv, restored, is
	// carried over on upload.
	if r := e.upload("0.3.0", false); r.ConsentStatus != ConsentApproved {
		t.Fatalf("0.3.0 (kv restored, log dropped) not approved on upload: %s", r.ConsentStatus)
	}
	e.activate("0.3.0", false)
	if g, _ := LoadGrants(ctx, e.db.Gorm, "mini"); g["log"].Status != GrantRevoked {
		t.Fatalf("log after a version without it: %+v", g["log"])
	}
	e.upload("0.4.0", true)
	if err := e.consent("0.4.0", ConsentRequest{}); err != nil {
		t.Fatalf("consent to 0.4.0: %v", err)
	}
	if s := e.statuses("0.4.0"); s["kv"] != GrantGranted || s["log"] != GrantRevoked {
		t.Fatalf("staged grants of 0.4.0: %v", s)
	}

	// Granting log explicitly restores it for later consents too.
	e.upload("0.5.0", true)
	if err := e.consent("0.5.0", ConsentRequest{Grants: []GrantInput{{Permission: "log"}}}); err != nil {
		t.Fatalf("consent to 0.5.0: %v", err)
	}
	e.upload("0.6.0", true)
	if err := e.consent("0.6.0", ConsentRequest{}); err != nil {
		t.Fatalf("consent to 0.6.0: %v", err)
	}
	if s := e.statuses("0.6.0"); s["log"] != GrantGranted {
		t.Fatalf("log after its explicit grant to 0.5.0: %v", s)
	}
}

// A revocation made only in a staged set (the running version does not ask
// for the permission) survives the withdrawal of that set when the upgrade
// is abandoned: consenting to the version again does not grant it by
// default.
func TestRevocationOutlivesAWithdrawnStagedSet(t *testing.T) {
	e := revokedEnv{env: newEnv(t), t: t}
	ctx := context.Background()
	e.upload("0.1.0", false)
	if err := e.consent("0.1.0", ConsentRequest{}); err != nil {
		t.Fatal(err)
	}
	e.activate("0.1.0", false)
	e.upload("0.2.0", true)
	if err := e.consent("0.2.0", ConsentRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeGrant(ctx, "mini", "log", e.admin); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Tx(ctx, func(tx *gorm.DB) error { return grants.Withdraw(ctx, tx, "mini", "0.2.0", "failed") }); err != nil {
		t.Fatal(err)
	}
	if err := e.consent("0.2.0", ConsentRequest{}); err != nil {
		t.Fatalf("consent to 0.2.0 again: %v", err)
	}
	if s := e.statuses("0.2.0"); s["kv"] != GrantGranted || s["log"] != GrantRevoked {
		t.Fatalf("staged grants of 0.2.0 after the withdrawal: %v", s)
	}
	// The running version, which does not ask for log, is not given it.
	if g, err := grants.Granted(ctx, e.db.Gorm, "mini", "0.1.0", e.manifest("0.1.0", false).HostPermissions); err != nil || len(g) != 1 {
		t.Fatalf("0.1.0 runs with %v (%v)", g, err)
	}
}

// decide on revoked permissions, by risk: a revoked one is granted only when
// named (never carried: the grant rights apply), a required one not named
// fails, an optional one stays revoked; without a revocation the risk rules
// are unchanged.
func TestDecideRevokedPermissions(t *testing.T) {
	m := &manifest.Manifest{Publisher: manifest.OfficialPublisher, Key: "mini", HostPermissions: []manifest.HostPermission{
		{ID: "kv"}, {ID: "log", Optional: true}, {ID: "db.schema"}, {ID: "net", Optional: true}}}
	all := map[string]bool{"kv": true, "log": true, "db.schema": true, "net": true}
	current := map[string]Grant{}
	for id := range all {
		current[id] = Grant{Permission: id, Status: GrantRevoked}
	}
	outcome := func(ds []decision, errs []core.FieldError) string {
		out := map[string]string{}
		for _, d := range ds {
			out[d.permission] = fmt.Sprintf("%s/%v", d.status, d.carried)
		}
		for _, e := range errs {
			out[strings.TrimPrefix(e.Field, "permission:")] = e.Code
		}
		return fmt.Sprint(out)
	}
	for _, c := range []struct {
		name    string
		req     ConsentRequest
		revoked map[string]bool
		want    string
	}{
		{"nothing named", ConsentRequest{}, all,
			"map[db.schema:consent_required kv:consent_required log:revoked/false net:revoked/false]"},
		{"required named, optional denied", ConsentRequest{Grants: []GrantInput{{Permission: "kv"}, {Permission: "db.schema"}}, Denied: []string{"log", "net"}}, all,
			"map[db.schema:granted/false kv:granted/false log:revoked/false net:revoked/false]"},
		{"everything named", ConsentRequest{Grants: []GrantInput{{Permission: "kv"}, {Permission: "log"}, {Permission: "db.schema"}, {Permission: "net"}}}, all,
			"map[db.schema:granted/false kv:granted/false log:granted/false net:granted/false]"},
		{"required denied", ConsentRequest{Grants: []GrantInput{{Permission: "db.schema"}}, Denied: []string{"kv"}}, all,
			"map[db.schema:granted/false kv:required log:revoked/false net:revoked/false]"},
		{"no revocation", ConsentRequest{}, nil,
			"map[db.schema:consent_required kv:granted/false log:granted/false net:denied/false]"},
	} {
		if got := outcome(decide(m, c.req, current, c.revoked, true)); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// An emergency revocation made while an upgrade waits (staged) reaches the
// staged set too, so activating that version keeps it revoked, and the
// consent to the next version does not grant it by default.
func TestRevocationDuringAStagedUpgradeOutlivesItsActivation(t *testing.T) {
	e := revokedEnv{env: newEnv(t), t: t}
	ctx := context.Background()
	e.upload("0.1.0", true)
	if err := e.consent("0.1.0", ConsentRequest{}); err != nil {
		t.Fatal(err)
	}
	e.activate("0.1.0", true)
	if r := e.upload("0.2.0", true); r.ConsentStatus != ConsentApproved { // carried over, staged
		t.Fatalf("0.2.0 not approved on upload: %s", r.ConsentStatus)
	}
	if err := e.svc.RevokeGrant(ctx, "mini", "log", e.admin); err != nil {
		t.Fatal(err)
	}
	if s := e.statuses("0.2.0"); s["log"] != GrantRevoked {
		t.Fatalf("staged log after the revocation: %v", s)
	}
	e.activate("0.2.0", true)
	e.upload("0.3.0", true)
	if err := e.consent("0.3.0", ConsentRequest{}); err != nil {
		t.Fatal(err)
	}
	if s := e.statuses("0.3.0"); s["kv"] != GrantGranted || s["log"] != GrantRevoked {
		t.Fatalf("staged grants of 0.3.0: %v", s)
	}
}

// SetGrant narrows a host permission grant with plugin:manage alone, and
// grants a revoked one again as a consent does (plugin:install and the
// grant rights by risk),
// never wider than the active version requests. Staged sets follow:
// narrowed where wider, a revocation lifted where the version requests at
// least the scope; a set granting a scope neither wider nor within it, or a
// version requesting less, keeps its own decision.
func TestSetGrantNarrowsAndRegrants(t *testing.T) {
	const a, b, c = "a.example.com", "b.example.com", "c.example.com"
	e := revokedEnv{env: newEnv(t), t: t, net: map[string][]any{"0.1.0": {a, b}, "0.2.0": {a, b}, "0.3.0": {b}}}
	ctx := context.Background()
	domains := func(d ...any) map[string]any { return map[string]any{"domains": d} }
	grant := func(version string) Grant {
		t.Helper()
		g, err := LoadVersionGrants(ctx, e.db.Gorm, "mini", version)
		require.NoError(t, err)
		return g["net"]
	}
	audited := func(action string) int {
		var n int
		require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action = $1 AND target_id = 'mini'`, action).Scan(&n))
		return n
	}

	e.upload("0.1.0", true)
	require.NoError(t, e.consent("0.1.0", ConsentRequest{Grants: []GrantInput{{Permission: "net"}}}))
	e.activate("0.1.0", true)
	// Both upgrades are carried over on upload and staged.
	for _, v := range []string{"0.2.0", "0.3.0"} {
		require.Equal(t, ConsentApproved, e.upload(v, true).ConsentStatus, v)
	}
	require.Equal(t, domains(b), grant("0.3.0").Scope)

	// Narrowing takes no grant right; 0.2.0 (wider) follows, 0.3.0 ([b],
	// neither wider nor within [a]) keeps its own grant.
	e.authz.perms[e.limited] = map[string]bool{}
	g, err := e.svc.SetGrant(ctx, "mini", "net", domains(a), e.limited)
	require.NoError(t, err)
	assert.Equal(t, GrantGranted, g.Status)
	assert.Equal(t, domains(a), g.Scope)
	assert.Equal(t, domains(a), grant("0.2.0").Scope)
	assert.Equal(t, GrantGranted, grant("0.3.0").Status)
	assert.Equal(t, domains(b), grant("0.3.0").Scope)
	assert.Equal(t, 1, audited("plugin.grant.narrow"))

	// Wider than requested: 400; not requested: 404; back to the requested
	// scope is a grant decision: plugin:install, then plugin:grant:high
	// for this high-risk one.
	_, err = e.svc.SetGrant(ctx, "mini", "net", domains(a, c), e.admin)
	assert.Equal(t, "scope_too_wide", fieldCode(err, "scope"))
	_, err = e.svc.SetGrant(ctx, "mini", "users.read", nil, e.admin)
	assert.Equal(t, "not_found", core.AsError(err).Code)
	_, err = e.svc.SetGrant(ctx, "mini", "net", nil, e.limited)
	assert.Equal(t, "permission_denied", core.AsError(err).Code)
	assert.Equal(t, "plugin:install", core.AsError(err).Details["permission"])
	e.authz.perms[e.limited] = map[string]bool{"plugin:install": true}
	_, err = e.svc.SetGrant(ctx, "mini", "net", nil, e.limited)
	assert.Equal(t, "permission_denied", core.AsError(err).Code)
	assert.Equal(t, PermGrantHigh, core.AsError(err).Details["permission"])

	// A revocation reaches every staged set; granting again lifts it where
	// the version requests [a] (0.2.0), not where it requests less (0.3.0).
	require.NoError(t, e.svc.RevokeGrant(ctx, "mini", "net", e.admin))
	e.authz.perms[e.limited] = map[string]bool{"plugin:install": true, PermGrantHigh: true}
	g, err = e.svc.SetGrant(ctx, "mini", "net", domains(a), e.limited)
	require.NoError(t, err)
	assert.Equal(t, GrantGranted, g.Status)
	assert.Equal(t, domains(a), g.Scope)
	assert.Equal(t, GrantGranted, grant("0.2.0").Status)
	assert.Equal(t, domains(a), grant("0.2.0").Scope)
	assert.Equal(t, GrantRevoked, grant("0.3.0").Status)
	revoked, err := grants.Revoked(ctx, e.db.Gorm, "mini")
	require.NoError(t, err)
	assert.False(t, revoked["net"])
	assert.Equal(t, 1, audited("plugin.grant.regrant"))

	// A low-risk one takes no grant right, but still plugin:install:
	// plugin:manage alone does not undo a revocation.
	require.NoError(t, e.svc.RevokeGrant(ctx, "mini", "log", e.admin))
	e.authz.perms[e.limited] = map[string]bool{}
	_, err = e.svc.SetGrant(ctx, "mini", "log", nil, e.limited)
	assert.Equal(t, "plugin:install", core.AsError(err).Details["permission"])
	current, err := LoadGrants(ctx, e.db.Gorm, "mini")
	require.NoError(t, err)
	assert.Equal(t, GrantRevoked, current["log"].Status)
	e.authz.perms[e.limited] = map[string]bool{"plugin:install": true}
	g, err = e.svc.SetGrant(ctx, "mini", "log", nil, e.limited)
	require.NoError(t, err)
	assert.Equal(t, GrantGranted, g.Status)

	// A staged version whose requested scope does not decode fails the
	// grant instead of counting as requesting none.
	_, err = e.db.SQL.ExecContext(ctx, `UPDATE plugin_versions SET manifest = jsonb_set(manifest, '{hostPermissions}',
		(SELECT jsonb_agg(CASE WHEN hp->>'id' = 'net' THEN hp || '{"scope":"x"}' ELSE hp END) FROM jsonb_array_elements(manifest->'hostPermissions') hp))
		WHERE plugin_key = 'mini' AND version = '0.3.0'`)
	require.NoError(t, err)
	require.NoError(t, e.svc.RevokeGrant(ctx, "mini", "net", e.admin))
	e.authz.perms[e.limited] = map[string]bool{"plugin:install": true, PermGrantHigh: true}
	_, err = e.svc.SetGrant(ctx, "mini", "net", domains(a), e.limited)
	require.ErrorContains(t, err, "mini@0.3.0")
	assert.Equal(t, GrantRevoked, grant("0.3.0").Status)
}
