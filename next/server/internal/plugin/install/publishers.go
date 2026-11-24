package install

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// nameRe is the rule of a signing key id.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// Publisher is a publishers row with its keys.
type Publisher struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	TrustLevel string         `json:"trust_level"`
	Status     string         `json:"status"`
	CreatedAt  time.Time      `json:"created_at"`
	RevokedAt  *time.Time     `json:"revoked_at"`
	Keys       []PublisherKey `json:"keys"`
}

// PublisherKey is a publisher_keys row.
type PublisherKey struct {
	KeyID     string     `json:"key_id"`
	PublicKey string     `json:"public_key"`
	Status    string     `json:"status"`
	NotBefore *time.Time `json:"not_before"`
	NotAfter  *time.Time `json:"not_after"`
	CreatedAt time.Time  `json:"created_at"`
}

// CreatePublisherInput is the body of POST /publishers.
type CreatePublisherInput struct {
	Name       string     `json:"name"`
	TrustLevel string     `json:"trust_level"` // verified | community
	Keys       []KeyInput `json:"keys"`
}

// KeyInput is the body of POST /publishers/:id/keys.
type KeyInput struct {
	KeyID     string     `json:"key_id"`
	PublicKey string     `json:"public_key"`
	NotBefore *time.Time `json:"not_before"`
	NotAfter  *time.Time `json:"not_after"`
}

// RevokeResult summarizes a revocation.
type RevokeResult struct {
	RevokedVersions int64    `json:"revoked_versions"`
	DisabledPlugins []string `json:"disabled_plugins"`
}

// ListPublishers returns all publishers with keys.
func (s *Service) ListPublishers(ctx context.Context) ([]Publisher, error) {
	g := store.G(ctx, s.d.DB.Gorm)
	var pubs []model.Publisher
	if err := g.Order("name").Find(&pubs).Error; err != nil {
		return nil, err
	}
	out := make([]Publisher, len(pubs))
	idx := make(map[int64]int, len(pubs))
	for i, p := range pubs {
		out[i] = Publisher{ID: p.ID, Name: p.Name, TrustLevel: p.TrustLevel, Status: core.ValueOr(p.Status, ""),
			CreatedAt: p.CreatedAt, RevokedAt: p.RevokedAt, Keys: []PublisherKey{}}
		idx[p.ID] = i
	}
	var keys []model.PublisherKey
	if err := g.Order("created_at").Find(&keys).Error; err != nil {
		return nil, err
	}
	for _, k := range keys {
		if i, ok := idx[k.PublisherID]; ok {
			out[i].Keys = append(out[i].Keys, PublisherKey{KeyID: k.KeyID, PublicKey: k.PublicKey, Status: core.ValueOr(k.Status, ""),
				NotBefore: k.NotBefore, NotAfter: k.NotAfter, CreatedAt: k.CreatedAt})
		}
	}
	return out, nil
}

func validateKey(i int, k KeyInput) []core.FieldError {
	var errs []core.FieldError
	f := "key_id"
	pf := "public_key"
	if i >= 0 {
		f, pf = "keys["+strconv.Itoa(i)+"].key_id", "keys["+strconv.Itoa(i)+"].public_key"
	}
	if !nameRe.MatchString(k.KeyID) {
		errs = append(errs, core.FieldError{Field: f, Code: "invalid_format", Message: "key id must be 1-100 letters, digits, _ . -"})
	}
	if _, err := pkgsig.ParsePublicKey(k.PublicKey); err != nil {
		errs = append(errs, core.FieldError{Field: pf, Code: "invalid", Message: "public key must be a base64 Ed25519 key"})
	}
	if k.NotBefore != nil && k.NotAfter != nil && !k.NotAfter.After(*k.NotBefore) {
		errs = append(errs, core.FieldError{Field: "not_after", Code: "invalid", Message: "not_after must be after not_before"})
	}
	return errs
}

// ErrStorePublisher: a local publisher would take the id of a publisher the
// official store lists, whose plugins it could then upgrade (CONTRACTS §137).
var ErrStorePublisher = core.NewError(http.StatusConflict, "publisher_in_store",
	"the official plugin store lists a publisher with this id; a local publisher must have another")

// StorePublishers tells whether the official store's index lists a
// publisher id (market.Service).
type StorePublishers interface {
	ListsPublisher(ctx context.Context, id string) (bool, error)
}

