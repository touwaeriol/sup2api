package pkg

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Trust levels.
const (
	TrustOfficial  = "official"
	TrustVerified  = "verified"
	TrustCommunity = "community"
	TrustUnsigned  = "unsigned"
)

// Signature statuses stored in plugin_versions.signature_status.
const (
	SigValid    = "valid"
	SigUnsigned = "unsigned"
	SigRevoked  = "revoked"
)

// Verification is the outcome of signature and trust checks.
type Verification struct {
	Publisher       string `json:"publisher"`
	PublisherID     *int64 `json:"publisher_id,omitempty"`
	KeyID           string `json:"key_id,omitempty"`
	Trust           string `json:"trust"`
	SignatureStatus string `json:"signature_status"`
}

// TrustStore verifies package signatures against publisher_keys and the
// configured official root keys.
type TrustStore struct {
	official      map[string]officialKey
	allowUnsigned bool
	skipSigCheck  bool
	now           func() time.Time
}

type officialKey struct {
	b64 string
	pub ed25519.PublicKey
}

// NewTrustStore parses official root keys ("keyId=base64pub").
func NewTrustStore(officialRootKeys []string, allowUnsigned bool) (*TrustStore, error) {
	t := &TrustStore{official: map[string]officialKey{}, allowUnsigned: allowUnsigned, now: time.Now}
	for _, entry := range officialRootKeys {
		id, b64, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok || id == "" {
			return nil, fmt.Errorf("official root key %q: expected keyId=base64", entry)
		}
		// base64 padding may contain '='; Cut splits on the first one only.
		pub, err := pkgsig.ParsePublicKey(b64)
		if err != nil {
			return nil, fmt.Errorf("official root key %q: %w", id, err)
		}
		t.official[id] = officialKey{b64: strings.TrimSpace(b64), pub: pub}
	}
	return t, nil
}

// SetClock overrides time.Now (tests).
func (t *TrustStore) SetClock(now func() time.Time) { t.now = now }

// SetVerifySignatures(false) skips the cryptographic signature check while
// keeping publisher/key resolution and revocation checks (test deployments).
func (t *TrustStore) SetVerifySignatures(on bool) { t.skipSigCheck = !on }

// AllowUnsigned reports whether unsigned packages are accepted.
func (t *TrustStore) AllowUnsigned() bool { return t.allowUnsigned }

func sigError(msg string) error {
	return core.ErrInvalidArgument.WithMessage("plugin signature: " + msg).
		WithDetails(map[string]any{"fields": []core.FieldError{{Field: "signature", Code: "invalid_signature", Message: msg}}})
}

func revokedError(msg string) error {
	return core.ErrPermissionDenied.WithMessage(msg).WithDetails(map[string]any{"reason": "revoked"})
}

// verifyMode is how verify checks a package: validity, the signing key's
// validity window (not for an installed package); register, an official
// publisher and root key are registered (not in a read-only transaction).
type verifyMode struct{ validity, register bool }

// Verify checks the package signature and resolves publisher trust. Run it
// inside the upload transaction: packages signed by an official root key
// auto-register their publisher (trust official) and key.
func (t *TrustStore) Verify(ctx context.Context, q *gorm.DB, p *Package) (*Verification, error) {
	return t.verify(ctx, q, p, verifyMode{validity: true, register: true})
}

// VerifyReadOnly is Verify registering nothing, for the install preview's
// read-only transaction: an official publisher not registered yet has no
// PublisherID, and the checks Verify makes of what it registers are made of
// what is registered.
func (t *TrustStore) VerifyReadOnly(ctx context.Context, q *gorm.DB, p *Package) (*Verification, error) {
	return t.verify(ctx, q, p, verifyMode{validity: true})
}

// VerifyInstalled re-checks an already installed package when a node loads
// it: the signature must still verify and neither the publisher nor the key
// may be revoked, but the key's validity window is not checked again (the
// key was valid when the package was uploaded; expiry must not stop plugins
// that are already installed).
func (t *TrustStore) VerifyInstalled(ctx context.Context, q *gorm.DB, p *Package) (*Verification, error) {
	return t.verify(ctx, q, p, verifyMode{register: true})
}

