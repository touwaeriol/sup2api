package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
)

// The plugin stores and their channels (CONTRACTS §137.6) and the market
// listing.

// marketStores lists the plugin stores (market.ListStores).
func (a *API) marketStores(c *gin.Context) {
	if a.d.Market == nil {
		httpapi.OK(c, []any{})
		return
	}
	stores, err := a.d.Market.ListStores(ctx(c))
	reply(c, stores, err)
}

// createStore adds a third-party store (market.CreateStore).
func (a *API) createStore(c *gin.Context) {
	var in market.StoreInput
	if !a.marketReady(c) || !httpapi.BindJSON(c, &in) {
		return
	}
	st, err := a.d.Market.CreateStore(ctx(c), in, actor(c))
	created(c, st, err)
}

// updateStore renames a store (market.UpdateStore).
func (a *API) updateStore(c *gin.Context) {
	var in market.StorePatch
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) || !httpapi.BindJSON(c, &in) {
		return
	}
	st, err := a.d.Market.UpdateStore(ctx(c), id, in, actor(c))
	reply(c, st, err)
}

// deleteStore deletes a third-party store (market.DeleteStore).
func (a *API) deleteStore(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) {
		return
	}
	noContent(c, a.d.Market.DeleteStore(ctx(c), id, actor(c)))
}

// marketSources lists the channels, of one store with ?store_id=
// (market.ListSources).
func (a *API) marketSources(c *gin.Context) {
	if a.d.Market == nil {
		httpapi.OK(c, []any{})
		return
	}
	storeID, ok := queryID(c, "store_id")
	if !ok {
		return
	}
	srcs, err := a.d.Market.ListSources(ctx(c), storeID)
	reply(c, srcs, err)
}

// createChannel adds a channel to a store (market.CreateChannel).
func (a *API) createChannel(c *gin.Context) {
	var in market.ChannelInput
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) || !httpapi.BindJSON(c, &in) {
		return
	}
	src, err := a.d.Market.CreateChannel(ctx(c), id, in, actor(c))
	created(c, src, err)
}

// created answers 201 with v, or err.
func created(c *gin.Context, v any, err error) {
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, v)
}

// noContent answers 204, or err.
func noContent(c *gin.Context, err error) {
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

// queryID is the positive id of query parameter name, 0 without one; it
// fails the request for an invalid one.
func queryID(c *gin.Context, name string) (int64, bool) {
	s := c.Query(name)
	if s == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("invalid "+name))
		return 0, false
	}
	return id, true
}

// updateChannel changes a channel (market.UpdateChannel).
func (a *API) updateChannel(c *gin.Context) {
	var in market.ChannelPatch
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) || !httpapi.BindJSON(c, &in) {
		return
	}
	src, err := a.d.Market.UpdateChannel(ctx(c), id, in, actor(c))
	reply(c, src, err)
}

// deleteChannel deletes a channel (market.DeleteChannel).
func (a *API) deleteChannel(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok || !a.marketReady(c) {
		return
	}
	noContent(c, a.d.Market.DeleteChannel(ctx(c), id, actor(c)))
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
	sourceID, ok := queryID(c, "source_id")
	if !ok {
		return
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
