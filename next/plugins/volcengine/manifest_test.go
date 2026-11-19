package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/plugins/volcengine/internal/volcengine"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// decodeManifest decodes manifest.json into the SDK types, rejecting unknown
// fields (a typo in a field name must not pass silently).
func decodeManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	return &m
}

// TestManifest checks manifest.json against the SDK types (no unknown
// fields), the built-in openai platform and the files it references.
func TestManifest(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.Key != "volcengine" || m.Version != "0.13.0" || m.Publisher != "sub2api" || m.APIVersion != manifest.APIVersion {
		t.Fatalf("identity = %s %s %s", m.Key, m.Version, m.Publisher)
	}
	if m.Name["en"] == "" || m.Name["zh"] == "" || m.Description["en"] == "" || m.Description["zh"] == "" {
		t.Fatal("name and description need en and zh")
	}
	if m.Runtime != "grpc" || m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat == "" {
		t.Fatalf("runtime/entry/hostCompat = %s %+v %s", m.Runtime, m.Entry, m.HostCompat)
	}
	// The forwarding-v2 contract (one Execute path, Monitor returning its
	// result) ships with core 0.1.304.
	for version, want := range map[string]bool{"0.1.303": false, "0.1.304": true} {
		if got, err := check.HostCompatible(m.HostCompat, version); err != nil || got != want {
			t.Fatalf("host %s compatibility: %v %v", version, got, err)
		}
	}
	// No jobs, no hooks and no scheduler extension.
	if len(m.Jobs) != 0 || len(m.Hooks) != 0 || m.Scheduler != nil {
		t.Fatalf("stage three declares no jobs, hooks or scheduler extension: %+v", m)
	}
	if m.Database == nil || m.Database.Schema != "plg_volcengine" || m.Database.Migrations != "migrations/" {
		t.Fatalf("database = %+v", m.Database)
	}
	wantCaps := []string{manifest.CapPlatformAdapter, manifest.CapPlatformTasks, manifest.CapPlatformMonitor, manifest.CapHTTPRoutes}
	gotCaps := make([]string, 0, len(m.Capabilities))
	for _, c := range m.Capabilities {
		gotCaps = append(gotCaps, c.ID)
	}
	if !slices.Equal(gotCaps, wantCaps) {
		t.Fatalf("capabilities = %v, want %v", gotCaps, wantCaps)
	}

	// Two account types, split the way the upstreams are: apikey is Ark itself
	// (address pinned, protocol-specific official paths) and relay is an
	// Ark-compatible relay (address free, paths configurable, Anthropic
	// served). Looked up by ID, never by index.
	types := map[string]manifest.AccountType{}
	for _, at := range m.AccountTypes {
		types[at.ID] = at
	}
	if len(m.AccountTypes) != 2 || len(types) != 2 {
		t.Fatalf("accountTypes = %+v, want exactly apikey and relay", m.AccountTypes)
	}
	official, ok := types[volcengine.AccountTypeAPIKey]
	if !ok {
		t.Fatalf("no %s account type: %+v", volcengine.AccountTypeAPIKey, m.AccountTypes)
	}
	relay, ok := types[volcengine.AccountTypeRelay]
	if !ok {
		t.Fatalf("no %s account type: %+v", volcengine.AccountTypeRelay, m.AccountTypes)
	}
	for _, at := range m.AccountTypes {
		if at.Label["en"] == "" || at.Label["zh"] == "" || at.Description["en"] == "" || at.Description["zh"] == "" {
			t.Fatalf("account type %s needs an en and zh label and description", at.ID)
		}
		// secret_key is masked like the API key; access_key is NOT, because an
		// access key id identifies the Volcengine account and hiding it would
		// stop an operator from telling two accounts apart in the form. It is
		// still stored encrypted: everything outside settingsFields is
		// (server account/creds.go split()).
		if !slices.Equal(at.SensitiveFields, []string{"api_key", volcengine.FieldSecretKey}) {
			t.Fatalf("%s sensitiveFields = %v", at.ID, at.SensitiveFields)
		}
		if slices.Contains(at.SettingsFields, volcengine.FieldAccessKey) ||
			slices.Contains(at.SettingsFields, volcengine.FieldSecretKey) {
			t.Fatalf("%s: the AK/SK pair must not be plaintext settings: %v", at.ID, at.SettingsFields)
		}
		// Model mapping is a core account field (CONTRACTS §18).
		if slices.Contains(at.SettingsFields, "model_mapping") {
			t.Fatalf("%s: model_mapping must not be a settings field", at.ID)
		}
		// The two prefixes are paths, and guardedSettings is a URL allow-list
		// (check requires every allowed entry to be an absolute http(s) URL),
		// so they cannot be guarded that way. What keeps a prefix from moving a
		// request off its account's host is upstreamURL's origin check, on
		// every request.
		for _, g := range at.GuardedSettings {
			if g.Field == volcengine.FieldAPIPrefix || g.Field == volcengine.FieldVideoAPIPrefix {
				t.Fatalf("%s cannot be a guarded setting: guardedSettings only allow-lists absolute URLs", g.Field)
			}
		}
	}

	// ---- the official type: pinned address, protocol paths fixed by the plugin.
	if !slices.Equal(official.SettingsFields,
		[]string{"base_url", volcengine.FieldAssetBaseURL, volcengine.FieldAssetRegion}) {
		t.Fatalf("apikey settingsFields = %v", official.SettingsFields)
	}
	// The prefixes must NOT be offered here: Ark serves one layout, and a
	// prefix field on this type would be a setting that changes nothing
	// (prefixesOf reads none for it) while looking like it does.
	for _, f := range []string{volcengine.FieldAPIPrefix, volcengine.FieldVideoAPIPrefix} {
		if slices.Contains(official.SettingsFields, f) {
			t.Errorf("the apikey type must not offer %s: Ark has one path layout", f)
		}
	}
	// base_url and asset_base_url are both guarded (CONTRACTS §21.3):
	// without account:settings:custom only the official addresses (or an
	// empty value, which the plugin normalizes to the default) may be used.
	want := []string{volcengine.DefaultBaseURL, volcengine.BytePlusBaseURL}
	if len(official.GuardedSettings) != 2 || official.GuardedSettings[0].Field != "base_url" ||
		!slices.Equal(official.GuardedSettings[0].Allowed, want) {
		t.Fatalf("apikey guardedSettings = %+v, want base_url -> %v", official.GuardedSettings, want)
	}
	if g := official.GuardedSettings[1]; g.Field != volcengine.FieldAssetBaseURL ||
		!slices.Equal(g.Allowed, []string{volcengine.DefaultAssetBaseURL}) {
		t.Fatalf("apikey guardedSettings[1] = %+v, want %s -> %q", g, volcengine.FieldAssetBaseURL, volcengine.DefaultAssetBaseURL)
	}
	// The asset endpoint is the Ark CONTROL plane and a different host from
	// the API base URL. Conflating them yields a signed request to a host
	// that does not serve Action=..., so the two constants must not share a
	// host.
	if hostOf(t, volcengine.DefaultAssetBaseURL) == hostOf(t, volcengine.DefaultBaseURL) {
		t.Fatalf("the asset endpoint must not be the API host: %s", volcengine.DefaultAssetBaseURL)
	}

	// ---- the relay type: free address, both paths, Anthropic served.
	if !slices.Equal(relay.SettingsFields, []string{"base_url", "video_endpoint", "asset_endpoint", volcengine.FieldAPIPrefix,
		volcengine.FieldVideoAPIPrefix, volcengine.FieldAssetBaseURL, volcengine.FieldAssetRegion}) {
		t.Fatalf("relay settingsFields = %v", relay.SettingsFields)
	}
	// The two prefixes MUST be settings fields of this type. Nothing in the
	// core checks it: split() partitions on exactly this list, so a prefix left
	// out of it goes into the ENCRYPTED blob while prefixesOf reads the
	// plaintext one. The result is an operator who sets a prefix, gets no
	// error, and sees every request keep going to /v1.
	for _, f := range []string{volcengine.FieldAPIPrefix, volcengine.FieldVideoAPIPrefix} {
		if !slices.Contains(relay.SettingsFields, f) {
			t.Fatalf("%s must be a settings field or it is stored encrypted and never read: %v", f, relay.SettingsFields)
		}
	}
	// A relay's address is not an enumerable set, so it cannot be guarded -
	// which is exactly why it is a separate account type rather than a value of
	// the official one. Asserted so that nobody "fixes" it by adding a guard
	// with one example relay in it.
	if len(relay.GuardedSettings) != 0 {
		t.Fatalf("relay guardedSettings = %+v, want none: a relay's address cannot be an allow-list", relay.GuardedSettings)
	}

	// Both forms are native code (ui/native/src): the components entry.ts
	// registers. What a field may hold is checked by ValidateCredentials
	// (internal/volcengine/form_constraints_test.go); in particular neither
	// half of the AK/SK pair is required, since an account without an asset
	// library is legal.
	for _, c := range []struct {
		at        manifest.AccountType
		component string
	}{{official, "VolcengineKeyForm"}, {relay, "VolcengineRelayForm"}} {
		if !reflect.DeepEqual(c.at.Form, manifest.Form{Mode: "native", Component: c.component}) {
			t.Errorf("%s form = %+v, want the native %s", c.at.ID, c.at.Form, c.component)
		}
	}
	if m.UI == nil || m.UI.Native == nil || m.UI.Native.Entry != "ui/native/entry.js" || m.HostUICompat != "^4.0" {
		t.Fatalf("native UI = %+v, hostUICompat %q", m.UI, m.HostUICompat)
	}

	// Official Ark now serves Messages through its compatibility API. Existing
	// relay text integrations retain their platform declarations.
	//
	// Looked up by name, never by index: these lists have grown before, and an
	// index-based assertion silently starts checking a different platform when
	// they grow again.
	platformsOf := func(at manifest.AccountType) map[string]manifest.AccountPlatform {
		byName := map[string]manifest.AccountPlatform{}
		for _, ap := range at.Platforms {
			byName[ap.Platform] = ap
		}
		if len(at.Platforms) != len(byName) {
			t.Fatalf("%s lists a platform twice: %+v", at.ID, at.Platforms)
		}
		return byName
	}
	officialPlatforms, relayPlatforms := platformsOf(official), platformsOf(relay)
	for _, want := range []string{volcengine.PlatformID, volcengine.PlatformVolcengine, volcengine.PlatformAnthropic} {
		for id, byName := range map[string]map[string]manifest.AccountPlatform{
			volcengine.AccountTypeAPIKey: officialPlatforms, volcengine.AccountTypeRelay: relayPlatforms} {
			if _, ok := byName[want]; !ok {
				t.Fatalf("%s platforms = %v, want %s among them", id, byName, want)
			}
		}
	}
	if len(officialPlatforms) != 3 {
		t.Fatalf("apikey platforms = %+v, want openai, anthropic and volcengine", official.Platforms)
	}
	if len(relayPlatforms) != 3 {
		t.Fatalf("relay platforms = %+v, want exactly the three known ones", relay.Platforms)
	}
	if _, ok := relayPlatforms[volcengine.PlatformAnthropic]; !ok {
		t.Fatalf("the relay type must declare the %s platform: that is what it exists for", volcengine.PlatformAnthropic)
	}

	// The own platform inherits everything from platforms[] (no override), on
	// both types.
	for id, byName := range map[string]map[string]manifest.AccountPlatform{
		volcengine.AccountTypeAPIKey: officialPlatforms, volcengine.AccountTypeRelay: relayPlatforms} {
		own := byName[volcengine.PlatformVolcengine]
		if len(own.PassHeaders) != 0 || len(own.Usage) != 0 {
			t.Fatalf("%s own platform entry = %+v", id, own)
		}
	}
	// anthropic's passHeaders are narrowed the same way openai's is: the
	// plugin forwards exactly what the core hands over. anthropic-version and
	// anthropic-beta are in it because they change how the upstream reads the
	// body.
	anth := relayPlatforms[volcengine.PlatformAnthropic]
	if !reflect.DeepEqual(officialPlatforms[volcengine.PlatformAnthropic], anth) {
		t.Fatal("official and relay Messages must share exclusive usage and header declarations")
	}
	if want := []string{"anthropic-version", "anthropic-beta", "user-agent"}; !slices.Equal(anth.PassHeaders, want) {
		t.Fatalf("anthropic platform entry = %+v, want passHeaders %v", anth, want)
	}
	// The old override existed for ONE measured reason; the platform now
	// handles it, so the account type must inherit the complete rules.
	//
	// Measured 2026-09-30 against an Ark-compatible relay: on a streaming
	// Anthropic request the same fixed prompt reports input_tokens 34 in
	// message_start and 54 in message_delta, and 54 non-streaming - twice,
	// identically. The platform reads input from message_start, so every
	// streaming request through that upstream under-counted input by 37%.
	//
	// Reading it from message_delta as well is safe in both directions
	// because Acc.set OVERWRITES and SKIPS A MISSING PATH: on that relay the
	// later 54 replaces 34, and on Anthropic itself, whose message_delta
	// carries no usage.input_tokens at all, nothing is overwritten and
	// message_start's own value stands.
	if len(anth.Usage) != 0 {
		t.Fatalf("anthropic usage must stay inherited: %+v", anth.Usage)
	}
	if b := builtinNamed(t, volcengine.PlatformAnthropic); b != nil {
		// An AccountPlatform usage override REPLACES the platform's whole
		// block, so a field left out of it is a field nobody reads - and a
		// forgotten SSE list bills every streaming request at zero, silently.
		// Rather than maintain a hand copy, inherit the platform's rules and
		// verify the delta input key that originally required an override.
		var inputPath string
		for _, rule := range b.Usage.SSE {
			if rule.Event == "message_delta" {
				inputPath = rule.Map[manifest.UsageInputTokens]
			}
		}
		if inputPath != "usage.input_tokens" || gjson.Get(`{"usage":{"input_tokens":54}}`, inputPath).Int() != 54 || gjson.Get(`{"usage":{"output_tokens":7}}`, inputPath).Exists() {
			t.Fatal("inherited delta input must overwrite reported input and skip missing input")
		}
	}
	// Ark ignores OpenAI's org/project and x-stainless-* headers, so both
	// account types narrow the built-in platform's passHeaders; the usage
	// rules are inherited (Ark is OpenAI-compatible and
	// its token counts are inclusive, like the built-in platform's).
	for id, byName := range map[string]map[string]manifest.AccountPlatform{
		volcengine.AccountTypeAPIKey: officialPlatforms, volcengine.AccountTypeRelay: relayPlatforms} {
		ap := byName[volcengine.PlatformID]
		if want := []string{"user-agent"}; !slices.Equal(ap.PassHeaders, want) {
			t.Errorf("%s openai passHeaders = %v, want %v", id, ap.PassHeaders, want)
		}
		if len(ap.Usage) != 0 {
			t.Errorf("%s: usage must stay inherited: %+v", id, ap)
		}
		// The endpoints Ark serves on the built-in platform are listed, so an
		// endpoint the core adds there (OpenAI's image generations and edits)
		// does not reach Ark; Ark's own images are on the volcengine platform.
		if want := []string{"chat_completions", "responses", "responses_websocket", "embeddings"}; !slices.Equal(ap.Endpoints, want) {
			t.Errorf("%s openai endpoints = %v, want %v", id, ap.Endpoints, want)
		}
	}
	if b := builtin(t); b != nil {
		// WebSocket endpoints are only routed to plugins declaring
		// platform.websocket.v1, which this one does not.
		var got []string
		for _, e := range b.Endpoints {
			if officialPlatforms[volcengine.PlatformID].SupportsEndpoint(e.ID) && !e.WebSocket() {
				got = append(got, e.Protocol)
			}
		}
		if !slices.Equal(got, volcengine.Protocols) {
			t.Fatalf("served built-in openai protocols %v, implemented %v", got, volcengine.Protocols)
		}
		if b.Usage.Semantics != "inclusive" {
			t.Errorf("built-in openai usage semantics = %q; Ark is inclusive too, check the override", b.Usage.Semantics)
		}
	}

	perms := map[string]manifest.HostPermission{}
	for _, p := range m.HostPermissions {
		if _, ok := manifest.HostPermissionRisk[p.ID]; !ok {
			t.Errorf("unknown host permission %q", p.ID)
		}
		if p.Reason["en"] == "" || p.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", p.ID)
		}
		perms[p.ID] = p
	}
	// Every permission is what the administrator sees on the consent
	// screen. ui.native (Critical) is the plugin's own console code (the
	// account forms). Notably absent: net - the asset library's requests are
	// sent by the host (SendAccountRequest), the plugin opens no connection.
	wantPerms := []string{
		"platform.register", "gateway.endpoint", "db.schema", "routes.admin", "ui.menu", "ui.native",
	}
	if len(perms) != len(wantPerms) {
		t.Fatalf("host permissions = %v, want %v", perms, wantPerms)
	}
	for _, id := range wantPerms {
		if _, ok := perms[id]; !ok {
			t.Fatalf("missing host permission %q", id)
		}
	}
	// The core sends the asset library's requests only to hosts the
	// manifest allows: the default asset endpoint has to be an external
	// service, or an account that leaves asset_base_url empty is refused.
	if !manifest.ExternalServiceHost(m.ExternalServices, hostOf(t, volcengine.DefaultAssetBaseURL)) {
		t.Errorf("externalServices %v do not cover the default asset endpoint %q", m.ExternalServices, volcengine.DefaultAssetBaseURL)
	}
	// The addresses shown on the consent screen must be the ones the plugin
	// can actually reach by default - the two API hosts and the asset
	// endpoint, which is a third host.
	if !slices.Equal(m.ExternalServices, []string{
		"ark.cn-beijing.volces.com", "ark.ap-southeast.bytepluses.com", "ark.cn-beijing.volcengineapi.com"}) {
		t.Errorf("externalServices = %v", m.ExternalServices)
	}
}

