package core

import (
	"context"
	"encoding/json"
	"os/exec"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// ============================================================ lifecycle <-> runtime (C1 <-> C2)

// Rollout is the API view of one plugin_rollouts row plus live node states.
type Rollout struct {
	ID            int64              `json:"id"`
	PluginKey     string             `json:"plugin_key"`
	Action        string             `json:"action"` // enable | upgrade | disable
	FromVersion   string             `json:"from_version"`
	TargetVersion string             `json:"target_version"`
	Phase         string             `json:"phase"`
	Coordinator   string             `json:"coordinator"`
	Error         string             `json:"error"`
	Nodes         []RolloutNodeState `json:"nodes"`
}

type RolloutNodeState struct {
	NodeID string `json:"node_id"`
	BootID string `json:"boot_id"`
	State  string `json:"state"` // pending | ready | active | failed
	Error  string `json:"error"`
}

// PluginNodeState is the JSON document each node reports per plugin through
// NodeRegistry.ReportPlugin (NodeStatus.Plugins), written by the rollout
// reconciler and read by the rollout coordinator and the plugin console.
type PluginNodeState struct {
	// State summarizes this node for the console (CONTRACTS §5.7): the
	// rollout state while a rollout is open, else the serving instance as
	// active | pending | failed, or "stopped" when nothing is served.
	State     string `json:"state"`
	Serving   string `json:"serving,omitempty"`
	Standby   string `json:"standby,omitempty"`
	RolloutID int64  `json:"rollout_id,omitempty"`
	Rollout   string `json:"rollout,omitempty"` // pending | ready | active | failed
	// Fallback is the version this node still serves because the one it
	// should serve is not ready here yet (starting, or failed and retried).
	Fallback  string                `json:"fallback,omitempty"`
	Error     string                `json:"error,omitempty"`
	Instances []PluginInstanceState `json:"instances,omitempty"`
}

type PluginInstanceState struct {
	Version string `json:"version"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
	// Code classifies a failed load where the console acts on it:
	// check.CodeOutdatedContract when the runtime refused a package built
	// for an older plugin contract. Error is the text to show.
	Code     string `json:"code,omitempty"`
	Restarts int    `json:"restarts"`
}

// InstanceCode is the code of the failed load of version reported among the
// instances, "" when none.
func (s PluginNodeState) InstanceCode(version string) string {
	for _, in := range s.Instances {
		if in.Version == version {
			return in.Code
		}
	}
	return ""
}

// ParsePluginNodeState decodes one NodeStatus.Plugins document; false when
// it is empty or not one.
func ParsePluginNodeState(s string) (PluginNodeState, bool) {
	var st PluginNodeState
	if s == "" || json.Unmarshal([]byte(s), &st) != nil {
		return PluginNodeState{}, false
	}
	return st, true
}

// EffectiveMemoryMB is a plugin's memory limit in MB, 0 for none: the
// operator's per-plugin override when set (> 0), else the node's cap; an
// override above a set cap is the cap.
func EffectiveMemoryMB(override, capMB int) int {
	if capMB > 0 && (override <= 0 || override > capMB) {
		return capMB
	}
	return max(override, 0)
}

// RolloutController (C2) starts and tracks two-phase rollouts.
type RolloutController interface {
	Enable(ctx context.Context, pluginKey string, actorID int64) (*Rollout, error)
	Upgrade(ctx context.Context, pluginKey, version string, actorID int64) (*Rollout, error)
	Disable(ctx context.Context, pluginKey string, actorID int64, reason string) (*Rollout, error)
	Current(ctx context.Context, pluginKey string) (*Rollout, error) // nil when none open
	Cancel(ctx context.Context, pluginKey string, rolloutID, actorID int64) error
}

// PluginSchemaManager (C2) owns plg_<key> schemas, roles and migrations.
type PluginSchemaManager interface {
	// Drop removes schema, role and migration records (uninstall with purge).
	Drop(ctx context.Context, pluginKey string) error
	// DropTx participates in the lifecycle transaction holding the plugin row.
	// A concurrent re-install cannot begin between DROP and plugin deletion.
	DropTx(ctx context.Context, tx *gorm.DB, pluginKey string) error
}

// PluginDefaultsApplier (C1) writes version-scoped defaults (user
// permissions, sticky rules) for a manifest. C1 calls it on
// first install; C2 calls it when an upgrade activates.
type PluginDefaultsApplier interface {
	// ApplyDefaults grants each permission it creates what grants holds for
	// its local key, role -> scope suffixes (none when absent; activation
	// passes nil).
	ApplyDefaults(ctx context.Context, tx *gorm.DB, m *manifest.Manifest, grants map[string]map[string][]string) error
}

// ============================================================ sandbox & egress (owner: D)

// LaunchSpec describes how to start one plugin process.
type LaunchSpec struct {
	PluginKey  string
	Version    string
	BinaryPath string
	// DataDir is the plugin's data directory, set only when it is granted
	// "fs.data": the core creates it, passes it as SUB2API_PLUGIN_DATA_DIR
	// and Landlock grants it (CONTRACTS §43.8). "" = no file access.
	DataDir string
	// Net is set when the plugin is granted "net": Landlock lets it read the
	// CA certificates for TLS through the egress tunnel.
	Net           bool
	Env           []string // extra env, e.g. SUB2API_PLUGIN_KEY
	MemoryMB      int      // 0 = no limit
	CPU           float64  // cores, informational without cgroup
	MaxOpenFiles  int
	MaxThreads    int
	StrictNetwork bool // seccomp denies AF_INET/AF_INET6 (Linux only)
	Seccomp       bool
	// RunDir is the private 0700 directory of this process: go-plugin
	// sockets and the plugin's TMPDIR live there. Landlock grants only the
	// socket directory inside it, never the shared temporary directory.
	RunDir string
}

// PluginSandboxStatus is what the plugin sandbox of a node enforces
// (CONTRACTS §43.8), probed once at startup and shown on the nodes page.
type PluginSandboxStatus struct {
	Wrapped  bool     `json:"wrapped"`      // plugins run under plugin-exec (Linux, not development mode)
	Landlock bool     `json:"landlock"`     // SUB2API_PLUGIN_LANDLOCK
	ABI      int      `json:"landlock_abi"` // the kernel's Landlock ABI, 0 = none
	Active   []string `json:"active"`       // Landlock layers in force
	Missing  []string `json:"missing"`      // layers this kernel or configuration lacks
}

// ResourceEvent is reported by the watchdog.
type ResourceEvent struct {
	PluginKey string
	PID       int
	Kind      string // memory_exceeded | threads_exceeded | sample
	RSSBytes  int64
	Threads   int
	CPUPct    float64
	Message   string
}

// PluginLauncher builds the sandboxed command for a plugin and watches the
// running process. On non-Linux (dev mode) Command returns a plain command.
type PluginLauncher interface {
	Command(ctx context.Context, spec LaunchSpec) (*exec.Cmd, error)
	// Watch samples the process every 5 s and calls onEvent; the runtime
	// restarts the plugin on memory_exceeded. stop ends the watch.
	Watch(spec LaunchSpec, pid int, onEvent func(ResourceEvent)) (stop func())
}

// EgressPolicy is the per-plugin outbound policy.
type EgressPolicy struct {
	Mode           string   // allow_all | allowlist
	AllowedDomains []string // from the approved "net" grant scope
	// Net: the plugin holds the "net" grant. Without it nothing but the
	// database (below) is reachable, whatever Mode says.
	Net bool
	// Database: the plugin holds "db.schema" and declares a database, so the
	// PostgreSQL address handed out by HostService.GetDSN is reachable.
	Database bool
}

// EgressProvider serves EgressService for one plugin instance; the runtime
// registers the returned server on the plugin's host broker.
type EgressProvider interface {
	ServerFor(pluginKey string, policy func() EgressPolicy) pluginv1.EgressServiceServer
}

// PluginAccountPurger (owner: account) deletes the accounts of a plugin's
// account types; called by uninstall when the operator asks to remove them.
// Returns the number of accounts deleted.
type PluginAccountPurger interface {
	PurgePluginAccounts(ctx context.Context, pluginKey string) (int, error)
}

// ---------------------------------------------------------------- plugin account access

// PluginAccountQuery selects a page of PluginAccountReader.ListPluginAccounts.
type PluginAccountQuery struct {
	// Type restricts the page to one account type id ("" = every type of the
	// plugin). An unknown id matches nothing.
	Type string
	// AfterID is the keyset cursor: only accounts with a greater id are
	// returned, in ascending id order. 0 starts at the beginning.
	AfterID int64
	// Limit is the maximum number of rows; the implementation caps it.
	Limit int
	// IncludeInactive also returns accounts that are disabled, in error or
	// taken out of scheduling.
	IncludeInactive bool
}

// PluginAccountSummary is account metadata without credentials.
type PluginAccountSummary struct {
	ID          int64
	Name        string
	Type        string
	Status      string
	Schedulable bool
	// Settings is the plain (non-encrypted) settings object of the account:
	// the account type's settingsFields, e.g. base_url.
	Settings json.RawMessage
}

type AccountAutomaticStateController interface {
	ApplyAccountAutomaticState(ctx context.Context, id int64, state *pluginv1.AccountAutomaticState) (*pluginv1.SetAccountAutomaticStateResponse, error)
	ApplyPluginAccountAutomaticState(ctx context.Context, pluginKey string, id int64, state *pluginv1.AccountAutomaticState) (*pluginv1.SetAccountAutomaticStateResponse, error)
}

// PluginAccountReader (owner: account) serves HostService.ListAccounts and
// HostService.GetAccount: it lets a plugin read the accounts of its OWN
// account types outside a gateway request (CONTRACTS §124), with no grant.
//
// Both methods take the calling plugin's key and filter on accounts.plugin_key
// in SQL, which is by construction the plugin that declared the account type.
// That filter is the whole security boundary, so it lives here, in the module
// that owns accounts, and not in the caller: a caller cannot ask for another
// plugin's accounts even by mistake, and there is no "all accounts" variant to
// reach for.
type PluginAccountReader interface {
	// ListPluginAccounts returns one page of the plugin's own accounts,
	// ordered by id. It never returns credentials.
	ListPluginAccounts(ctx context.Context, pluginKey string, q PluginAccountQuery) ([]PluginAccountSummary, error)
	// ReadPluginAccount returns one account of pluginKey's own account types
	// as the plugin sees it on PlatformService calls (credentials, settings,
	// properties, the saved proxy), after writing the audit row: exactly one
	// audit_logs row per successful call. An account that exists but belongs
	// to another plugin's account type is reported as ErrNotFound, like one
	// that does not exist at all: telling the two apart would leak which
	// account ids exist. An account whose proxy cannot be had is
	// ErrUnavailable: it is never handed out without its proxy.
	//
	// The audit row is the price of the pull-style access, so it is not best
	// effort: when it cannot be written the call fails and nothing is handed
	// out.
	ReadPluginAccount(ctx context.Context, pluginKey string, id int64) (*pluginv1.Account, error)
}

// PluginAccountRequester (owner: account, optional on the PluginAccountReader)
// serves HostService.SendAccountRequest: one HTTP request a plugin describes
// for one of its OWN accounts, sent through that account's proxy the way the
// quota and refresh requests are (CONTRACTS §91). The caller has checked
// that the URL is http(s) with a host. The host must pass
// CheckPluginUpstream with the account's type, settings and credentials (the
// plugin's manifest m and the administrator's plugin network access,
// CONTRACTS §112.17, §113); another is ErrPermissionDenied and nothing is
// sent. An account of another
// plugin's account type is ErrNotFound, as in ReadPluginAccount.
// A transport failure is not an error: it is the answer's transport_error
// with status 0. Neither the body nor the answer is bounded. Every request
// sent is audited (never its headers or bodies).
type PluginAccountRequester interface {
	SendPluginAccountRequest(ctx context.Context, pluginKey string, m *manifest.Manifest, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error)
}

// ProxyTicketFetcher (owner: account, optional on the PluginAccountReader)
// serves the fetches of a plugin's console route calls (HostService.
// ExecuteHTTP with a proxy ticket, CONTRACTS §117): the authorization of an
// account not saved yet, sent through the proxy the operator chose in the
// account form. ctx is the route call's (its user, access key and request);
// the caller has checked the execution token and the URL's host. The ticket
// must be one the account module issued for pluginKey to the user of ctx,
// who must still be allowed to use its proxy; anything else is
// ErrPermissionDenied (or the error of that check). A transport failure is
// not an error: it is the answer's transport_error with status 0.
type ProxyTicketFetcher interface {
	FetchWithProxyTicket(ctx context.Context, pluginKey string, in *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error)
}

// StickyClearRequest selects the sticky session bindings HostService.
// ClearStickyBindings deletes (CONTRACTS §74.2).
type StickyClearRequest struct {
	AccountID int64
	GroupID   int64
	Model     string // client model, before account mapping
	Percent   int    // 1..100
	Salt      string
}

// StickyBindingClearer (owner: gateway) deletes a share of the sticky session
// bindings that point at an account and could serve a (group, model).
type StickyBindingClearer interface {
	ClearStickyBindings(ctx context.Context, req StickyClearRequest) (matched, cleared int64, err error)
}

// UserSummary is what HostService.GetUsers tells a plugin about a user: no
// email, no roles (CONTRACTS §75.3).
type UserSummary struct {
	ID          int64
	DisplayName string
	Status      string // active | disabled
}

// UserDirectory (owner: iam) serves HostService.GetUsers (CONTRACTS §75.3).
type UserDirectory interface {
	// LookupUsers returns the users among ids that exist and are not
	// deleted, once each, in the order of ids.
	LookupUsers(ctx context.Context, ids []int64) ([]UserSummary, error)
}

// PluginWrite is one write a plugin admin route served (CONTRACTS §75.2).
type PluginWrite struct {
	UserID    int64
	Plugin    string
	Method    string
	Path      string // the request path under /api/v1/p/<plugin>
	Route     string // the manifest route pattern it matched
	Status    int    // the status the console answered
	RequestID string
	ClientIP  string
	Body      []byte
}

// PluginWriteAuditor (owner: audit) records PluginWrite rows. Failures are
// logged, never returned: the plugin's answer has already been sent.
type PluginWriteAuditor interface {
	AuditPluginWrite(ctx context.Context, w PluginWrite)
}
