package install

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/grants"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Review is the consent screen payload (CONTRACTS §5.7).
type Review struct {
	PluginKey        string              `json:"plugin_key"`
	Version          string              `json:"version"`
	Name             core.LocalizedText  `json:"name"`
	Description      core.LocalizedText  `json:"description,omitempty"`
	Icon             string              `json:"icon,omitempty"`
	Publisher        string              `json:"publisher"`
	Trust            string              `json:"trust"`
	SignatureStatus  string              `json:"signature_status"`
	KeyID            string              `json:"key_id,omitempty"`
	ConsentStatus    string              `json:"consent_status"`
	HostCompat       string              `json:"host_compat"`
	HostCompatOK     bool                `json:"host_compat_ok"`
	PackageSHA256    string              `json:"package_sha256"`
	PackageSize      int64               `json:"package_size"`
	UpgradeFrom      string              `json:"upgrade_from,omitempty"`
	Capabilities     []string            `json:"capabilities"`
	GatewayEndpoints []ReviewEndpoint    `json:"gateway_endpoints"`
	Platforms        []ReviewPlatform    `json:"platforms"`
	AccountTypes     []ReviewAccountType `json:"account_types"`
	Hooks            []ReviewHook        `json:"hooks"`
	Jobs             []ReviewJob         `json:"jobs"`
	Events           []string            `json:"events"`
	Routes           []ReviewRoute       `json:"routes"`
	Menus            []ReviewMenu        `json:"menus"`
	Slots            []ReviewSlot        `json:"slots"`
	UserPermissions  []ReviewUserPerm    `json:"user_permissions"`
	Database         *ReviewDatabase     `json:"database"`
	Resources        ReviewResources     `json:"resources"`
	ExternalServices []string            `json:"external_services"`
	HostPermissions  []ReviewHostPerm    `json:"host_permissions"`
	Diff             *ReviewDiff         `json:"diff,omitempty"`
	// PermissionGroups and RoleColumns lay out the consent matrix of
	// UserPermissions: its rows' groups and its columns, every role, as the
	// viewer may name them.
	PermissionGroups []manifest.PermissionGroup `json:"permission_groups"`
	RoleColumns      []ReviewRoleColumn         `json:"role_columns"`
	// RemovedUserPermissions are the permissions of the plugin this version
	// no longer declares (upgrades only): marked removed, their grants kept
	// (D6).
	RemovedUserPermissions []ReviewRemovedPerm `json:"removed_user_permissions"`
	// NewRoleGrants are the (role, scope) pairs the permissions this
	// version creates go to without overrides (manifest defaults and the
	// holders of their inheritFrom keys now), marked as the viewer would
	// consent; RoleGrantsNeedConsent is set when one of them needs
	// consent, so an upgrade is not carried over (rolegrants.go).
	NewRoleGrants         []RoleGrant `json:"new_role_grants"`
	RoleGrantsNeedConsent bool        `json:"role_grants_need_consent"`
	// NeedsDBSchemaUnconfined: granting the db.schema this version asks for
	// takes plugin:grant:db_schema_unconfined (dbSchemaUnconfined), so the
	// console can tell before the consent is sent.
	NeedsDBSchemaUnconfined bool `json:"needs_db_schema_unconfined"`
	// RequestAccess is the fixed statement shown for a plugin declaring
	// account types: it receives everything about the accounts of its types
	// (proxy included) and the whole of every request they serve. It is how
	// a platform plugin works, not a permission to grant or deny
	// (forwarding v2, D15; CONTRACTS §124).
	RequestAccess core.LocalizedText `json:"request_access,omitempty"`
	UploadedAt    time.Time          `json:"uploaded_at"`
}

// requestAccessStatement is Review.RequestAccess.
var requestAccessStatement = core.LocalizedText{
	"en": "This plugin receives everything about the accounts of the account types it declares — their settings, " +
		"decrypted credentials and proxy (address, username and password) — and the full content of every request " +
		"sent through them and the data relevant to their responses, and it decides where each request is sent. " +
		"The core sends every request; the plugin never connects to the network itself. This is how every platform " +
		"plugin works; it is not a permission and cannot be turned off.",
	"zh": "此插件会收到它声明的账号类型下账号的全部信息（设置、解密的凭证、代理的地址、用户名和密码），" +
		"以及经这些账号发出的每个请求的完整内容和与响应有关的数据，并决定请求发往哪个地址。" +
		"请求都由核心发出，插件自己不联网。所有平台插件都是这样工作的：这不是一项权限，不能取消。",
}