// hostOf returns the host of an absolute URL.
func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Fatalf("not an absolute URL: %q", raw)
	}
	return u.Host
}

// TestAssetRoutesAndPages pins the asset library's declared surface: every
// route is an admin route behind one of the plugin's two permissions and
// account:read (reads) or account:update (writes), and the two native pages
// and the routes they use exist. Only the routes naming an account
// directly pass the own level; the others reach accounts through rows of
// this plugin, which the core cannot see.
func TestAssetRoutesAndPages(t *testing.T) {
	m := decodeManifest(t)
	if len(m.Routes) == 0 {
		t.Fatal("stage three declares the asset library routes")
	}
	accountFrom := map[string][]string{
		"POST /asset-groups":                              {"body:account_id"},
		"GET /accounts/:account_id/upstream/asset-groups": {"path:account_id"},
		"GET /accounts/:account_id/upstream/assets":       {"path:account_id"},
	}
	declared := map[string]bool{}
	for _, r := range m.Routes {
		if r.Scope != "admin" {
			t.Errorf("route %s %s scope = %q; the asset library is administrator-only", r.Method, r.Path, r.Scope)
		}
		action := manifest.AccountsRead
		switch r.Permission {
		case volcengine.PermAssetsRead:
			if r.Method != "GET" {
				t.Errorf("route %s %s is a mutation behind the read permission", r.Method, r.Path)
			}
		case volcengine.PermAssetsManage:
			if r.Method == "GET" {
				t.Errorf("route %s %s is a read behind the manage permission", r.Method, r.Path)
			}
			action = manifest.AccountsUpdate
		default:
			t.Errorf("route %s %s permission = %q", r.Method, r.Path, r.Permission)
		}
		want := &manifest.RouteAccounts{Action: action, From: accountFrom[r.Method+" "+r.Path]}
		if !reflect.DeepEqual(r.Accounts, want) {
			t.Errorf("route %s %s accounts = %+v, want %+v", r.Method, r.Path, r.Accounts, want)
		}
		declared[r.Method+" "+r.Path] = true
	}
	// The two list pages are native (ui/native/src/pages.ts): the create
	// dialogs live inside them, so there are no form pages any more.
	wantPages := map[string]string{"assets": "VolcengineAssetsPage", "asset_groups": "VolcengineAssetGroupsPage"}
	if m.UI == nil || len(m.UI.Pages) != len(wantPages) {
		t.Fatalf("pages = %+v, want %v", m.UI, wantPages)
	}
	for id, component := range wantPages {
		pg := m.UI.Pages[id]
		if pg.Type != "native" || pg.Component != component || pg.Title["en"] == "" || pg.Title["zh"] == "" {
			t.Errorf("page %s = %+v, want the native %s with a title", id, pg, component)
		}
	}
	// The routes the pages read and post to.
	for _, r := range []string{"GET /assets", "POST /assets", "GET /asset-groups", "POST /asset-groups"} {
		if !declared[r] {
			t.Errorf("route %s is not declared", r)
		}
	}
	// Every menu points at a declared page and carries the read permission.
	for _, mn := range m.UI.Menus {
		if _, ok := m.UI.Pages[mn.Page]; !ok {
			t.Errorf("menu %s points at unknown page %q", mn.ID, mn.Page)
		}
		if mn.Permission != volcengine.PermAssetsRead && mn.Permission != volcengine.PermAssetsManage {
			t.Errorf("menu %s permission = %q", mn.ID, mn.Permission)
		}
	}
}

