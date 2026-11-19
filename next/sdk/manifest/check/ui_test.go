package check

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// ---------------------------------------------------------------- icons

// TestEmbeddedIconsMatchTheSource is the whole reason icons.json may be a
// generated file. It re-hashes the TypeScript module the names come from and
// fails when the recorded digest differs, so "added an icon to icons.ts and
// forgot to regenerate" is a red test naming the command to run - not a
// manifest whose valid icon the validator rejects, or an invalid one it
// accepts, which is the class of silent failure this validation was added to
// remove in the first place.
//
// It hashes the source rather than re-deriving the names because a second,
// looser TypeScript parser written in Go is a thing that eventually disagrees
// with the real one. The cost of hashing is that a comment-only edit to
// icons.ts also asks for a regeneration; the fix is one idempotent command.
//
// The source is looked up by walking up from this file, and a checkout without
// it is a FAILURE, never a skip (CONTRACTS §26.4: "found nothing, so skipped"
// is how a cross-module test stays green for months while checking nothing).
// Only this repository runs this module's tests; a plugin module that imports
// sdk/manifest/check runs its own.
func TestEmbeddedIconsMatchTheSource(t *testing.T) {
	path := findRepoFile(t, IconSource())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got, want := HashIconSource(b), IconSourceSHA256(); got != want {
		t.Fatalf("icons.json is stale: %s hashes to %s but icons.json records %s.\n"+
			"Run \"npm run icons:json\" in next/web and commit the regenerated "+
			"sdk/manifest/check/icons.json.", IconSource(), got, want)
	}
	// The digest alone would also be satisfied by an icons.json with a correct
	// hash and a mangled name list, so check the list is usable on its own.
	names := IconNames()
	if len(names) == 0 {
		t.Fatal("icons.json lists no names")
	}
	for i, n := range names {
		if n == "" || strings.ContainsAny(n, " \t\"'/\\") {
			t.Errorf("icon name %q is not a usable name", n)
		}
		if i > 0 && names[i-1] >= n {
			t.Errorf("icon names are not sorted and unique at %q (previous %q)", n, names[i-1])
		}
	}
	// The names the repository's own manifests use must be in there; if they
	// are not, the new rule has false positives and the gate below would only
	// say "unknown icon".
	for _, n := range []string{"list", "shield", "inbox", "group", "plus", "search"} {
		if !KnownIcon(n) {
			t.Errorf("icon %q is used by a manifest or by the console but is not in icons.json", n)
		}
	}
	if KnownIcon("definitely-not-an-icon") {
		t.Error("KnownIcon accepts a name that is not in the set")
	}
}

// HashIconSource must give the same digest for a CRLF and an LF checkout:
// .gitattributes pins *.ts to LF, but git core.autocrlf is on on at least one
// developer machine and a hash that depended on it would make this test fail
// on one platform and pass on the other - a gate nobody would trust.
func TestHashIconSourceIgnoresLineEndingsAndBOM(t *testing.T) {
	lf := []byte("export const a = 1\nexport const b = 2\n")
	crlf := []byte("export const a = 1\r\nexport const b = 2\r\n")
	bom := append([]byte{0xEF, 0xBB, 0xBF}, lf...)
	if HashIconSource(lf) != HashIconSource(crlf) || HashIconSource(lf) != HashIconSource(bom) {
		t.Fatal("HashIconSource must normalise CRLF and a leading BOM")
	}
	if HashIconSource(lf) == HashIconSource([]byte("export const a = 2\n")) {
		t.Fatal("HashIconSource must distinguish different content")
	}
}

// The staleness gate is proved directly rather than inferred: apply the exact
// mistake it exists to catch - an icon added to icons.ts without regenerating
// icons.json - to the real source bytes and require the recorded digest to
// disagree. (CONTRACTS §26.7: do not prove B by watching A fail.)
func TestAnUnregeneratedIconsTSIsDetected(t *testing.T) {
	b, err := os.ReadFile(findRepoFile(t, IconSource()))
	if err != nil {
		t.Fatalf("read %s: %v", IconSource(), err)
	}
	edited := append(append([]byte{}, b...), []byte("\nexport const NEW_ICON = ['M0 0h24v24H0z']\n")...)
	if HashIconSource(edited) == IconSourceSHA256() {
		t.Fatal("editing icons.ts left the recorded digest unchanged; the staleness gate cannot fire")
	}
	// ...and the unedited source still matches, so the gate is not simply
	// always red.
	if HashIconSource(b) != IconSourceSHA256() {
		t.Fatalf("icons.json is stale: run \"npm run icons:json\" in next/web")
	}
}

