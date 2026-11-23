// Package api exposes the plugin lifecycle console API (CONTRACTS §5.7):
// /plugins, /publishers, /market, /nodes and /ui/plugins.
package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/secret"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/market"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
)

// Deps are the API collaborators. Nodes, Registry, Bus, Jobs and HookStats may be nil.
type Deps struct {
	DB        *store.DB
	Install   *install.Service
	Market    *market.Service
	Rollout   core.RolloutController
	Nodes     core.NodeRegistry
	Registry  core.PluginRegistry
	Authz     core.Authorizer
	Cipher    *secret.Cipher
	Bus       core.Bus
	Jobs      core.JobTrigger
	HookStats core.HookStatsSource
	Plugins   config.PluginConfig
}

// API holds the handlers. stepUp spends a step-up proof on a scope the
// handler picks (httpapi.Router.SpendStepUp), set by RegisterRoutes.
type API struct {
	d      Deps
	stepUp func(c *gin.Context, scope string) bool
}

// New builds the API.
func New(d Deps) *API { return &API{d: d} }

// Console permissions used by this module.
const (
	PermRead       = "plugin:read"
	PermInstall    = "plugin:install"
	PermManage     = "plugin:manage"
	PermUninstall  = "plugin:uninstall"
	PermEgressRead = "plugin:egress:read"
	PermMarketRead = "plugin:market:read"
	PermPubRead    = "publisher:read"
	PermPubManage  = "publisher:manage"
	PermNodeRead   = "node:read"
	PermSelfUI     = "self:plugin_ui"
)

// RegisterRoutes mounts every route of the module. Code execution and trust
// roots take a step-up proof each time (§80.2).
func (a *API) RegisterRoutes(r *httpapi.Router) {
	a.registerInstall(r)
	a.registerConsent(r)
	a.registerRuntime(r)

	r.Self(http.MethodGet, "/ui/plugins", PermSelfUI, a.uiPlugins)
	r.Perm(http.MethodGet, "/nodes", PermNodeRead, a.nodes)

	r.Perm(http.MethodGet, "/market/sources", PermMarketRead, a.marketSources)
	r.Perm(http.MethodGet, "/market/plugins", PermMarketRead, a.marketPlugins)

	r.Perm(http.MethodGet, "/publishers", PermPubRead, a.listPublishers)
	publishers := r.StepUp(core.StepUpPublisherManage)
	publishers.Perm(http.MethodPost, "/publishers", PermPubManage, a.createPublisher)
	publishers.Perm(http.MethodPost, "/publishers/:id/keys", PermPubManage, a.addPublisherKey)
	publishers.Perm(http.MethodPost, "/publishers/:id/revoke", PermPubManage, a.revokePublisher)
	publishers.Perm(http.MethodPost, "/publisher-keys/:key_id/revoke", PermPubManage, a.revokeKey)
}

// registerInstall mounts the plugin list and installing, uninstalling and
// reviewing plugins.
func (a *API) registerInstall(r *httpapi.Router) {
	r.Perm(http.MethodGet, "/plugins", PermRead, a.listPlugins)
	r.Perm(http.MethodGet, "/plugins/rollouts", PermRead, a.listRollouts)
	r.Perm(http.MethodGet, "/plugins/:key/rollouts", PermRead, a.listRollouts)
	r.Perm(http.MethodGet, "/plugins/:key/history", PermRead, a.pluginHistory)
	r.Perm(http.MethodGet, "/plugins/:key/database", PermRead, a.pluginDatabase)
	// An install spends its proof in the handler, once it has read whether
	// the body carries the consent (installStepUp): plugin.install, or the
	// consent's plugin.grant, whose grants are checked as the consent
	// route's. The install preview writes nothing and takes no proof.
	a.stepUp = r.SpendStepUp
	r.Perm(http.MethodPost, "/plugins/upload", PermInstall, a.upload).Uses(consentKeys...)
	r.Perm(http.MethodPost, "/plugins/install-from-market", PermInstall, a.installFromMarket).Uses(consentKeys...)
	r.Perm(http.MethodPost, "/plugins/review-upload", PermInstall, a.reviewUpload)
	r.Perm(http.MethodPost, "/plugins/review-from-market", PermInstall, a.reviewFromMarket)
	r.Perm(http.MethodGet, "/plugins/:key", PermRead, a.getPlugin)
	r.Perm(http.MethodDelete, "/plugins/:key", PermUninstall, a.uninstall)
	r.Perm(http.MethodGet, "/plugins/retired-keys", PermRead, a.listRetiredKeys)
	r.Perm(http.MethodPost, "/plugins/retired-keys/:key/release", PermUninstall, a.releaseRetiredKey)
	r.Perm(http.MethodGet, "/plugins/:key/uninstall", PermRead, a.uninstallState)
	r.Perm(http.MethodPost, "/plugins/:key/uninstall/confirm-stopped", PermUninstall, a.confirmStopped)
	r.Perm(http.MethodGet, "/plugins/:key/versions/:version/review", PermRead, a.review)
}