// TestManifestServes runs the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, volcengine.New(), pluginsdktest.Options{Manifest: manifestJSON})
	want := []string{manifest.CapPlatformAdapter, manifest.CapPlatformTasks, manifest.CapPlatformMonitor, manifest.CapHTTPRoutes}
	got := h.Info.GetCapabilities()
	slices.Sort(got)
	slices.Sort(want)
	if h.Info.GetPluginKey() != "volcengine" || !slices.Equal(got, want) {
		t.Fatalf("info = %v", h.Info)
	}
}

// TestValidate runs the host's own static manifest validation (the package
// in sdk/manifest/check that the server and the packaging CLI both call) on
// this package, so a rule the core enforces at install time fails here first.
func TestValidate(t *testing.T) {
	files := map[string][]byte{
		"manifest.json":                        manifestJSON,
		"runtimes/linux-amd64/plugin":          []byte("elf"),
		"runtimes/linux-arm64/plugin":          []byte("elf"),
		"ui/native/entry.js":                   []byte("export function register() {}"),
		"migrations/0001_init.sql":             mustFile(t, "migrations/0001_init.sql"),
		"migrations/0002_video_tasks.sql":      mustFile(t, "migrations/0002_video_tasks.sql"),
		"migrations/0003_drop_video_tasks.sql": mustFile(t, "migrations/0003_drop_video_tasks.sql"),
	}
	for _, icon := range []string{"assets/volcengine-color.svg", "assets/doubao-color.svg"} {
		files[icon] = mustFile(t, icon)
	}
	if err := check.Validate(decodeManifest(t), files, check.ValidateOptions{HostVersion: "0.1.304"}); err != nil {
		t.Fatalf("core manifest validation: %v", err)
	}
}