// ReviewEndpoint is a gateway endpoint of a declared platform. In
// Review.GatewayEndpoints it carries id and platform as well.
type ReviewEndpoint struct {
	ID       string `json:"id,omitempty"`
	Platform string `json:"platform,omitempty"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Billing  string `json:"billing"`
}

// ReviewPlatform is a platform declared by the plugin (CONTRACTS §13).
type ReviewPlatform struct {
	ID        string             `json:"id"`
	Label     core.LocalizedText `json:"label,omitempty"`
	Endpoints []ReviewEndpoint   `json:"endpoints"`
}

// ReviewAccountType is one top-level account type (ARCHITECTURE 6.6).
type ReviewAccountType struct {
	PlatformDeclarations []manifest.AccountPlatform `json:"platform_declarations"`
	ID                   string                     `json:"id"`
	Label                core.LocalizedText         `json:"label"`
	Platforms            []string                   `json:"platforms"`
	FormMode             string                     `json:"form_mode"`
}

type ReviewHook struct {
	ID             string   `json:"id,omitempty"`
	Point          string   `json:"point"`
	Order          int      `json:"order"`
	Protocols      []string `json:"protocols"`
	Models         []string `json:"models"`
	Groups         []string `json:"groups"`
	Needs          []string `json:"needs"`
	MaxPromptBytes int      `json:"max_prompt_bytes"`
	TimeoutMs      int      `json:"timeout_ms"`
	Failure        string   `json:"failure"`
}

type ReviewJob struct {
	ID         string `json:"id"`
	Schedule   string `json:"schedule"`
	TimeoutSec int    `json:"timeout_sec"`
}

type ReviewRoute struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Scope      string `json:"scope"`
	Permission string `json:"permission,omitempty"`
}

type ReviewMenu struct {
	ID         string             `json:"id"`
	Label      core.LocalizedText `json:"label"`
	Page       string             `json:"page"`
	PageType   string             `json:"page_type"`
	Permission string             `json:"permission,omitempty"`
}

type ReviewSlot struct {
	Slot       string `json:"slot"`
	Component  string `json:"component"`
	Permission string `json:"permission,omitempty"`
}

type ReviewUserPerm struct {
	Key         string                    `json:"key"` // local
	Label       core.LocalizedText        `json:"label"`
	Description core.LocalizedText        `json:"description,omitempty"`
	Risk        string                    `json:"risk"`
	Group       string                    `json:"group,omitempty"`
	Requires    []string                  `json:"requires,omitempty"` // local keys
	Scope       *manifest.PermissionScope `json:"scope,omitempty"`    // Resource = ResourceName(), as GET /permissions
	// Defaults are the manifest's suggestion (DefaultGrants): role ->
	// canonical suffixes, as Cells' Grant.
	Defaults    map[string][]string `json:"defaults"`
	InheritFrom []string            `json:"inherit_from,omitempty"`
	// New: the catalog has no such permission yet; the consent creates it,
	// and only then do its grants (new_permission_roles) apply.
	New bool `json:"new"`
	// Revived: removed earlier and declared again, its grants come back as
	// they were (D6).
	Revived bool `json:"revived"`
	// Renamed: it inherits from a key the catalog has, so it gets no
	// defaults, only that key's holders.
	Renamed bool `json:"renamed"`
	// Cells cover every role of Review.RoleColumns.
	Cells map[string]ReviewCell `json:"cells"`
}

// ReviewCell is one (permission, role) of the consent matrix, for the viewer.
type ReviewCell struct {
	// Grant is what a consent without override gives (canonical suffixes;
	// [] none, [""] every row). For a revived key, the grants it revives
	// with; for any other existing key, []. Absent on a revived or renamed
	// row for a viewer without role:read: it tells who holds the old key
	// (C4).
	Grant    []string `json:"grant,omitzero"`
	Nameable bool     `json:"nameable"`
	Reason   string   `json:"reason,omitempty"` // role_define|critical|superuser_role|not_editable|not_assignable|held
	Withheld bool     `json:"withheld"`         // Grant goes to review instead (D2)
}

// ReviewRoleColumn is one role of the consent matrix, for the viewer.
type ReviewRoleColumn struct {
	Key       string             `json:"key"`
	Name      core.LocalizedText `json:"name"`
	Builtin   bool               `json:"builtin"`
	Superuser bool               `json:"superuser"`
	Nameable  bool               `json:"nameable"`
	Reason    string             `json:"reason,omitempty"`
}

// ReviewRemovedPerm is a permission an upgrade no longer declares.
type ReviewRemovedPerm struct {
	Key   string             `json:"key"` // local
	Label core.LocalizedText `json:"label"`
	// Holders are the role keys holding it, super admins aside; absent for
	// a viewer without role:read (C4).
	Holders []string `json:"holders,omitzero"`
}

type ReviewDatabase struct {
	Schema     string   `json:"schema"`
	Migrations []string `json:"migrations"`
}

type ReviewResources struct {
	MemoryMB     int     `json:"memory_mb"`
	CPU          float64 `json:"cpu"`
	MaxThreads   int     `json:"max_threads"`
	MaxOpenFiles int     `json:"max_open_files"`
}

type ReviewHostPerm struct {
	ID       string             `json:"id"`
	Risk     string             `json:"risk"`
	Scope    map[string]any     `json:"scope"`
	Reason   core.LocalizedText `json:"reason,omitempty"`
	Optional bool               `json:"optional"`
	// Requires is the extra console permission needed to grant it.
	Requires string `json:"requires"`
	// Current grant (upgrades only).
	CurrentStatus string         `json:"current_status,omitempty"`
	CurrentScope  map[string]any `json:"current_scope,omitempty"`
	// Revoked: an administrator revoked it and no consent has granted it
	// since (grants.Revoked); a consent grants it only when it names it.
	Revoked bool `json:"revoked"`
}

type ReviewDiff struct {
	Added   []DiffItem `json:"added"`
	Widened []DiffItem `json:"widened"`
	Removed []DiffItem `json:"removed"`
}

type DiffItem struct {
	ID            string         `json:"id"`
	Risk          string         `json:"risk"`
	Scope         map[string]any `json:"scope,omitempty"`
	PreviousScope map[string]any `json:"previous_scope,omitempty"`
}

// Grant is one plugin_permission_grants row.
type Grant struct {
	Permission    string         `json:"permission"`
	Risk          string         `json:"risk"`
	Scope         map[string]any `json:"scope"`
	Status        string         `json:"status"`
	PluginVersion string         `json:"plugin_version"`
	GrantedBy     *int64         `json:"granted_by"`
	GrantedAt     time.Time      `json:"granted_at"`
}

// RequiredGrantPermission maps a risk level to the console permission an
// operator needs to grant it ("" when plugin:install is enough).
func RequiredGrantPermission(risk string) string {
	switch risk {
	case manifest.RiskHigh:
		return PermGrantHigh
	case manifest.RiskCritical:
		return PermGrantCritical
	}
	return ""
}

func riskOf(id string) string {
	if r, ok := manifest.HostPermissionRisk[id]; ok {
		return r
	}
	return manifest.RiskCritical
}

// LoadGrants reads all grant rows of a plugin: what its active version is
// granted.
func LoadGrants(ctx context.Context, q *gorm.DB, key string) (map[string]Grant, error) {
	return loadGrants(pluginGrants(store.G(ctx, q), key))
}

// grantColumns are the columns of a grant row a Grant shows.
const grantColumns = "permission, scope, status, plugin_version, granted_by, granted_at"

// pluginGrants selects the grant rows of plugin key (LoadGrants).
func pluginGrants(db *gorm.DB, key string) *gorm.DB {
	return db.Model(&model.PluginPermissionGrant{}).Select(grantColumns).Where("plugin_key = ?", key)
}

// loadPriorGrants reads what an upgrade is reviewed and decided against:
// the plugin's grants and its revoked host permissions (grants.Revoked).
func loadPriorGrants(ctx context.Context, q *gorm.DB, key string) (map[string]Grant, map[string]bool, error) {
	current, err := LoadGrants(ctx, q, key)
	if err != nil {
		return nil, nil, err
	}
	revoked, err := grants.Revoked(ctx, q, key)
	return current, revoked, err
}

// LoadVersionGrants reads the grant rows key@version runs with: the set
// staged for it while it is not active, else the plugin's grants.
func LoadVersionGrants(ctx context.Context, q *gorm.DB, key, version string) (map[string]Grant, error) {
	return loadGrants(grants.VersionGrantsQuery(store.G(ctx, q), key, version))
}

// grantRow is a grant row as a Grant shows it.
type grantRow struct {
	Permission    string          `gorm:"column:permission"`
	Scope         json.RawMessage `gorm:"column:scope"`
	Status        string          `gorm:"column:status"`
	PluginVersion string          `gorm:"column:plugin_version"`
	GrantedBy     *int64          `gorm:"column:granted_by"`
	GrantedAt     time.Time       `gorm:"column:granted_at"`
}

// loadGrants reads the grant rows q selects, by permission.
func loadGrants(q *gorm.DB) (map[string]Grant, error) {
	var rows []grantRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]Grant, len(rows))
	for _, r := range rows {
		g := Grant{Permission: r.Permission, Risk: riskOf(r.Permission), Scope: map[string]any{}, Status: r.Status,
			PluginVersion: r.PluginVersion, GrantedBy: r.GrantedBy, GrantedAt: r.GrantedAt}
		_ = json.Unmarshal(r.Scope, &g.Scope)
		out[g.Permission] = g
	}
	return out, nil
}

// SortedGrants returns grants ordered by permission id.
func SortedGrants(m map[string]Grant) []Grant {
	out := make([]Grant, 0, len(m))
	for _, g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Permission < out[j].Permission })
	return out
}

// buildReview renders the review. current is nil for first installs.
func buildReview(m *manifest.Manifest, files map[string][]byte, ver *pkg.Verification, hostVersion string, current map[string]Grant) *Review {
	r := &Review{
		PluginKey:              m.Key,
		Version:                m.Version,
		Name:                   core.LocalizedText(m.Name),
		Description:            core.LocalizedText(m.Description),
		Icon:                   m.Icon,
		Publisher:              ver.Publisher,
		Trust:                  ver.Trust,
		SignatureStatus:        ver.SignatureStatus,
		KeyID:                  ver.KeyID,
		HostCompat:             m.HostCompat,
		Capabilities:           []string{},
		GatewayEndpoints:       []ReviewEndpoint{},
		Platforms:              []ReviewPlatform{},
		AccountTypes:           []ReviewAccountType{},
		Hooks:                  []ReviewHook{},
		Jobs:                   []ReviewJob{},
		Events:                 []string{},
		Routes:                 []ReviewRoute{},
		Menus:                  []ReviewMenu{},
		Slots:                  []ReviewSlot{},
		UserPermissions:        []ReviewUserPerm{},
		PermissionGroups:       append([]manifest.PermissionGroup{}, m.PermissionGroups...),
		RoleColumns:            []ReviewRoleColumn{},
		RemovedUserPermissions: []ReviewRemovedPerm{},
		ExternalServices:       append([]string{}, m.ExternalServices...),
		HostPermissions:        []ReviewHostPerm{},
	}
	r.HostCompatOK, _ = pkg.HostCompatible(m.HostCompat, hostVersion)
	if len(m.AccountTypes) > 0 {
		r.RequestAccess = requestAccessStatement
	}
	for _, c := range m.Capabilities {
		r.Capabilities = append(r.Capabilities, c.ID)
	}
	for _, p := range m.Platforms {
		rp := ReviewPlatform{ID: p.ID, Label: core.LocalizedText(p.Label), Endpoints: []ReviewEndpoint{}}
		for _, e := range p.Endpoints {
			billing := e.Billing
			if billing == "" {
				billing = "usage"
			}
			rp.Endpoints = append(rp.Endpoints, ReviewEndpoint{Method: e.Method, Path: e.Path, Protocol: e.Protocol, Billing: billing})
			r.GatewayEndpoints = append(r.GatewayEndpoints, ReviewEndpoint{ID: e.ID, Platform: p.ID, Method: e.Method, Path: e.Path,
				Protocol: e.Protocol, Billing: billing})
		}
		r.Platforms = append(r.Platforms, rp)
	}
	for _, at := range m.AccountTypes {
		pfs := make([]string, 0, len(at.Platforms))
		for _, ap := range at.Platforms {
			pfs = append(pfs, ap.Platform)
		}
		r.AccountTypes = append(r.AccountTypes, ReviewAccountType{ID: at.ID, Label: core.LocalizedText(at.Label), Platforms: pfs, PlatformDeclarations: at.Platforms, FormMode: at.Form.Mode})
	}
	for _, h := range m.Hooks {
		failure := h.Failure
		if failure == "" {
			failure = "open"
		}
		r.Hooks = append(r.Hooks, ReviewHook{ID: h.ID, Point: h.Point, Order: h.Order, Protocols: h.Match.Protocols, Models: h.Match.Models,
			Groups: h.Match.Groups, Needs: h.Needs, MaxPromptBytes: h.MaxPromptBytes, TimeoutMs: h.TimeoutMs, Failure: failure})
	}
	for _, j := range m.Jobs {
		r.Jobs = append(r.Jobs, ReviewJob{ID: j.ID, Schedule: j.Schedule, TimeoutSec: j.TimeoutSec})
	}
	if m.Events != nil {
		r.Events = append(r.Events, m.Events.Subscribe...)
	}
	for _, rt := range m.Routes {
		r.Routes = append(r.Routes, ReviewRoute{Method: rt.Method, Path: rt.Path, Scope: rt.Scope, Permission: rt.Permission})
	}
	if m.UI != nil {
		for _, mn := range m.UI.Menus {
			r.Menus = append(r.Menus, ReviewMenu{ID: mn.ID, Label: core.LocalizedText(mn.Label), Page: mn.Page,
				PageType: m.UI.Pages[mn.Page].Type, Permission: mn.Permission})
		}
		for _, sl := range m.UI.Slots {
			r.Slots = append(r.Slots, ReviewSlot{Slot: sl.Slot, Component: sl.Component, Permission: sl.Permission})
		}
	}
	for _, up := range m.UserPermissions {
		defaults := up.DefaultGrants()
		if defaults == nil {
			defaults = map[string][]string{}
		}
		r.UserPermissions = append(r.UserPermissions, ReviewUserPerm{Key: up.Key, Label: core.LocalizedText(up.Label),
			Description: core.LocalizedText(up.Description), Risk: up.RiskLevel(), Group: up.Group, Requires: up.Requires,
			Scope: core.StoredPluginScope(up.Scope), Defaults: defaults, InheritFrom: up.InheritFrom, Cells: map[string]ReviewCell{}})
	}
	if m.Database != nil {
		r.Database = &ReviewDatabase{Schema: m.Database.Schema, Migrations: pkg.MigrationFiles(files, m.Database.Migrations)}
	}
	if m.Resources != nil {
		r.Resources = ReviewResources{MemoryMB: m.Resources.MemoryMB, CPU: m.Resources.CPU, MaxThreads: m.Resources.MaxThreads, MaxOpenFiles: m.Resources.MaxOpenFiles}
	}
	for _, hp := range m.HostPermissions {
		risk := riskOf(hp.ID)
		item := ReviewHostPerm{ID: hp.ID, Risk: risk, Scope: pkg.NormalizeScope(hp.Scope), Reason: core.LocalizedText(hp.Reason),
			Optional: hp.Optional, Requires: RequiredGrantPermission(risk)}
		if g, ok := current[hp.ID]; ok {
			item.CurrentStatus = g.Status
			item.CurrentScope = g.Scope
		}
		r.HostPermissions = append(r.HostPermissions, item)
	}
	if current != nil {
		r.Diff = diffGrants(m, current)
	}
	return r
}

// noteRevoked marks the host permissions an administrator revoked, which
// the consent grants only when it names them (decide).
func (r *Review) noteRevoked(revoked map[string]bool) {
	for i := range r.HostPermissions {
		r.HostPermissions[i].Revoked = revoked[r.HostPermissions[i].ID]
	}
}

// diffGrants compares the requested permissions with the current grants.
func diffGrants(m *manifest.Manifest, current map[string]Grant) *ReviewDiff {
	d := &ReviewDiff{Added: []DiffItem{}, Widened: []DiffItem{}, Removed: []DiffItem{}}
	requested := map[string]bool{}
	for _, hp := range m.HostPermissions {
		requested[hp.ID] = true
		scope := pkg.NormalizeScope(hp.Scope)
		g, ok := current[hp.ID]
		switch {
		case !ok || g.Status != GrantGranted:
			d.Added = append(d.Added, DiffItem{ID: hp.ID, Risk: riskOf(hp.ID), Scope: scope})
		case !pkg.ScopeWithin(scope, g.Scope):
			d.Widened = append(d.Widened, DiffItem{ID: hp.ID, Risk: riskOf(hp.ID), Scope: scope, PreviousScope: g.Scope})
		}
	}
	for _, g := range SortedGrants(current) {
		if !requested[g.Permission] && g.Status == GrantGranted {
			d.Removed = append(d.Removed, DiffItem{ID: g.Permission, Risk: g.Risk, PreviousScope: g.Scope})
		}
	}
	return d
}

// resolveRoleGrants fills in the consent matrix for the viewer actorID, as
// a consent without overrides by it would settle given the catalog now
// (markPlan in the viewer's mode: a system viewer, a built-in plugin's
// upload, may name every role): the role columns, each permission's cells
// and flags, the role grants (roleGrants, planRoleGrants) and, on an
// upgrade (r.Diff set), the permissions this version no longer declares.
// Who holds an existing key is role:read's to see (C4): without it the
// viewer gets no cell grants of revived or renamed rows, no inherited role
// grants and no holders of removed keys.
func (s *Service) resolveRoleGrants(ctx context.Context, r *Review, m *manifest.Manifest, actorID int64) error {
	mode := consentModeOf(ctx, false)
	seeHolders, err := s.seesHolders(ctx, actorID)
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		cat, err := s.catalog()
		if err != nil {
			return err
		}
		existing, err := loadCatalogPerms(ctx, cat, tx, m.Key)
		if err != nil {
			return err
		}
		x := consentMatrix{roles: roleGrants(m, nil, existing)}
		if x.plan, _, err = s.planRoleGrants(ctx, tx, m, existing, x.roles, nil, r.Diff != nil); err != nil {
			return err
		}
		if x.gs, x.canDefine, err = s.noteRoleColumns(ctx, tx, r, actorID, mode); err != nil {
			return err
		}
		markPlan(x.plan, x.gs, x.canDefine, mode)
		r.RoleGrantsNeedConsent = needConsent(x.plan)
		r.NewRoleGrants = slices.DeleteFunc(slices.Clone(x.plan), func(g RoleGrant) bool { return !seeHolders && g.Via == viaInherit })
		if r.NewRoleGrants == nil {
			r.NewRoleGrants = []RoleGrant{}
		}
		var revivedKeys []string
		for _, up := range m.UserPermissions {
			if key := manifest.PermissionKey(m.Key, up.Key); existing.removed(key) {
				revivedKeys = append(revivedKeys, key)
			}
		}
		if len(revivedKeys) > 0 {
			if x.held, err = cat.PermissionHolders(ctx, tx, revivedKeys); err != nil {
				return err
			}
		}
		for i := range r.UserPermissions {
			up := &r.UserPermissions[i]
			key := manifest.PermissionKey(m.Key, up.Key)
			decl, _ := m.FindUserPermission(up.Key)
			up.New, up.Revived, up.Renamed = !existing.has(key), existing.removed(key), renamed(m, decl, existing)
			for _, col := range r.RoleColumns {
				c := x.cell(key, up, col)
				if !seeHolders && (up.Revived || up.Renamed) {
					// Both come from the old key's holders (C4).
					c.Grant, c.Withheld = nil, false
				}
				up.Cells[col.Key] = c
			}
		}
		if r.Diff == nil {
			return nil
		}
		return s.noteRemovedPerms(ctx, tx, r, m, seeHolders)
	})
}

// consentMatrix is what the consent matrix's cells are read from: the
// viewer's rights (nameable), the grants a consent without overrides plans
// (roleGrants, planRoleGrants) and the grants of the revived keys.
type consentMatrix struct {
	gs        core.PluginGrantees
	canDefine bool
	roles     map[string]map[string][]string
	plan      []RoleGrant
	held      map[string][]core.PermissionHolder
}

// cell is the consent matrix cell of permission key (up) and role column
// col. A new key's grant is its roles and the scopes plan inherits for the
// role, withheld when the viewer may not name it; a revived key's grant is
// what it gives the role back; any other existing key's none.
func (x *consentMatrix) cell(key string, up *ReviewUserPerm, col ReviewRoleColumn) ReviewCell {
	c := ReviewCell{Grant: []string{}, Reason: col.Reason}
	if col.Superuser {
		return c
	}
	c.Nameable, c.Reason = nameable(x.gs, x.canDefine, col.Key, up.Risk)
	var grant []string
	switch {
	case up.Revived:
		for _, h := range x.held[key] {
			if h.Role == col.Key {
				grant = append(grant, h.Scope)
			}
		}
	case up.New:
		grant = slices.Clone(x.roles[up.Key][col.Key])
		for _, g := range x.plan {
			if g.Permission == key && g.Role == col.Key && g.Via == viaInherit {
				grant = append(grant, g.Scope)
			}
		}
		c.Withheld = len(grant) > 0 && !c.Nameable
	}
	if grant = manifest.CanonicalSuffixes(grant); grant != nil {
		c.Grant = grant
	}
	return c
}

// noteRoleColumns sets the consent matrix's columns: every role, super
// admins first (they hold every permission, so nothing is written to them),
// then built-in ones, as the viewer actorID may name them for a permission
// of low risk. It returns what it read of the viewer (grantees).
func (s *Service) noteRoleColumns(ctx context.Context, tx *gorm.DB, r *Review, actorID int64, mode consentMode) (core.PluginGrantees, bool, error) {
	var roles []roleColumnRow
	if err := roleColumns(store.G(ctx, tx)).Find(&roles).Error; err != nil {
		return core.PluginGrantees{}, false, err
	}
	cols := make([]ReviewRoleColumn, len(roles))
	for i, r := range roles {
		cols[i] = ReviewRoleColumn{Key: r.Key, Name: r.Name.V, Builtin: r.Builtin, Superuser: r.Superuser}
	}
	var keys []string
	for _, c := range cols {
		if !c.Superuser {
			keys = append(keys, c.Key)
		}
	}
	gs, canDefine, err := s.grantees(ctx, tx, actorID, keys, mode)
	if err != nil {
		return core.PluginGrantees{}, false, err
	}
	for i := range cols {
		c := &cols[i]
		if c.Superuser {
			c.Reason = core.GrantDenySuperuserRole
			continue
		}
		c.Nameable, c.Reason = nameable(gs, canDefine, c.Key, manifest.RiskLow)
	}
	r.RoleColumns = append([]ReviewRoleColumn{}, cols...)
	return gs, canDefine, nil
}

// noteRemovedPerms lists the permissions of the plugin the catalog has but
// m no longer declares (still active or disabled: SyncPlugin's removal
// rule), with the roles holding them when seeHolders.
func (s *Service) noteRemovedPerms(ctx context.Context, tx *gorm.DB, r *Review, m *manifest.Manifest, seeHolders bool) error {
	cat, err := s.catalog()
	if err != nil {
		return err
	}
	perms, err := cat.PluginPermissions(ctx, tx, m.Key)
	if err != nil {
		return err
	}
	var gone []core.PluginPermission
	for _, p := range perms {
		local, _ := manifest.LocalPermission(m.Key, p.Key)
		if _, declared := m.FindUserPermission(local); !declared && p.Status != permStatusRemoved {
			gone = append(gone, p)
		}
	}
	var held map[string][]core.PermissionHolder
	if seeHolders && len(gone) > 0 {
		keys := make([]string, len(gone))
		for i, p := range gone {
			keys[i] = p.Key
		}
		if held, err = cat.PermissionHolders(ctx, tx, keys); err != nil {
			return err
		}
	}
	for _, p := range gone {
		rp := ReviewRemovedPerm{Label: p.Label}
		rp.Key, _ = manifest.LocalPermission(m.Key, p.Key)
		if seeHolders {
			rp.Holders = []string{}
		}
		for _, h := range held[p.Key] {
			if !slices.Contains(rp.Holders, h.Role) {
				rp.Holders = append(rp.Holders, h.Role)
			}
		}
		r.RemovedUserPermissions = append(r.RemovedUserPermissions, rp)
	}
	return nil
}

// noteDBSchema sets NeedsDBSchemaUnconfined from the manifest's db.schema
// request and the plugin's trust. A db.schema an upgrade carries over is not
// checked again (decide); the console then asks for no grant right.
func (s *Service) noteDBSchema(r *Review, m *manifest.Manifest) {
	asks := slices.ContainsFunc(m.HostPermissions, func(hp manifest.HostPermission) bool { return hp.ID == "db.schema" })
	r.NeedsDBSchemaUnconfined = asks && s.dbSchemaUnconfined(r.Trust == pkg.TrustOfficial)
}

// Review returns the review of a stored version (for the consent page), its
// consent matrix as actorID may name the roles.
func (s *Service) Review(ctx context.Context, key, version string, actorID int64) (*Review, error) {
	var (
		status, consent, sigStatus string
		keyID                      *string
		pubName, trust             *string
		size                       int64
		sha                        string
		uploadedAt                 time.Time
	)
	err := reviewedVersion(store.G(ctx, s.d.DB.Gorm), key, version).
		Row().Scan(&status, &consent, &sigStatus, &keyID, &pubName, &trust, &size, &sha, &uploadedAt)
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("plugin version not found")
	}
	if err != nil {
		return nil, err
	}
	p, err := s.Package(ctx, key, version)
	if err != nil {
		return nil, err
	}
	ver := &pkg.Verification{Publisher: p.Manifest.Publisher, Trust: pkg.TrustUnsigned, SignatureStatus: sigStatus}
	if pubName != nil {
		ver.Publisher, ver.Trust = *pubName, *trust
	}
	if keyID != nil {
		ver.KeyID = *keyID
	}
	var (
		current map[string]Grant
		revoked map[string]bool
	)
	upgradeFrom := ""
	if status != StatusAwaitingConsent {
		if current, revoked, err = loadPriorGrants(ctx, s.d.DB.Gorm, key); err != nil {
			return nil, err
		}
		upgradeFrom, _ = s.runningVersion(ctx, s.d.DB.Gorm, key, version)
	}
	r := buildReview(p.Manifest, p.Files, ver, s.opt.HostVersion, current)
	r.noteRevoked(revoked)
	if err := s.resolveRoleGrants(ctx, r, p.Manifest, actorID); err != nil {
		return nil, err
	}
	s.noteDBSchema(r, p.Manifest)
	r.ConsentStatus = consent
	r.PackageSHA256, r.PackageSize, r.UploadedAt = sha, size, uploadedAt
	r.UpgradeFrom = upgradeFrom
	return r, nil
}

// runningVersion is the version an upgrade to target replaces: the active
// version, else the newest approved version other than target.
func (s *Service) runningVersion(ctx context.Context, q *gorm.DB, key, target string) (string, error) {
	var v string
	err := runningVersion(store.G(ctx, q), key, target).Row().Scan(&v)
	return v, err
}

// runningVersion is the statement of Service.runningVersion.
func runningVersion(db *gorm.DB, key, target string) *gorm.DB {
	newest := db.Model(&model.PluginVersion{}).Select("version").
		Where("plugin_key = p.key AND consent_status = 'approved' AND version <> ?", target).Order("uploaded_at DESC").Limit(1)
	return db.Table("plugins AS p").Select("COALESCE(p.active_version, (?), '')", newest).Where("p.key = ?", key)
}

// roleColumnRow is a roles row as a consent matrix column shows it.
type roleColumnRow struct {
	Key       string                         `gorm:"column:key"`
	Name      store.JSON[core.LocalizedText] `gorm:"column:name"`
	Builtin   bool                           `gorm:"column:builtin"`
	Superuser bool                           `gorm:"column:superuser"`
}

// roleColumns selects the consent matrix's roles: super admins first, then
// built-in ones.
func roleColumns(db *gorm.DB) *gorm.DB {
	return db.Model(&model.Role{}).Select("key, name, builtin, superuser").Order("superuser DESC, builtin DESC, id")
}

// reviewedVersion selects what the review of key@version shows of the
// plugin, the version and its publisher.
func reviewedVersion(db *gorm.DB, key, version string) *gorm.DB {
	return db.Table("plugin_versions AS v").
		Select(`p.status, v.consent_status, v.signature_status, v.key_id, pub.name, pub.trust_level,
			v.package_size, v.package_sha256, v.uploaded_at`).
		Joins("JOIN plugins p ON p.key = v.plugin_key").
		Joins("LEFT JOIN publishers pub ON pub.id = v.publisher_id").
		Where("v.plugin_key = ? AND v.version = ?", key, version)
}
