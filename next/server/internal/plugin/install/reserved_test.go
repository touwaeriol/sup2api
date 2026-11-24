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

// allowUnsigned makes e accept unsigned packages, beside the signed ones.
func allowUnsigned(t *testing.T, e *env) {
	t.Helper()
	ts, err := pkg.NewTrustStore([]string{e.root.ID + "=" + e.root.PubB64()}, true)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.d.Trust = ts
}

// After an uninstall the plugin id stays with its publisher (audit
// 2026-10-09 P1-4): another claimant of it - here an unsigned package
// naming the same publisher, which no publisher row vouches for - cannot
// take it and inherit the accounts, schema and KV left behind until an
// administrator releases it.
func TestRetiredKeyStaysWithPublisher(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kv := &fakeKV{}
	e.svc.d.KV = kv
	acme := acmePublisher(t, e, pkg.TrustCommunity)
	allowUnsigned(t, e)
	const id = "_acme__acme_tool"
	install := func(version string) {
		t.Helper()
		if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("acme_tool", version, "acme"), acme), e.admin, UploadOptions{}); err != nil {
			t.Fatalf("upload %s: %v", version, err)
		}
		if _, err := e.svc.Consent(ctx, id, version, ConsentRequest{}, e.admin); err != nil {
			t.Fatal(err)
		}
	}
	install("1.0.0")
	if _, err := e.svc.Uninstall(ctx, id, UninstallOptions{}, e.admin); err != nil {
		t.Fatal(err)
	}
	if len(kv.purged) != 0 {
		t.Fatal("KV purged without purge")
	}
	other := pkgtest.Build(pkgtest.Minimal("acme_tool", "2.0.0", "acme"), pkgtest.Key{})
	if _, err := e.svc.Upload(ctx, other, e.admin, UploadOptions{}); errCode(err) != "plugin_key_retired" {
		t.Fatalf("other publisher claimed a retired id: %v", err)
	}
	// The same publisher reinstalls freely, and the retirement survives it.
	install("1.0.1")
	if _, err := e.svc.ReleaseRetiredKey(ctx, id, ReleaseOptions{}, e.admin); errCode(err) != "conflict" {
		t.Fatalf("release of an installed id: %v", err)
	}
	if _, err := e.svc.Uninstall(ctx, id, UninstallOptions{Purge: true}, e.admin); err != nil {
		t.Fatal(err)
	}
	if len(kv.purged) != 1 {
		t.Fatalf("uninstall with purge: KV purges %v", kv.purged)
	}
	checkRelease(t, e, id, other)
}

