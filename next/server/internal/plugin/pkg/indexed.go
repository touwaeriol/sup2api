package pkg

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// SigIndexed is the signature status of a version installed from a market
// source (CONTRACTS §136): the index vouched for its sha256 and its
// publisher's trust level, and its signature was not read.
const SigIndexed = "indexed"

// Publisher statuses, of the publishers table and of a market index.
const (
	PublisherActive  = "active"
	PublisherRevoked = "revoked"
)

// IndexedPublisher is what a market index says of the publisher of a
// package it lists: its id (the manifest's publisher), trust level and
// status, "" when the index does not list it.
type IndexedPublisher struct {
	Name   string
	Trust  string
	Status string
}

// Check fails unless the index lists the publisher as active: a revoked
// one as a revoked publisher, a missing one as unlisted (both 403).
func (ip IndexedPublisher) Check() error {
	switch ip.Status {
	case PublisherActive:
		return nil
	case "":
		return core.ErrPermissionDenied.WithMessage(fmt.Sprintf("publisher %q is not listed in the market index", ip.Name)).
			WithDetails(map[string]any{"reason": "publisher_unlisted"})
	}
	return revokedError(fmt.Sprintf("publisher %q is revoked in the market index", ip.Name))
}

// VerifyIndexed resolves the trust of p, installed from a market source,
// from what its index says of the publisher (ip) instead of its signature,
// which is not read: the trust level is the version's (recorded on its row,
// plugin_versions.trust_level), never written to the publisher. The
// publisher must be listed active, be the manifest's and not be revoked on
// this core. register (an upload) registers it if needed (registerIndexed);
// otherwise (the install preview) it is only read, and one not registered
// yet gets id 0 as in lookupOfficial.
func (t *TrustStore) VerifyIndexed(ctx context.Context, q *gorm.DB, p *Package, ip IndexedPublisher, register bool) (*Verification, error) {
	if err := ip.Check(); err != nil {
		return nil, err
	}
	if ip.Name != p.Manifest.Publisher {
		return nil, core.InvalidFields(core.FieldError{Field: "publisher", Code: "publisher_mismatch",
			Message: fmt.Sprintf("the market index lists publisher %q, the package names %q", ip.Name, p.Manifest.Publisher)})
	}
	id, err := indexedPublisherID(store.G(ctx, q), ip, register)
	if err != nil {
		return nil, err
	}
	return &Verification{Publisher: ip.Name, PublisherID: id, Trust: ip.Trust, SignatureStatus: SigIndexed}, nil
}

// indexedPublisherID is the id of the active publisher ip, registered
// first when register.
func indexedPublisherID(g *gorm.DB, ip IndexedPublisher, register bool) (*int64, error) {
	if register {
		if err := registerIndexed(g, ip.Name).Error; err != nil {
			return nil, err
		}
	}
	pub, err := activePublisher(g, ip.Name)
	switch {
	case err == nil:
		return &pub.ID, nil
	case !register && store.IsNoRows(err):
		return new(int64), nil
	}
	return nil, err
}

// registerIndexed registers the publisher name of a market install unless
// it is registered: as community, the level of a publisher without a key
// here (its hand uploads are not trusted any higher). A registered one is
// never changed by an index.
func registerIndexed(g *gorm.DB, name string) *gorm.DB {
	return g.Clauses(onConflictNothing("name")).Create(&model.Publisher{Name: name, TrustLevel: TrustCommunity})
}

// VerifyRecorded re-checks the package p of a recorded version when a node
// loads it. A version installed from a market source (SigIndexed) is not
// signature-checked (the node matched the package against the sha256 the
// version row pins), but its publisher must still be active; a revoked
// version fails; any other is VerifyInstalled.
func (t *TrustStore) VerifyRecorded(ctx context.Context, q *gorm.DB, p *Package) error {
	g := store.G(ctx, q)
	var v model.PluginVersion
	err := recordedVersion(g, p.Manifest.ID(), p.Manifest.Version).Take(&v).Error
	switch {
	case err != nil && !store.IsNoRows(err):
		return err
	case v.SignatureStatus == SigRevoked:
		return revokedError("the package signature has been revoked")
	case v.SignatureStatus == SigIndexed:
		return checkIndexedPublisher(g, p.Manifest.Publisher)
	}
	_, err = t.VerifyInstalled(ctx, q, p)
	return err
}

// checkIndexedPublisher fails unless the publisher name of an indexed
// version is registered and active.
func checkIndexedPublisher(g *gorm.DB, name string) error {
	_, err := activePublisher(g, name)
	if store.IsNoRows(err) {
		return revokedError(fmt.Sprintf("publisher %q is not registered", name))
	}
	return err
}

// recordedVersion reads the signature status of version of key.
func recordedVersion(db *gorm.DB, key, version string) *gorm.DB {
	return db.Model(&model.PluginVersion{}).Select("signature_status").Where("plugin_key = ? AND version = ?", key, version)
}
