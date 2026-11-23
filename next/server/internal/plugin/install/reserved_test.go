package install

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

type fakeKV struct{ purged []string }

func (f *fakeKV) PurgePluginKV(_ context.Context, key string) (int, error) {
	f.purged = append(f.purged, key)
	return 2, nil
}

func errCode(err error) string {
	if err == nil {
		return ""
	}
	return core.AsError(err).Code
}

func acmePublisher(t *testing.T, e *env, trust string) pkgtest.Key {
	t.Helper()
	k := pkgtest.NewKey("acme-1")
	if _, err := e.svc.CreatePublisher(context.Background(), CreatePublisherInput{Name: "acme", TrustLevel: trust,
		Keys: []KeyInput{{KeyID: k.ID, PublicKey: k.PubB64()}}}, e.admin); err != nil {
		t.Fatal(err)
	}
	return k
}

func allGrants(m *manifest.Manifest) ConsentRequest {
	var req ConsentRequest
	for _, hp := range m.HostPermissions {
		req.Grants = append(req.Grants, GrantInput{Permission: hp.ID})
	}
	return req
}

// After an uninstall the key stays with its publisher (audit 2026-10-09
// P1-4): another publisher cannot claim it - and inherit the accounts, schema
// and KV left behind - until an administrator releases it.
func TestRetiredKeyStaysWithPublisher(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kv := &fakeKV{}
	e.svc.d.KV = kv
	acme := acmePublisher(t, e, pkg.TrustCommunity)
	install := func(version string) {
		t.Helper()
		if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("acme_tool", version, "acme"), acme), e.admin, UploadOptions{}); err != nil {
			t.Fatalf("upload %s: %v", version, err)
		}
		if _, err := e.svc.Consent(ctx, "acme_tool", version, ConsentRequest{}, e.admin); err != nil {
			t.Fatal(err)
		}
	}
	install("1.0.0")
	if _, err := e.svc.Uninstall(ctx, "acme_tool", UninstallOptions{}, e.admin); err != nil {
		t.Fatal(err)
	}
	if len(kv.purged) != 0 {
		t.Fatal("KV purged without purge")
	}
	other := pkgtest.Build(pkgtest.Minimal("acme_tool", "2.0.0", "sub2api"), e.root)
	if _, err := e.svc.Upload(ctx, other, e.admin, UploadOptions{}); errCode(err) != "plugin_key_retired" {
		t.Fatalf("other publisher claimed a retired key: %v", err)
	}
	// The same publisher reinstalls freely, and the retirement survives it.
	install("1.0.1")
	if _, err := e.svc.ReleaseRetiredKey(ctx, "acme_tool", ReleaseOptions{}, e.admin); errCode(err) != "conflict" {
		t.Fatalf("release of an installed key: %v", err)
	}
	if _, err := e.svc.Uninstall(ctx, "acme_tool", UninstallOptions{Purge: true}, e.admin); err != nil {
		t.Fatal(err)
	}
	if len(kv.purged) != 1 {
		t.Fatalf("uninstall with purge: KV purges %v", kv.purged)
	}
	// A relation of the key's prefix is what the plugin left in the database.
	if _, err := e.db.SQL.ExecContext(ctx, `CREATE TABLE plg_acme_tool__kept (id int)`); err != nil {
		t.Fatal(err)
	}
	keys, err := e.svc.ListRetiredKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Key != "acme_tool" || keys[0].Publisher != "acme" || keys[0].Installed || !keys[0].Schema {
		t.Fatalf("retired keys: %v %+v", err, keys)
	}
	res, err := e.svc.ReleaseRetiredKey(ctx, "acme_tool", ReleaseOptions{Purge: true, PurgeAccounts: true}, e.admin)
	if err != nil || res.AccountsDeleted != 3 || res.KVDeleted != 2 {
		t.Fatalf("release: %v %+v", err, res)
	}
	if len(e.accounts.purged) != 1 || e.accounts.purged[0] != "acme_tool" || e.schemas.dropped[len(e.schemas.dropped)-1] != "acme_tool" {
		t.Fatalf("release side effects: accounts=%v schemas=%v", e.accounts.purged, e.schemas.dropped)
	}
	if _, err := e.svc.ReleaseRetiredKey(ctx, "acme_tool", ReleaseOptions{}, e.admin); errCode(err) != "not_found" {
		t.Fatalf("second release: %v", err)
	}
	if _, err := e.svc.Upload(ctx, other, e.admin, UploadOptions{}); err != nil {
		t.Fatalf("upload after release: %v", err)
	}
	var audits int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'plugin.key.release' AND target_id = 'acme_tool'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("release audit rows = %d", audits)
	}
}

