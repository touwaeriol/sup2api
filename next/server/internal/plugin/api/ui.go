package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
)

// UIPlugin is one entry of GET /ui/plugins.
type UIPlugin struct {
	Key string `json:"key"` // the plugin id
	pkg.Identity

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
// caller's permissions (manifest.FilterUI); native entries are only returned
// for official/verified plugins.
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
		// A plugin key shows at any scope; the plugin routes narrow the data.
		vis := manifest.FilterUI(m.UI,
			func(perm string) bool { return ps.HasSome(manifest.PermissionKey(p.Key, perm)) }, ps.Has)
		item := UIPlugin{Key: p.Key, Identity: pkg.IdentityOf(p.Key), Version: p.Version, Name: core.LocalizedText(m.Name), AssetBase: p.AssetBase,
			Menus: vis.Menus, Pages: vis.Pages, Slots: vis.Slots, AccountActions: vis.AccountActions,
			Trust: p.Trust, HostUICompat: m.HostUICompat}
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

// createChannel adds a channel of the store (market.CreateChannel).
func (a *API) createChannel(c *gin.Context) {
	var in market.ChannelInput
	if !a.marketReady(c) || !httpapi.BindJSON(c, &in) {
		return
	}
	src, err := a.d.Market.CreateChannel(ctx(c), in, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, src)
}

// updateChannel changes a channel of the store (market.UpdateChannel).
func (a *API) updateChannel(c *gin.Context) {
	var in market.ChannelPatch
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) || !httpapi.BindJSON(c, &in) {
		return
	}
	src, err := a.d.Market.UpdateChannel(ctx(c), id, in, actor(c))
	reply(c, src, err)
}

// deleteChannel deletes a channel of the store (market.DeleteChannel).
func (a *API) deleteChannel(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) {
		return
	}
	if err := a.d.Market.DeleteChannel(ctx(c), id, actor(c)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

// marketReady fails the request when this node has no market client.
func (a *API) marketReady(c *gin.Context) bool {
	if a.d.Market == nil {
		httpapi.Fail(c, core.ErrUnavailable.WithMessage("market unavailable"))
		return false
	}
	return true
}

func (a *API) marketPlugins(c *gin.Context) {
	if a.d.Market == nil {
		marketList(c, &market.Listing{Plugins: []market.MarketPlugin{}}, a.hostVersion())
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

// marketList renders listing l: {"data": [...], "host_version": "0.2.0",
// "generated_at": ..., "fetched_at": ...}, the times only once the core
// has fetched an index.
func marketList(c *gin.Context, l *market.Listing, hostVersion string) {
	body := gin.H{"data": l.Plugins, "host_version": hostVersion}
	if !l.FetchedAt.IsZero() {
		body["generated_at"], body["fetched_at"] = l.GeneratedAt, l.FetchedAt
	}
	c.JSON(http.StatusOK, body)
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