func (t *TrustStore) verify(ctx context.Context, q *gorm.DB, p *Package, mode verifyMode) (*Verification, error) {
	sig := p.Signature
	if sig == nil {
		if !t.allowUnsigned {
			return nil, sigError("package is not signed and unsigned plugins are not allowed")
		}
		return &Verification{Publisher: p.Manifest.Publisher, Trust: TrustUnsigned, SignatureStatus: SigUnsigned}, nil
	}
	if sig.Publisher != p.Manifest.Publisher {
		return nil, sigError(fmt.Sprintf("signature publisher %q does not match manifest publisher %q", sig.Publisher, p.Manifest.Publisher))
	}
	if ok, found := t.official[sig.KeyID]; found {
		return t.verifyOfficial(ctx, q, p, ok, mode.register)
	}
	return t.verifyPublisherKey(ctx, q, p, mode.validity)
}

// verifyOfficial verifies p, signed by the official root key ok, and
// registers its publisher and key (register) or reads them.
func (t *TrustStore) verifyOfficial(ctx context.Context, q *gorm.DB, p *Package, ok officialKey, register bool) (*Verification, error) {
	sig := p.Signature
	if err := t.check(p, sig, ok.pub); err != nil {
		return nil, sigError(err.Error())
	}
	var id *int64
	var err error
	if register {
		id, err = t.registerOfficial(ctx, q, sig.Publisher, sig.KeyID, ok.b64)
	} else {
		id, err = lookupOfficial(store.G(ctx, q), sig.Publisher, sig.KeyID)
	}
	if err != nil {
		return nil, err
	}
	return &Verification{Publisher: sig.Publisher, PublisherID: id, KeyID: sig.KeyID, Trust: TrustOfficial, SignatureStatus: SigValid}, nil
}

// verifyPublisherKey verifies p, signed by a key of publisher_keys: known,
// its publisher's, neither revoked and, with validity, within its validity
// window.
func (t *TrustStore) verifyPublisherKey(ctx context.Context, q *gorm.DB, p *Package, validity bool) (*Verification, error) {
	sig := p.Signature
	k, err := readSigningKey(store.G(ctx, q), sig)
	if err != nil {
		return nil, err
	}
	if validity {
		if err := t.checkWindow(sig.KeyID, k); err != nil {
			return nil, err
		}
	}
	pub, err := pkgsig.ParsePublicKey(k.PublicKey)
	if err != nil {
		return nil, err
	}
	if err := t.check(p, sig, pub); err != nil {
		return nil, sigError(err.Error())
	}
	return &Verification{Publisher: k.Name, PublisherID: &k.PublisherID, KeyID: sig.KeyID, Trust: k.Trust, SignatureStatus: SigValid}, nil
}

// signingKeyRow is what signingKey reads of a key and its publisher.
type signingKeyRow struct {
	PublicKey, KeyStatus         string
	NotBefore, NotAfter          *time.Time
	PublisherID                  int64
	Name, Trust, PublisherStatus string
}

// readSigningKey reads the key sig names and checks that it is sig's
// publisher's and that neither is revoked.
func readSigningKey(g *gorm.DB, sig *pkgsig.Signature) (*signingKeyRow, error) {
	var k signingKeyRow
	err := signingKey(g, sig.KeyID).Row().
		Scan(&k.PublicKey, &k.KeyStatus, &k.NotBefore, &k.NotAfter, &k.PublisherID, &k.Name, &k.Trust, &k.PublisherStatus)
	switch {
	case store.IsNoRows(err):
		return nil, sigError(fmt.Sprintf("unknown signing key %q", sig.KeyID))
	case err != nil:
		return nil, err
	case k.Name != sig.Publisher:
		return nil, sigError(fmt.Sprintf("key %q does not belong to publisher %q", sig.KeyID, sig.Publisher))
	case k.PublisherStatus != "active":
		return nil, revokedError(fmt.Sprintf("publisher %q has been revoked", k.Name))
	case k.KeyStatus != "active":
		return nil, revokedError(fmt.Sprintf("signing key %q has been revoked", sig.KeyID))
	}
	return &k, nil
}

// checkWindow fails when key keyID (k) is used outside its validity window.
func (t *TrustStore) checkWindow(keyID string, k *signingKeyRow) error {
	now := t.now()
	if k.NotBefore != nil && now.Before(*k.NotBefore) {
		return sigError(fmt.Sprintf("signing key %q is not valid yet", keyID))
	}
	if k.NotAfter != nil && now.After(*k.NotAfter) {
		return sigError(fmt.Sprintf("signing key %q has expired", keyID))
	}
	return nil
}

// signingKey reads the signing key keyID and its publisher.
func signingKey(db *gorm.DB, keyID string) *gorm.DB {
	return db.Table("publisher_keys AS k").
		Select("k.public_key, k.status, k.not_before, k.not_after, p.id, p.name, p.trust_level, p.status").
		Joins("JOIN publishers p ON p.id = k.publisher_id").Where("k.key_id = ?", keyID)
}

