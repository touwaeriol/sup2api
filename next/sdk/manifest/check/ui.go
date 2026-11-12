package check

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// routeRefRe matches a manifest route reference such as "GET /models" or
// "/models" (method defaulting to the caller's). It is the Go mirror of
// parseRouteRef() in web/src/components/plugin/declarative.ts, which is what
// actually turns Page.Source and Page.Submit into requests; the two must agree
// on what parses, or the host would accept a reference the console cannot
// call.
var routeRefRe = regexp.MustCompile(`^\s*(?i:(GET|POST|PUT|PATCH|DELETE)\s+)?(/\S*)\s*$`)

// ParseRouteRef parses a manifest route reference ("GET /models", "/models")
// into an upper-case method and a path. defaultMethod is used when the
// reference names none: GET for Page.Source, POST for Page.Submit, matching
// the console.
func ParseRouteRef(ref, defaultMethod string) (method, path string, ok bool) {
	m := routeRefRe.FindStringSubmatch(ref)
	if m == nil {
		return "", "", false
	}
	method = strings.ToUpper(m[1])
	if method == "" {
		method = strings.ToUpper(defaultMethod)
	}
	return method, m[2], true
}

// routeRef checks one route reference (a page's source or submit, an account
// action's route) and returns the declared route it names, or nil after
// recording the error.
//
// Until now nothing checked this. A table page whose source did not parse, or
// named a route that does not exist, rendered an empty table with no error:
// the console's parseRouteRef returns null and load() returns, and a form page
// whose submit is missing or unparsable has a Save button that does nothing at
// all. Both are exactly the "declared and nobody honours it" shape - the page
// is not visibly broken, it looks like a page with no rows.
//
// The route is matched by method plus NormalizeRoutePath, the same signature
// routes() uses to find duplicates, so "/assets/:id" in routes and
// "/assets/:asset" in a page reference are the same route (gin binds by
// position, not by name).
func (v *validator) routeRef(field, ref, defaultMethod string) *manifest.Route {
	method, path, ok := ParseRouteRef(ref, defaultMethod)
	if !ok {
		v.add(field, "invalid_route_ref", "%q is not a route reference; expected %q or %q",
			ref, defaultMethod+" /path", "/path")
		return nil
	}
	want := method + " " + NormalizeRoutePath(path)
	for i := range v.m.Routes {
		if r := &v.m.Routes[i]; strings.ToUpper(r.Method)+" "+NormalizeRoutePath(r.Path) == want {
			return r
		}
	}
	v.add(field, "unknown_route", "route %s %s is not declared in routes", method, path)
	return nil
}

// searchParamRe is what a declared search parameter name may look like: the
// same grammar as a gateway endpoint's request.queryParams (§26.6), because it
// is the same thing - a query key a client types and a plugin reads.
var searchParamRe = queryParamRe

// tableQueryParams are the query parameters the host's table page sends on
// every request by itself (DeclarativeTable). A search parameter may not be
// one of them: the search box would then overwrite the page number on every
// keystroke, and it would look like a search that silently resets nothing.
var tableQueryParams = []string{"page", "page_size"}

// pageSearch validates Page.Search, the declaration that makes the host render
// a search box (CONTRACTS: manifest.Page.search).
//
// There is one name, not a list, so there is nothing to de-duplicate here -
// see the field's own comment for why a list would be the wrong shape while
// the host has exactly one text box to draw.
//
// The name is compared to the host's own parameters case-insensitively even
// though the host sends them lower-case and plugins read them exactly. "Page"
// would not in fact collide today; accepting it would make the rule depend on
// the case a plugin author happened to type, and on pluginsdk.Query staying
// case-sensitive forever. The same reasoning is already settled for
// request.queryParams vs auth.query in §26.6.
func (v *validator) pageSearch(field string, pg manifest.Page) {
	if pg.Search == "" {
		return
	}
	// Only a table page has a search box. On a form, iframe or native page the
	// declaration would be read by nobody, which is the failure this field was
	// introduced to stop rather than reproduce.
	if pg.Type != "table" {
		v.add(field, "unsupported", "search is only honoured on table pages (this page is %q)", pg.Type)
		return
	}
	if !searchParamRe.MatchString(pg.Search) {
		v.add(field, "invalid_format", "search parameter name %q must match %s", pg.Search, searchParamRe.String())
		return
	}
	for _, p := range tableQueryParams {
		if strings.EqualFold(pg.Search, p) {
			v.add(field, "reserved_param",
				"search parameter %q is sent by the table itself (%s); the search box would overwrite it",
				pg.Search, strings.Join(tableQueryParams, ", "))
			return
		}
	}
	// Declaring a search parameter says "the route in source honours it", so
	// without a source the declaration cannot mean anything. source is already
	// required for table pages; this keeps the two from being reported as
	// unrelated problems.
	if pg.Source == "" {
		v.add(field, "missing_source", "search needs source: it names a query parameter of that route")
	}
}