// findRepoFile locates a repository-relative path by walking up from this
// source file. It fails the test when the file is absent rather than skipping.
func findRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(self)
	for i := 0; i < 12; i++ {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("cannot find %s above %s; this test must not be skipped - it is the only thing that "+
		"notices sdk/manifest/check/icons.json going stale", rel, filepath.Dir(self))
	return ""
}

// ---------------------------------------------------------------- ui rules

// uiManifest is a manifest with one admin route, one native page and one menu
// pointing at the page - the shape the ui rules act on.
func uiManifest() *manifest.Manifest {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapHTTPRoutes}}
	m.HostPermissions = []manifest.HostPermission{{ID: "ui.menu"}, {ID: "routes.admin"}, {ID: "ui.native"}}
	m.HostUICompat = "^4.0"
	m.UserPermissions = []manifest.UserPermission{{Key: "assets:read", Label: manifest.LocalizedText{"en": "Read assets"}}}
	m.Routes = []manifest.Route{
		{Method: "GET", Path: "/assets", Scope: "admin", Permission: "assets:read"},
		{Method: "GET", Path: "/assets/:id", Scope: "admin", Permission: "assets:read"},
	}
	m.UI = &manifest.UI{
		Menus:  []manifest.Menu{{ID: "assets", Section: "plugins", Label: manifest.LocalizedText{"en": "Assets"}, Icon: "inbox", Page: "assets"}},
		Pages:  map[string]manifest.Page{"assets": {Type: "native", Title: manifest.LocalizedText{"en": "Assets"}, Component: "Assets"}},
		Native: &manifest.NativeUI{Entry: "ui/native/entry.js"},
	}
	return m
}

func uiCodes(m *manifest.Manifest) map[string]string {
	return codes(Validate(m, nil, ValidateOptions{Tooling: true}))
}

// The fixture itself must be valid, otherwise every case below could pass for
// the wrong reason.
func TestUIManifestFixtureIsValid(t *testing.T) {
	if c := uiCodes(uiManifest()); len(c) > 0 {
		t.Fatalf("fixture must validate cleanly: %v", c)
	}
}

// An icon name that <SIcon> cannot draw is rejected at install time. In the
// browser it used to render a plain square with no error and no console
// output, so a typo looked like a deliberate choice.
func TestMenuIconMustBeKnown(t *testing.T) {
	m := uiManifest()
	m.UI.Menus[0].Icon = "inbocks"
	c := uiCodes(m)
	if c["ui.menus[0].icon"] != "unknown_icon" {
		t.Fatalf("unknown icon must be rejected: %v", c)
	}
	// No icon at all is allowed: the console falls back to "puzzle".
	m.UI.Menus[0].Icon = ""
	if c := uiCodes(m); len(c) > 0 {
		t.Fatalf("an absent icon is legal: %v", c)
	}
	// Every name in the set is accepted, so the rule cannot be satisfied only
	// by the handful used today.
	for _, n := range IconNames() {
		m.UI.Menus[0].Icon = n
		if c := uiCodes(m); len(c) > 0 {
			t.Fatalf("icon %q from the set was rejected: %v", n, c)
		}
	}
	// The plugin avatar is a different vocabulary and must NOT be held to the
	// icon set: every shipped plugin uses "text:X", which is not an icon name.
	m = uiManifest()
	m.Icon = "text:V"
	if c := uiCodes(m); len(c) > 0 {
		t.Fatalf("top-level icon %q must not be checked against the icon set: %v", m.Icon, c)
	}
}