// TestPlatformDeclaration checks platforms[]: the volcengine platform and its
// one gateway endpoint (ARCHITECTURE 6.4). Ark's image generation has no
// built-in platform, so this is the plugin's own.
func TestPlatformDeclaration(t *testing.T) {
	m := decodeManifest(t)
	if len(m.Platforms) != 1 {
		t.Fatalf("platforms = %+v, want exactly one", m.Platforms)
	}
	p := m.Platforms[0]
	if p.ID != volcengine.PlatformVolcengine || platforms.IsBuiltin(p.ID) {
		t.Fatalf("platform id = %q (builtin=%v)", p.ID, platforms.IsBuiltin(p.ID))
	}
	if p.Label["en"] == "" || p.Label["zh"] == "" {
		t.Errorf("platform label = %v", p.Label)
	}
	// Ark's image response carries the token counts the same way OpenAI's
	// does (usage.*, no separate cache figures), so the semantics are
	// inclusive like every other Ark surface.
	if p.Usage.Semantics != "inclusive" {
		t.Errorf("platform usage semantics = %q", p.Usage.Semantics)
	}
	if !slices.Equal(p.Protocols(), volcengine.OwnProtocols) {
		t.Fatalf("protocols = %v, want %v", p.Protocols(), volcengine.OwnProtocols)
	}
	// Stage two's image endpoint plus stage five's two video endpoints, plus
	// the legacy /v1 prefix for video (see TestV1AliasEndpoints).
	if len(p.Endpoints) != 5 {
		t.Fatalf("endpoints = %+v, want images, video_submit, video_query and their v1_ aliases", p.Endpoints)
	}
	e := p.Endpoints[0]
	if e.ID != "images" || e.Method != "POST" || e.Path != "/api/v3/images/generations" {
		t.Fatalf("endpoint = %s %s %s", e.ID, e.Method, e.Path)
	}
	// The client-facing path is Ark's official one, so the Ark SDK works with
	// base_url https://<host>/api/v3 unchanged. That needs the core to reserve
	// only /api/v1 rather than the whole "api" first segment; the core
	// validation in TestManifest is what refuses a reserved path.
	if !strings.HasPrefix(e.Path, volcengine.APIPrefix+"/") {
		t.Fatalf("path %q must be under Ark's own %s prefix", e.Path, volcengine.APIPrefix)
	}
	// The generic OpenAI image path stays free for a future built-in openai
	// endpoint; binding it to one vendor's plugin would block that.
	if e.Path == "/v1/images/generations" {
		t.Fatal("the generic /v1/images/generations path is deliberately not claimed")
	}
	if e.Protocol != volcengine.ProtocolImages || !strings.HasPrefix(e.Protocol, p.ID+".") {
		t.Fatalf("protocol = %q, want %q under %q.", e.Protocol, volcengine.ProtocolImages, p.ID)
	}
	if e.Kind != "proxy" || e.ErrorFormat != "openai" || e.Billing != "usage" {
		t.Fatalf("kind/errorFormat/billing = %q %q %q", e.Kind, e.ErrorFormat, e.Billing)
	}
	if !slices.Equal(e.Auth.Headers, []string{"authorization"}) || e.Auth.Query != "" {
		t.Fatalf("auth = %+v", e.Auth)
	}
	// Ark images are synchronous JSON; the model is a body field.
	if e.Request.ModelPath != "model" || e.Request.ModelParam != "" || e.Request.Stream || e.Request.StreamPath != "" {
		t.Fatalf("request = %+v", e.Request)
	}
	if e.Response.NonStream != "json" || e.Response.Stream != "" {
		t.Fatalf("response = %+v", e.Response)
	}
	// No endpoint of a built-in platform may be shadowed (the host refuses
	// the package otherwise).
	for _, bp := range platforms.Builtin() {
		for _, be := range bp.Endpoints {
			for _, ee := range p.Endpoints {
				if check.EndpointsConflict(be, ee) {
					t.Errorf("endpoint %s %s conflicts with built-in %q (%s %s)", ee.Method, ee.Path, bp.ID, be.Method, be.Path)
				}
			}
		}
	}
}