// A built-in plugin takes its key back from a retirement: the image decides.
func TestBuiltinTakesBackRetiredKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.SQL.ExecContext(ctx, `INSERT INTO plugin_key_retirements (plugin_key, publisher_id) VALUES ('acme_tool', NULL)`); err != nil {
		t.Fatal(err)
	}
	data := pkgtest.Build(pkgtest.Minimal("acme_tool", "1.0.0", "sub2api"), e.root)
	if _, err := e.svc.Upload(ctx, data, 0, UploadOptions{}); errCode(err) != "plugin_key_retired" {
		t.Fatalf("hand upload: %v", err)
	}
	if _, err := e.svc.Upload(ctx, data, 0, UploadOptions{Source: "builtin"}); err != nil {
		t.Fatalf("builtin upload: %v", err)
	}
	var n int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM plugin_key_retirements`).Scan(&n)
	if n != 0 {
		t.Fatal("retirement kept after the builtin took the key")
	}
}

// First-party keys and platform ids are reserved (audit 2026-10-09 P1-5).
func TestReservedKeysAndPlatforms(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acme := acmePublisher(t, e, pkg.TrustVerified)
	guard := pkgtest.Build(pkgtest.Minimal("guard", "1.0.0", "acme"), acme)
	if _, err := e.svc.Upload(ctx, guard, e.admin, UploadOptions{}); errCode(err) != "plugin_key_reserved" {
		t.Fatalf("hand upload of a reserved key: %v", err)
	}
	if _, err := e.svc.Upload(ctx, guard, e.admin, UploadOptions{Source: "market:1"}); errCode(err) != "plugin_key_reserved" {
		t.Fatalf("verified market install of a reserved key: %v", err)
	}
	// An officially signed hand upload may claim it.
	if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("payment", "1.0.0", "sub2api"), e.root), e.admin, UploadOptions{}); err != nil {
		t.Fatalf("official upload of a reserved key: %v", err)
	}
	// A reserved platform id belongs to its plugin, whatever the channel.
	m := renamePlatform(pkgtest.Platform("acme_video", "1.0.0", "acme"), "typesafe")
	if _, err := e.svc.Upload(ctx, pkgtest.Build(m, acme), e.admin, UploadOptions{Source: "market:1"}); errCode(err) != "plugin_key_reserved" {
		t.Fatalf("reserved platform id: %v", err)
	}
}

// A version awaiting approval holds no platform or endpoint; Consent checks
// again what other plugins were approved for in the meantime.
func TestUnapprovedVersionsHoldNoResources(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acme := acmePublisher(t, e, pkg.TrustVerified)
	a := renamePlatform(pkgtest.Platform("vid_a", "1.0.0", "acme"), "vid")
	b := renamePlatform(pkgtest.Platform("vid_b", "1.0.0", "sub2api"), "vid")
	if _, err := e.svc.Upload(ctx, pkgtest.Build(a, acme), e.admin, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Upload(ctx, pkgtest.Build(b, e.root), e.admin, UploadOptions{}); err != nil {
		t.Fatalf("an unapproved version blocked an upload: %v", err)
	}
	if _, err := e.svc.Consent(ctx, "vid_b", "1.0.0", allGrants(b), e.admin); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Consent(ctx, "vid_a", "1.0.0", allGrants(a), e.admin)
	if errCode(err) != "plugin_resource_conflict" || fieldCode(err, "platforms[0].id") != "platform_conflict" {
		t.Fatalf("consent of a conflicting version: %v", err)
	}
	// Once approved, the platform blocks new uploads.
	c := renamePlatform(pkgtest.Platform("vid_c", "1.0.0", "acme"), "vid")
	if _, err := e.svc.Upload(ctx, pkgtest.Build(c, acme), e.admin, UploadOptions{}); fieldCode(err, "platforms[0].id") != "platform_conflict" {
		t.Fatalf("upload against an approved platform: %v", err)
	}
}

// A notification channel is held like a platform: one provider per id, so
// the channel's notifications reach the plugin the sender meant (§74.4).
func TestChannelHeldByOneProvider(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acme := acmePublisher(t, e, pkg.TrustVerified)
	provider := func(key, publisher string) *manifest.Manifest {
		m := pkgtest.Minimal(key, "1.0.0", publisher)
		m.Capabilities = []manifest.Capability{{ID: manifest.CapNotifyChannel}}
		m.Notifications = &manifest.Notifications{Channels: []manifest.NotificationChannel{{ID: "sms", Label: manifest.LocalizedText{"en": "SMS"}}}}
		return m
	}
	a, b := provider("sms_a", "acme"), provider("sms_b", "sub2api")
	if _, err := e.svc.Upload(ctx, pkgtest.Build(a, acme), e.admin, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Upload(ctx, pkgtest.Build(b, e.root), e.admin, UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Consent(ctx, "sms_b", "1.0.0", allGrants(b), e.admin); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Consent(ctx, "sms_a", "1.0.0", allGrants(a), e.admin)
	if errCode(err) != "plugin_resource_conflict" || fieldCode(err, "notifications.channels[0].id") != "channel_conflict" {
		t.Fatalf("consent of a second provider: %v", err)
	}
}

// renamePlatform gives the platform of a pkgtest.Platform manifest the id id.
func renamePlatform(m *manifest.Manifest, id string) *manifest.Manifest {
	old := m.Platforms[0].ID
	p := &m.Platforms[0]
	p.ID = id
	for i := range p.Endpoints {
		ep := &p.Endpoints[i]
		ep.Protocol = id + strings.TrimPrefix(ep.Protocol, old)
		ep.Path = "/" + id + strings.TrimPrefix(ep.Path, "/"+old)
	}
	for i := range m.AccountTypes {
		for j := range m.AccountTypes[i].Platforms {
			m.AccountTypes[i].Platforms[j].Platform = id
		}
	}
	return m
}

// The reservations cover every first-party plugin in next/plugins, and only
// those: its key and the platform and notification channel ids it declares.
// A removed key is no longer in the repository.
func TestReservedCoverRepo(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "plugins", "*", "manifest.json"))
	if err != nil || len(paths) < 5 {
		t.Fatalf("first-party manifests: %v %v", err, paths)
	}
	keys := map[string]bool{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m manifest.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		keys[m.Key] = true
		if !reservedKeys[m.Key] {
			t.Errorf("first-party key %q is not reserved", m.Key)
		}
		if _, ok := removedKeys[m.Key]; ok {
			t.Errorf("removed key %q is still a first-party plugin", m.Key)
		}
		for _, p := range m.Platforms {
			if reservedPlatforms[p.ID] != m.Key {
				t.Errorf("platform %q of %q is not reserved for it", p.ID, m.Key)
			}
		}
		if m.Notifications != nil {
			for _, c := range m.Notifications.Channels {
				if reservedChannels[c.ID] != m.Key {
					t.Errorf("notification channel %q of %q is not reserved for it", c.ID, m.Key)
				}
			}
		}
	}
	for key := range reservedKeys {
		if !keys[key] {
			t.Errorf("reserved key %q is no first-party plugin: remove it, or move it to removedKeys", key)
		}
	}
	for id, owner := range reservedPlatforms {
		if !keys[owner] {
			t.Errorf("platform %q is reserved for %q, which is no first-party plugin", id, owner)
		}
	}
	for id, owner := range reservedChannels {
		if !keys[owner] {
			t.Errorf("notification channel %q is reserved for %q, which is no first-party plugin", id, owner)
		}
	}
}

// A removed plugin installs from no source, officially signed or not: the
// check comes before the retired key is claimed.
func TestRemovedKeyRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for key, release := range removedKeys {
		official := pkgtest.Build(pkgtest.Minimal(key, "0.9.0", "sub2api"), e.root)
		for _, source := range []string{"", "builtin", "market:1"} {
			_, err := e.svc.Upload(ctx, official, e.admin, UploadOptions{Source: source})
			if errCode(err) != "plugin_key_reserved" || !strings.Contains(err.Error(), "removed in core "+release) {
				t.Errorf("%s from source %q: %v", key, source, err)
			}
		}
	}
}

func TestReservedChannel(t *testing.T) {
	m := &manifest.Manifest{Key: "acme", Notifications: &manifest.Notifications{Channels: []manifest.NotificationChannel{{ID: "email"}}}}
	if err := checkReserved(m, "upload", pkg.TrustOfficial, true); err == nil {
		t.Fatal("a third-party plugin declared the reserved email channel")
	}
	m.Key = "mailer"
	if err := checkReserved(m, "builtin", "", true); err != nil {
		t.Fatalf("mailer: %v", err)
	}
}