// Menus join any core sidebar section by id; those ids are reserved, so a
// plugin cannot declare its own section with one. "safety" stays free: the
// moderation plugin declared it before the core grew "security".
func TestMenuSections(t *testing.T) {
	for _, sec := range []string{"plugins", "overview", "gateway", "security", "finance", "users", "system", "me"} {
		m := uiManifest()
		m.UI.Menus[0].Section = sec
		if c := uiCodes(m); len(c) > 0 {
			t.Fatalf("core section %q must be accepted: %v", sec, c)
		}
	}
	m := uiManifest()
	m.UI.Menus[0].Section = "nowhere"
	if c := uiCodes(m); c["ui.menus[0].section"] != "invalid" {
		t.Fatalf("an undeclared section must be rejected: %v", c)
	}
	for _, id := range []string{"security", "users", "plugins"} {
		m := uiManifest()
		m.UI.Sections = []manifest.MenuSection{{ID: id, Label: manifest.LocalizedText{"en": "Mine"}}}
		if c := uiCodes(m); c["ui.sections[0].id"] != "invalid" {
			t.Fatalf("section id %q is reserved for the core: %v", id, c)
		}
	}
	m = uiManifest()
	m.UI.Sections = []manifest.MenuSection{{ID: "safety", Label: manifest.LocalizedText{"en": "Safety"}, Order: 350}}
	m.UI.Menus[0].Section = "safety"
	if c := uiCodes(m); len(c) > 0 {
		t.Fatalf("a plugin's own section must stay valid: %v", c)
	}
}

// Declarative pages (table, form) are no longer rendered: a package that
// declares one is refused as unsupported (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY).
func TestDeclarativePagesUnsupported(t *testing.T) {
	for _, typ := range []string{"table", "form"} {
		m := uiManifest()
		m.UI.Pages["assets"] = manifest.Page{Type: typ, Title: manifest.LocalizedText{"en": "Assets"}}
		if c := uiCodes(m); c["ui.pages.assets.type"] != "unsupported" || len(c) != 1 {
			t.Errorf("%s page: %v", typ, c)
		}
	}
}

// ui.settings.permission names one of the plugin's own permissions; other
// forms cannot take one (the core only checks it on the settings endpoints).
func TestSettingsPermission(t *testing.T) {
	m := uiManifest()
	var files map[string][]byte
	m.UI.Settings = &manifest.Form{Mode: "native", Component: "Settings", Secrets: []manifest.SettingsSecret{}, Permission: "assets:read"}
	if c := codes(Validate(m, files, ValidateOptions{Tooling: true})); len(c) > 0 {
		t.Fatalf("a declared permission is valid: %v", c)
	}
	m.UI.Settings.Permission = "assets:write"
	if c := codes(Validate(m, files, ValidateOptions{Tooling: true})); c["ui.settings.permission"] != "unknown" {
		t.Fatalf("an undeclared permission must be rejected: %v", c)
	}
	// The settings form is authorized on the permission as a whole.
	m.UI.Settings.Permission = "assets:read"
	m.UserPermissions[0].Scope = &manifest.PermissionScope{Resource: "asset", Own: true}
	if c := codes(Validate(m, files, ValidateOptions{Tooling: true})); c["ui.settings.permission"] != "unsupported" {
		t.Fatalf("a scoped permission must be rejected: %v", c)
	}
	m.UserPermissions[0].Scope = nil
	m.UI.Settings.Permission = ""
	m.AccountTypes = []manifest.AccountType{{Form: manifest.Form{Mode: "native", Component: "X", Permission: "assets:read"}}}
	if c := codes(Validate(m, files, ValidateOptions{Tooling: true})); c["accountTypes[0].form.permission"] != "unsupported" {
		t.Fatalf("only ui.settings takes a permission: %v", c)
	}
}

// Native settings edited on the plugin's own page name one of its native
// pages.
func TestNativeSettingsPage(t *testing.T) {
	m := uiManifest()
	m.UI.Settings = &manifest.Form{Mode: "native", Page: "assets", Secrets: []manifest.SettingsSecret{}}
	if c := uiCodes(m); len(c) > 0 {
		t.Fatalf("native settings on a native page are valid: %v", c)
	}
	m.UI.Settings.Page = "missing"
	if c := uiCodes(m); c["ui.settings.page"] != "unknown" {
		t.Fatalf("the page must be a native page: %v", c)
	}
}

// Schema forms are no longer rendered: an account type or settings form in
// mode schema is refused as unsupported, so a new package cannot rely on a
// form the console does not draw (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY).
func TestSchemaFormsUnsupported(t *testing.T) {
	m := uiManifest()
	m.UI.Settings = &manifest.Form{Mode: "schema"}
	if c := uiCodes(m); c["ui.settings.mode"] != "unsupported" || len(c) != 1 {
		t.Fatalf("schema settings: %v", c)
	}
	m = controlsManifest(nil)
	m.AccountTypes[0].Form = manifest.Form{Mode: "schema"}
	if c := controlCodes(m); c["accountTypes[0].form.mode"] != "unsupported" || len(c) != 1 {
		t.Fatalf("schema account form: %v", c)
	}
}

