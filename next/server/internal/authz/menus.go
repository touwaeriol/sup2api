package authz

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// MenuItem is one sidebar entry.
type MenuItem struct {
	ID        string             `json:"id"`
	Label     core.LocalizedText `json:"label"`
	Icon      string             `json:"icon"`
	Path      string             `json:"path"`
	PluginKey string             `json:"plugin_key,omitempty"`
}

// MenuSection is one sidebar section.
type MenuSection struct {
	Section string             `json:"section"`
	Label   core.LocalizedText `json:"label"`
	Items   []MenuItem         `json:"items"`
}

type coreMenuItem struct {
	id    string
	label core.LocalizedText
	icon  string // console icon name (see web/)
	path  string
	// apis are the page's main routes ("GET /accounts", httpapi.RouteSpec.ID):
	// the item shows to holders of any of their permissions, each held with
	// any scope (PermissionSet.HasAction), as bound by BindMenuRoutes; none =
	// any user.
	apis []string
}

type coreMenuSection struct {
	id    string
	label core.LocalizedText
	items []coreMenuItem
}

// coreMenus follows CONTRACTS §22.1, §90 and §132: settings with settings,
// features with features. Every feature page sits in the section it belongs
// to, and system settings is one entry (/settings), the last core item of
// system; the console lists the pages behind that entry itself (model prices
// among them), so they have no item here.
// The console shows three groups: overview (the console every role has), me
// (personal) and admin, made of the other sections. Sections stay apart so
// plugins can join the one their pages belong to.
var coreMenus = []coreMenuSection{
	// Own usage for everyone, site-wide usage beside it for those who may
	// read it; the old usage statistics page (/me/stats) redirects to the
	// usage overview.
	{"overview", lt("Console", "控制台"), []coreMenuItem{
		{"dashboard", lt("Dashboard", "仪表盘"), "dashboard", "/dashboard", nil},
		{"my-api-keys", lt("API keys", "API 密钥"), "key", "/me/api-keys", []string{"GET /me/api-keys"}},
		{"my-models", lt("Models & prices", "模型与价格"), "price", "/me/models", []string{"GET /me/prices"}},
		{"my-usage-overview", lt("Usage overview", "用量概览"), "chart", "/me/usage/overview", []string{"GET /me/usage/overview"}},
		{"my-usage", lt("Usage logs", "使用日志"), "usage", "/me/usage", []string{"GET /me/usage"}},
		{"usage-overview", lt("Site overview", "全站概览"), "chart", "/usage/overview", []string{"GET /usage/overview"}},
		{"usage", lt("Site usage logs", "全站日志"), "usage", "/usage", []string{"GET /usage"}},
		// The usage of the accounts in the caller's scope (CONTRACTS
		// §103.5), for those who do not see the whole site's (hideWith).
		{"account-usage-overview", lt("Account usage overview", "账号用量概览"), "chart", "/account-usage/overview", []string{"GET /account-usage/overview"}},
		{"account-usage", lt("Account usage logs", "账号日志"), "usage", "/account-usage", []string{"GET /account-usage"}},
	}},
	// Accounts, the groups they serve in and the proxies they go out through,
	// each for the keys of its list endpoint; the console folds them into one
	// submenu (§132).
	{"gateway", lt("Accounts & proxies", "账号/代理"), []coreMenuItem{
		{"accounts", lt("Accounts", "账号"), "account", "/accounts", []string{"GET /accounts"}},
		{"groups", lt("Account groups", "账号分组"), "group", "/groups", []string{"GET /groups"}},
		{"proxies", lt("Proxies", "代理"), "proxy", "/proxies", []string{"GET /proxies"}},
	}},
	// No core items: request guards and moderation are plugins and join it.
	{"security", lt("Security", "安全"), nil},
	// The site-wide balance ledger; plugins may join the section.
	{"finance", lt("Finance", "财务"), []coreMenuItem{
		{"ledger", lt("Balance ledger", "余额流水"), "balance", "/ledger", []string{"GET /ledger"}},
	}},
	// Roles are part of managing users, and so are the user groups they
	// belong to (CONTRACTS §97).
	{"users", lt("Users", "用户"), []coreMenuItem{
		{"users", lt("Users", "用户"), "user", "/users", []string{"GET /users"}},
		{"segments", lt("User groups", "用户分组"), "publisher", "/segments", []string{"GET /segments"}},
		{"roles", lt("Roles & permissions", "角色与权限"), "role", "/roles", []string{"GET /roles"}},
	}},
	// Plugin management; plugin menus with section "plugins" (the default)
	// come after it.
	{"plugins", lt("Plugins", "插件"), []coreMenuItem{
		{"plugins", lt("Installed plugins", "已安装插件"), "plugin", "/plugins", []string{"GET /plugins"}},
		{"market", lt("Plugin market", "插件市场"), "market", "/market", []string{"GET /market/plugins"}},
		{"publishers", lt("Publishers", "发布者"), "publisher", "/publishers", []string{"GET /publishers"}},
	}},
	// Operations pages, then the one entry of system settings, shown to
	// holders of a permission one of its core pages needs: a configuration
	// page (settings, sticky sessions) or model prices. Plugin menus
	// with section "system" still join it, after /settings (dynweight 0.5.4).
	{"system", lt("System", "系统"), []coreMenuItem{
		{"nodes", lt("Cluster nodes", "集群节点"), "node", "/nodes", []string{"GET /nodes"}},
		{"tasks", lt("Scheduled tasks", "定时任务"), "clock", "/system/tasks", []string{"GET /system/tasks"}},
		{"upgrades", lt("Core updates", "核心升级"), "refresh", "/system/upgrades", []string{"GET /system/upgrades"}},
		{"audit", lt("Audit logs", "审计日志"), "clock", "/system/audit", []string{"GET /audit-logs"}},
		// The first page of each kind the entry leads to: a configuration
		// page, sticky sessions, model prices.
		{"settings", lt("System settings", "系统设置"), "settings", "/settings", []string{"GET /settings/gateway", "GET /sticky-rules", "GET /prices"}},
	}},
	{"me", lt("Personal", "个人中心"), []coreMenuItem{
		{"my-wallet", lt("Wallet", "钱包"), "balance", "/me/wallet", []string{"GET /me/balance"}},
		{"my-profile", lt("Profile", "个人设置"), "user", "/me/profile", nil},
	}},
}

