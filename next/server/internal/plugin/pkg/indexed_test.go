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

// checkIndexedRegistration: an upload registers the publisher as
// community, whatever level the index gives (that level is the version's),
// and never changes a registered one; a publisher revoked here is refused.
func checkIndexedRegistration(t *testing.T, db *store.DB, ts *TrustStore, p *Package, acme IndexedPublisher) {
	ctx := context.Background()
	v, err := ts.VerifyIndexed(ctx, db.Gorm, p, acme, true)
	if err != nil || v.PublisherID == nil || *v.PublisherID == 0 || v.Trust != TrustVerified {
		t.Fatalf("upload = %+v %v", v, err)
	}
	wantPublisherTrust(t, db, "acme", TrustCommunity)
	execSQL(t, db, `UPDATE publishers SET trust_level = 'official' WHERE name = 'acme'`)
	if _, err := ts.VerifyIndexed(ctx, db.Gorm, p, acme, true); err != nil {
		t.Fatal(err)
	}
	wantPublisherTrust(t, db, "acme", TrustOfficial)
	execSQL(t, db, `UPDATE publishers SET status = 'revoked' WHERE name = 'acme'`)
	_, err = ts.VerifyIndexed(ctx, db.Gorm, p, acme, false)
	wantCode(t, "revoked here", err, "permission_denied", "revoked")
}

func wantPublisherTrust(t *testing.T, db *store.DB, name, want string) {
	t.Helper()
	var trust string
	err := db.SQL.QueryRowContext(context.Background(), `SELECT trust_level FROM publishers WHERE name = $1`, name).Scan(&trust)
	if err != nil || trust != want {
		t.Fatalf("%s trust_level = %q %v, want %q", name, trust, err, want)
	}
}

func execSQL(t *testing.T, db *store.DB, q string) {
	t.Helper()
	if _, err := db.SQL.ExecContext(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}

// A node loads a market version without reading its signature while its
// publisher is active; a revoked version, or one of a revoked or missing
// publisher, does not load; a version not recorded as indexed is
// signature-checked.
func TestVerifyRecorded(t *testing.T) {
	db, ctx := testutil.DB(t), context.Background()
	ts, _ := NewTrustStore(nil, false)
	p := unsignedPackage(t, "tool", "acme")
	if err := ts.VerifyRecorded(ctx, db.Gorm, p); err == nil {
		t.Fatal("unrecorded unsigned package loaded")
	}
	insertIndexedVersion(t, db)
	if err := ts.VerifyRecorded(ctx, db.Gorm, p); err != nil {
		t.Fatalf("indexed version: %v", err)
	}
	execSQL(t, db, `UPDATE publishers SET status = 'revoked'`)
	wantCode(t, "revoked publisher", ts.VerifyRecorded(ctx, db.Gorm, p), "permission_denied", "revoked")
	execSQL(t, db, `UPDATE publishers SET status = 'active', name = 'other'`)
	wantCode(t, "missing publisher", ts.VerifyRecorded(ctx, db.Gorm, p), "permission_denied", "revoked")
	execSQL(t, db, `UPDATE publishers SET name = 'acme'`)
	execSQL(t, db, `UPDATE plugin_versions SET signature_status = 'revoked'`)
	wantCode(t, "revoked version", ts.VerifyRecorded(ctx, db.Gorm, p), "permission_denied", "revoked")
}

// insertIndexedVersion records tool 0.1.0 installed from a market whose
// index rated publisher acme (community here) official.
func insertIndexedVersion(t *testing.T, db *store.DB) {
	t.Helper()
	execSQL(t, db, `INSERT INTO publishers (name, trust_level) VALUES ('acme', 'community')`)
	execSQL(t, db, `INSERT INTO plugins (key, name, publisher_id, status) SELECT 'tool', '{}', id, 'installed' FROM publishers WHERE name = 'acme'`)
	execSQL(t, db, `INSERT INTO plugin_versions (plugin_key, version, manifest, manifest_hash, package_sha256, package_size, publisher_id, signature_status, trust_level)
		SELECT 'tool', '0.1.0', '{}', 'h', 's', 1, id, 'indexed', 'official' FROM publishers WHERE name = 'acme'`)
}

// A version's trust is its own level, else its publisher's; a version
// without either (unsigned, or not recorded) is unsigned.
func TestReadVersionTrust(t *testing.T) {
	db := testutil.DB(t)
	insertIndexedVersion(t, db)
	want := func(version, trust string) {
		t.Helper()
		got, err := ReadVersionTrust(db.Gorm, "tool", version)
		if err != nil || got != trust {
			t.Fatalf("%s trust = %q %v, want %q", version, got, err, trust)
		}
	}
	want("0.1.0", TrustOfficial)
	want("0.2.0", TrustUnsigned)
	execSQL(t, db, `UPDATE plugin_versions SET trust_level = NULL`)
	want("0.1.0", TrustCommunity)
	execSQL(t, db, `UPDATE plugin_versions SET publisher_id = NULL`)
	want("0.1.0", TrustUnsigned)
}