// uiNativeManifest is uiManifest with a native UI on the given hostUICompat
// and hostCompat, the shape slot declarations need.
func uiNativeManifest(uiCompat, hostCompat string) *manifest.Manifest {
	m := uiManifest()
	m.HostUICompat, m.HostCompat = uiCompat, hostCompat
	return m
}

// The known slots are accepted, unknown slot names rejected.
func TestSlotNames(t *testing.T) {
	for _, slot := range []string{"dashboard.widgets", "account.detail.tabs", "account.form.widgets", "account.list.weight", "account.list.toolbar"} {
		m := uiNativeManifest("^3.3", ">=0.1.256 <0.2.0")
		m.UI.Slots = []manifest.Slot{{Slot: slot, Component: "X", Permission: "assets:read"}}
		if c := uiCodes(m); len(c) > 0 {
			t.Errorf("slot %q must be accepted: %v", slot, c)
		}
	}
	m := uiNativeManifest("^3.3", ">=0.1.256 <0.2.0")
	m.UI.Slots = []manifest.Slot{{Slot: "account.list.footer", Component: "X"}}
	if c := uiCodes(m); c["ui.slots[0].slot"] != "invalid" {
		t.Fatalf("an unknown slot must be rejected: %v", c)
	}
}

// account.editor.section (UI 3.7) carries a label, a known icon and
// optional account type ids; the other slots take none of these fields.
func TestEditorSectionSlot(t *testing.T) {
	section := manifest.Slot{Slot: manifest.SlotAccountEditorSection, Component: "X", Label: manifest.LocalizedText{"en": "Extras"}, Icon: "refresh"}
	m := uiNativeManifest("^3.7", ">=0.1.262 <0.2.0")
	m.UI.Slots = []manifest.Slot{section}
	if c := uiCodes(m); len(c) > 0 {
		t.Fatalf("a valid editor section must be accepted: %v", c)
	}
	m = uiNativeManifest("^3.7", ">=0.1.262 <0.2.0")
	m.UI.Slots = []manifest.Slot{{Slot: manifest.SlotAccountEditorSection, Component: "X", Icon: "no-such-icon", AccountTypes: []string{"apikey"}}}
	c := uiCodes(m)
	if c["ui.slots[0].label"] != "required" || c["ui.slots[0].icon"] != "unknown_icon" || c["ui.slots[0].accountTypes[0]"] != "invalid" {
		t.Fatalf("editor section fields (apikey is not an account type of this plugin): %v", c)
	}
	// accountTypes name the plugin's own types, once each.
	m = controlsManifest(nil)
	m.HostUICompat = "^3.7"
	section.AccountTypes = []string{"apikey"}
	m.UI = &manifest.UI{Native: &manifest.NativeUI{Entry: "ui/native/entry.js"}, Slots: []manifest.Slot{section}}
	if c := controlCodes(m); len(c) > 0 {
		t.Fatalf("a section for an own account type must be accepted: %v", c)
	}
	m.UI.Slots[0].AccountTypes = []string{"apikey", "apikey"}
	if c := controlCodes(m); c["ui.slots[0].accountTypes[1]"] != "invalid" || len(c) != 1 {
		t.Fatalf("a repeated account type must be refused: %v", c)
	}
	m.UI.Slots = []manifest.Slot{{Slot: "account.detail.tabs", Component: "X", Label: manifest.LocalizedText{"en": "Tab"}}}
	if c := uiCodes(m); c["ui.slots[0]"] != "unsupported" {
		t.Fatalf("label on another slot must be refused: %v", c)
	}
}

