package api

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/job"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/install"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/rows"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/task"
)

// ---------------------------------------------------------------- nodes

func (a *API) liveNodes(ctx context.Context) []core.NodeStatus {
	if a.d.Nodes == nil {
		return nil
	}
	nodes, err := a.d.Nodes.LiveNodes(ctx)
	if err != nil {
		slog.WarnContext(ctx, "list live nodes", "err", err)
		return nil
	}
	return nodes
}

func stateJSON(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

func summarizeNodes(nodes []core.NodeStatus, key string) NodeSummary {
	out := NodeSummary{Total: len(nodes), States: map[string]int{}}
	for _, n := range nodes {
		raw, ok := n.Plugins[key]
		if !ok {
			out.States["absent"]++
			continue
		}
		st, _ := core.ParsePluginNodeState(raw)
		out.States[cmp.Or(st.State, "unknown")]++
	}
	return out
}

// Refusal is why the nodes refuse to load a plugin version: it was built
// for an older plugin contract (Code check.CodeOutdatedContract). Message
// is the runtime's refusal, protocol.OutdatedContractMessage;
// FirstCoreVersion is the oldest core a replacement may be built for.
type Refusal struct {
	Code             string `json:"code"`
	Version          string `json:"version"`
	HostVersion      string `json:"host_version"`
	ProtocolVersion  int    `json:"protocol_version"`
	FirstCoreVersion string `json:"first_core_version"`
	Message          string `json:"message"`
}

// outdatedContract is the refusal of version on a core at hostVersion, with
// the runtime's message.
func outdatedContract(version, hostVersion, message string) *Refusal {
	return &Refusal{Code: check.CodeOutdatedContract, Version: version, HostVersion: hostVersion,
		ProtocolVersion: protocol.ProtocolVersion, FirstCoreVersion: protocol.FirstCoreVersion, Message: message}
}

// refusal finds a node whose instance of the plugin failed to load with the
// runtime's refusal of an older plugin contract, which the node records as
// the instance's code; nil when none did.
func refusal(nodes []core.NodeStatus, key string) *Refusal {
	for _, n := range nodes {
		st, _ := core.ParsePluginNodeState(n.Plugins[key])
		for _, in := range st.Instances {
			if in.Code == check.CodeOutdatedContract {
				return outdatedContract(in.Version, n.HostVersion, in.Error)
			}
		}
	}
	return nil
}

// recordedRefusals are the refusals kept with the outcome of each plugin's
// latest rollout, by plugin key: a node whose load of the target version was
// refused records the code there (plugin_rollout_nodes.code), which outlives
// the instance a rollout failing in preparation removes. hostVersion is this
// core's; a plugin none of whose nodes was refused has no entry. One query
// for all the keys.
func recordedRefusals(ctx context.Context, db *gorm.DB, keys []string, hostVersion string) (map[string]*Refusal, error) {
	out := map[string]*Refusal{}
	if len(keys) == 0 {
		return out, nil
	}
	err := store.EachRow(recordedRefusalRows(store.G(ctx, db), keys), func(rows *sql.Rows) error {
		var key, version, msg string
		if err := rows.Scan(&key, &version, &msg); err != nil {
			return err
		}
		out[key] = outdatedContract(version, hostVersion, msg)
		return nil
	})
	return out, err
}

// recordedRefusalRows is the first refused node (by node and boot id) of
// the latest rollout of each of keys: key, target version, error.
func recordedRefusalRows(g *gorm.DB, keys []string) *gorm.DB {
	latest := g.Model(&model.PluginRollout{}).Select("max(id)").Where("plugin_key = ANY(?)", store.Strings(keys)).Group("plugin_key")
	return g.Table("plugin_rollout_nodes AS n").
		Select("DISTINCT ON (r.plugin_key) r.plugin_key, COALESCE(r.target_version, ''), n.error").
		Joins("JOIN plugin_rollouts AS r ON r.id = n.rollout_id").
		Where("n.rollout_id IN (?) AND n.code = ?", latest, check.CodeOutdatedContract).
		Order("r.plugin_key, n.node_id, n.boot_id")
}

func nodeStates(nodes []core.NodeStatus, key string) []NodeState {
	out := []NodeState{}
	for _, n := range nodes {
		raw, ok := n.Plugins[key]
		if !ok {
			continue
		}
		out = append(out, NodeState{NodeID: n.NodeID, BootID: n.BootID, Addr: n.Addr, LastHeartbeat: n.LastHeartbeat, State: stateJSON(raw)})
	}
	return out
}

// NodeView is one row of GET /nodes (plugin states decoded as JSON).
type NodeView struct {
	NodeID        string                     `json:"node_id"`
	BootID        string                     `json:"boot_id"`
	Addr          string                     `json:"addr"`
	HostVersion   string                     `json:"host_version"`
	StartedAt     time.Time                  `json:"started_at"`
	LastHeartbeat time.Time                  `json:"last_heartbeat"`
	Plugins       map[string]json.RawMessage `json:"plugins"`
	// PluginSandbox is what the node's plugin sandbox enforces (CONTRACTS §43.8).
	PluginSandbox *core.PluginSandboxStatus `json:"plugin_sandbox,omitempty"`
}

func (a *API) nodes(c *gin.Context) {
	if a.d.Nodes == nil {
		httpapi.OK(c, []NodeView{})
		return
	}
	nodes, err := a.d.Nodes.LiveNodes(c.Request.Context())
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]NodeView, 0, len(nodes))
	for _, n := range nodes {
		v := NodeView{NodeID: n.NodeID, BootID: n.BootID, Addr: n.Addr, HostVersion: n.HostVersion, StartedAt: n.StartedAt,
			LastHeartbeat: n.LastHeartbeat, Plugins: map[string]json.RawMessage{}, PluginSandbox: n.PluginSandbox}
		for k, s := range n.Plugins {
			v.Plugins[k] = stateJSON(s)
		}
		out = append(out, v)
	}
	httpapi.OK(c, out)
}

