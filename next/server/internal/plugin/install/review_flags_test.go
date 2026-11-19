package install

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

// The review says beforehand what the consent will ask for db.schema
// (needs_db_schema_unconfined, the same dbSchemaUnconfined the consent
// checks), and which user permissions the consent creates (new).
func TestReviewConsentFlags(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := pkgtest.Guard("guard", "0.1.0", "sub2api")
	if _, err := e.svc.Upload(ctx, pkgtest.Build(m, e.root), e.admin, UploadOptions{Source: "upload"}); err != nil {
		t.Fatal(err)
	}
	review := func() *Review {
		t.Helper()
		r, err := e.svc.Review(ctx, "guard", "0.1.0", e.admin)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// Official publisher: no override needed.
	if r := review(); r.NeedsDBSchemaUnconfined {
		t.Fatal("official plugin flagged")
	}
	// The catalog has one of the two permissions already: only the other is new.
	if _, err := e.db.SQL.ExecContext(ctx, `INSERT INTO permissions (key, module, label, source, plugin_key, status)
VALUES ('plugin.guard:`+m.UserPermissions[0].Key+`', 'plugin.guard', '{"en":"x"}', 'plugin', 'guard', 'active')`); err != nil {
		t.Fatal(err)
	}
	if r := review(); r.UserPermissions[0].New || !r.UserPermissions[1].New {
		t.Fatalf("new flags = %+v", r.UserPermissions)
	}

	// A verified (not official) publisher: flagged, and the consent of a
	// caller without the override is refused.
	if _, err := e.db.SQL.ExecContext(ctx, `UPDATE publishers SET trust_level = 'verified'`); err != nil {
		t.Fatal(err)
	}
	if r := review(); !r.NeedsDBSchemaUnconfined {
		t.Fatal("verified plugin not flagged")
	}
	_, err := e.svc.Consent(ctx, "guard", "0.1.0", ConsentRequest{Grants: explicitGuardGrants()}, e.admin)
	if core.AsError(err).Code != core.ErrPermissionDenied.Code {
		t.Fatalf("consent without the override: %v", err)
	}
}