// TestAnthropicMessagesOnly: both account types serve only the messages
// endpoint of the anthropic platform. Without the subset the core would
// route count_tokens to them natively (and never use its local estimate),
// and Ark has no token-counting endpoint, so the request could only fail.
func TestAnthropicMessagesOnly(t *testing.T) {
	m := decodeManifest(t)
	if len(m.AccountTypes) != 2 {
		t.Fatalf("account types = %d, want official and relay", len(m.AccountTypes))
	}
	for _, at := range m.AccountTypes {
		found := false
		for _, ap := range at.Platforms {
			if ap.Platform != volcengine.PlatformAnthropic {
				continue
			}
			found = true
			if !slices.Equal(ap.Endpoints, []string{"messages"}) {
				t.Errorf("%s: anthropic endpoints = %v, want [messages]", at.ID, ap.Endpoints)
			}
		}
		if !found {
			t.Errorf("%s: does not serve the anthropic platform", at.ID)
		}
	}
}

// TestVideoEndpoints pins the two Seedance endpoints (stage five): a submit
// whose usage the plugin reads and pre-charges, and a poll whose model the
// plugin resolves and which is never billed.
func TestVideoEndpoints(t *testing.T) {
	p := decodeManifest(t).Platforms[0]
	byID := map[string]manifest.Endpoint{}
	for _, e := range p.Endpoints {
		byID[e.ID] = e
	}

	submit, ok := byID["video_submit"]
	if !ok {
		t.Fatal("no video_submit endpoint")
	}
	if submit.Method != "POST" || submit.Path != "/api/v3/contents/generations/tasks" {
		t.Fatalf("video_submit = %s %s", submit.Method, submit.Path)
	}
	if submit.Protocol != volcengine.ProtocolVideoSubmit || submit.Kind != "proxy" {
		t.Fatalf("video_submit protocol/kind = %q %q", submit.Protocol, submit.Kind)
	}
	// The submit body carries the model; the response carries only the task
	// id, so usage comes from the plugin (ExtractUsage) and billing is usage
	// (the estimate is charged - "free" would drop the reservation entirely).
	if submit.Request.ModelPath != "model" || submit.Request.ModelSource != "" {
		t.Fatalf("video_submit request = %+v", submit.Request)
	}
	if !submit.PluginUsage() || submit.UsageSource != manifest.UsageSourcePlugin {
		t.Fatalf("video_submit usageSource = %q, want plugin", submit.UsageSource)
	}
	if submit.Billing != "usage" {
		t.Fatalf("video_submit billing = %q; the estimate is only charged when billing is usage", submit.Billing)
	}
	if submit.ErrorFormat != "openai" || submit.Response.NonStream != "json" || submit.Response.Stream != "" {
		t.Fatalf("video_submit errorFormat/response = %q %+v", submit.ErrorFormat, submit.Response)
	}
	// usageRequestFields is what makes the pre-charge a reading instead of a
	// guess. The manifest and the estimate have to agree EXACTLY, including the
	// order: a path the manifest does not declare is never delivered, and the
	// estimate would then silently fall back to a default - the request would
	// succeed and the wrong amount would be charged, with nothing to see. The
	// order matters because the host spends its total byte budget in
	// declaration order, so the five short scalars have to come before the
	// prompt texts (CONTRACTS §25.6).
	if !slices.Equal(submit.UsageRequestFields, volcengine.UsageRequestFields) {
		t.Fatalf("video_submit usageRequestFields = %v, want exactly %v (same order)",
			submit.UsageRequestFields, volcengine.UsageRequestFields)
	}
	for i, p := range submit.UsageRequestFields {
		if !check.ValidUsageRequestPath(p) {
			t.Errorf("usageRequestFields[%d] = %q is not a path that reads one value", i, p)
		}
		if i < 5 && strings.Contains(p, "text") {
			t.Errorf("usageRequestFields[%d] = %q: a prompt text must not come before the scalars", i, p)
		}
	}
	if n := len(submit.UsageRequestFields); n > check.MaxUsageRequestFields {
		t.Fatalf("usageRequestFields declares %d paths, over the SDK cap of %d", n, check.MaxUsageRequestFields)
	}
	// Only the submit endpoints declare them: the query endpoints read no
	// usage at all, and the checker refuses the field outside usageSource
	// "plugin".
	for _, e := range p.Endpoints {
		if e.Protocol != volcengine.ProtocolVideoSubmit && len(e.UsageRequestFields) != 0 {
			t.Errorf("endpoint %q declares usageRequestFields: %v", e.ID, e.UsageRequestFields)
		}
	}
	// The resolution fact is the estimate's tier and the real tier after
	// reconcile; declared as an enum so a bad upstream value is dropped.
	if submit.Usage == nil {
		t.Fatal("video_submit has no usage block for the resolution fact")
	}
	rf, ok := submit.Usage.Facts[volcengine.FactResolution]
	if !ok || rf.Type != "enum" || !slices.Equal(rf.Enum, volcengine.VideoResolutions) {
		t.Fatalf("resolution fact = %+v, want enum %v", rf, volcengine.VideoResolutions)
	}
	// source "plugin" allows an empty path (the plugin supplies the value),
	// but the fact must still declare its type.
	if rf.Path != "" {
		t.Errorf("resolution fact path = %q; the plugin supplies the value, not a gjson path", rf.Path)
	}

	query, ok := byID["video_query"]
	if !ok {
		t.Fatal("no video_query endpoint")
	}
	if query.Method != "GET" || query.Path != "/api/v3/contents/generations/tasks/:"+volcengine.TaskIDParam {
		t.Fatalf("video_query = %s %s", query.Method, query.Path)
	}
	if query.Protocol != volcengine.ProtocolVideoQuery {
		t.Fatalf("video_query protocol = %q", query.Protocol)
	}
	// The core resolves owner, model and original account from its task record.
	if query.Request.ModelSource != "" ||
		query.Request.ModelPath != "" || query.Request.ModelParam != "" {
		t.Fatalf("video_query request = %+v, want core task lookup", query.Request)
	}
	if !submit.TaskSubmit() || !query.TaskQuery() || submit.Task.Kind != "video" || query.Task.Kind != "video" ||
		query.Task.IDParam != volcengine.TaskIDParam || !slices.Equal(submit.Task.IDPaths, []string{"id", "task_id", "data.id", "result.id"}) ||
		!slices.Equal(query.Task.IDPaths, submit.Task.IDPaths) {
		t.Fatalf("video task contract: submit=%+v query=%+v", submit.Task, query.Task)
	}
	// modelSource "plugin" must not also declare streamPath (ResolveModel
	// answers stream); this endpoint never streams anyway.
	if query.Request.StreamPath != "" || query.Request.Stream {
		t.Fatalf("video_query stream = %+v", query.Request)
	}
	// Polling is free, however many times a client does it - charging is the
	// reconcile loop's job.
	if query.Billing != "free" {
		t.Fatalf("video_query billing = %q, want free", query.Billing)
	}
	// The task id is the one path parameter, and the constant the plugin reads
	// it by must match the pattern.
	if params := check.PathParams(query.Path); !slices.Equal(params, []string{volcengine.TaskIDParam}) {
		t.Fatalf("video_query path params = %v", params)
	}
	// Both video paths are Ark's official ones, so the Ark SDK's
	// base_url https://<host>/api/v3 reaches them unchanged.
	for _, e := range []manifest.Endpoint{submit, query} {
		if !strings.HasPrefix(e.Path, volcengine.APIPrefix+"/") {
			t.Errorf("video path %q is not under Ark's own %s prefix", e.Path, volcengine.APIPrefix)
		}
	}
}