// ---------------------------------------------------------------- jobs

// JobInfo is a manifest job with its latest run.
type JobInfo struct {
	ID         string                 `json:"id"`
	Title      manifest.LocalizedText `json:"title,omitempty"`
	Scope      string                 `json:"scope"` // cluster | node
	Task       string                 `json:"task"`  // its name on the scheduled task page
	Schedule   string                 `json:"schedule"`
	TimeoutSec int                    `json:"timeout_sec"`
	// The schedule and timeout the core runs it with: the manifest's, with
	// the default timeout and the limits applied (CONTRACTS §77.8). Adjusted
	// lists what was cut to the limits.
	EffectiveSchedule   string     `json:"effective_schedule"`
	EffectiveTimeoutSec int        `json:"effective_timeout_sec"`
	Adjusted            []string   `json:"adjusted,omitempty"`
	NextRunAt           *time.Time `json:"next_run_at"`
	LastRun             *JobRun    `json:"last_run"`
}

// JobRun is a plugin_job_runs row.
type JobRun struct {
	ID          int64      `json:"id"`
	JobID       string     `json:"job_id"`
	Scope       string     `json:"scope"`
	NodeID      string     `json:"node_id"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	Status      string     `json:"status"`
	Manual      bool       `json:"manual"`
	Message     string     `json:"message"`
}

const jobRunCols = `id, job_id, scope, node_id, scheduled_at, started_at, finished_at, status, manual, message`

// lastRuns selects the latest run of every job of key, as JobRun, whose
// fields are the jobRunCols.
func lastRuns(db *gorm.DB, key string) *gorm.DB {
	return db.Model(&model.PluginJobRun{}).Select("DISTINCT ON (job_id) "+jobRunCols).Where("plugin_key = ?", key).Order("job_id, id DESC")
}

func (a *API) jobInfos(ctx context.Context, key string, m *manifest.Manifest) ([]JobInfo, error) {
	var runs []JobRun
	if err := lastRuns(store.G(ctx, a.d.DB.Gorm), key).Scan(&runs).Error; err != nil {
		return nil, err
	}
	last := map[string]JobRun{}
	for _, r := range runs {
		last[r.JobID] = r
	}
	out := []JobInfo{}
	now := time.Now().UTC()
	for _, j := range m.Jobs {
		ji := JobInfo{ID: j.ID, Title: j.Title, Scope: manifest.JobScopeCluster, Task: "plugin." + key + "." + j.ID,
			Schedule: j.Schedule, TimeoutSec: j.TimeoutSec}
		if j.NodeScope() {
			ji.Scope = manifest.JobScopeNode
		}
		if eff, err := job.EffectiveJob(j, 0); err == nil {
			ji.EffectiveSchedule, ji.EffectiveTimeoutSec, ji.Adjusted = eff.Schedule, int(eff.Timeout/time.Second), eff.Adjusted
			if sch, err := task.ParseSchedule(eff.Schedule); err == nil {
				next := sch.Next(now)
				ji.NextRunAt = &next
			}
		}
		if r, ok := last[j.ID]; ok {
			r := r
			ji.LastRun = &r
		}
		out = append(out, ji)
	}
	return out, nil
}

func (a *API) currentManifest(c *gin.Context, key string) (*manifest.Manifest, string, bool) {
	m, v, err := a.loadCurrentManifest(c.Request.Context(), key)
	if err != nil {
		httpapi.Fail(c, err)
		return nil, "", false
	}
	return m, v, true
}

// loadCurrentManifest returns the manifest of the plugin's current version
// and that version; not_found when the plugin or its version is missing.
func (a *API) loadCurrentManifest(ctx context.Context, key string) (*manifest.Manifest, string, error) {
	v, err := install.CurrentVersion(ctx, a.d.DB.Gorm, key)
	if err != nil {
		return nil, "", err
	}
	if v == "" {
		return nil, "", core.ErrNotFound.WithMessage("plugin has no version")
	}
	m, err := install.LoadManifest(ctx, a.d.DB.Gorm, key, v)
	if err != nil {
		return nil, "", err
	}
	return m, v, nil
}

func (a *API) jobs(c *gin.Context) {
	key := c.Param("key")
	m, _, ok := a.currentManifest(c, key)
	if !ok {
		return
	}
	rc := c.Request.Context()
	jobs, err := a.jobInfos(rc, key, m)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	runs := []JobRun{}
	if err := store.G(rc, a.d.DB.Gorm).Model(&model.PluginJobRun{}).Select(jobRunCols).Where("plugin_key = ?", key).
		Order("id DESC").Limit(50).Scan(&runs).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"jobs": jobs, "recent_runs": runs})
}

func (a *API) runJob(c *gin.Context) {
	key, jobID := c.Param("key"), c.Param("job_id")
	m, _, ok := a.currentManifest(c, key)
	if !ok {
		return
	}
	found := false
	for _, j := range m.Jobs {
		found = found || j.ID == jobID
	}
	if !found {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("job not found"))
		return
	}
	if a.d.Jobs == nil {
		httpapi.Fail(c, core.ErrUnavailable.WithMessage("job runner unavailable"))
		return
	}
	if err := a.d.Jobs.RunNow(ctx(c), key, jobID, actor(c)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	a.audit(c, "plugin.job.run", key, map[string]any{"job_id": jobID})
	httpapi.NoContent(c)
}

// ---------------------------------------------------------------- events

// EventsInfo is the event subscription state of a plugin.
type EventsInfo struct {
	Subscribe       []string     `json:"subscribe"`
	BatchSize       int          `json:"batch_size"`
	Cursor          *EventCursor `json:"cursor"`
	Backlog         int64        `json:"backlog"`
	BacklogCapped   bool         `json:"backlog_capped"`
	DeadletterCount int64        `json:"deadletter_count"`
	Deadletters     []Deadletter `json:"deadletters"`
}

type EventCursor struct {
	LastEventID         int64      `json:"last_event_id"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	NextRetryAt         *time.Time `json:"next_retry_at"`
	LastError           string     `json:"last_error"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type Deadletter struct {
	ID           int64     `json:"id"`
	FirstEventID int64     `json:"first_event_id"`
	LastEventID  int64     `json:"last_event_id"`
	Error        string    `json:"error"`
	CreatedAt    time.Time `json:"created_at"`
}

const backlogCap = 100000

// likePatterns converts subscribe patterns to exact types and LIKE patterns.
func likePatterns(subs []string) (exact, like []string) {
	for _, s := range subs {
		if strings.HasSuffix(s, "*") {
			like = append(like, store.Like(strings.TrimSuffix(s, "*"))+"%")
		} else {
			exact = append(exact, s)
		}
	}
	return exact, like
}

func (a *API) eventsInfo(ctx context.Context, key string, m *manifest.Manifest) (*EventsInfo, error) {
	info := &EventsInfo{Subscribe: []string{}, Deadletters: []Deadletter{}}
	if m.Events != nil {
		info.Subscribe, info.BatchSize = m.Events.Subscribe, m.Events.BatchSize
	}
	g := store.G(ctx, a.d.DB.Gorm)
	var cur EventCursor
	err := g.Model(&model.PluginEventCursor{}).Select("last_event_id, consecutive_failures, next_retry_at, last_error, updated_at").
		Where("plugin_key = ?", key).Take(&cur).Error
	switch {
	case err == nil:
		info.Cursor = &cur
	case !isNoRows(err):
		return nil, err
	}
	if len(info.Subscribe) > 0 && info.Cursor != nil {
		exact, like := likePatterns(info.Subscribe)
		if exact == nil {
			exact = []string{}
		}
		if like == nil {
			like = []string{}
		}
		if err := backlog(g, cur.LastEventID, exact, like).Row().Scan(&info.Backlog); err != nil {
			return nil, err
		}
		if info.Backlog > backlogCap {
			info.Backlog, info.BacklogCapped = backlogCap, true
		}
	}
	if err := deadletters(g, key).Count(&info.DeadletterCount).Error; err != nil {
		return nil, err
	}
	if err := deadletters(g, key).Select("id, first_event_id, last_event_id, error, created_at").
		Order("id DESC").Limit(50).Scan(&info.Deadletters).Error; err != nil {
		return nil, err
	}
	return info, nil
}

// deadletters selects the event deadletters of key.
func deadletters(db *gorm.DB, key string) *gorm.DB {
	return db.Model(&model.PluginEventDeadletter{}).Where("plugin_key = ?", key)
}

// backlog counts the events after the cursor at id that the subscription
// (exact types, LIKE patterns) matches, up to backlogCap+1.
func backlog(db *gorm.DB, id int64, exact, like []string) *gorm.DB {
	matched := db.Model(&model.Event{}).Select("1").
		Where("id > ? AND (type = ANY(?) OR type LIKE ANY(?))", id, store.Strings(exact), store.Strings(like)).Limit(backlogCap + 1)
	return db.Table("(?) AS t", matched).Select("count(*)")
}

func (a *API) events(c *gin.Context) {
	key := c.Param("key")
	m, _, ok := a.currentManifest(c, key)
	if !ok {
		return
	}
	info, err := a.eventsInfo(c.Request.Context(), key, m)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, info)
}

// ---------------------------------------------------------------- resources & egress policy

// Resources are resource limits in API form.
type Resources struct {
	MemoryMB     int     `json:"memory_mb"`
	CPU          float64 `json:"cpu"`
	MaxThreads   int     `json:"max_threads"`
	MaxOpenFiles int     `json:"max_open_files"`
}

// ResourcesInfo shows manifest requests, admin overrides and the result.
// Requested.MemoryMB is the plugin's suggestion; Effective.MemoryMB 0 is
// unlimited.
type ResourcesInfo struct {
	Requested Resources `json:"requested"`
	Overrides Resources `json:"overrides"`
	Effective Resources `json:"effective"`
	MaxMemory int       `json:"max_memory_mb"`
}

func (a *API) resourcesInfo(ctx context.Context, key string, m *manifest.Manifest) (ResourcesInfo, error) {
	var ri ResourcesInfo
	ri.MaxMemory = a.d.Plugins.MaxMemoryMB
	if m.Resources != nil {
		ri.Requested = Resources{MemoryMB: m.Resources.MemoryMB, CPU: m.Resources.CPU, MaxThreads: m.Resources.MaxThreads, MaxOpenFiles: m.Resources.MaxOpenFiles}
	}
	var row model.Plugin
	if err := store.G(ctx, a.d.DB.Gorm).Select("resource_limits").Where("key = ?", key).Take(&row).Error; err != nil {
		return ri, err
	}
	_ = json.Unmarshal(row.ResourceLimits, &ri.Overrides)
	// As the runtime launches it: the manifest's memory figure is what the
	// plugin says it needs, not a limit.
	ri.Effective = ri.Requested
	ri.Effective.MemoryMB = core.EffectiveMemoryMB(ri.Overrides.MemoryMB, ri.MaxMemory)
	if ri.Overrides.CPU > 0 {
		ri.Effective.CPU = ri.Overrides.CPU
	}
	if ri.Overrides.MaxThreads > 0 {
		ri.Effective.MaxThreads = ri.Overrides.MaxThreads
	}
	if ri.Overrides.MaxOpenFiles > 0 {
		ri.Effective.MaxOpenFiles = ri.Overrides.MaxOpenFiles
	}
	return ri, nil
}

func (a *API) putResources(c *gin.Context) {
	key := c.Param("key")
	var in Resources
	if !httpapi.BindJSON(c, &in) {
		return
	}
	var errs []core.FieldError
	if in.MemoryMB < 0 || (a.d.Plugins.MaxMemoryMB > 0 && in.MemoryMB > a.d.Plugins.MaxMemoryMB) {
		errs = append(errs, core.FieldError{Field: "memory_mb", Code: "out_of_range", Message: "memory_mb exceeds the global limit"})
	}
	if in.CPU < 0 || in.CPU > pkg.MaxCPU {
		errs = append(errs, core.FieldError{Field: "cpu", Code: "out_of_range", Message: "cpu is out of range"})
	}
	if in.MaxThreads < 0 || in.MaxThreads > pkg.MaxThreads {
		errs = append(errs, core.FieldError{Field: "max_threads", Code: "out_of_range", Message: "max_threads is out of range"})
	}
	if in.MaxOpenFiles < 0 || in.MaxOpenFiles > pkg.MaxOpenFiles {
		errs = append(errs, core.FieldError{Field: "max_open_files", Code: "out_of_range", Message: "max_open_files is out of range"})
	}
	if len(errs) > 0 {
		httpapi.Fail(c, core.InvalidFields(errs...))
		return
	}
	m, _, ok := a.currentManifest(c, key)
	if !ok {
		return
	}
	b, _ := json.Marshal(in)
	if err := updatePlugin(store.G(c.Request.Context(), a.d.DB.Gorm), key, "resource_limits", b).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	a.audit(c, "plugin.resources.update", key, in)
	// Nodes restart their instances of the plugin one by one with the new
	// limits (new instance first, then the old one drains).
	notified := install.NotifyResources(c.Request.Context(), a.d.Bus, key)
	ri, err := a.resourcesInfo(c.Request.Context(), key, m)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	st, _ := a.pluginStatusValue(c.Request.Context(), key)
	httpapi.OK(c, ResourcesUpdate{ResourcesInfo: ri, RestartNotified: notified, Message: resourcesMessage(notified, st)})
}

// ResourcesUpdate is the PUT /plugins/:key/resources response: the
// resulting limits plus a note on how they take effect.
type ResourcesUpdate struct {
	ResourcesInfo
	// RestartNotified is true when the nodes were told to restart the
	// plugin's instances with the new limits.
	RestartNotified bool               `json:"restart_notified"`
	Message         core.LocalizedText `json:"message"`
}

func resourcesMessage(notified bool, status string) core.LocalizedText {
	switch {
	case !notified:
		return core.LocalizedText{
			"en": "Resource limits saved. They take effect the next time the plugin instances restart.",
			"zh": "资源限制已保存，将在插件实例下次重启时生效。",
		}
	case status == install.StatusEnabled || status == install.StatusEnabling || status == install.StatusUpgrading:
		return core.LocalizedText{
			"en": "Resource limits saved. Every node has been notified to restart the plugin instances with the new limits.",
			"zh": "资源限制已保存，已通知各节点按新限制重启插件实例。",
		}
	default:
		return core.LocalizedText{
			"en": "Resource limits saved. They apply when the plugin is enabled.",
			"zh": "资源限制已保存，将在启用插件时生效。",
		}
	}
}

func (a *API) pluginStatusValue(ctx context.Context, key string) (string, error) {
	var row model.Plugin
	err := store.G(ctx, a.d.DB.Gorm).Select("status").Where("key = ?", key).Take(&row).Error
	return row.Status, err
}

// updatePlugin sets column of plugin key to value, as a settings change:
// updated_at and row_version move with it.
func updatePlugin(db *gorm.DB, key, column string, value any) *gorm.DB {
	return db.Model(&model.Plugin{}).Where("key = ?", key).Updates(rows.Change(map[string]any{column: value}))
}

func (a *API) putEgressPolicy(c *gin.Context) {
	key := c.Param("key")
	var in struct {
		Policy string `json:"policy"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if in.Policy != "allow_all" && in.Policy != "allowlist" {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "policy", Code: "invalid", Message: "policy must be allow_all or allowlist"}))
		return
	}
	res := updatePlugin(store.G(c.Request.Context(), a.d.DB.Gorm), key, "egress_policy", in.Policy)
	if res.Error != nil {
		httpapi.Fail(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("plugin not found"))
		return
	}
	a.audit(c, "plugin.egress_policy.update", key, in)
	install.Notify(c.Request.Context(), a.d.Bus, key)
	httpapi.OK(c, gin.H{"policy": in.Policy})
}