// CreatePublisher registers a verified or community publisher. Official
// publishers come only from the configured root keys.
func (s *Service) CreatePublisher(ctx context.Context, in CreatePublisherInput, actorID int64) (*Publisher, error) {
	if err := s.checkNewPublisher(ctx, in); err != nil {
		return nil, err
	}
	var id int64
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		pub := model.Publisher{Name: in.Name, TrustLevel: in.TrustLevel}
		if err := store.G(ctx, tx).Create(&pub).Error; err != nil {
			return err
		}
		id = pub.ID
		for _, k := range in.Keys {
			if err := insertKey(ctx, tx, id, k); err != nil {
				return err
			}
		}
		return audit.Audit(ctx, tx, actorID, "publisher.create", "publisher", strconv.FormatInt(id, 10), map[string]any{"name": in.Name, "trust_level": in.TrustLevel, "keys": len(in.Keys)})
	})
	if store.IsUniqueViolation(err, "") {
		return nil, core.ErrConflict.WithMessage("publisher or key id already exists")
	}
	if err != nil {
		return nil, err
	}
	return s.getPublisher(ctx, id)
}

// checkNewPublisher checks a new local publisher: its fields, and an id
// (its name, the manifests' publisher) that is a valid publisher id, not
// the first-party one and not one the official store lists.
func (s *Service) checkNewPublisher(ctx context.Context, in CreatePublisherInput) error {
	errs := publisherNameErrors(in.Name)
	if in.TrustLevel != pkg.TrustVerified && in.TrustLevel != pkg.TrustCommunity {
		errs = append(errs, core.FieldError{Field: "trust_level", Code: "invalid", Message: "trust_level must be verified or community"})
	}
	for i, k := range in.Keys {
		errs = append(errs, validateKey(i, k)...)
	}
	if len(errs) > 0 {
		return core.InvalidFields(errs...)
	}
	if s.d.Store == nil {
		return nil
	}
	listed, err := s.d.Store.ListsPublisher(ctx, in.Name)
	if err == nil && listed {
		err = ErrStorePublisher.WithMessage(fmt.Sprintf("the official plugin store lists publisher %q; choose another id for a local publisher", in.Name))
	}
	return err
}

// publisherNameErrors are the problems of name as a local publisher id.
func publisherNameErrors(name string) []core.FieldError {
	if err := check.ValidatePublisherID(name); err != nil {
		return []core.FieldError{{Field: "name", Code: "invalid_format", Message: err.Error()}}
	}
	if name == manifest.OfficialPublisher {
		return []core.FieldError{{Field: "name", Code: "reserved", Message: fmt.Sprintf("publisher %q is reserved for the first-party plugins", name)}}
	}
	return nil
}

func insertKey(ctx context.Context, tx *gorm.DB, pubID int64, k KeyInput) error {
	return store.G(ctx, tx).Create(newPublisherKey(pubID, k)).Error
}

// newPublisherKey is the publisher_keys row of k; status takes the
// column's default (active).
func newPublisherKey(pubID int64, k KeyInput) *model.PublisherKey {
	return &model.PublisherKey{KeyID: k.KeyID, PublisherID: pubID, PublicKey: k.PublicKey, NotBefore: k.NotBefore, NotAfter: k.NotAfter}
}

// AddPublisherKey adds a signing key (key rotation).
func (s *Service) AddPublisherKey(ctx context.Context, pubID int64, k KeyInput, actorID int64) (*Publisher, error) {
	if errs := validateKey(-1, k); len(errs) > 0 {
		return nil, core.InvalidFields(errs...)
	}
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockKeyTarget(store.G(ctx, tx), pubID); err != nil {
			return err
		}
		if err := insertKey(ctx, tx, pubID, k); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "publisher.key.add", "publisher", strconv.FormatInt(pubID, 10), map[string]any{"key_id": k.KeyID})
	})
	if store.IsUniqueViolation(err, "") {
		return nil, core.ErrConflict.WithMessage("key id already exists")
	}
	if err != nil {
		return nil, err
	}
	return s.getPublisher(ctx, pubID)
}

// lockKeyTarget locks publisher pubID, which may get a signing key: active
// and not official. The official publisher's keys come with the core (the
// first-party key) and the official trust of others with the index; a key
// added here would make hand uploads official.
func lockKeyTarget(g *gorm.DB, pubID int64) error {
	if err := store.RequireTx(g); err != nil {
		return err
	}
	var pub model.Publisher
	err := g.Select("status", "trust_level").Where("id = ?", pubID).Clauses(store.ForUpdate()).Take(&pub).Error
	switch {
	case store.IsNoRows(err):
		return core.ErrNotFound.WithMessage("publisher not found")
	case err != nil:
		return err
	case core.ValueOr(pub.Status, "") != pkg.PublisherActive:
		return core.ErrConflict.WithMessage("publisher is revoked")
	case pub.TrustLevel == pkg.TrustOfficial:
		return core.ErrConflict.WithMessage("keys cannot be added to an official publisher")
	}
	return nil
}