// arkImageResponse is a documented Ark image generation response
// (docs.volcengine.com/docs/ark/image-generation-api): three images from one
// request, with reference images. usage carries generated_images,
// output_tokens and total_tokens - and no input_tokens, which is why the
// usage map declares none.
const arkImageResponse = `{
  "model": "doubao-seedream-4-5-251128",
  "created": 1757323224,
  "data": [
    {"url": "https://example.invalid/a.jpeg", "size": "2848x1600"},
    {"url": "https://example.invalid/b.jpeg", "size": "2848x1600"},
    {"url": "https://example.invalid/c.jpeg", "size": "2848x1600"}
  ],
  "usage": {"generated_images": 3, "input_images": 2, "output_tokens": 53400, "total_tokens": 53400}
}`

// TestImageUsageRules applies the declared usage rules to that response, the
// way server/internal/usagerules does: every declared path must resolve, and
// the images fact is the metering key an administrator writes a price
// expression against, e.g. u("images") * 0.03 (ARCHITECTURE 7.3).
func TestImageUsageRules(t *testing.T) {
	e := decodeManifest(t).Platforms[0].Endpoints[0]
	u := e.Usage
	if u == nil || u.Semantics != "inclusive" {
		t.Fatalf("endpoint usage = %+v", u)
	}
	if len(u.SSE) != 0 {
		t.Errorf("Ark image generation is synchronous JSON; sse rules = %+v", u.SSE)
	}
	if u.JSON == nil {
		t.Fatal("endpoint usage has no json rules")
	}
	// Only the fields Ark actually reports: there is no input_tokens and no
	// cache accounting on image generation.
	want := map[string]string{
		manifest.UsageModel:        "model",
		manifest.UsageOutputTokens: "usage.output_tokens",
	}
	if len(u.JSON.Map) != len(want) {
		t.Fatalf("json map = %v, want %v", u.JSON.Map, want)
	}
	for field, path := range want {
		if u.JSON.Map[field] != path {
			t.Fatalf("json map[%s] = %q, want %q", field, u.JSON.Map[field], path)
		}
	}
	for field, path := range u.JSON.Map {
		if r := gjson.Get(arkImageResponse, path); !r.Exists() {
			t.Errorf("json map[%s]: path %q is not in a documented Ark image response", field, path)
		}
	}
	if got := gjson.Get(arkImageResponse, u.JSON.Map[manifest.UsageOutputTokens]).Int(); got != 53400 {
		t.Errorf("output tokens = %d, want 53400", got)
	}

	// The metering key. Prices belong to the core and are set per model by an
	// administrator (CONTRACTS §17), so the manifest declares the key only.
	if len(u.Facts) != 1 {
		t.Fatalf("facts = %v, want exactly \"images\"", u.Facts)
	}
	f, ok := u.Facts["images"]
	if !ok || f.Type != "number" || f.Path != "usage.generated_images" {
		t.Fatalf("facts[images] = %+v", f)
	}
	if f.Description["en"] == "" || f.Description["zh"] == "" {
		t.Errorf("facts[images] needs an en and zh description: %v", f.Description)
	}
	r := gjson.Get(arkImageResponse, f.Path)
	if !r.Exists() || r.Int() != 3 {
		t.Errorf("facts[images] resolved to %v, want 3", r.Value())
	}
	if len(gjson.Get(arkImageResponse, "data").Array()) != int(r.Int()) {
		t.Error("usage.generated_images must count the delivered images")
	}
}

