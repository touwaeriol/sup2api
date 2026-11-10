package authz

import (
	"cmp"
	"context"
	"sort"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
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
	// anyOf lists the permissions granting visibility; empty = any user.
	anyOf []string
}

type coreMenuSection struct {
	id    string
	label core.LocalizedText
	items []coreMenuItem
}

// coreMenus follows CONTRACTS §22.1 and §90: settings with settings, features
// with features. Every feature page sits in the section it belongs to, and
// system settings is one entry (/settings), the last core item of system; the
// console lists the pages behind that entry itself (groups, model prices and
// plugin settings among them), so they have no item here.
// The console shows three groups: overview (the console every role has), me
// (personal) and admin, made of the other sections. Sections stay apart so
// plugins can join the one their pages belong to.
var coreMenus = []coreMenuSection{
	// Own usage for everyone, site-wide usage beside it for those who may
	// read it; the old usage statistics page (/me/stats) redirects to the
	// usage overview.
	{"overview", lt("Console", "控制台"), []coreMenuItem{
		{"dashboard", lt("Dashboard", "仪表盘"), "dashboard", "/dashboard", nil},
		{"my-api-keys", lt("API keys", "API 密钥"), "key", "/me/api-keys", []string{"apikey:self:manage"}},
		{"my-models", lt("Models & prices", "模型与价格"), "price", "/me/models", []string{"apikey:self:manage"}},
		{"my-usage-overview", lt("Usage overview", "用量概览"), "chart", "/me/usage/overview", []string{"usage:self:read"}},
		{"my-usage", lt("Usage logs", "使用日志"), "usage", "/me/usage", []string{"usage:self:read"}},
		{"usage-overview", lt("Site overview", "全站概览"), "chart", "/usage/overview", []string{"usage:all:read"}},
		{"usage", lt("Site usage logs", "全站日志"), "usage", "/usage", []string{"usage:all:read"}},
	}},
	// Accounts and the proxies they go out through.
	{"gateway", lt("Gateway", "网关"), []coreMenuItem{
		{"accounts", lt("Accounts", "账号"), "account", "/accounts", []string{"account:read", "account:own:read", "account:own:create"}},
		{"proxies", lt("Proxies", "代理"), "proxy", "/proxies", []string{"proxy:read", "proxy:own:read", "proxy:own:manage"}},
	}},
	// No core items: request guards and moderation are plugins and join it.
	{"security", lt("Security", "安全"), nil},
	// No core items: the balance ledger is opened from the users list, and
	// plugins (growth) join the section.
	{"finance", lt("Finance", "财务"), nil},
	// Roles are part of managing users.
	{"users", lt("Users", "用户"), []coreMenuItem{
		{"users", lt("Users", "用户"), "user", "/users", []string{"user:read"}},
		{"roles", lt("Roles & permissions", "角色与权限"), "role", "/roles", []string{"role:read"}},
	}},
	// Plugin management; plugin menus with section "plugins" (the default)
	// come after it.
	{"plugins", lt("Plugins", "插件"), []coreMenuItem{
		{"plugins", lt("Installed plugins", "已安装插件"), "plugin", "/plugins", []string{"plugin:read"}},
		{"market", lt("Plugin market", "插件市场"), "market", "/market", []string{"plugin:market:read"}},
		{"publishers", lt("Publishers", "发布者"), "publisher", "/publishers", []string{"publisher:read"}},
	}},
	// Operations pages, then the one entry of system settings, shown to
	// whoever may open a page of the settings view: a configuration page
	// (settings, sticky sessions), groups or model prices. Plugin menus with
	// section "system" still join it, after /settings (dynweight 0.5.4).
	{"system", lt("System", "系统"), []coreMenuItem{
		{"nodes", lt("Cluster nodes", "集群节点"), "node", "/nodes", []string{"node:read"}},
		{"tasks", lt("Scheduled tasks", "定时任务"), "clock", "/system/tasks", []string{"task:read"}},
		{"upgrades", lt("Core updates", "核心升级"), "refresh", "/system/upgrades", []string{"system:update:read"}},
		{"audit", lt("Audit logs", "审计日志"), "clock", "/system/audit", []string{"audit:read"}},
		{"settings", lt("System settings", "系统设置"), "settings", "/settings", []string{"settings:read", "sticky:read", "group:read", "price:read"}},
	}},
	{"me", lt("Personal", "个人中心"), []coreMenuItem{
		{"my-wallet", lt("Wallet", "钱包"), "balance", "/me/wallet", []string{"balance:self:read"}},
		{"my-profile", lt("Profile", "个人设置"), "user", "/me/profile", nil},
	}},
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
	type section struct {
		MenuSection
		order int
	}
	var out []section
	byKey := map[string]int{}
	add := func(key string, label core.LocalizedText, order int) int {
		if i, ok := byKey[key]; ok {
			return i
		}
		out = append(out, section{MenuSection{Section: key, Label: label, Items: []MenuItem{}}, order})
		byKey[key] = len(out) - 1
		return len(out) - 1
	}
	for _, sec := range coreMenus {
		order, _ := manifest.CoreMenuSectionOrder(sec.id)
		i := add(sec.id, sec.label, order)
		for _, it := range sec.items {
			if !hasAny(set, it.anyOf) {
				continue
			}
			out[i].Items = append(out[i].Items, MenuItem{ID: it.id, Label: it.label, Icon: it.icon, Path: it.path})
		}
	}
	for _, pm := range pluginMenus(s.generation(), set) {
		i := add(pm.sectionKey, pm.sectionLabel, pm.sectionOrder)
		out[i].Items = append(out[i].Items, pm.MenuItem)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].order < out[j].order })
	res := make([]MenuSection, 0, len(out))
	for _, sec := range out {
		if len(sec.Items) > 0 {
			res = append(res, sec.MenuSection)
		}
	}
	return res, nil
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