func (s *Service) getPublisher(ctx context.Context, id int64) (*Publisher, error) {
	all, err := s.ListPublishers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, core.ErrNotFound.WithMessage("publisher not found")
}

// RevokePublisher revokes a publisher and all its keys, marks its versions
// signature-revoked and disables its running plugins.
func (s *Service) RevokePublisher(ctx context.Context, pubID int64, reason string, actorID int64) (*RevokeResult, error) {
	res := &RevokeResult{DisabledPlugins: []string{}}
	var toDisable []string
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		r := g.Model(&model.Publisher{}).Where("id = ?", pubID).
			Updates(map[string]any{"status": publisherRevoked, "revoked_at": gorm.Expr("now()")})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return core.ErrNotFound.WithMessage("publisher not found")
		}
		if err := g.Model(&model.PublisherKey{}).Where("publisher_id = ?", pubID).Update("status", publisherRevoked).Error; err != nil {
			return err
		}
		r = g.Model(&model.PluginVersion{}).Where("publisher_id = ?", pubID).Update("signature_status", pkg.SigRevoked)
		if r.Error != nil {
			return r.Error
		}
		res.RevokedVersions = r.RowsAffected
		if err := g.Model(&model.Plugin{}).Where("publisher_id = ? AND status IN ('enabled', 'enabling', 'upgrading')", pubID).
			Pluck("key", &toDisable).Error; err != nil {
			return err
		}
		if err := setPublisherPluginsReason(g, pubID, "publisher revoked: "+reason).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "publisher.revoke", "publisher", strconv.FormatInt(pubID, 10), map[string]any{"reason": reason, "revoked_versions": res.RevokedVersions})
	})
	if err != nil {
		return nil, err
	}
	res.DisabledPlugins = append(res.DisabledPlugins, s.disableRevoked(ctx, toDisable, actorID, "publisher revoked: "+reason)...)
	return res, nil
}

// RevokeKey revokes one signing key, marks the versions it signed and
// disables plugins whose active (or only) version it signed.
func (s *Service) RevokeKey(ctx context.Context, keyID, reason string, actorID int64) (*RevokeResult, error) {
	res := &RevokeResult{DisabledPlugins: []string{}}
	var toDisable []string
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		r := g.Model(&model.PublisherKey{}).Where("key_id = ?", keyID).Update("status", publisherRevoked)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return core.ErrNotFound.WithMessage("key not found")
		}
		r = g.Model(&model.PluginVersion{}).Where("key_id = ?", keyID).Update("signature_status", pkg.SigRevoked)
		if r.Error != nil {
			return r.Error
		}
		res.RevokedVersions = r.RowsAffected
		if err := runningSignedBy(g, keyID).Scan(&toDisable).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "publisher_key.revoke", "publisher_key", keyID, map[string]any{"reason": reason, "revoked_versions": res.RevokedVersions})
	})
	if err != nil {
		return nil, err
	}
	res.DisabledPlugins = append(res.DisabledPlugins, s.disableRevoked(ctx, toDisable, actorID, "signing key revoked: "+reason)...)
	return res, nil
}

// publisherRevoked is the status of a revoked publisher or key.
const publisherRevoked = "revoked"

// setPublisherPluginsReason sets the status reason of every plugin of
// publisher pubID.
func setPublisherPluginsReason(db *gorm.DB, pubID int64, reason string) *gorm.DB {
	return db.Model(&model.Plugin{}).Where("publisher_id = ?", pubID).
		Updates(map[string]any{"status_reason": reason, "updated_at": gorm.Expr("now()")})
}

// runningSignedBy selects the keys of the running plugins whose active or
// desired version key keyID signed.
func runningSignedBy(db *gorm.DB, keyID string) *gorm.DB {
	signed := db.Table("plugin_versions AS v").Select("1").
		Where("v.plugin_key = p.key AND v.key_id = ? AND (v.version = p.active_version OR v.version = p.desired_version)", keyID)
	return db.Table("plugins AS p").Select("p.key").
		Where("p.status IN ('enabled', 'enabling', 'upgrading') AND EXISTS (?)", signed)
}