// builtin returns the core's built-in openai platform. The definitions live
// in the SDK (sdk/platforms) precisely so a plugin can check itself against
// them (CONTRACTS §13).
func builtin(t *testing.T) *manifest.Platform {
	t.Helper()
	for _, p := range platforms.Builtin() {
		if p.ID == volcengine.PlatformID {
			return &p
		}
	}
	t.Fatalf("no built-in platform %q", volcengine.PlatformID)
	return nil
}

func mustFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(p))
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

// builtinNamed returns one built-in platform by id.
func builtinNamed(t *testing.T, id string) *manifest.Platform {
	t.Helper()
	for _, p := range platforms.Builtin() {
		if p.ID == id {
			return &p
		}
	}
	t.Fatalf("no built-in platform %q", id)
	return nil
}

// New endpoint settings are optional and have no defaults: merely editing an
// old account must not override its existing video prefix or asset URL.
func TestEndpointFormsPreserveLegacyAccounts(t *testing.T) {
	// The endpoint fields themselves are checked by ValidateCredentials
	// (TestRelayFormConstraints): optional, no default, a URL or a path.
	m := decodeManifest(t)
	for _, at := range m.AccountTypes {
		if at.ID == "apikey" && at.Label["zh"] != "字节火山方舟 · 官方通用" || at.ID == "relay" && at.Label["zh"] != "豆包视频" {
			t.Fatalf("unexpected account label: %+v", at.Label)
		}
	}
}

