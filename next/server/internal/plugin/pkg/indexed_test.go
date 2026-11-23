package pkg

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// unsignedPackage opens an unsigned package of key 0.1.0 by publisher.
func unsignedPackage(t *testing.T, key, publisher string) *Package {
	t.Helper()
	p, err := Open(pkgtest.Zip(pkgtest.Files(pkgtest.Minimal(key, "0.1.0", publisher))), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// wantCode fails unless err is a core error with code and, when reason is
// set, details.reason.
func wantCode(t *testing.T, what string, err error, code, reason string) {
	t.Helper()
	e := core.AsError(err)
	if e == nil || e.Code != code || reason != "" && e.Details["reason"] != reason {
		t.Fatalf("%s: %v", what, err)
	}
}

// A market install's trust is the index's: the signature is not read (the
// trust store allows no unsigned package), the publisher must be listed
// active, be the manifest's and not be revoked here.
func TestVerifyIndexed(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	ts, _ := NewTrustStore(nil, false)
	p := unsignedPackage(t, "tool", "acme")
	acme := IndexedPublisher{Name: "acme", Trust: TrustVerified, Status: PublisherActive}
	v, err := ts.VerifyIndexed(ctx, db.Gorm, p, acme, false)
	if err != nil || v.Trust != TrustVerified || v.SignatureStatus != SigIndexed || v.PublisherID == nil || *v.PublisherID != 0 {
		t.Fatalf("preview = %+v %v", v, err)
	}
	_, err = ts.VerifyIndexed(ctx, db.Gorm, p, IndexedPublisher{Name: "acme", Trust: TrustOfficial, Status: PublisherRevoked}, true)
	wantCode(t, "revoked in the index", err, "permission_denied", "revoked")
	_, err = ts.VerifyIndexed(ctx, db.Gorm, p, IndexedPublisher{Name: "acme"}, true)
	wantCode(t, "not in the index", err, "permission_denied", "publisher_unlisted")
	_, err = ts.VerifyIndexed(ctx, db.Gorm, p, IndexedPublisher{Name: "other", Trust: TrustOfficial, Status: PublisherActive}, true)
	wantCode(t, "another publisher", err, "invalid_argument", "")
	checkIndexedRegistration(t, db, ts, p, acme)
}

// checkIndexedRegistration: an upload registers the publisher with the
// index's trust level and later ones set it; a publisher revoked here is
// refused.
func checkIndexedRegistration(t *testing.T, db *store.DB, ts *TrustStore, p *Package, acme IndexedPublisher) {
	ctx := context.Background()
	v, err := ts.VerifyIndexed(ctx, db.Gorm, p, acme, true)
	if err != nil || v.PublisherID == nil || *v.PublisherID == 0 {
		t.Fatalf("upload = %+v %v", v, err)
	}
	acme.Trust = TrustCommunity
	if _, err := ts.VerifyIndexed(ctx, db.Gorm, p, acme, true); err != nil {
		t.Fatal(err)
	}
	var trust string
	if err := db.SQL.QueryRowContext(ctx, `SELECT trust_level FROM publishers WHERE name = 'acme'`).Scan(&trust); err != nil || trust != TrustCommunity {
		t.Fatalf("trust_level = %q %v", trust, err)
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE publishers SET status = 'revoked' WHERE name = 'acme'`); err != nil {
		t.Fatal(err)
	}
	_, err = ts.VerifyIndexed(ctx, db.Gorm, p, acme, false)
	wantCode(t, "revoked here", err, "permission_denied", "revoked")
}

// A node loads a market version without reading its signature while its
// publisher is active; a revoked version, or one of a revoked publisher,
// does not load; a version not recorded as indexed is signature-checked.
func TestVerifyRecorded(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	ts, _ := NewTrustStore(nil, false)
	p := unsignedPackage(t, "tool", "acme")
	if err := ts.VerifyRecorded(ctx, db.Gorm, p); err == nil {
		t.Fatal("unrecorded unsigned package loaded")
	}
	exec := func(q string) {
		t.Helper()
		if _, err := db.SQL.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO publishers (name, trust_level) VALUES ('acme', 'verified')`)
	exec(`INSERT INTO plugins (key, name, publisher_id, status) SELECT 'tool', '{}', id, 'installed' FROM publishers WHERE name = 'acme'`)
	exec(`INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, publisher_id, signature_status)
		SELECT 'tool', '0.1.0', '{}', 'h', 's', 1, id, 'indexed' FROM publishers WHERE name = 'acme'`)
	if err := ts.VerifyRecorded(ctx, db.Gorm, p); err != nil {
		t.Fatalf("indexed version: %v", err)
	}
	exec(`UPDATE publishers SET status = 'revoked'`)
	wantCode(t, "revoked publisher", ts.VerifyRecorded(ctx, db.Gorm, p), "permission_denied", "revoked")
	exec(`UPDATE publishers SET status = 'active'`)
	exec(`UPDATE plugin_versions SET signature_status = 'revoked'`)
	wantCode(t, "revoked version", ts.VerifyRecorded(ctx, db.Gorm, p), "permission_denied", "revoked")
}