// registerConsent mounts the consent to a version and the rollouts.
func (a *API) registerConsent(r *httpapi.Router) {
	grant := r.StepUp(core.StepUpPluginGrant)
	// The grants and role grants chosen are checked against the caller
	// (install.checkGrantRights, checkRoleGrantRights).
	grant.Perm(http.MethodPost, "/plugins/:key/versions/:version/consent", PermInstall, a.consent).Uses(consentKeys...)
	r.Perm(http.MethodPost, "/plugins/:key/versions/:version/reject", PermInstall, a.reject)
	// Confirming writes the held-back grants into roles, the same decision as
	// the consent: the same step-up. Rejecting writes nothing.
	grant.Perm(http.MethodPost, "/plugins/:key/versions/:version/role-grants/confirm", PermInstall, a.reviewRoleGrants(true)).Uses(perm.RoleDefine)
	r.Perm(http.MethodPost, "/plugins/:key/versions/:version/role-grants/reject", PermInstall, a.reviewRoleGrants(false)).Uses(perm.RoleDefine)
	r.Perm(http.MethodPost, "/plugins/:key/enable", PermManage, a.enable)
	r.Perm(http.MethodPost, "/plugins/:key/disable", PermManage, a.disable)
	r.Perm(http.MethodPost, "/plugins/:key/upgrade", PermManage, a.upgrade)
	r.Perm(http.MethodGet, "/plugins/:key/rollouts/current", PermRead, a.currentRollout)
	r.Perm(http.MethodPost, "/plugins/:key/rollouts/:id/cancel", PermManage, a.cancelRollout)
}

// registerRuntime mounts what an installed plugin is run with: settings,
// grants, resources, egress, jobs, events and scope options.
func (a *API) registerRuntime(r *httpapi.Router) {
	grant := r.StepUp(core.StepUpPluginGrant)
	// plugin:read / plugin:manage, or the plugin's ui.settings.permission,
	// checked with CanAny in the handler: that key comes from the manifest,
	// a dynamic key no static route can name. The route's own key is the
	// plugin pages' self permission, which an access key must carry too.
	r.Self(http.MethodGet, "/plugins/:key/settings", PermSelfUI, a.getSettings).Uses(PermRead).UsesPluginKeys()
	r.Self(http.MethodPut, "/plugins/:key/settings", PermSelfUI, a.putSettings).Uses(PermManage).UsesPluginKeys()
	r.Perm(http.MethodGet, "/plugins/:key/grants", PermRead, a.listGrants)
	r.Perm(http.MethodDelete, "/plugins/:key/grants/:permission", PermManage, a.revokeGrant)
	// Narrowing takes plugin:manage only, granting again what a consent
	// takes: plugin:install and the grant rights (install.SetGrant); one
	// route, so both spend the consent's step-up.
	grant.Perm(http.MethodPut, "/plugins/:key/grants/:permission", PermManage, a.setGrant).
		Uses(perm.PluginInstall, perm.PluginGrantHigh, perm.PluginGrantCritical, perm.PluginGrantDBSchemaUnconfined)
	r.Perm(http.MethodPut, "/plugins/:key/resources", PermManage, a.putResources)
	r.Perm(http.MethodPut, "/plugins/:key/egress-policy", PermManage, a.putEgressPolicy)
	r.Perm(http.MethodGet, "/plugins/:key/egress", PermEgressRead, a.egress)
	r.Perm(http.MethodGet, "/plugins/:key/jobs", PermRead, a.jobs)
	// A manual run is a task run, whoever declared the task: the same key as
	// POST /system/tasks/:name/run.
	r.Perm(http.MethodPost, "/plugins/:key/jobs/:job_id/run", task.PermRun, a.runJob)
	r.Perm(http.MethodGet, "/plugins/:key/events", PermRead, a.events)
	// Candidates are kept to the caller's own scope, and names show on read
	// only role and plugin pages too: either read key will do.
	r.PermAny(http.MethodGet, "/plugins/:key/scope-options", a.scopeOptions, perm.RoleRead, perm.PluginRead)

}

// ctx returns the request context carrying the client IP for audit logs.
func ctx(c *gin.Context) context.Context {
	return audit.WithClientIP(c.Request.Context(), c.ClientIP())
}

func actor(c *gin.Context) int64 {
	id, _ := core.UserID(c.Request.Context())
	return id
}

// audit writes an audit row outside a transaction (after controller calls).
func (a *API) audit(c *gin.Context, action, key string, detail any) {
	_ = audit.Audit(ctx(c), a.d.DB.Gorm, actor(c), action, "plugin", key, detail)
}

// pluginExists returns the plugin status or renders not_found.
func (a *API) pluginStatus(c *gin.Context, key string) (string, bool) {
	st, err := a.pluginStatusValue(c.Request.Context(), key)
	if store.IsNoRows(err) {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("plugin not found"))
		return "", false
	}
	if err != nil {
		httpapi.Fail(c, err)
		return "", false
	}
	return st, true
}

func (a *API) rollout(c *gin.Context) (core.RolloutController, bool) {
	if a.d.Rollout == nil {
		httpapi.Fail(c, core.ErrUnavailable.WithMessage("rollout controller unavailable"))
		return nil, false
	}
	return a.d.Rollout, true
}