// checkRelease lists and releases the retired id, after which other
// installs.
func checkRelease(t *testing.T, e *env, id string, other []byte) {
	t.Helper()
	ctx, kv := context.Background(), e.svc.d.KV.(*fakeKV)
	// A relation of the id's prefix is what the plugin left in the database.
	if _, err := e.db.SQL.ExecContext(ctx, `CREATE TABLE `+manifest.TablePrefix(id)+`kept (id int)`); err != nil {
		t.Fatal(err)
	}
	keys, err := e.svc.ListRetiredKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Key != id || keys[0].DisplayID != manifest.DisplayID(id) || keys[0].Publisher != "acme" || keys[0].Installed || !keys[0].Schema {
		t.Fatalf("retired keys: %v %+v", err, keys)
	}
	res, err := e.svc.ReleaseRetiredKey(ctx, id, ReleaseOptions{Purge: true, PurgeAccounts: true}, e.admin)
	if err != nil || res.AccountsDeleted != 3 || res.KVDeleted != 2 || len(kv.purged) != 2 {
		t.Fatalf("release: %v %+v", err, res)
	}
	if len(e.accounts.purged) != 1 || e.accounts.purged[0] != id || e.schemas.dropped[len(e.schemas.dropped)-1] != id {
		t.Fatalf("release side effects: accounts=%v schemas=%v", e.accounts.purged, e.schemas.dropped)
	}
	if _, err := e.svc.ReleaseRetiredKey(ctx, id, ReleaseOptions{}, e.admin); errCode(err) != "not_found" {
		t.Fatalf("second release: %v", err)
	}
	if _, err := e.svc.Upload(ctx, other, e.admin, UploadOptions{}); err != nil {
		t.Fatalf("upload after release: %v", err)
	}
	var audits int
	_ = e.db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'plugin.key.release' AND target_id = $1`, id).Scan(&audits)
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

// The first-party publisher is reserved (CONTRACTS §137): a package naming
// it installs with official trust, from the built-in channel or in dev
// mode only. Another publisher's plugin of a first-party key installs next
// to it under its own id. A reserved platform id belongs to its plugin
// (audit 2026-10-09 P1-5).
func TestReservedPublisherAndPlatforms(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acme := acmePublisher(t, e, pkg.TrustVerified)
	allowUnsigned(t, e)
	rogue := pkgtest.Build(pkgtest.Minimal("codex", "1.0.0", "sub2api"), pkgtest.Key{})
	verified := &pkg.IndexedPublisher{Name: "sub2api", Trust: pkg.TrustVerified, Status: pkg.PublisherActive}
	for name, opt := range map[string]UploadOptions{"unsigned": {}, "community market": {Source: "market:1", Indexed: verified}} {
		if _, err := e.svc.Upload(ctx, rogue, e.admin, opt); errCode(err) != "plugin_publisher_reserved" {
			t.Fatalf("%s package naming sub2api: %v", name, err)
		}
	}
	if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("codex", "1.0.0", "sub2api"), e.root), e.admin, UploadOptions{}); err != nil {
		t.Fatalf("official upload: %v", err)
	}
	if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("codex", "1.0.0", "acme"), acme), e.admin, UploadOptions{}); err != nil {
		t.Fatalf("acme/codex next to codex: %v", err)
	}
	if ids := pluginIDs(t, e); strings.Join(ids, " ") != "_acme__codex codex" {
		t.Fatalf("plugins = %v", ids)
	}
	if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("guard", "1.0.0", "sub2api"), pkgtest.Key{}), e.admin, UploadOptions{Source: "builtin"}); err != nil {
		t.Fatalf("built-in unsigned package naming sub2api: %v", err)
	}
	e.svc.opt.Plugins.DevMode = true
	if _, err := e.svc.Upload(ctx, pkgtest.Build(pkgtest.Minimal("relay", "1.0.0", "sub2api"), pkgtest.Key{}), e.admin, UploadOptions{}); err != nil {
		t.Fatalf("dev mode: %v", err)
	}
	m := renamePlatform(pkgtest.Platform("typesafe", "1.0.0", "acme"), "typesafe")
	if _, err := e.svc.Upload(ctx, pkgtest.Build(m, acme), e.admin, UploadOptions{}); errCode(err) != "plugin_key_reserved" {
		t.Fatalf("acme/typesafe declared the reserved platform: %v", err)
	}
}

// pluginIDs are the keys of the plugins rows, sorted.
func pluginIDs(t *testing.T, e *env) []string {
	t.Helper()
	rows, err := e.db.SQL.QueryContext(context.Background(), `SELECT key FROM plugins ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
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
	_, err := e.svc.Consent(ctx, "_acme__vid_a", "1.0.0", allGrants(a), e.admin)
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
	_, err := e.svc.Consent(ctx, "_acme__sms_a", "1.0.0", allGrants(a), e.admin)
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

// The reservations cover the first-party plugins in next/plugins, and
// only those: every one is the first-party publisher's, its platform and
// notification channel ids are reserved for it, and a removed plugin is no
// longer in the repository.
func TestReservedCoverRepo(t *testing.T) {
	ids := firstPartyIDs(t)
	for id, m := range ids {
		if m.Publisher != manifest.OfficialPublisher || id != m.Key {
			t.Errorf("first-party plugin %q is published by %q", m.Key, m.Publisher)
		}
		if _, ok := removedKeys[id]; ok {
			t.Errorf("removed plugin %q is still a first-party plugin", id)
		}
		for _, p := range m.Platforms {
			if reservedPlatforms[p.ID] != id {
				t.Errorf("platform %q of %q is not reserved for it", p.ID, id)
			}
		}
		if m.Notifications != nil {
			for _, c := range m.Notifications.Channels {
				if reservedChannels[c.ID] != id {
					t.Errorf("notification channel %q of %q is not reserved for it", c.ID, id)
				}
			}
		}
	}
	for _, owners := range []map[string]string{reservedPlatforms, reservedChannels} {
		for res, owner := range owners {
			if ids[owner] == nil {
				t.Errorf("%q is reserved for %q, which is no first-party plugin", res, owner)
			}
		}
	}
}

// firstPartyIDs reads the manifests of next/plugins by plugin id.
func firstPartyIDs(t *testing.T) map[string]*manifest.Manifest {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "plugins", "*", "manifest.json"))
	if err != nil || len(paths) < 5 {
		t.Fatalf("first-party manifests: %v %v", err, paths)
	}
	ids := map[string]*manifest.Manifest{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m manifest.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		ids[m.ID()] = &m
	}
	return ids
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

// A notification channel id is reserved for its plugin id: another
// publisher's plugin of the same key does not get it.
func TestReservedChannel(t *testing.T) {
	m := &manifest.Manifest{Key: "mailer", Publisher: "acme", Notifications: &manifest.Notifications{Channels: []manifest.NotificationChannel{{ID: "email"}}}}
	if err := checkReserved(m, "upload", pkg.TrustOfficial, false); err == nil {
		t.Fatal("acme/mailer declared the reserved email channel")
	}
	m.Publisher = manifest.OfficialPublisher
	if err := checkReserved(m, "builtin", "", false); err != nil {
		t.Fatalf("mailer: %v", err)
	}
}