// ---------------------------------------------------------------- egress logs

// EgressSummary aggregates egress logs per destination.
type EgressSummary struct {
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	Count    int64     `json:"count"`
	OK       int64     `json:"ok"`
	Open     int64     `json:"open"` // connections still open
	Denied   int64     `json:"denied"`
	Errors   int64     `json:"errors"`
	BytesIn  int64     `json:"bytes_in"`
	BytesOut int64     `json:"bytes_out"`
	LastAt   time.Time `json:"last_at"`
}

// EgressDomain is one plugin_egress_domains row: every host the plugin has
// connected to through the egress tunnel.
type EgressDomain struct {
	Host        string    `json:"host"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Connections int64     `json:"connections"`
	// New is true when the host was first seen within the last 24 hours.
	New bool `json:"new"`
}

// maxEgressDomains bounds the domains list of GET /plugins/:key/egress.
const maxEgressDomains = 500

// EgressLog is one plugin_egress_logs row. Rows are written when the
// connection opens (result "open", closed_at null) and completed when it
// closes.
type EgressLog struct {
	ID         int64      `json:"id"`
	NodeID     string     `json:"node_id"`
	Network    string     `json:"network"`
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	StartedAt  time.Time  `json:"started_at"`
	ClosedAt   *time.Time `json:"closed_at"`
	DurationMs int        `json:"duration_ms"`
	BytesIn    int64      `json:"bytes_in"`
	BytesOut   int64      `json:"bytes_out"`
	Result     string     `json:"result"`
	Error      string     `json:"error"`
}

func parseTime(c *gin.Context, name string, def time.Time) (time.Time, bool) {
	s := c.Query(name)
	if s == "" {
		return def, true
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: name, Code: "invalid", Message: "expected an RFC 3339 time"}))
		return t, false
	}
	return t, true
}

func (a *API) egress(c *gin.Context) {
	key := c.Param("key")
	if _, ok := a.pluginStatus(c, key); !ok {
		return
	}
	now := time.Now().UTC()
	from, ok := parseTime(c, "from", now.Add(-24*time.Hour))
	if !ok {
		return
	}
	to, ok := parseTime(c, "to", now)
	if !ok {
		return
	}
	rc := c.Request.Context()
	g := store.G(rc, a.d.DB.Gorm)
	byHost := egressLogs(g, key, from, to).Select(`host, port, count(*),
		count(*) FILTER (WHERE result = 'ok'), count(*) FILTER (WHERE result = 'open'),
		count(*) FILTER (WHERE result = 'denied'),
		count(*) FILTER (WHERE result NOT IN ('ok', 'open', 'denied')),
		COALESCE(sum(bytes_in), 0), COALESCE(sum(bytes_out), 0), max(started_at)`).
		Group("host, port").Order("count(*) DESC").Limit(200)
	summary, err := store.CollectRows(byHost, func(rows *sql.Rows) (EgressSummary, error) {
		var s EgressSummary
		err := rows.Scan(&s.Host, &s.Port, &s.Count, &s.OK, &s.Open, &s.Denied, &s.Errors, &s.BytesIn, &s.BytesOut, &s.LastAt)
		return s, err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	domains, err := a.egressDomains(rc, key)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	page, size := httpapi.Pagination(c)
	var total int64
	if err := egressLogs(g, key, from, to).Count(&total).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items := []EgressLog{}
	if err := egressLogs(g, key, from, to).Select("id, node_id, network, host, port, started_at, closed_at, duration_ms, bytes_in, bytes_out, result, error").
		Order("started_at DESC, id DESC").Limit(size).Offset((page - 1) * size).Scan(&items).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, gin.H{"from": from, "to": to, "summary": summary, "domains": domains, "items": items,
		"page": httpapi.Page{Page: page, PageSize: size, Total: total}})
}

// egressLogs selects the egress logs of key started in [from, to).
func egressLogs(db *gorm.DB, key string, from, to time.Time) *gorm.DB {
	return db.Model(&model.PluginEgressLog{}).Where("plugin_key = ? AND started_at >= ? AND started_at < ?", key, from, to)
}

// egressDomains lists every host the plugin has reached (not limited to the
// from/to window), most recently used first.
func (a *API) egressDomains(ctx context.Context, key string) ([]EgressDomain, error) {
	out := []EgressDomain{}
	err := egressDomainRows(store.G(ctx, a.d.DB.Gorm), key).Scan(&out).Error
	return out, err
}

// egressDomainRows selects the egress domains of key as EgressDomain,
// whose fields are the columns read.
func egressDomainRows(db *gorm.DB, key string) *gorm.DB {
	return db.Model(&model.PluginEgressDomain{}).
		Select("host, first_seen_at, last_seen_at, connections, first_seen_at > now() - interval '24 hours' AS new").
		Where("plugin_key = ?", key).Order("last_seen_at DESC, host").Limit(maxEgressDomains)
}