// typedManifest is controlsManifest (one account type, apikey) with a native
// UI, a detail tab, a quota column decoration and a bulk account action all
// limited to apikey, on hostUICompat ^3.8 and the given hostCompat.
func typedManifest(hostCompat string) *manifest.Manifest {
	m := controlsManifest(nil)
	m.HostCompat, m.HostUICompat = hostCompat, "^3.8"
	m.Capabilities = append(m.Capabilities, manifest.Capability{ID: manifest.CapHTTPRoutes})
	m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "routes.admin"})
	m.UserPermissions = []manifest.UserPermission{{Key: "quota:reset", Label: manifest.LocalizedText{"en": "Reset quota"}}}
	m.Routes = []manifest.Route{{Method: "POST", Path: "/quota/reset", Scope: "admin", Permission: "quota:reset"}}
	m.UI = &manifest.UI{
		Native: &manifest.NativeUI{Entry: "ui/native/entry.js"},
		Slots: []manifest.Slot{
			{Slot: manifest.SlotAccountDetailTabs, Component: "QuotaTab", AccountTypes: []string{"apikey"}},
			{Slot: manifest.SlotAccountListQuota, Component: "QuotaBadge", AccountTypes: []string{"apikey"}},
		},
		AccountActions: []manifest.AccountAction{{ID: "reset", Label: manifest.LocalizedText{"en": "Reset"}, Permission: "quota:reset",
			Route: "POST /quota/reset", Bulk: true, AccountTypes: []string{"apikey"}}},
	}
	return m
}

// account.detail.tabs, account.list.quota and ui.accountActions take
// accountTypes (UI 3.8) under the editor section's rules - the plugin's own
// types, once each.
func TestUI38TypedExtensions(t *testing.T) {
	if c := controlCodes(typedManifest(">=0.1.269 <0.2.0")); len(c) > 0 {
		t.Fatalf("typed UI 3.8 declarations must be accepted: %v", c)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*manifest.UI)
		want   map[string]string
	}{
		{"foreign types", func(u *manifest.UI) {
			u.Slots[0].AccountTypes = []string{"oauth"}
			u.Slots[1].AccountTypes = []string{"oauth"}
			u.AccountActions[0].AccountTypes = []string{"apikey", "oauth"}
		}, map[string]string{"ui.slots[0].accountTypes[0]": "invalid", "ui.slots[1].accountTypes[0]": "invalid", "ui.accountActions[0].accountTypes[1]": "invalid"}},
		{"repeated types", func(u *manifest.UI) {
			u.Slots[0].AccountTypes = []string{"apikey", "apikey"}
			u.Slots[1].AccountTypes = []string{"apikey", "apikey"}
			u.AccountActions[0].AccountTypes = []string{"apikey", "apikey"}
		}, map[string]string{"ui.slots[0].accountTypes[1]": "invalid", "ui.slots[1].accountTypes[1]": "invalid", "ui.accountActions[0].accountTypes[1]": "invalid"}},
		{"label and icon stay editor-section only", func(u *manifest.UI) {
			u.Slots[0].Icon = "refresh"
			u.Slots[1].Label = manifest.LocalizedText{"en": "Quota"}
		}, map[string]string{"ui.slots[0]": "unsupported", "ui.slots[1]": "unsupported"}},
		{"other slots take no accountTypes", func(u *manifest.UI) { u.Slots[0].Slot = "account.form.widgets" },
			map[string]string{"ui.slots[0]": "unsupported"}},
	} {
		m := typedManifest(">=0.1.269 <0.2.0")
		tc.mutate(m.UI)
		if c := controlCodes(m); !maps.Equal(c, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, c, tc.want)
		}
	}
}

// withAccountAction adds a POST /assets/reset admin route (plus two
// parameterized ones) and one account action over it to the ui fixture (no
// ui.native: actions need none).
func withAccountAction(hostCompat string, act manifest.AccountAction) *manifest.Manifest {
	m := uiManifest()
	m.HostCompat = hostCompat
	m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "routes.public"})
	m.UserPermissions = append(m.UserPermissions, manifest.UserPermission{Key: "assets:manage", Label: manifest.LocalizedText{"en": "Manage assets"}})
	m.Routes = append(m.Routes,
		manifest.Route{Method: "POST", Path: "/assets/reset", Scope: "admin", Permission: "assets:manage"},
		manifest.Route{Method: "POST", Path: "/assets/:id/reset", Scope: "admin", Permission: "assets:manage"},
		manifest.Route{Method: "POST", Path: "/assets/*rest", Scope: "admin", Permission: "assets:manage"},
		manifest.Route{Method: "POST", Path: "/assets/ping", Scope: "public"})
	m.UI.AccountActions = []manifest.AccountAction{act}
	return m
}

