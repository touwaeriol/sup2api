package pkg

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
// which is not read. The publisher must be listed active, be the
// manifest's and not be revoked on this core. register (an upload)
// records it with the index's trust level; otherwise (the install preview)
// it is only read, and one not registered yet gets id 0 as in
// lookupOfficial.
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
// first with the index's trust level when register.
func indexedPublisherID(g *gorm.DB, ip IndexedPublisher, register bool) (*int64, error) {
	if register {
		if err := upsertPublisher(g, ip.Name, ip.Trust).Error; err != nil {
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

// upsertPublisher registers the publisher name with trust, or sets the
// trust level of the one registered: the index decides it.
func upsertPublisher(g *gorm.DB, name, trust string) *gorm.DB {
	return g.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"trust_level"})}).
		Create(&model.Publisher{Name: name, TrustLevel: trust})
}

// VerifyRecorded re-checks the package p of a recorded version when a node
// loads it. A version installed from a market source (SigIndexed) is not
// signature-checked (the node matched the package against the sha256 the
// version row pins), but its publisher must still be active; a revoked
// version fails; any other is VerifyInstalled.
func (t *TrustStore) VerifyRecorded(ctx context.Context, q *gorm.DB, p *Package) error {
	g := store.G(ctx, q)
	var v model.PluginVersion
	err := recordedVersion(g, p.Manifest.Key, p.Manifest.Version).Take(&v).Error
	switch {
	case err != nil && !store.IsNoRows(err):
		return err
	case v.SignatureStatus == SigRevoked:
		return revokedError("the package signature has been revoked")
	case v.SignatureStatus == SigIndexed:
		_, err = activePublisher(g, p.Manifest.Publisher)
		return err
	}
	_, err = t.VerifyInstalled(ctx, q, p)
	return err
}

// recordedVersion reads the signature status of version of key.
func recordedVersion(db *gorm.DB, key, version string) *gorm.DB {
	return db.Model(&model.PluginVersion{}).Select("signature_status").Where("plugin_key = ? AND version = ?", key, version)
}