// hideWith hides a core menu item, by id, from the holders of any of its
// keys for every row (PermissionSet.Has), who have a wider page in its
// place: the site's usage pages stand for the account-side ones.
var hideWith = map[string][]string{
	"account-usage-overview": {string(perm.UsageAllRead)},
	"account-usage":          {string(perm.UsageAllRead)},
}

// BindMenuRoutes gives the core menu items the permissions of their main
// routes (coreMenuItem.apis): keysOf returns a route's keys by
// httpapi.RouteSpec.ID, false when there is no such route. It fails on a
// menu naming a route that does not exist; call it once the routes are
// registered, before serving. Until then the items with routes stay hidden.
func (s *Service) BindMenuRoutes(keysOf func(id string) ([]string, bool)) error {
	anyOf := map[string][]string{}
	var errs []error
	for _, sec := range coreMenus {
		for _, it := range sec.items {
			var keys []string
			for _, api := range it.apis {
				k, ok := keysOf(api)
				if !ok {
					errs = append(errs, fmt.Errorf("menu %s: no route %s", it.id, api))
				}
				keys = append(keys, k...)
			}
			anyOf[it.id] = dedupe(keys)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	s.mu.Lock()
	s.menuKeys = anyOf
	s.mu.Unlock()
	return nil
}

// menuAnyOf is the permissions showing it, false when it names routes not
// bound yet (BindMenuRoutes).
func (s *Service) menuAnyOf(it coreMenuItem) ([]string, bool) {
	if len(it.apis) == 0 {
		return nil, true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys, ok := s.menuKeys[it.id]
	return keys, ok
}

// Core sections are placed by their manifest.CoreMenuSections order (the ids
// and orders manifest validation uses too); plugin sections (manifest
// ui.sections) interleave with them by their own order, and a plugin section
// without one goes after the shared "plugins" section
// (manifest.PluginMenuSectionOrder).

// Menus builds the sidebar of userID: core menus filtered by permission,
// plugin menus appended to the core section they name (the shared "plugins"
// section included) and plugin-declared sections placed by order.
func (s *Service) Menus(ctx context.Context, userID int64) ([]MenuSection, error) {
	set, err := s.PermissionSet(ctx, userID)
	if err != nil {
		return nil, err
	}
	var b sidebar
	for _, sec := range coreMenus {
		order, _ := manifest.CoreMenuSectionOrder(sec.id)
		i := b.section(sec.id, sec.label, order)
		for _, it := range sec.items {
			if s.showsCore(it, set) {
				b.add(i, MenuItem{ID: it.id, Label: it.label, Icon: it.icon, Path: it.path})
			}
		}
	}
	for _, pm := range s.pluginMenus(set) {
		b.add(b.section(pm.sectionKey, pm.sectionLabel, pm.sectionOrder), pm.MenuItem)
	}
	return b.sections(), nil
}

// showsCore reports whether the core menu item shows to the holder of set:
// its routes are bound, set holds one of their permissions and none of
// those that hide it (hideWith).
func (s *Service) showsCore(it coreMenuItem, set core.PermissionSet) bool {
	anyOf, bound := s.menuAnyOf(it)
	return bound && hasAny(set, anyOf) && !slices.ContainsFunc(hideWith[it.id], set.Has)
}

// sidebar collects the sections of a sidebar, each once by key, in the
// order they were first named.
type sidebar struct {
	list  []orderedSection
	byKey map[string]int
}

// orderedSection is a section with where it goes among the others.
type orderedSection struct {
	MenuSection
	order int
}

// section returns the index of the section key, adding it with the label
// and the order the first time.
func (b *sidebar) section(key string, label core.LocalizedText, order int) int {
	if i, ok := b.byKey[key]; ok {
		return i
	}
	if b.byKey == nil {
		b.byKey = map[string]int{}
	}
	b.list = append(b.list, orderedSection{MenuSection{Section: key, Label: label, Items: []MenuItem{}}, order})
	b.byKey[key] = len(b.list) - 1
	return len(b.list) - 1
}

// add appends the item to section i.
func (b *sidebar) add(i int, item MenuItem) { b.list[i].Items = append(b.list[i].Items, item) }

// sections are the sections that have items, by order.
func (b *sidebar) sections() []MenuSection {
	sort.SliceStable(b.list, func(i, j int) bool { return b.list[i].order < b.list[j].order })
	out := make([]MenuSection, 0, len(b.list))
	for _, sec := range b.list {
		if len(sec.Items) > 0 {
			out = append(out, sec.MenuSection)
		}
	}
	return out
}

// pluginMenu is a plugin menu item with the section it goes to: a core
// section, the plugin's own section (key "<plugin>:<section>") or "plugins".
type pluginMenu struct {
	MenuItem
	sectionKey   string
	sectionLabel core.LocalizedText
	sectionOrder int
	order        int
}

// pluginMenus are the menus of the running plugins that set allows, by
// order and then id.
func (s *Service) pluginMenus(set core.PermissionSet) []pluginMenu {
	if s.plugins == nil {
		return nil
	}
	gen := s.plugins.Current()
	if gen == nil {
		return nil
	}
	var list []pluginMenu
	for _, p := range gen.Plugins() {
		if p.Manifest != nil && p.Manifest.UI != nil {
			list = append(list, pluginMenusOf(p.Key, p.Manifest.UI, set)...)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		return cmp.Or(cmp.Compare(list[i].order, list[j].order), cmp.Compare(list[i].ID, list[j].ID)) < 0
	})
	return list
}

// pluginMenusOf are the menus of the UI of plugin key that set allows. A
// plugin key shows at any scope, as the core's account and proxy menus do
// (hasAny); the plugin routes narrow the data.
func pluginMenusOf(key string, ui *manifest.UI, set core.PermissionSet) []pluginMenu {
	allowed := func(perm string) bool { return set.HasSome(manifest.PermissionKey(key, perm)) }
	var list []pluginMenu
	for _, m := range manifest.FilterUI(ui, allowed, set.Has).Menus {
		label := core.LocalizedText(m.Label)
		if label == nil {
			label = core.LocalizedText{"en": m.ID}
		}
		pm := pluginMenu{MenuItem: MenuItem{
			ID:        key + ":" + m.ID,
			Label:     label,
			Icon:      cmp.Or(m.Icon, "puzzle"),
			Path:      "/p/" + key + "/" + strings.TrimPrefix(m.Page, "/"),
			PluginKey: key,
		}, order: m.Order}
		pm.place(key, ui, m.Section)
		list = append(list, pm)
	}
	return list
}

// place puts the menu in its section: a core one (the shared "plugins" one
// by default), which already exists with its own label by the time plugin
// menus join it, or the plugin's own section, labelled and ordered as its
// UI declares it.
func (pm *pluginMenu) place(key string, ui *manifest.UI, section string) {
	shared := cmp.Or(section, "plugins")
	if order, ok := manifest.CoreMenuSectionOrder(shared); ok {
		pm.sectionKey, pm.sectionOrder = shared, order
		return
	}
	pm.sectionKey = key + ":" + section
	pm.sectionLabel, pm.sectionOrder = core.LocalizedText{"en": section}, manifest.PluginMenuSectionOrder
	for _, sec := range ui.Sections {
		if sec.ID != section {
			continue
		}
		if l := core.LocalizedText(sec.Label); l != nil {
			pm.sectionLabel = l
		}
		if sec.Order != 0 {
			pm.sectionOrder = sec.Order
		}
	}
}

func hasAny(set core.PermissionSet, perms []string) bool {
	if len(perms) == 0 {
		return true
	}
	for _, p := range perms {
		if a, err := perm.Parse(p); err == nil && set.HasAction(a.Action) {
			return true
		}
	}
	return false
}