// ui.accountActions: each entry names a declared POST admin route carrying
// the same permission, so the menu shows exactly what the route allows.
func TestAccountActions(t *testing.T) {
	good := manifest.AccountAction{ID: "reset", Label: manifest.LocalizedText{"en": "Reset"}, Icon: "refresh",
		Permission: "assets:manage", Route: "POST /assets/reset", Bulk: true, Confirm: manifest.LocalizedText{"en": "Reset {n}?"}}
	if c := uiCodes(withAccountAction(">=0.1.256 <0.2.0", good)); len(c) > 0 {
		t.Fatalf("a valid action was rejected: %v", c)
	}
	m := withAccountAction(">=0.1.256 <0.2.0", good)
	m.UI.AccountActions = append(m.UI.AccountActions, good)
	if c := uiCodes(m); c["ui.accountActions[1].id"] != "duplicate" {
		t.Fatalf("a repeated id must be rejected: %v", c)
	}
	cases := []struct {
		name   string
		mutate func(*manifest.AccountAction)
		field  string
		code   string
	}{
		{"bad id", func(a *manifest.AccountAction) { a.ID = "Reset!" }, "id", "invalid_format"},
		{"no label", func(a *manifest.AccountAction) { a.Label = nil }, "label", "required"},
		{"unknown icon", func(a *manifest.AccountAction) { a.Icon = "refrsh" }, "icon", "unknown_icon"},
		{"no permission", func(a *manifest.AccountAction) { a.Permission = "" }, "permission", "required"},
		{"unknown permission", func(a *manifest.AccountAction) { a.Permission = "assets:write" }, "permission", "unknown"},
		{"not a route ref", func(a *manifest.AccountAction) { a.Route = "POST assets/reset" }, "route", "invalid_route_ref"},
		{"undeclared route", func(a *manifest.AccountAction) { a.Route = "POST /assets/nope" }, "route", "unknown_route"},
		{"GET route", func(a *manifest.AccountAction) { a.Route = "GET /assets" }, "route", "invalid"},
		{"public route", func(a *manifest.AccountAction) { a.Route = "POST /assets/ping" }, "route", "invalid"},
		{"route permission differs", func(a *manifest.AccountAction) { a.Permission = "assets:read" }, "route", "permission_mismatch"},
		{"param route", func(a *manifest.AccountAction) { a.Route = "POST /assets/:id/reset" }, "route", "parameterized_route"},
		{"wildcard route", func(a *manifest.AccountAction) { a.Route = "POST /assets/*rest" }, "route", "parameterized_route"},
		{"visibleFor bad", func(a *manifest.AccountAction) { a.VisibleFor = "owners" }, "visibleFor", "invalid"},
	}
	for _, tc := range cases {
		act := good
		tc.mutate(&act)
		if c := uiCodes(withAccountAction(">=0.1.256 <0.2.0", act)); c["ui.accountActions[0]."+tc.field] != tc.code {
			t.Errorf("%s: want %s = %s, got %v", tc.name, tc.field, tc.code, c)
		}
	}
	// The core checks an action on accounts alone, so a plugin scope on its
	// permission would mean nothing.
	m = withAccountAction(">=0.1.290 <0.2.0", good)
	m.UserPermissions[1].Scope = &manifest.PermissionScope{Resource: "asset", Own: true}
	if c := uiCodes(m); c["ui.accountActions[0].permission"] != "unsupported" {
		t.Errorf("an action over a scoped permission must be rejected: %v", c)
	}
	// Like a form submit, the method may be left out and means POST.
	act := good
	act.Route = "/assets/reset"
	if c := uiCodes(withAccountAction(">=0.1.256 <0.2.0", act)); len(c) > 0 {
		t.Fatalf("a bare path means POST: %v", c)
	}
	// A route that declares accounts itself must still read what the console
	// posts.
	m = withAccountAction(">=0.1.286 <0.2.0", good)
	m.Routes[2].Accounts = &manifest.RouteAccounts{Action: "update", From: []string{"body:account_id"}}
	if c := uiCodes(m); c["ui.accountActions[0].route"] != "accounts_mismatch" {
		t.Errorf("an action route whose accounts miss body:account_ids must be rejected: %v", c)
	}
	m.Routes[2].Accounts.From = append(m.Routes[2].Accounts.From, manifest.AccountActionIDs)
	if c := uiCodes(m); len(c) > 0 {
		t.Errorf("an action route reading body:account_ids is valid: %v", c)
	}
	// visibleFor takes its two values; empty means "editable".
	for _, vf := range []string{"editable", "all", ""} {
		act := good
		act.VisibleFor = vf
		if c := uiCodes(withAccountAction(">=0.1.256 <0.2.0", act)); len(c) > 0 {
			t.Errorf("visibleFor %q is valid: %v", vf, c)
		}
	}
}

