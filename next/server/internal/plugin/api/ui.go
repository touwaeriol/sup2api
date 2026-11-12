package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// UIPlugin is one entry of GET /ui/plugins.
type UIPlugin struct {
	Key       string                   `json:"key"`
	Version   string                   `json:"version"`
	Name      core.LocalizedText       `json:"name"`
	AssetBase string                   `json:"asset_base"`
	Menus     []manifest.Menu          `json:"menus"`
	Pages     map[string]manifest.Page `json:"pages"`
	Slots     []manifest.Slot          `json:"slots"`
	// AccountActions are the manifest's ui.accountActions the caller may
	// use (UI 3.6); the plugin route behind each one checks again.
	AccountActions []manifest.AccountAction `json:"account_actions"`
	NativeEntry    string                   `json:"native_entry"`
	Trust          string                   `json:"trust"`
	HostUICompat   string                   `json:"host_ui_compat"`
}

// uiPlugins lists console extensions of active plugins, filtered by the
// caller's permissions. Pages only reachable through hidden menus are
// dropped; native entries are only returned for official/verified plugins.
// A plugin with nothing left for the caller (no menu, page, slot, account
// action or native entry) is left out.
func (a *API) uiPlugins(c *gin.Context) {
	out := []UIPlugin{}
	if a.d.Registry == nil {
		httpapi.OK(c, out)
		return
	}
	gen := a.d.Registry.Current()
	if gen == nil {
		httpapi.OK(c, out)
		return
	}
	rc := c.Request.Context()
	ps, err := a.d.Authz.PermissionSet(rc, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	for _, p := range gen.Plugins() {
		m := p.Manifest
		if m == nil || m.UI == nil {
			continue
		}
		allowed := func(perm string) bool { return perm == "" || ps.Has(install.PermissionKey(p.Key, perm)) }
		allowedCore := func(perm string) bool { return ps.Has(perm) }
		item := UIPlugin{Key: p.Key, Version: p.Version, Name: core.LocalizedText(m.Name), AssetBase: p.AssetBase,
			Menus: []manifest.Menu{}, Pages: map[string]manifest.Page{}, Slots: []manifest.Slot{}, AccountActions: []manifest.AccountAction{},
			Trust: p.Trust, HostUICompat: m.HostUICompat}
		item.Menus, item.Pages = visibleMenusAndPages(m.UI, allowed, allowedCore)
		for _, sl := range m.UI.Slots {
			if allowed(sl.Permission) {
				item.Slots = append(item.Slots, sl)
			}
		}
		for _, act := range m.UI.AccountActions {
			if allowed(act.Permission) {
				item.AccountActions = append(item.AccountActions, act)
			}
		}
		if m.UI.Native != nil && (p.Trust == pkg.TrustOfficial || p.Trust == pkg.TrustVerified) {
			item.NativeEntry = m.UI.Native.Entry
		}
		// A native entry alone is an extension too: it may hold only an
		// account form (form.mode "native") that no menu, page or slot names.
		if len(item.Menus) == 0 && len(item.Slots) == 0 && len(item.Pages) == 0 && len(item.AccountActions) == 0 && item.NativeEntry == "" {
			continue
		}
		out = append(out, item)
	}
	httpapi.OK(c, out)
}

// visibleMenusAndPages filters a plugin's menus and pages for one caller.
// A page reached only through menus the caller may not see is dropped; so is
// a table's "New" link the caller may not use, and the form page it opens
// unless a visible menu or another shown table's usable link reaches it (a
// table hidden by its menu's permission makes its link unusable). Pages no
// menu or link names stay (their routes check permissions themselves).
// A kept page's nav loses the sections the caller may not see (see
// visibleNav); allowedCore checks a core permission key.
func visibleMenusAndPages(ui *manifest.UI, allowed, allowedCore func(perm string) bool) ([]manifest.Menu, map[string]manifest.Page) {
	menus := []manifest.Menu{}
	pages := map[string]manifest.Page{}
	hidden := map[string]bool{}
	visible := map[string]bool{}
	for _, mn := range ui.Menus {
		if allowed(mn.Permission) {
			menus = append(menus, mn)
			visible[mn.Page] = true
		} else {
			hidden[mn.Page] = true
		}
	}
	// A link counts only from a page the menus leave shown: a table the
	// caller cannot see must not reveal the form it opens.
	linkVisible := map[string]bool{}
	linkHidden := map[string]bool{}
	for id, pg := range ui.Pages {
		if pg.Create == nil {
			continue
		}
		if (visible[id] || !hidden[id]) && allowed(pg.Create.Permission) {
			linkVisible[pg.Create.Page] = true
		} else {
			linkHidden[pg.Create.Page] = true
		}
	}
	for id := range linkVisible {
		visible[id] = true
	}
	for id := range linkHidden {
		hidden[id] = true
	}
	shown := func(id string) bool { return visible[id] || !hidden[id] }
	for id, pg := range ui.Pages {
		if !shown(id) {
			continue
		}
		if pg.Create != nil && (!allowed(pg.Create.Permission) || !shown(pg.Create.Page)) {
			pg.Create = nil
		}
		if pg.Nav != nil {
			pg.Nav = visibleNav(pg.Nav, allowed, allowedCore)
		}
		pages[id] = pg
	}
	return menus, pages
}

// visibleNav returns the part of a page's nav the caller may see: an item
// shows when it names no permission or the caller holds its plugin
// permission or its core one. Groups left empty are dropped, and nil means
// nothing is left, so the console falls back to the page's own tabs instead
// of drawing an empty sidebar. The result is a copy: the manifest is shared
// by every caller.
func visibleNav(nav *manifest.PageNav, allowed, allowedCore func(perm string) bool) *manifest.PageNav {
	var groups []manifest.NavGroup
	for _, g := range nav.Groups {
		var items []manifest.NavItem
		for _, it := range g.Items {
			if (it.Permission == "" && it.CorePermission == "") ||
				(it.Permission != "" && allowed(it.Permission)) ||
				(it.CorePermission != "" && allowedCore(it.CorePermission)) {
				items = append(items, it)
			}
		}
		if len(items) > 0 {
			g.Items = items
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	return &manifest.PageNav{Groups: groups}
}

// ---------------------------------------------------------------- market

func (a *API) marketSources(c *gin.Context) {
	if a.d.Market == nil {
		httpapi.OK(c, []any{})
		return
	}
	srcs, err := a.d.Market.ListSources(c.Request.Context())
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, srcs)
}

func (a *API) marketPlugins(c *gin.Context) {
	if a.d.Market == nil {
		marketList(c, []any{}, a.hostVersion())
		return
	}
	var sourceID int64
	if s := c.Query("source_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("invalid source_id"))
			return
		}
		sourceID = id
	}
	refresh, _ := strconv.ParseBool(c.Query("refresh"))
	list, err := a.d.Market.ListPlugins(c.Request.Context(), sourceID, refresh)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	marketList(c, list, a.hostVersion())
}

func (a *API) hostVersion() string {
	if a.d.Install == nil {
		return ""
	}
	return a.d.Install.HostVersion()
}

// marketList renders the market listing. data stays the plugin array (as
// before); host_version is a top-level sibling so existing clients keep
// working: {"data": [...], "host_version": "0.2.0"}.
func marketList(c *gin.Context, list any, hostVersion string) {
	c.JSON(http.StatusOK, gin.H{"data": list, "host_version": hostVersion})
}

// ---------------------------------------------------------------- publishers

func (a *API) listPublishers(c *gin.Context) {
	list, err := a.d.Install.ListPublishers(c.Request.Context())
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, list)
}

func (a *API) createPublisher(c *gin.Context) {
	var in install.CreatePublisherInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	p, err := a.d.Install.CreatePublisher(ctx(c), in, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, p)
}

func (a *API) addPublisherKey(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in install.KeyInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	p, err := a.d.Install.AddPublisherKey(ctx(c), id, in, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, p)
}

type revokeBody struct {
	Reason string `json:"reason"`
}

func (a *API) revokeBody(c *gin.Context) (string, bool) {
	var in revokeBody
	if c.Request.ContentLength > 0 && !httpapi.BindJSON(c, &in) {
		return "", false
	}
	if in.Reason == "" {
		in.Reason = "revoked by administrator"
	}
	return in.Reason, true
}

func (a *API) revokePublisher(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	reason, ok := a.revokeBody(c)
	if !ok {
		return
	}
	res, err := a.d.Install.RevokePublisher(ctx(c), id, reason, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, res)
}

func (a *API) revokeKey(c *gin.Context) {
	reason, ok := a.revokeBody(c)
	if !ok {
		return
	}
	res, err := a.d.Install.RevokeKey(ctx(c), c.Param("key_id"), reason, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, res)
}
