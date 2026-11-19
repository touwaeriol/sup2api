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

// ParseRouteRef is manifest.ParseRouteRef.
func ParseRouteRef(ref, defaultMethod string) (method, path string, ok bool) {
	return manifest.ParseRouteRef(ref, defaultMethod)
}

// routeRef checks one route reference (an account action's route) and
// returns the declared route it names, or nil after recording the error.
//
// The route is matched by manifest.RouteSignature, the same signature
// routes() uses to find duplicates, so "/assets/:id" in routes and
// "/assets/:asset" in a reference are the same route (gin binds by
// position, not by name).
func (v *validator) routeRef(field, ref, defaultMethod string) *manifest.Route {
	method, path, ok := manifest.ParseRouteRef(ref, defaultMethod)
	if !ok {
		v.add(field, "invalid_route_ref", "%q is not a route reference; expected %q or %q",
			ref, defaultMethod+" /path", "/path")
		return nil
	}
	want := manifest.RouteSignature(method, path)
	for i := range v.m.Routes {
		if r := &v.m.Routes[i]; manifest.RouteSignature(r.Method, r.Path) == want {
			return r
		}
	}
	v.add(field, "unknown_route", "route %s %s is not declared in routes", method, path)
	return nil
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
		case "table", "form":
			v.add(f+".type", "unsupported", "page type %s is no longer supported: write the page as a native component (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY)", pg.Type)
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
			v.add(f+".type", "invalid", "page type must be iframe or native")
		}
		v.pageNav(f+".nav", pg)
	}
}

// navKeyRe is what a nav group or item key may look like. An item key is
// sent as ?tab=<key>, so it stays short, lower-case and URL-safe.
var navKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// pageNav validates a page's sub-navigation (UI 3.14). Only a native page
// can switch sections by ?tab=: an iframe page would draw
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
		} else if v.m.UserPermissionScope(act.Permission) != nil {
			// The core checks an account action on routes[].accounts and
			// account:* alone, so a plugin scope would mean nothing there.
			v.add(f+".permission", "unsupported", "permission %q declares a scope; account actions take an unscoped permission", act.Permission)
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
		case hasPathParam(r.Path):
			// The console posts {"account_ids": [...]} to the path as
			// declared; it has nothing to fill a :param or *wildcard with.
			v.add(f+".route", "parameterized_route", "route %s %s has a path parameter; the console posts {\"account_ids\":[...]} and cannot fill it", r.Method, r.Path)
		case r.Accounts != nil && !slices.Contains(r.Accounts.From, manifest.AccountActionIDs):
			// An explicit declaration replaces the inferred one
			// (EffectiveRouteAccounts); it must still check what the console posts.
			v.add(f+".route", "accounts_mismatch", "route %s %s declares accounts without %q, which the console posts", r.Method, r.Path, manifest.AccountActionIDs)
		}
	}
}

// hasPathParam reports a :param or *wildcard segment in a route path.
func hasPathParam(path string) bool {
	return slices.ContainsFunc(strings.Split(path, "/"), func(s string) bool {
		return strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*")
	})
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