func pluginMenus(gen core.Generation, set core.PermissionSet) []pluginMenu {
	if gen == nil {
		return nil
	}
	var list []pluginMenu
	for _, p := range gen.Plugins() {
		if p.Manifest == nil || p.Manifest.UI == nil {
			continue
		}
		for _, m := range p.Manifest.UI.Menus {
			if m.Permission != "" && !set.Has(PluginPermissionKey(p.Key, m.Permission)) {
				continue
			}
			label := core.LocalizedText(m.Label)
			if label == nil {
				label = core.LocalizedText{"en": m.ID}
			}
			pm := pluginMenu{MenuItem: MenuItem{
				ID:        p.Key + ":" + m.ID,
				Label:     label,
				Icon:      cmp.Or(m.Icon, "puzzle"),
				Path:      "/p/" + p.Key + "/" + strings.TrimPrefix(m.Page, "/"),
				PluginKey: p.Key,
			}, order: m.Order}
			// A core section (the shared "plugins" one by default) already
			// exists with its own label by the time plugin menus join it.
			section := cmp.Or(m.Section, "plugins")
			if order, ok := manifest.CoreMenuSectionOrder(section); ok {
				pm.sectionKey, pm.sectionOrder = section, order
			} else {
				pm.sectionKey = p.Key + ":" + m.Section
				pm.sectionLabel, pm.sectionOrder = core.LocalizedText{"en": m.Section}, manifest.PluginMenuSectionOrder
				for _, sec := range p.Manifest.UI.Sections {
					if sec.ID == m.Section {
						if l := core.LocalizedText(sec.Label); l != nil {
							pm.sectionLabel = l
						}
						if sec.Order != 0 {
							pm.sectionOrder = sec.Order
						}
					}
				}
			}
			list = append(list, pm)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].order != list[j].order {
			return list[i].order < list[j].order
		}
		return list[i].ID < list[j].ID
	})
	return list
}

// generation returns the current plugin generation, nil without a registry.
func (s *Service) generation() core.Generation {
	if s.plugins == nil {
		return nil
	}
	return s.plugins.Current()
}

func hasAny(set core.PermissionSet, perms []string) bool {
	if len(perms) == 0 {
		return true
	}
	for _, p := range perms {
		if set.Has(p) {
			return true
		}
	}
	return false
}
