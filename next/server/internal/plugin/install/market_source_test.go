package install

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

// fromMarket are the options of an install from market source src whose
// index lists publisher name with trust.
func fromMarket(src, name, trust string) UploadOptions {
	return UploadOptions{Source: "market:" + src, Indexed: &pkg.IndexedPublisher{Name: name, Trust: trust, Status: pkg.PublisherActive}}
}

// indexedPackage is an unsigned package of key@version by publisher.
func indexedPackage(key, version, publisher string) []byte {
	return pkgtest.Zip(pkgtest.Files(pkgtest.Minimal(key, version, publisher)))
}

// installed uploads data with opt and approves it unless the upgrade
// needed no consent.
func (e *env) installed(t *testing.T, data []byte, opt UploadOptions) error {
	t.Helper()
	r, err := e.svc.Upload(context.Background(), data, e.admin, opt)
	if err != nil || r.ConsentStatus == ConsentApproved {
		return err
	}
	_, err = e.svc.Consent(context.Background(), r.PluginKey, r.Version, ConsentRequest{}, e.admin)
	return err
}

// A plugin installed from a market source upgrades from that source only
// (CONTRACTS §136.2); one uploaded by hand may move to a market. Each
// market version runs with the trust level its index gave, while the
// publisher row keeps its own.
func TestMarketUpgradeSameSource(t *testing.T) {
	e := newEnv(t)
	acme := acmePublisher(t, e, pkg.TrustCommunity)
	if err := e.installed(t, pkgtest.Build(pkgtest.Minimal("acme_tool", "1.0.0", "acme"), acme), UploadOptions{Source: "upload"}); err != nil {
		t.Fatal(err)
	}
	if err := e.installed(t, indexedPackage("acme_tool", "1.0.1", "acme"), fromMarket("2", "acme", pkg.TrustVerified)); err != nil {
		t.Fatalf("hand upload to market: %v", err)
	}
	_, err := e.svc.Upload(context.Background(), indexedPackage("acme_tool", "1.0.2", "acme"), e.admin, fromMarket("3", "acme", pkg.TrustOfficial))
	if e := core.AsError(err); e == nil || e.Code != "permission_denied" || e.Details["reason"] != "source_mismatch" {
		t.Fatalf("other market source: %v", err)
	}
	if err := e.installed(t, indexedPackage("acme_tool", "1.0.2", "acme"), fromMarket("2", "acme", pkg.TrustVerified)); err != nil {
		t.Fatalf("same market source: %v", err)
	}
	for version, want := range map[string]string{"1.0.0": pkg.TrustCommunity, "1.0.2": pkg.TrustVerified} {
		if got, err := pkg.ReadVersionTrust(e.db.Gorm, "acme_tool", version); err != nil || got != want {
			t.Fatalf("%s trust = %q %v, want %q", version, got, err, want)
		}
	}
}

// A market install claims a reserved key only when its index lists the
// publisher as official: the market is no trusted channel by itself.
func TestReservedKeyFromMarket(t *testing.T) {
	e := newEnv(t)
	acmePublisher(t, e, pkg.TrustVerified)
	_, err := e.svc.Upload(context.Background(), indexedPackage("guard", "1.0.0", "acme"), e.admin, fromMarket("1", "acme", pkg.TrustVerified))
	if errCode(err) != "plugin_key_reserved" {
		t.Fatalf("verified market install of a reserved key: %v", err)
	}
	if err := e.installed(t, indexedPackage("guard", "1.0.0", "sub2api"), fromMarket("1", "sub2api", pkg.TrustOfficial)); err != nil {
		t.Fatalf("first-party market install: %v", err)
	}
}