// menuIcon checks that a menu names an icon the console can actually draw.
//
// This is the whole reason icons.json is embedded. An unknown name used to
// render a plain square - no blank space, no error, no console output - which
// looked like a deliberate choice of icon rather than a typo. The console now
// draws a loud placeholder and warns, but only the person looking at that page
// in a browser ever finds out; here the plugin does not install.
//
// Only ui.menus[].icon is checked. The top-level manifest.icon is the plugin
// avatar and a different vocabulary entirely ("text:AB", a path inside the
// package, an absolute URL, a data: URI); basics() checks that one.
func (v *validator) menuIcon(field, icon string) {
	if icon == "" || KnownIcon(icon) {
		return
	}
	v.add(field, "unknown_icon", "unknown icon %q; %s", icon, iconsStaleHint())
}

// isCoreMenuSection reports whether id is a core sidebar group of the
// console ("plugins" included) that a plugin menu may join.
func isCoreMenuSection(id string) bool {
	_, ok := manifest.CoreMenuSectionOrder(id)
	return ok
}

// coreMenuSectionList is "overview, gateway, ..." for error messages.
func coreMenuSectionList() string {
	ids := []string{}
	for _, sec := range manifest.CoreMenuSections() {
		ids = append(ids, sec.ID)
	}
	return strings.Join(ids, ", ")
}

func (v *validator) ui() {
	u := v.m.UI
	if u == nil {
		return
	}
	if len(u.Menus) > 0 || len(u.Pages) > 0 {
		v.needPerm("ui", "ui.menu", "menus and pages")
	}
	sectionIDs := v.uiSections(u)
	v.uiMenus(u, sectionIDs)
	v.uiPages(u)
	v.uiSlots(u)
	v.uiAccountActions(u)
	v.uiNative(u)
	v.uiCoreFloor(u)
	if u.Settings != nil {
		v.form("ui.settings", *u.Settings, true)
	}
}

// uiSections validates ui.sections and returns the declared section ids.
func (v *validator) uiSections(u *manifest.UI) map[string]bool {
	sectionIDs := map[string]bool{}
	for i, sec := range u.Sections {
		f := fmt.Sprintf("ui.sections[%d]", i)
		if !idRe.MatchString(sec.ID) {
			v.add(f+".id", "invalid_format", "section id %q is invalid", sec.ID)
		} else if isCoreMenuSection(sec.ID) {
			v.add(f+".id", "invalid", "section id %q is reserved for the core sidebar", sec.ID)
		} else if sectionIDs[sec.ID] {
			v.add(f+".id", "duplicate", "section %q declared twice", sec.ID)
		}
		sectionIDs[sec.ID] = true
		if firstText(sec.Label) == "" {
			v.add(f+".label", "required", "label is required")
		}
	}
	return sectionIDs
}

// uiMenus validates ui.menus against the declared sections and pages.
func (v *validator) uiMenus(u *manifest.UI, sectionIDs map[string]bool) {
	menuIDs := map[string]bool{}
	for i, mn := range u.Menus {
		f := fmt.Sprintf("ui.menus[%d]", i)
		if !idRe.MatchString(mn.ID) {
			v.add(f+".id", "invalid_format", "menu id %q is invalid", mn.ID)
		} else if menuIDs[mn.ID] {
			v.add(f+".id", "duplicate", "menu %q declared twice", mn.ID)
		}
		menuIDs[mn.ID] = true
		if !isCoreMenuSection(mn.Section) && !sectionIDs[mn.Section] {
			v.add(f+".section", "invalid", "section must be a core section (%s) or one of ui.sections", coreMenuSectionList())
		}
		if firstText(mn.Label) == "" {
			v.add(f+".label", "required", "label is required")
		}
		if _, ok := u.Pages[mn.Page]; !ok {
			v.add(f+".page", "unknown", "page %q is not declared in ui.pages", mn.Page)
		}
		v.menuIcon(f+".icon", mn.Icon)
		if mn.Permission != "" && !v.userPerm(mn.Permission) {
			v.add(f+".permission", "unknown", "permission %q is not declared in userPermissions", mn.Permission)
		}
	}
}

