package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	rc "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// PluginSummary is one row of GET /plugins.
type PluginSummary struct {
	Key             string             `json:"key"`
	Name            core.LocalizedText `json:"name"`
	Status          string             `json:"status"`
	StatusReason    string             `json:"status_reason"`
	ActiveVersion   *string            `json:"active_version"`
	DesiredVersion  *string            `json:"desired_version"`
	CurrentVersion  string             `json:"current_version"`
	Publisher       string             `json:"publisher"`
	Trust           string             `json:"trust"`
	SignatureStatus string             `json:"signature_status"`
	PendingVersions []string           `json:"pending_versions"`
	Nodes           NodeSummary        `json:"node_summary"`
	// Refusal is set when nodes refuse to load the plugin because it was
	// built for an older plugin contract (forwarding v2, design 9.2).
	Refusal      *Refusal  `json:"refusal"`
	EgressPolicy string    `json:"egress_policy"`
	InstalledAt  time.Time `json:"installed_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Builtin plugins ship with the image: they can be disabled, not uninstalled.
	Builtin     bool `json:"builtin"`
	HasSettings bool `json:"has_settings"` // the current version declares native ui.settings (not iframe, not an old package's schema)
	// Icon is the manifest icon of the current version: "text:<label>", or a
	// package path below AssetBase, which the core serves whether or not the
	// plugin runs, once the version is approved (registry.ReadVersionAsset).
	Icon      string `json:"icon"`
	AssetBase string `json:"asset_base"`
	// ResourceConflicts are the exclusive resources the current generation
	// of this node could not give the plugin (another plugin claimed them
	// first, or the core holds them): the console shows the plugin as
	// abnormal (audit 2026-10-09 P2-2).
	ResourceConflicts []core.ResourceConflict `json:"resource_conflicts"`
	// RoleGrantsToReview are the role grants an automatic (built-in) upgrade
	// withheld until someone confirms or rejects them (install
	// PendingRoleGrants).
	RoleGrantsToReview []install.RoleGrant `json:"role_grants_to_review"`
}

// NodeSummary counts live nodes by reported plugin state.
type NodeSummary struct {
	Total  int            `json:"total"`
	States map[string]int `json:"states"`
}

// pluginSummaries selects the PluginSummary columns of the plugins (as p),
// in scanSummary's order.
func pluginSummaries(db *gorm.DB) *gorm.DB {
	pending := db.Model(&model.PluginVersion{}).Select("array_agg(version ORDER BY uploaded_at)").
		Where("plugin_key = p.key AND consent_status = 'awaiting_consent'")
	return db.Table("plugins AS p").
		Select(`p.key, p.name, p.status, p.status_reason, p.active_version, p.desired_version,
			COALESCE(pub.name, ''), COALESCE(pub.trust_level, 'unsigned'), p.egress_policy, p.installed_at, p.updated_at, p.builtin,
			COALESCE((?), '{}')`, pending).
		Joins("LEFT JOIN publishers pub ON pub.id = p.publisher_id")
}

func scanSummary(row store.RowScanner) (*PluginSummary, error) {
	var s PluginSummary
	var name []byte
	if err := row.Scan(&s.Key, &name, &s.Status, &s.StatusReason, &s.ActiveVersion, &s.DesiredVersion,
		&s.Publisher, &s.Trust, &s.EgressPolicy, &s.InstalledAt, &s.UpdatedAt, &s.Builtin, &s.PendingVersions); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(name, &s.Name)
	return &s, nil
}

// fillSummary adds current version, signature status, publisher fallback,
// icon and the resource conflicts of the current generation.
func (a *API) fillSummary(ctx context.Context, s *PluginSummary) error {
	s.ResourceConflicts = []core.ResourceConflict{}
	if a.d.Registry != nil {
		if g := a.d.Registry.Current(); g != nil {
			if info, ok := g.Plugin(s.Key); ok && len(info.Conflicts) > 0 {
				s.ResourceConflicts = info.Conflicts
			}
		}
	}
	viewer, _ := core.UserID(ctx)
	pending, err := a.d.Install.RoleGrantsToReview(ctx, a.d.DB.Gorm, s.Key, viewer)
	if err != nil {
		return err
	}
	s.RoleGrantsToReview = append([]install.RoleGrant{}, pending...)
	v, err := install.CurrentVersion(ctx, a.d.DB.Gorm, s.Key)
	if err != nil {
		return err
	}
	s.CurrentVersion = v
	if v != "" {
		var sig string
		var pub string
		var versionHash string
		if err := store.G(ctx, a.d.DB.Gorm).Model(&model.PluginVersion{}).
			Select(`signature_status, manifest->>'publisher', COALESCE(manifest->'ui'->'settings'->>'mode' = 'native', false),
				COALESCE(manifest->>'icon', ''), version || '-' || left(package_sha256, 8)`).
			Where("plugin_key = ? AND version = ?", s.Key, v).Row().Scan(&sig, &pub, &s.HasSettings, &s.Icon, &versionHash); err != nil {
			return err
		}
		s.SignatureStatus = sig
		s.AssetBase = core.PluginAssetBase(s.Key, versionHash)
		if s.Publisher == "" {
			s.Publisher = pub
		}
	}
	return nil
}

// setNodes adds what the live nodes report of each plugin and, for those no
// node currently refuses, the refusal recorded by their latest rollout
// (recordedRefusals: one query for all of them).
func (a *API) setNodes(ctx context.Context, items []*PluginSummary, nodes []core.NodeStatus) error {
	var unrefused []string
	for _, s := range items {
		s.Nodes = summarizeNodes(nodes, s.Key)
		if s.Refusal = refusal(nodes, s.Key); s.Refusal == nil {
			unrefused = append(unrefused, s.Key)
		}
	}
	recorded, err := recordedRefusals(ctx, a.d.DB.Gorm, unrefused, a.d.Install.HostVersion())
	if err != nil {
		return err
	}
	for _, s := range items {
		if r, ok := recorded[s.Key]; ok {
			s.Refusal = r
		}
	}
	return nil
}

func (a *API) listPlugins(c *gin.Context) {
	rc := c.Request.Context()
	page, size := httpapi.Pagination(c)
	g := store.G(rc, a.d.DB.Gorm)
	var total int64
	if err := g.Model(&model.Plugin{}).Count(&total).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items, err := store.CollectRows(pluginSummaries(g).Order("p.key").Limit(size).Offset((page-1)*size),
		func(rows *sql.Rows) (*PluginSummary, error) { return scanSummary(rows) })
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	for _, s := range items {
		if err := a.fillSummary(rc, s); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	if err := a.setNodes(rc, items, a.liveNodes(rc)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// PluginDetail is GET /plugins/:key.
type PluginDetail struct {
	PluginSummary
	Manifest  *install.Review `json:"manifest"`
	Grants    []install.Grant `json:"grants"`
	NodeList  []NodeState     `json:"node_states"`
	Hooks     []HookInfo      `json:"hooks"`
	Jobs      []JobInfo       `json:"jobs"`
	Events    *EventsInfo     `json:"events"`
	Resources ResourcesInfo   `json:"resources"`
	Versions  []VersionInfo   `json:"versions"`
	Rollout   *core.Rollout   `json:"rollout"`
}

// MarshalJSON flattens the summary and renders node_states as "nodes"
// (per-node list); the summary stays under "node_summary" as in the list.
func (d PluginDetail) MarshalJSON() ([]byte, error) {
	type alias PluginDetail
	b, err := json.Marshal(alias(d))
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["nodes"] = m["node_states"]
	delete(m, "node_states")
	return json.Marshal(m)
}

// NodeState is one live node's view of a plugin.
type NodeState struct {
	NodeID        string          `json:"node_id"`
	BootID        string          `json:"boot_id"`
	Addr          string          `json:"addr"`
	LastHeartbeat time.Time       `json:"last_heartbeat"`
	State         json.RawMessage `json:"state"` // JSON owned by the plugin runtime
}

// HookInfo is a manifest hook; stats are filled when a source exists.
type HookInfo struct {
	ID        string   `json:"id,omitempty"`
	Point     string   `json:"point"`
	Order     int      `json:"order"`
	Failure   string   `json:"failure"`
	TimeoutMs int      `json:"timeout_ms"`
	Needs     []string `json:"needs"`
	Stats     any      `json:"stats"`
}

// VersionInfo is one plugin_versions row.
type VersionInfo struct {
	Version         string    `json:"version"`
	ConsentStatus   string    `json:"consent_status"`
	SignatureStatus string    `json:"signature_status"`
	PackageSHA256   string    `json:"package_sha256"`
	PackageSize     int64     `json:"package_size"`
	UploadedAt      time.Time `json:"uploaded_at"`
}

func (a *API) getPlugin(c *gin.Context) {
	rc := c.Request.Context()
	key := c.Param("key")
	s, err := scanSummary(pluginSummaries(store.G(rc, a.d.DB.Gorm)).Where("p.key = ?", key).Row())
	if store.IsNoRows(err) {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("plugin not found"))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := a.fillSummary(rc, s); err != nil {
		httpapi.Fail(c, err)
		return
	}
	nodes := a.liveNodes(rc)
	if err := a.setNodes(rc, []*PluginSummary{s}, nodes); err != nil {
		httpapi.Fail(c, err)
		return
	}
	d := PluginDetail{PluginSummary: *s, Grants: []install.Grant{}, NodeList: nodeStates(nodes, key), Hooks: []HookInfo{}, Jobs: []JobInfo{}}

	if s.CurrentVersion != "" {
		if d.Manifest, err = a.d.Install.DetailReview(rc, key, s.CurrentVersion, actor(c)); err != nil {
			httpapi.Fail(c, err)
			return
		}
		m, err := install.LoadManifest(rc, a.d.DB.Gorm, key, s.CurrentVersion)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		stats := map[string]core.HookStat{}
		if a.d.HookStats != nil {
			list, err := a.d.HookStats.HookStats(rc, key)
			if err != nil {
				httpapi.Fail(c, err)
				return
			}
			for _, st := range list {
				stats[st.HookID] = st
			}
		}
		for i, h := range m.Hooks {
			failure := h.Failure
			if failure == "" {
				failure = "open"
			}
			info := HookInfo{ID: h.ID, Point: h.Point, Order: h.Order, Failure: failure, TimeoutMs: h.TimeoutMs, Needs: h.Needs}
			// Stats are keyed by the manifest id, or the index when the id is empty.
			sid := h.ID
			if sid == "" {
				sid = strconv.Itoa(i)
			}
			if st, ok := stats[sid]; ok {
				info.Stats = st
			}
			d.Hooks = append(d.Hooks, info)
		}
		if d.Jobs, err = a.jobInfos(rc, key, m); err != nil {
			httpapi.Fail(c, err)
			return
		}
		if d.Events, err = a.eventsInfo(rc, key, m); err != nil {
			httpapi.Fail(c, err)
			return
		}
		if d.Resources, err = a.resourcesInfo(rc, key, m); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	grants, err := install.LoadGrants(rc, a.d.DB.Gorm, key)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	d.Grants = install.SortedGrants(grants)
	if d.Versions, err = a.versions(rc, key); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if a.d.Rollout != nil {
		if d.Rollout, err = a.d.Rollout.Current(rc, key); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	httpapi.OK(c, d)
}

func (a *API) versions(ctx context.Context, key string) ([]VersionInfo, error) {
	var rows []model.PluginVersion
	if err := versionRows(store.G(ctx, a.d.DB.Gorm), key).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]VersionInfo, len(rows))
	for i, v := range rows {
		out[i] = VersionInfo{Version: v.Version, ConsentStatus: core.ValueOr(v.ConsentStatus, ""), SignatureStatus: v.SignatureStatus,
			PackageSHA256: v.PackageSha256, PackageSize: v.PackageSize, UploadedAt: v.UploadedAt}
	}
	return out, nil
}

// versionRows selects the plugin_versions rows of key, newest first.
func versionRows(db *gorm.DB, key string) *gorm.DB {
	return db.Select("version", "consent_status", "signature_status", "package_sha256", "package_size", "uploaded_at").
		Where("plugin_key = ?", key).Order("uploaded_at DESC")
}

// ---------------------------------------------------------------- install flow

func (a *API) upload(c *gin.Context) {
	max := a.d.Install.Limits().MaxPackageBytes
	if max <= 0 {
		max = rc.PluginPackageMaxBytes
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "file", Code: "required", Message: "multipart field \"file\" is required (or too large)"}))
		return
	}
	if fh.Size > max {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(fmt.Sprintf("package exceeds %d bytes", max)))
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	r, err := a.d.Install.Upload(ctx(c), data, actor(c), install.UploadOptions{Source: "upload"})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, r)
}

func (a *API) installFromMarket(c *gin.Context) {
	var in struct {
		SourceID int64  `json:"source_id"`
		Key      string `json:"key"`
		Version  string `json:"version"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if in.SourceID <= 0 || in.Key == "" || in.Version == "" {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("source_id, key and version are required"))
		return
	}
	if a.d.Market == nil {
		httpapi.Fail(c, core.ErrUnavailable.WithMessage("market unavailable"))
		return
	}
	r, err := a.d.Market.Install(ctx(c), in.SourceID, in.Key, in.Version, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, r)
}