// TestV1AliasEndpoints pins the /v1/contents/... aliases for the legacy Ark
// video API. The old Ark video endpoint was /v1/contents/generations/tasks;
// new-api's DoubaoVideoLegacyAPI just replaces /api/v3 with /v1 in the request
// body (the report formats are identical). These aliases let clients configured
// for the old endpoint keep working. Like the doubao aliases, billing, metering
// and polling must be identical to the primary /api/v3 endpoints.
func TestV1AliasEndpoints(t *testing.T) {
	p := decodeManifest(t).Platforms[0]
	byID := map[string]manifest.Endpoint{}
	for _, e := range p.Endpoints {
		byID[e.ID] = e
	}
	// Only video endpoints have v1_ aliases; images does not.
	videoIDs := []string{"video_submit", "video_query"}
	for _, id := range videoIDs {
		primary, ok := byID[id]
		if !ok {
			t.Fatalf("no %s endpoint", id)
		}
		alias, ok := byID["v1_"+id]
		if !ok {
			t.Fatalf("no v1_%s alias", id)
		}
		// The v1 path replaces /api/v3 with /v1.
		if want := strings.Replace(primary.Path, "/api/v3", "/v1", 1); alias.Path != want {
			t.Errorf("v1_%s path = %q, want %q", id, alias.Path, want)
		}
		if primary.Task != nil {
			if alias.Task == nil || primary.Task.Kind != "video" || alias.Task.Kind != "v1_video" {
				t.Fatalf("%s task kinds = %+v / %+v, want video / v1_video", id, primary.Task, alias.Task)
			}
		}
		// Everything else must be identical.
		norm := func(e manifest.Endpoint) string {
			e.ID, e.Path = "", ""
			if e.Task != nil {
				task := *e.Task
				task.Kind = ""
				e.Task = &task
			}
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		if a, b := norm(primary), norm(alias); a != b {
			t.Errorf("v1_%s differs from %s beyond id, path and task kind:\n primary %s\n alias   %s", id, id, a, b)
		}
	}
}

// TestUpstreamFieldsNameTheTaskHosts: Monitor's fetch may reach the hosts of
// the account's declared upstreamFields besides externalServices, so every
// field taskQuery takes an address from must be declared: base_url on both
// types, video_endpoint (an absolute URL is allowed) on the relay type.
func TestUpstreamFieldsNameTheTaskHosts(t *testing.T) {
	want := map[string][]string{
		volcengine.AccountTypeAPIKey: {"base_url"},
		volcengine.AccountTypeRelay:  {"base_url", volcengine.FieldVideoEndpoint},
	}
	for _, at := range decodeManifest(t).AccountTypes {
		for _, field := range want[at.ID] {
			if !slices.Contains(at.UpstreamFields, field) {
				t.Errorf("%s upstreamFields = %v, missing %s", at.ID, at.UpstreamFields, field)
			}
		}
	}
}