// A native page may list its sections for the console's sidebar (UI 3.14).
func TestPageNav(t *testing.T) {
	label := manifest.LocalizedText{"en": "x"}
	valid := func() *manifest.PageNav {
		return &manifest.PageNav{Groups: []manifest.NavGroup{
			{Key: "rules", Label: label, Items: []manifest.NavItem{
				{Key: "words", Label: label, Icon: "inbox", Permission: "assets:read"},
				{Key: "models", Label: label, CorePermission: "settings:read"},
			}},
			{Key: "logs", Label: label, Items: []manifest.NavItem{{Key: "hits", Label: label}}},
		}}
	}
	cases := []struct {
		name   string
		typ    string
		mutate func(*manifest.PageNav)
		field  string // "" means the manifest is valid
		code   string
	}{
		{name: "valid nav", typ: "native"},
		{name: "nav on an iframe page", typ: "iframe", field: "ui.pages.panel.nav", code: "unsupported"},
		{name: "no groups", typ: "native", mutate: func(n *manifest.PageNav) { n.Groups = nil },
			field: "ui.pages.panel.nav.groups", code: "required"},
		{name: "item key repeated in another group", typ: "native",
			mutate: func(n *manifest.PageNav) { n.Groups[1].Items[0].Key = "words" },
			field:  "ui.pages.panel.nav.groups[1].items[0].key", code: "duplicate"},
		{name: "bad item key", typ: "native", mutate: func(n *manifest.PageNav) { n.Groups[0].Items[0].Key = "Words" },
			field: "ui.pages.panel.nav.groups[0].items[0].key", code: "invalid_format"},
		{name: "unknown permission", typ: "native", mutate: func(n *manifest.PageNav) { n.Groups[0].Items[0].Permission = "assets:write" },
			field: "ui.pages.panel.nav.groups[0].items[0].permission", code: "unknown"},
		{name: "blank core permission", typ: "native", mutate: func(n *manifest.PageNav) { n.Groups[0].Items[1].CorePermission = " " },
			field: "ui.pages.panel.nav.groups[0].items[1].corePermission", code: "invalid"},
		{name: "unknown icon", typ: "native", mutate: func(n *manifest.PageNav) { n.Groups[0].Items[0].Icon = "inbocks" },
			field: "ui.pages.panel.nav.groups[0].items[0].icon", code: "unknown_icon"},
		{name: "missing group label", typ: "native", mutate: func(n *manifest.PageNav) { n.Groups[0].Label = nil },
			field: "ui.pages.panel.nav.groups[0].label", code: "required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := uiNativeManifest("^3.14", ">=0.1.284 <0.2.0")
			nav := valid()
			if tc.mutate != nil {
				tc.mutate(nav)
			}
			pg := manifest.Page{Type: "native", Component: "Panel", Nav: nav}
			if tc.typ == "iframe" {
				m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "ui.iframe"})
				pg = manifest.Page{Type: "iframe", Src: "ui/panel.html", Nav: nav}
			}
			m.UI.Pages["panel"] = pg
			c := uiCodes(m)
			if tc.field == "" {
				if len(c) > 0 {
					t.Fatalf("must be valid: %v", c)
				}
				return
			}
			if c[tc.field] != tc.code {
				t.Fatalf("%s = %q, want %q (%v)", tc.field, c[tc.field], tc.code, c)
			}
		})
	}
}