// pluginDatabase shows the plugin's database side (CONTRACTS §130).
func (a *API) pluginDatabase(c *gin.Context) {
	d, err := a.d.Install.Database(ctx(c), c.Param("key"))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, d)
}

func (a *API) review(c *gin.Context) {
	r, err := a.d.Install.Review(c.Request.Context(), c.Param("key"), c.Param("version"), actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, r)
}

func (a *API) consent(c *gin.Context) {
	var in install.ConsentRequest
	if !httpapi.BindJSON(c, &in) {
		return
	}
	res, err := a.d.Install.Consent(ctx(c), c.Param("key"), c.Param("version"), in, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, res)
}

// reviewRoleGrants confirms or rejects the role grants an automatic upgrade
// of the version withheld; it returns the grants it settled.
func (a *API) reviewRoleGrants(confirm bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		gs, err := a.d.Install.ReviewRoleGrants(ctx(c), c.Param("key"), c.Param("version"), confirm, actor(c))
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		httpapi.OK(c, gin.H{"grants": gs, "confirmed": confirm})
	}
}

func (a *API) reject(c *gin.Context) {
	if err := a.d.Install.Reject(ctx(c), c.Param("key"), c.Param("version"), actor(c)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}

func (a *API) uninstall(c *gin.Context) {
	var opt install.UninstallOptions
	opt.Purge, _ = strconv.ParseBool(c.DefaultQuery("purge", "false"))
	opt.PurgeAccounts, _ = strconv.ParseBool(c.DefaultQuery("purge_accounts", "false"))
	res, err := a.d.Install.Uninstall(ctx(c), c.Param("key"), opt, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, res)
}

// ---------------------------------------------------------------- rollouts

func (a *API) enable(c *gin.Context) {
	key := c.Param("key")
	st, ok := a.pluginStatus(c, key)
	if !ok {
		return
	}
	if st == install.StatusAwaitingConsent {
		httpapi.Fail(c, core.ErrConflict.WithMessage("the plugin is awaiting consent"))
		return
	}
	// The resources the version holds are checked again: another plugin may
	// have been approved, or the core upgraded, while it was disabled.
	if v, err := install.CurrentVersion(c.Request.Context(), a.d.DB.Gorm, key); err != nil {
		httpapi.Fail(c, err)
		return
	} else if v != "" {
		if err := a.d.Install.CheckResources(c.Request.Context(), key, v); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	rc, ok := a.rollout(c)
	if !ok {
		return
	}
	ro, err := rc.Enable(ctx(c), key, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	a.audit(c, "plugin.enable", key, rolloutDetail(ro))
	httpapi.OK(c, ro)
}

func (a *API) disable(c *gin.Context) {
	key := c.Param("key")
	var in struct {
		Reason string `json:"reason"`
	}
	if c.Request.ContentLength > 0 && !httpapi.BindJSON(c, &in) {
		return
	}
	if _, ok := a.pluginStatus(c, key); !ok {
		return
	}
	rc, ok := a.rollout(c)
	if !ok {
		return
	}
	if in.Reason == "" {
		in.Reason = "disabled by administrator"
	}
	ro, err := rc.Disable(ctx(c), key, actor(c), in.Reason)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	a.audit(c, "plugin.disable", key, map[string]any{"reason": in.Reason, "rollout": rolloutDetail(ro)})
	httpapi.OK(c, ro)
}

func (a *API) upgrade(c *gin.Context) {
	key := c.Param("key")
	var in struct {
		Version string `json:"version"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if in.Version == "" {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "version", Code: "required", Message: "version is required"}))
		return
	}
	var row model.PluginVersion
	err := store.G(c.Request.Context(), a.d.DB.Gorm).Select("consent_status", "signature_status").
		Where("plugin_key = ? AND version = ?", key, in.Version).Take(&row).Error
	consent, sig := core.ValueOr(row.ConsentStatus, ""), row.SignatureStatus
	if store.IsNoRows(err) {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("plugin version not found"))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if consent != install.ConsentApproved {
		httpapi.Fail(c, core.ErrConflict.WithMessage("the version has not been approved"))
		return
	}
	if sig == pkg.SigRevoked {
		httpapi.Fail(c, core.ErrPermissionDenied.WithMessage("the version's signature has been revoked"))
		return
	}
	if err := a.d.Install.CheckResources(c.Request.Context(), key, in.Version); err != nil {
		httpapi.Fail(c, err)
		return
	}
	rc, ok := a.rollout(c)
	if !ok {
		return
	}
	ro, err := rc.Upgrade(ctx(c), key, in.Version, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	a.audit(c, "plugin.upgrade", key, rolloutDetail(ro))
	httpapi.OK(c, ro)
}

func (a *API) currentRollout(c *gin.Context) {
	key := c.Param("key")
	if _, ok := a.pluginStatus(c, key); !ok {
		return
	}
	rc, ok := a.rollout(c)
	if !ok {
		return
	}
	ro, err := rc.Current(c.Request.Context(), key)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, ro)
}

func (a *API) cancelRollout(c *gin.Context) {
	key := c.Param("key")
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	rc, ok := a.rollout(c)
	if !ok {
		return
	}
	if err := rc.Cancel(ctx(c), key, id, actor(c)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	a.audit(c, "plugin.rollout.cancel", key, map[string]any{"rollout_id": id})
	httpapi.NoContent(c)
}

func rolloutDetail(ro *core.Rollout) map[string]any {
	if ro == nil {
		return map[string]any{}
	}
	return map[string]any{"rollout_id": ro.ID, "action": ro.Action, "from": ro.FromVersion, "to": ro.TargetVersion}
}

// ---------------------------------------------------------------- grants

func (a *API) listGrants(c *gin.Context) {
	key := c.Param("key")
	if _, ok := a.pluginStatus(c, key); !ok {
		return
	}
	g, err := install.LoadGrants(c.Request.Context(), a.d.DB.Gorm, key)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, install.SortedGrants(g))
}

// setGrant narrows or grants again one host permission; body {scope?}
// (an empty body or no scope: the scope the manifest requests). It returns
// the grant. The body is read whatever Content-Length says: a chunked
// request carries none, and skipping its scope would turn a narrowing into
// a grant of the whole requested scope.
func (a *API) setGrant(c *gin.Context) {
	var in struct {
		Scope map[string]any `json:"scope"`
	}
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(err.Error()))
		return
	}
	g, err := a.d.Install.SetGrant(ctx(c), c.Param("key"), c.Param("permission"), in.Scope, actor(c))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, g)
}

func (a *API) revokeGrant(c *gin.Context) {
	if err := a.d.Install.RevokeGrant(ctx(c), c.Param("key"), c.Param("permission"), actor(c)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.NoContent(c)
}
