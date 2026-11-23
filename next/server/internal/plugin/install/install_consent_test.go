package install

import (
	"context"
	"slices"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// rowCounts are the rows an install writes, and the publisher an official
// package registers.
type rowCounts struct {
	Plugins, Versions, Audit, Publishers, Keys, Grants int
}

func countRows(t *testing.T, db *store.DB) rowCounts {
	t.Helper()
	var c rowCounts
	err := db.SQL.QueryRowContext(context.Background(), `SELECT
		(SELECT count(*) FROM plugins), (SELECT count(*) FROM plugin_versions), (SELECT count(*) FROM audit_logs),
		(SELECT count(*) FROM publishers), (SELECT count(*) FROM publisher_keys), (SELECT count(*) FROM plugin_permission_grants)`).
		Scan(&c.Plugins, &c.Versions, &c.Audit, &c.Publishers, &c.Keys, &c.Grants)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// versionStatus is the consent status of key@version, "" when there is no
// such row.
func versionStatus(t *testing.T, db *store.DB, key, version string) string {
	t.Helper()
	var status string
	_ = db.SQL.QueryRowContext(context.Background(), `SELECT consent_status FROM plugin_versions WHERE plugin_key = $1 AND version = $2`, key, version).Scan(&status)
	return status
}

// guardUpgrade is guard 0.2.0 asking for one more host permission (log).
func guardUpgrade() *manifest.Manifest {
	up := pkgtest.Guard("guard", "0.2.0", "sub2api")
	up.HostPermissions = append(up.HostPermissions, manifest.HostPermission{ID: "log"})
	return up
}

// installGuard installs and enables guard 0.1.0 with consent.
func installGuard(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	data := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), e.root)
	res, err := e.svc.InstallWithConsent(ctx, data, e.admin, UploadOptions{Source: "upload"},
		InstallConsent{ConsentRequest: ConsentRequest{Grants: explicitGuardGrants()}, PackageSHA256: pkg.SHA256Hex(data)})
	if err != nil {
		t.Fatalf("install with consent: %v", err)
	}
	if res.Review.ConsentStatus != ConsentApproved || res.Consent.Status != StatusInstalled || res.Consent.Upgrade {
		t.Fatalf("install = %+v %+v", res.Review, res.Consent)
	}
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE plugins SET status = 'enabled', active_version = '0.1.0' WHERE key = 'guard'`); err != nil {
		t.Fatal(err)
	}
}

// The install preview of a first install reviews the package as an upload
// would and writes nothing: no row (not even the official publisher a
// first upload registers), no audit entry, nothing stored.
func TestReviewPackageWritesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	data := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), e.root)
	before := countRows(t, e.db)
	r, err := e.svc.ReviewPackage(ctx, data, e.admin, UploadOptions{Source: "upload"})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if r.Trust != pkg.TrustOfficial || r.ConsentStatus != "" || r.Diff != nil || r.UpgradeFrom != "" ||
		r.PackageSHA256 != pkg.SHA256Hex(data) || !r.UploadedAt.IsZero() || len(r.HostPermissions) != 9 || len(r.RoleColumns) == 0 {
		t.Fatalf("review = %+v", r)
	}
	if after := countRows(t, e.db); after != before {
		t.Fatalf("rows %+v, before %+v", after, before)
	}
	if ok, err := e.svc.d.Packages.Has(ctx, r.PackageSHA256); err != nil || ok {
		t.Fatalf("package stored: %v %v", ok, err)
	}
}

// The install preview of an upgrade compares it with the running version
// and writes nothing either.
func TestReviewPackageUpgrade(t *testing.T) {
	e := newEnv(t)
	installGuard(t, e)
	before := countRows(t, e.db)
	r, err := e.svc.ReviewPackage(context.Background(), pkgtest.Build(guardUpgrade(), e.root), e.admin, UploadOptions{Source: "upload"})
	if err != nil {
		t.Fatalf("review upgrade: %v", err)
	}
	if r.UpgradeFrom != "0.1.0" || r.Diff == nil || !slices.ContainsFunc(r.Diff.Added, func(d DiffItem) bool { return d.ID == "log" }) ||
		r.ConsentStatus != "" || r.HostPermissions[0].CurrentStatus != GrantGranted {
		t.Fatalf("upgrade review = %+v", r)
	}
	if after := countRows(t, e.db); after != before {
		t.Fatalf("rows %+v, before %+v", after, before)
	}
}

// The preview fails as the upload would: a retired key (checked before the
// official publisher is registered, which the upload would register with
// an id no retirement holds), another package of an uploaded version.
func TestReviewPackageFailsAsUpload(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.SQL.ExecContext(ctx, `INSERT INTO plugin_key_retirements (plugin_key, publisher_id) VALUES ('tool', NULL)`); err != nil {
		t.Fatal(err)
	}
	tool := pkgtest.Build(pkgtest.Guard("tool", "1.0.0", "sub2api"), e.root)
	if _, err := e.svc.ReviewPackage(ctx, tool, e.admin, UploadOptions{}); core.AsError(err).Code != ErrKeyRetired.Code {
		t.Fatalf("retired key: %v", err)
	}
	installGuard(t, e)
	changed := pkgtest.Guard("guard", "0.1.0", "sub2api")
	changed.Description = manifest.LocalizedText{"en": "changed"}
	if _, err := e.svc.ReviewPackage(ctx, pkgtest.Build(changed, e.root), e.admin, UploadOptions{}); core.AsError(err).Code != "conflict" {
		t.Fatalf("other content: %v", err)
	}
}

// An install with consent records and approves the version in one call.
// Without consent an upload still waits for it.
func TestInstallWithConsent(t *testing.T) {
	e := newEnv(t)
	installGuard(t, e)
	if s := versionStatus(t, e.db, "guard", "0.1.0"); s != ConsentApproved {
		t.Fatalf("version = %q", s)
	}
	r, err := e.svc.Upload(context.Background(), pkgtest.Build(guardUpgrade(), e.root), e.admin, UploadOptions{})
	if err != nil || r.ConsentStatus != ConsentAwaiting || r.UpgradeFrom != "0.1.0" {
		t.Fatalf("upload without consent = %+v %v", r, err)
	}
}

// A package other than the one reviewed is refused before anything is
// written.
func TestInstallWithConsentPackageChanged(t *testing.T) {
	e := newEnv(t)
	data := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), e.root)
	before := countRows(t, e.db)
	consent := InstallConsent{ConsentRequest: ConsentRequest{Grants: explicitGuardGrants()}, PackageSHA256: pkg.SHA256Hex([]byte("other"))}
	_, err := e.svc.InstallWithConsent(context.Background(), data, e.admin, UploadOptions{}, consent)
	if ce := core.AsError(err); ce.Code != ErrPackageChanged.Code || ce.Status != 409 {
		t.Fatalf("changed package: %v", err)
	}
	consent.PackageSHA256 = ""
	if _, err := e.svc.InstallWithConsent(context.Background(), data, e.admin, UploadOptions{}, consent); fieldCode(err, "consent.package_sha256") != "required" {
		t.Fatalf("no sha: %v", err)
	}
	if after := countRows(t, e.db); after != before {
		t.Fatalf("rows %+v, before %+v", after, before)
	}
}

// A consent that fails undoes a first install: no plugin or version row is
// left, and the plugin's permissions are deleted as on a reject.
func TestInstallWithConsentUndo(t *testing.T) {
	e := newEnv(t)
	data := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), e.root)
	_, err := e.svc.InstallWithConsent(context.Background(), data, e.admin, UploadOptions{}, InstallConsent{PackageSHA256: pkg.SHA256Hex(data)})
	if fieldCode(err, "permission:gateway.hook") != "consent_required" {
		t.Fatalf("first install without grants: %v", err)
	}
	if c := countRows(t, e.db); c.Plugins != 0 || c.Versions != 0 {
		t.Fatalf("rows left: %+v", c)
	}
	if len(e.perms.deleted) != 1 || e.perms.deleted[0] != "guard" || undoAudits(t, e.db) != 1 {
		t.Fatalf("permissions deleted %v, undo audits %d", e.perms.deleted, undoAudits(t, e.db))
	}
}

// A consent that fails undoes an upgrade: the new version row goes, the
// running version and the plugin stay as they were.
func TestInstallWithConsentUndoUpgrade(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	installGuard(t, e)
	up := pkgtest.Build(guardUpgrade(), e.root)
	denied := InstallConsent{ConsentRequest: ConsentRequest{Grants: explicitGuardGrants()[1:], Denied: []string{"db.schema"}}, PackageSHA256: pkg.SHA256Hex(up)}
	if _, err := e.svc.InstallWithConsent(ctx, up, e.admin, UploadOptions{}, denied); fieldCode(err, "permission:db.schema") != "required" {
		t.Fatalf("upgrade denying a required permission: %v", err)
	}
	var status, active string
	if err := e.db.SQL.QueryRowContext(ctx, `SELECT status, active_version FROM plugins WHERE key = 'guard'`).Scan(&status, &active); err != nil {
		t.Fatal(err)
	}
	if status != StatusEnabled || active != "0.1.0" || versionStatus(t, e.db, "guard", "0.1.0") != ConsentApproved ||
		versionStatus(t, e.db, "guard", "0.2.0") != "" || undoAudits(t, e.db) != 1 {
		t.Fatalf("after a failed upgrade: %s %s, 0.2.0 %q", status, active, versionStatus(t, e.db, "guard", "0.2.0"))
	}
}

// undoAudits counts the plugin.upload.undo audit entries.
func undoAudits(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_logs WHERE action = 'plugin.upload.undo'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The preview of a package already stored and approved says so: installing
// it again takes no consent (the console starts the rollout directly).
func TestReviewPackageReportsApproved(t *testing.T) {
	e := newEnv(t)
	installGuard(t, e)
	data := pkgtest.Build(pkgtest.Guard("guard", "0.1.0", "sub2api"), e.root)
	r, err := e.svc.ReviewPackage(context.Background(), data, e.admin, UploadOptions{Source: "upload"})
	if err != nil || r.ConsentStatus != ConsentApproved {
		t.Fatalf("review = %+v, %v", r, err)
	}
}