func TestParseRouteRef(t *testing.T) {
	cases := []struct {
		ref, def     string
		method, path string
		ok           bool
	}{
		{"GET /models", "GET", "GET", "/models", true},
		{"get /models", "GET", "GET", "/models", true},
		{"  POST   /assets  ", "GET", "POST", "/assets", true},
		{"/models", "GET", "GET", "/models", true},
		{"/assets", "POST", "POST", "/assets", true},
		{"DELETE /assets/:id", "GET", "DELETE", "/assets/:id", true},
		{"", "GET", "", "", false},
		{"models", "GET", "", "", false},
		{"HEAD /models", "GET", "", "", false},
		{"GET /a b", "GET", "", "", false},
		{"GET", "GET", "", "", false},
	}
	for _, tc := range cases {
		method, path, ok := ParseRouteRef(tc.ref, tc.def)
		if ok != tc.ok || method != tc.method || path != tc.path {
			t.Errorf("ParseRouteRef(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.ref, tc.def, method, path, ok, tc.method, tc.path, tc.ok)
		}
	}
}

// ui.settings.secrets: only the settings form takes them, each path a
// maskable pointer, none repeating or inside another.
func TestSettingsSecretsCheck(t *testing.T) {
	m := uiManifest()
	files := map[string][]byte{"ui/native/entry.js": []byte("")}
	validate := func() map[string]string { return codes(Validate(m, files, ValidateOptions{Tooling: true})) }

	m.UI.Settings = &manifest.Form{Mode: "native", Component: "Settings", Secrets: []manifest.SettingsSecret{
		{Path: "password"}, {Path: "notify/url", Sensitive: true}, {Path: "notify/secret"}, {Path: "a~1b"}}}
	if c := validate(); len(c) > 0 {
		t.Fatalf("valid secrets: %v", c)
	}
	m.UI.Settings.Secrets = []manifest.SettingsSecret{{Path: "ok"}, {Path: ""}, {Path: "a//b"}, {Path: "x~2"},
		{Path: strings.Repeat("p/", manifest.MaxSecretDepth) + "p"}}
	c := validate()
	for i := 1; i <= 4; i++ {
		if f := fmt.Sprintf("ui.settings.secrets[%d].path", i); c[f] != "invalid" {
			t.Errorf("%s = %q: %v", f, c[f], c)
		}
	}
	m.UI.Settings.Secrets = []manifest.SettingsSecret{{Path: "notify"}, {Path: "other"}, {Path: "notify/url"}, {Path: "other"}}
	c = validate()
	if c["ui.settings.secrets[2].path"] != "duplicate" || c["ui.settings.secrets[3].path"] != "duplicate" || c["ui.settings.secrets[1].path"] != "" {
		t.Fatalf("overlapping paths: %v", c)
	}

	m.UI.Settings = nil
	m.AccountTypes = []manifest.AccountType{{Form: manifest.Form{Mode: "native", Component: "X",
		Secrets: []manifest.SettingsSecret{{Path: "api_key"}}}}}
	if c := validate(); c["accountTypes[0].form.secrets"] != "unsupported" {
		t.Fatalf("account types use sensitiveFields: %v", c)
	}
}

// Native settings are mounted from a component on the settings tab, or
// edited on one of the plugin's native pages: one of the two is needed.
func TestNativeSettingsNeedComponentOrPage(t *testing.T) {
	m := uiManifest()
	files := map[string][]byte{"ui/native/entry.js": []byte("")}
	m.UI.Settings = &manifest.Form{Mode: "native"}
	if c := codes(Validate(m, files, ValidateOptions{Tooling: true})); c["ui.settings.component"] != "required" {
		t.Fatalf("neither component nor page: %v", c)
	}
	m.UI.Settings.Component, m.UI.Settings.Secrets = "Settings", []manifest.SettingsSecret{}
	if c := codes(Validate(m, files, ValidateOptions{Tooling: true})); len(c) > 0 {
		t.Fatalf("a component is enough: %v", c)
	}
}

// Native or iframe settings say which settings are secret: without the key
// the core masks them all, so it is required; [] declares none.
func TestSettingsSecretsRequired(t *testing.T) {
	m := uiManifest()
	m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "ui.iframe"})
	files := map[string][]byte{"ui/native/entry.js": []byte(""), "ui/iframe/s.html": []byte("")}
	validate := func() map[string]string { return codes(Validate(m, files, ValidateOptions{Tooling: true})) }
	for _, f := range []*manifest.Form{{Mode: "native", Component: "Settings"}, {Mode: "iframe", Page: "ui/iframe/s.html"}} {
		m.UI.Settings = f
		if c := validate(); c["ui.settings.secrets"] != "required" {
			t.Fatalf("%s without secrets: %v", f.Mode, c)
		}
		f.Secrets = []manifest.SettingsSecret{}
		if c := validate(); len(c) > 0 {
			t.Fatalf("%s with secrets []: %v", f.Mode, c)
		}
	}
}