func (t *TrustStore) check(p *Package, sig *pkgsig.Signature, pub ed25519.PublicKey) error {
	if t.skipSigCheck {
		return nil
	}
	return pkgsig.Verify(p.Files, sig, pub)
}

// registerOfficial upserts the publisher as official and records the root
// key so it can be revoked like any other key. Both inserts tolerate a
// concurrent first registration (several plugins verifying at once on a
// fresh database): the loser's insert does nothing and the row is read back.
func (t *TrustStore) registerOfficial(ctx context.Context, q *gorm.DB, name, keyID, b64 string) (*int64, error) {
	g := store.G(ctx, q)
	if err := g.Clauses(onConflictNothing("name")).Create(&model.Publisher{Name: name, TrustLevel: TrustOfficial}).Error; err != nil {
		return nil, err
	}
	pub, err := activePublisher(g, name)
	if err != nil {
		return nil, err
	}
	if pub.TrustLevel != TrustOfficial {
		if err := g.Model(&model.Publisher{}).Where("id = ?", pub.ID).Update("trust_level", TrustOfficial).Error; err != nil {
			return nil, err
		}
	}
	if err := g.Clauses(onConflictNothing("key_id")).Create(&model.PublisherKey{KeyID: keyID, PublisherID: pub.ID, PublicKey: b64}).Error; err != nil {
		return nil, err
	}
	return &pub.ID, checkOfficialKey(g, pub.ID, keyID)
}

// lookupOfficial is registerOfficial for a read-only transaction: it reads
// the publisher name and the key keyID as registered, if they are (a
// publisher not registered yet has a nil id), and checks them as
// registerOfficial does.
func lookupOfficial(g *gorm.DB, name, keyID string) (*int64, error) {
	pub, err := activePublisher(g, name)
	if store.IsNoRows(err) {
		return nil, checkOfficialKey(g, 0, keyID)
	}
	if err != nil {
		return nil, err
	}
	return &pub.ID, checkOfficialKey(g, pub.ID, keyID)
}

// activePublisher reads the publisher name, revokedError when it is
// revoked.
func activePublisher(g *gorm.DB, name string) (*model.Publisher, error) {
	var pub model.Publisher
	if err := g.Select("id", "trust_level", "status").Where("name = ?", name).Take(&pub).Error; err != nil {
		return nil, err
	}
	if core.ValueOr(pub.Status, "") != "active" {
		return nil, revokedError(fmt.Sprintf("publisher %q has been revoked", name))
	}
	return &pub, nil
}

// checkOfficialKey checks the official key keyID as registered, if it is:
// it must be publisher id's and not revoked.
func checkOfficialKey(g *gorm.DB, id int64, keyID string) error {
	var key model.PublisherKey
	err := g.Select("publisher_id", "status").Where("key_id = ?", keyID).Take(&key).Error
	switch {
	case store.IsNoRows(err):
		return nil
	case err != nil:
		return err
	case key.PublisherID != id:
		return sigError(fmt.Sprintf("official key %q is registered to another publisher", keyID))
	case core.ValueOr(key.Status, "") != "active":
		return revokedError(fmt.Sprintf("signing key %q has been revoked", keyID))
	}
	return nil
}

// onConflictNothing skips an INSERT that conflicts on column.
func onConflictNothing(column string) clause.OnConflict {
	return clause.OnConflict{Columns: []clause.Column{{Name: column}}, DoNothing: true}
}

// CheckTrust enforces what a trust level may request: community and
// unsigned packages cannot ask for critical host permissions or native UI.
func CheckTrust(m *manifest.Manifest, trust string) error {
	if trust == TrustOfficial || trust == TrustVerified {
		return nil
	}
	var errs []core.FieldError
	for i, hp := range m.HostPermissions {
		if manifest.HostPermissionRisk[hp.ID] == manifest.RiskCritical {
			errs = append(errs, core.FieldError{
				Field: fmt.Sprintf("hostPermissions[%d]", i), Code: "trust_insufficient",
				Message: fmt.Sprintf("%s publishers cannot request critical permission %q", trust, hp.ID),
			})
		}
	}
	if m.UI != nil && m.UI.Native != nil {
		errs = append(errs, core.FieldError{Field: "ui.native", Code: "trust_insufficient",
			Message: fmt.Sprintf("%s publishers cannot ship native UI", trust)})
	}
	if len(errs) > 0 {
		return core.InvalidFields(errs...).WithMessage("publisher trust level does not allow the requested permissions")
	}
	return nil
}
