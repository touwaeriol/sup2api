package install

import (
	"context"
	"strings"
	"testing"

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

// A plugin with the same id upgrades from the store whatever it was
// installed from (CONTRACTS §136.2). Each store version runs with the trust
// level its index gave, while the publisher row keeps its own.
func TestMarketUpgradeTrust(t *testing.T) {
	e := newEnv(t)
	acme := acmePublisher(t, e, pkg.TrustCommunity)
	if err := e.installed(t, pkgtest.Build(pkgtest.Minimal("acme_tool", "1.0.0", "acme"), acme), UploadOptions{Source: "upload"}); err != nil {
		t.Fatal(err)
	}
	if err := e.installed(t, indexedPackage("acme_tool", "1.0.1", "acme"), fromMarket("1", "acme", pkg.TrustVerified)); err != nil {
		t.Fatalf("hand upload to store: %v", err)
	}
	if err := e.installed(t, indexedPackage("acme_tool", "1.0.2", "acme"), fromMarket("2", "acme", pkg.TrustOfficial)); err != nil {
		t.Fatalf("store upgrade: %v", err)
	}
	want := map[string]string{"1.0.0": pkg.TrustCommunity, "1.0.1": pkg.TrustVerified, "1.0.2": pkg.TrustOfficial}
	for version, trust := range want {
		if got, err := pkg.ReadVersionTrust(e.db.Gorm, "_acme__acme_tool", version); err != nil || got != trust {
			t.Fatalf("%s trust = %q %v, want %q", version, got, err, trust)
		}
	}
	wantPublisherTrust(t, e, "acme", pkg.TrustCommunity)
}

// wantPublisherTrust fails unless publisher name has trust level want.
func wantPublisherTrust(t *testing.T, e *env, name, want string) {
	t.Helper()
	var trust string
	err := e.db.SQL.QueryRowContext(context.Background(), `SELECT trust_level FROM publishers WHERE name = $1`, name).Scan(&trust)
	if err != nil || trust != want {
		t.Fatalf("%s trust_level = %q %v, want %q", name, trust, err, want)
	}
}

// A store listing of the same key by another publisher is another plugin:
// it installs under its own id and leaves the installed one alone
// (CONTRACTS §137).
func TestMarketSameKeyOtherPublisher(t *testing.T) {
	e := newEnv(t)
	acme := acmePublisher(t, e, pkg.TrustCommunity)
	if err := e.installed(t, pkgtest.Build(pkgtest.Minimal("acme_tool", "1.0.0", "acme"), acme), UploadOptions{Source: "upload"}); err != nil {
		t.Fatal(err)
	}
	if err := e.installed(t, indexedPackage("acme_tool", "1.0.1", "other"), fromMarket("1", "other", pkg.TrustVerified)); err != nil {
		t.Fatalf("store install by another publisher: %v", err)
	}
	if ids := pluginIDs(t, e); strings.Join(ids, " ") != "_acme__acme_tool _other__acme_tool" {
		t.Fatalf("plugins = %v", ids)
	}
}

// A market install of a first-party package needs its index to list the
// publisher as official: the market is no trusted channel by itself.
func TestReservedPublisherFromMarket(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Upload(context.Background(), indexedPackage("guard", "1.0.0", "sub2api"), e.admin, fromMarket("1", "sub2api", pkg.TrustVerified))
	if errCode(err) != "plugin_publisher_reserved" {
		t.Fatalf("verified market install naming sub2api: %v", err)
	}
	if err := e.installed(t, indexedPackage("guard", "1.0.0", "sub2api"), fromMarket("1", "sub2api", pkg.TrustOfficial)); err != nil {
		t.Fatalf("first-party market install: %v", err)
	}
}