// uiPages validates ui.pages (sorted by id so the reports are stable).
func (v *validator) uiPages(u *manifest.UI) {
	pageIDs := make([]string, 0, len(u.Pages))
	for id := range u.Pages {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	for _, id := range pageIDs {
		pg := u.Pages[id]
		f := "ui.pages." + id
		if !idRe.MatchString(id) {
			v.add(f, "invalid_format", "page id %q is invalid", id)
		}
		switch pg.Type {
		case "table":
			if pg.Source == "" {
				v.add(f+".source", "required", "table pages require source")
			} else {
				v.routeRef(f+".source", pg.Source, "GET")
			}
		case "form":
			v.needFile(f+".schema", pg.Schema)
			if pg.Source != "" {
				// Optional on a form: it pre-fills the fields.
				v.routeRef(f+".source", pg.Source, "GET")
			}
			// Without submit the console's Save button does nothing and says
			// nothing (DeclarativeForm returns early when the reference does
			// not parse), so a form page without it is a page that cannot be
			// used, not a read-only one.
			if pg.Submit == "" {
				v.add(f+".submit", "required", "form pages require submit (the route the form posts to)")
			} else {
				v.routeRef(f+".submit", pg.Submit, "POST")
			}
		case "iframe":
			v.needPerm(f, "ui.iframe", "iframe pages")
			v.needFile(f+".src", pg.Src)
		case "native":
			v.needPerm(f, "ui.native", "native pages")
			if pg.Component == "" {
				v.add(f+".component", "required", "component is required for native pages")
			}
			if u.Native == nil {
				v.add(f, "missing_native_ui", "native pages require ui.native")
			}
		default:
			v.add(f+".type", "invalid", "page type must be table, form, iframe or native")
		}
		v.pageSearch(f+".search", pg)
		v.pageCreate(f+".create", u, pg)
		v.pageNav(f+".nav", pg)
	}
}

// navKeyRe is what a nav group or item key may look like. An item key is
// sent as ?tab=<key>, so it stays short, lower-case and URL-safe.
var navKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// pageNav validates a page's sub-navigation (UI 3.14). Only a native page
// can switch sections by ?tab=: a table, form or iframe page would draw
// sidebar entries that all show the same thing.
func (v *validator) pageNav(f string, pg manifest.Page) {
	if pg.Nav == nil {
		return
	}
	if pg.Type != "native" {
		v.add(f, "unsupported", "nav is only honoured on native pages (this page is %q)", pg.Type)
		return
	}
	if len(pg.Nav.Groups) == 0 {
		v.add(f+".groups", "required", "nav needs at least one group")
		return
	}
	groups := map[string]bool{}
	// Item keys are unique across the whole page, not per group: ?tab= names
	// one section and does not say which group it belongs to.
	items := map[string]bool{}
	for i, g := range pg.Nav.Groups {
		gf := fmt.Sprintf("%s.groups[%d]", f, i)
		if !navKeyRe.MatchString(g.Key) {
			v.add(gf+".key", "invalid_format", "group key %q must match %s", g.Key, navKeyRe.String())
		} else if groups[g.Key] {
			v.add(gf+".key", "duplicate", "group %q declared twice", g.Key)
		}
		groups[g.Key] = true
		if firstText(g.Label) == "" {
			v.add(gf+".label", "required", "label is required")
		}
		if len(g.Items) == 0 {
			v.add(gf+".items", "required", "a group needs at least one item")
		}
		for j, it := range g.Items {
			itf := fmt.Sprintf("%s.items[%d]", gf, j)
			if !navKeyRe.MatchString(it.Key) {
				v.add(itf+".key", "invalid_format", "item key %q must match %s", it.Key, navKeyRe.String())
			} else if items[it.Key] {
				v.add(itf+".key", "duplicate", "item %q declared twice in this page", it.Key)
			}
			items[it.Key] = true
			if firstText(it.Label) == "" {
				v.add(itf+".label", "required", "label is required")
			}
			v.menuIcon(itf+".icon", it.Icon)
			if it.Permission != "" && !v.userPerm(it.Permission) {
				v.add(itf+".permission", "unknown", "permission %q is not declared in userPermissions", it.Permission)
			}
			// Core permission keys are not known to the SDK, so only a blank
			// one is caught here: it would read as "no permission" and show
			// the item to everyone.
			if it.CorePermission != "" && strings.TrimSpace(it.CorePermission) == "" {
				v.add(itf+".corePermission", "invalid", "corePermission must not be blank")
			}
		}
	}
}

// pageCreate validates a table page's "New" link: it must name a form page
// of the same plugin and, when given, a declared permission.
func (v *validator) pageCreate(f string, u *manifest.UI, pg manifest.Page) {
	if pg.Create == nil {
		return
	}
	if pg.Type != "table" {
		v.add(f, "invalid", "only table pages can link a create page")
		return
	}
	if target, ok := u.Pages[pg.Create.Page]; !ok {
		v.add(f+".page", "unknown", "page %q is not declared in ui.pages", pg.Create.Page)
	} else if target.Type != "form" {
		v.add(f+".page", "invalid", "page %q must be a form page", pg.Create.Page)
	}
	if pg.Create.Permission != "" && !v.userPerm(pg.Create.Permission) {
		v.add(f+".permission", "unknown", "permission %q is not declared in userPermissions", pg.Create.Permission)
	}
}

// uiSlots validates ui.slots.
func (v *validator) uiSlots(u *manifest.UI) {
	for i, s := range u.Slots {
		f := fmt.Sprintf("ui.slots[%d]", i)
		switch s.Slot {
		case "dashboard.widgets", "account.form.widgets", "account.list.weight", "account.list.toolbar":
			if len(s.Label) > 0 || s.Icon != "" || len(s.AccountTypes) > 0 {
				v.add(f, "unsupported", "label, icon and accountTypes are only honoured on %s (accountTypes also on %s and %s)",
					manifest.SlotAccountEditorSection, manifest.SlotAccountDetailTabs, manifest.SlotAccountListQuota)
			}
		case manifest.SlotAccountDetailTabs, manifest.SlotAccountListQuota:
			if len(s.Label) > 0 || s.Icon != "" {
				v.add(f, "unsupported", "label and icon are only honoured on %s", manifest.SlotAccountEditorSection)
			}
			v.ownAccountTypes(f+".accountTypes", s.AccountTypes)
		case manifest.SlotAccountEditorSection:
			v.editorSection(f, s)
		default:
			v.add(f+".slot", "invalid", "unknown slot %q", s.Slot)
		}
		if s.Component == "" {
			v.add(f+".component", "required", "component is required")
		}
		v.needPerm(f, "ui.native", "slot components")
		if u.Native == nil {
			v.add(f, "missing_native_ui", "slot components require ui.native")
		}
		if s.Permission != "" && !v.userPerm(s.Permission) {
			v.add(f+".permission", "unknown", "permission %q is not declared in userPermissions", s.Permission)
		}
	}
}

// editorSection validates the navigation entry of an account editor section
// (UI 3.7): a label, a known icon and the account types it is shown for.
func (v *validator) editorSection(f string, s manifest.Slot) {
	if firstText(s.Label) == "" {
		v.add(f+".label", "required", "label is required")
	}
	v.menuIcon(f+".icon", s.Icon)
	v.ownAccountTypes(f+".accountTypes", s.AccountTypes)
}

// ownAccountTypes checks the accountTypes of a slot (editor section, detail
// tab, quota decoration) or an account action: ids of the plugin's own
// account types without repeats (the console shows the extension for no
// other type).
func (v *validator) ownAccountTypes(f string, ids []string) {
	own := func(id string) bool {
		return slices.ContainsFunc(v.m.AccountTypes, func(at manifest.AccountType) bool { return at.ID == id })
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if !own(id) || seen[id] {
			v.add(fmt.Sprintf("%s[%d]", f, i), "invalid", "%q is not an account type of this plugin, or is repeated", id)
		}
		seen[id] = true
	}
}

// uiAccountActions validates ui.accountActions (UI 3.6). An action needs no
// hostPermission of its own and no ui.native: the console renders it from
// the declaration and the route it posts to is an admin route, which
// routes.admin already covers. The route's permission must be the action's,
// so what the console shows is exactly what the plugin lets through.
func (v *validator) uiAccountActions(u *manifest.UI) {
	ids := map[string]bool{}
	for i, act := range u.AccountActions {
		f := fmt.Sprintf("ui.accountActions[%d]", i)
		if !idRe.MatchString(act.ID) {
			v.add(f+".id", "invalid_format", "action id %q is invalid", act.ID)
		} else if ids[act.ID] {
			v.add(f+".id", "duplicate", "action %q declared twice", act.ID)
		}
		ids[act.ID] = true
		if firstText(act.Label) == "" {
			v.add(f+".label", "required", "label is required")
		}
		v.menuIcon(f+".icon", act.Icon)
		if act.Permission == "" {
			v.add(f+".permission", "required", "permission is required")
		} else if !v.userPerm(act.Permission) {
			v.add(f+".permission", "unknown", "permission %q is not declared in userPermissions", act.Permission)
		}
		switch act.VisibleFor {
		case "", "editable", "all":
		default:
			v.add(f+".visibleFor", "invalid", "visibleFor must be \"editable\" or \"all\"")
		}
		v.ownAccountTypes(f+".accountTypes", act.AccountTypes)
		r := v.routeRef(f+".route", act.Route, "POST")
		if r == nil {
			continue
		}
		switch {
		case strings.ToUpper(r.Method) != "POST":
			v.add(f+".route", "invalid", "route must be a POST route")
		case r.Scope != "admin":
			v.add(f+".route", "invalid", "route %s %s must have scope admin (has %q)", r.Method, r.Path, r.Scope)
		case r.Permission != act.Permission:
			v.add(f+".route", "permission_mismatch", "route %s %s requires permission %q, the action %q",
				r.Method, r.Path, r.Permission, act.Permission)
		case slices.ContainsFunc(strings.Split(r.Path, "/"), func(s string) bool {
			return strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*")
		}):
			// The console posts {"account_ids": [...]} to the path as
			// declared; it has nothing to fill a :param or *wildcard with.
			v.add(f+".route", "parameterized_route", "route %s %s has a path parameter; the console posts {\"account_ids\":[...]} and cannot fill it", r.Method, r.Path)
		}
	}
}

// uiNative validates ui.native and hostUICompat.
func (v *validator) uiNative(u *manifest.UI) {
	if u.Native == nil {
		return
	}
	v.needPerm("ui.native", "ui.native", "native UI")
	if !v.opt.Tooling {
		// The entry is produced by the native UI build, not written by
		// the author; tooling checks for it at packaging time instead.
		v.needFile("ui.native.entry", u.Native.Entry)
	} else if u.Native.Entry == "" {
		v.add("ui.native.entry", "required", "file path is required")
	}
	if u.Native.Entry != "" && !strings.HasPrefix(u.Native.Entry, "ui/") {
		v.add("ui.native.entry", "invalid_path", "native entry must be under ui/")
	}
	if v.m.HostUICompat == "" {
		v.add("hostUICompat", "required", "hostUICompat is required when ui.native is used")
	} else if _, err := semver.NewConstraint(v.m.HostUICompat); err != nil {
		v.add("hostUICompat", "invalid_constraint", "hostUICompat: %v", err)
	}
}

// uiFloor is one UI SDK minor that raised the core a plugin must
// require: the first core shipping it, the last core before it and that
// core's UI. uses says which declarations of a manifest need this UI even
// without ui.native (the console of an older core ignores them silently).
type uiFloor struct {
	minor                       uint64
	lastUI, lastCore, firstCore string
	uses                        func(*manifest.UI) bool
}

// uiFloors, newest first. Core 0.1.228 is the last that ships UI 3.3
// (3.4 starts at 0.1.229); 0.1.255 is the last with UI 3.5 (3.6 starts at
// 0.1.256); 0.1.261 is the last with UI 3.6 (3.7 starts at 0.1.262);
// 0.1.268 is the last with UI 3.7 (3.8 starts at 0.1.269); 0.1.276 is the
// last with UI 3.9 (3.10 starts at 0.1.277); 0.1.278 is the last with UI
// 3.10 (3.11 starts at 0.1.279); 0.1.279 is the last with UI 3.11 (3.12
// starts at 0.1.280); 0.1.280 is the last with UI 3.12 (3.13 starts at
// 0.1.281); 0.1.283 is the last with UI 3.13 (3.14 starts at 0.1.284).
// 3.10 to 3.13 add components and props, no manifest declaration, so only
// hostUICompat asks for them. 3.14 adds ui.pages.<page>.nav, but it needs
// no floor of its own either: an older console ignores nav and the page
// keeps its in-page section switcher, so the plugin still works there.
var uiFloors = []uiFloor{
	{minor: 14, lastUI: "3.13.0", lastCore: "0.1.283", firstCore: "0.1.284"},
	{minor: 13, lastUI: "3.12.0", lastCore: "0.1.280", firstCore: "0.1.281"},
	{minor: 12, lastUI: "3.11.0", lastCore: "0.1.279", firstCore: "0.1.280"},
	{minor: 11, lastUI: "3.10.0", lastCore: "0.1.278", firstCore: "0.1.279"},
	{minor: 10, lastUI: "3.9.0", lastCore: "0.1.276", firstCore: "0.1.277"},
	{minor: 8, lastUI: "3.7.0", lastCore: "0.1.268", firstCore: "0.1.269", uses: usesUI38},
	{minor: 7, lastUI: "3.6.0", lastCore: "0.1.261", firstCore: CommonControlsFirstCore, uses: func(u *manifest.UI) bool {
		return slices.ContainsFunc(u.Slots, func(s manifest.Slot) bool { return s.Slot == manifest.SlotAccountEditorSection })
	}},
	{minor: 6, lastUI: "3.5.0", lastCore: "0.1.255", firstCore: "0.1.256", uses: func(u *manifest.UI) bool {
		return len(u.AccountActions) > 0 ||
			slices.ContainsFunc(u.Slots, func(s manifest.Slot) bool { return s.Slot == "account.list.toolbar" })
	}},
	{minor: 4, lastUI: "3.3.0", lastCore: "0.1.228", firstCore: "0.1.229"},
}

// usesUI38 reports a UI 3.8 declaration: an account.list.quota slot, which
// an older console never renders, or a detail tab or an account action
// limited to some account types, which it would offer on every account.
func usesUI38(u *manifest.UI) bool {
	return slices.ContainsFunc(u.Slots, func(s manifest.Slot) bool {
		return s.Slot == manifest.SlotAccountListQuota || (s.Slot == manifest.SlotAccountDetailTabs && len(s.AccountTypes) > 0)
	}) || slices.ContainsFunc(u.AccountActions, func(a manifest.AccountAction) bool { return len(a.AccountTypes) > 0 })
}

// uiCoreFloor checks hostCompat against the newest UI floor the manifest
// needs. The core installs a plugin on hostCompat alone and the console then
// refuses a native UI whose hostUICompat its UI does not satisfy, so a plugin
// that needs UI 3.4 or later must also keep out the cores that ship 3.3, and
// one using a UI 3.6 declaration the cores that ship 3.5.
func (v *validator) uiCoreFloor(u *manifest.UI) {
	c, _ := semver.NewConstraint(v.m.HostUICompat) // invalid or empty: nil, reported by uiNative
	for _, fl := range uiFloors {
		if fl.needs(c) || (fl.uses != nil && fl.uses(u)) {
			if ok, err := HostCompatible(v.m.HostCompat, fl.lastCore); err == nil && ok {
				v.add("hostCompat", "below_ui_core", "hostCompat %q admits core %s, whose UI %s is below the UI 3.%d this manifest needs (UI 3.%d starts at core %s)",
					v.m.HostCompat, fl.lastCore, fl.lastUI, fl.minor, fl.minor, fl.firstCore)
			}
			return
		}
	}
}

// needs reports whether c rejects the UI of fl.lastCore but accepts a 3.x
// release from fl.minor on (^3.4, ~3.4.2, >=3.5 ...); a nil c or a range
// that never matches a 3.x UI is wrong in other ways and left alone.
func (fl uiFloor) needs(c *semver.Constraints) bool {
	if c == nil || c.Check(semver.MustParse(fl.lastUI)) {
		return false
	}
	for minor := fl.minor; minor <= 30; minor++ {
		if c.Check(semver.New(3, minor, 0, "", "")) || c.Check(semver.New(3, minor, 99, "", "")) {
			return true
		}
	}
	return false
}

// firstText returns the "en" text or any non-empty value.
func firstText(t map[string]string) string {
	if s := strings.TrimSpace(t["en"]); s != "" {
		return s
	}
	for _, s := range t {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}
