package core

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// ============================================================ accounts (owner: A)

// AccountAccess answers which accounts a console user may act on
// (CONTRACTS §21.0); account implements it. action is "read", "update" or
// "test": the user's scope of account:<action> (every account, own, groups,
// ids). ctx may carry an access key, which narrows as usual.
type AccountAccess interface {
	// AccountsInScope returns all=true for a scope of every account;
	// otherwise the ids that are live accounts in the scope (order kept,
	// deduplicated); ErrPermissionDenied without any scope.
	AccountsInScope(ctx context.Context, userID int64, action string, ids []int64) (all bool, allowed []int64, err error)
}

// AccountsDenied is the ErrPermissionDenied of an account action, naming its
// "all" key.
func AccountsDenied(action string) *Error {
	return ErrPermissionDenied.WithDetails(map[string]any{"permission": "account:" + action})
}

// AuthorizeAccounts is the strict form of AccountsInScope every caller uses:
// the "all" scope passes; the "own" scope needs at least one id (else
// AccountsDenied) and every id in scope (else ErrNotFound, as for an account
// outside the scope). A caller whose account module is not wired passes nil
// and gets ErrUnavailable.
func AuthorizeAccounts(ctx context.Context, aa AccountAccess, userID int64, action string, ids []int64) error {
	if aa == nil {
		return ErrUnavailable.WithMessage("account checks are not configured")
	}
	all, allowed, err := aa.AccountsInScope(ctx, userID, action, ids)
	if err != nil || all {
		return err
	}
	if len(ids) == 0 {
		return AccountsDenied(action)
	}
	if slices.ContainsFunc(ids, func(id int64) bool { return !slices.Contains(allowed, id) }) {
		return ErrNotFound.WithMessage(Text(ctx, "account not found", "账号不存在"))
	}
	return nil
}

// AccountRef is the scheduling view of an account (no secrets).
type AccountRef struct {
	ID             int64
	Name           string
	PluginKey      string // declaring plugin of the account type
	Type           string
	Priority       int // higher first
	Weight         int // within a priority, 1-1000
	MaxConcurrency int
	ProxyID        *int64
	// Models the account serves (complete ids); empty = all models.
	Models []string
	// ModelMapping rewrites the client model to the upstream model.
	ModelMapping map[string]string
	// Rate limits; 0 = unlimited (CONTRACTS §18). SPMLimit caps the sticky
	// sessions seen in the last SPMWindowSeconds (0 = 60; CONTRACTS §71).
	RPMLimit         int
	TPMLimit         int64
	SPMLimit         int
	SPMWindowSeconds int
	// ModelLimits cap lists of (client) models on top of the account's own
	// limits (CONTRACTS §68); a model is in at most one list.
	ModelLimits []ModelLimit
	// Environment is the account layer of the environment variables (§74.3).
	Environment EnvironmentValues
	// ConversionLossPolicy is the account's conversion loss policy (§88), ""
	// to inherit the group's.
	ConversionLossPolicy string
	// ProtocolRules is the account's protocol binding (protocols and
	// model_protocols), nil without one: ProtocolRules.Serves tells whether
	// the account serves a model on an inbound protocol.
	ProtocolRules *ProtocolRules
}

// ModelLimit caps the requests of a list of models on one account. The
// models of a list share one set of counters; 0 = unlimited. Its sessions
// are counted over SPMWindowSeconds (absent or 0 = 60; CONTRACTS §71).
type ModelLimit struct {
	Models           []string `json:"models"`
	MaxConcurrency   int      `json:"max_concurrency"`
	RPMLimit         int      `json:"rpm_limit"`
	TPMLimit         int64    `json:"tpm_limit"`
	SPMLimit         int      `json:"spm_limit"`
	SPMWindowSeconds int      `json:"spm_window_seconds"`
}

// Key identifies the counters of the list: a hash of its sorted models, so
// changing the limits keeps the counts and changing the models starts anew.
func (l *ModelLimit) Key() string {
	models := slices.Clone(l.Models)
	slices.Sort(models)
	h := fnv.New64a()
	for _, m := range models {
		h.Write([]byte(m))
		h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// ModelLimitSlots is the Slots kind of the requests in flight of one model
// list of an account (the id is the account's).
func ModelLimitSlots(key string) string { return "account-model:" + key }

// ModelSlots is the Slots kind of the requests in flight of one client model
// of an account, taken uncapped: it only counts them.
func ModelSlots(model string) string { return "account-md:" + model }

// ModelLimitFor returns the list holding the (client) model, or nil.
func (a *AccountRef) ModelLimitFor(model string) *ModelLimit {
	if i := a.ModelLimitIndex(model); i >= 0 {
		return &a.ModelLimits[i]
	}
	return nil
}

// ModelLimitIndex returns the index of the list holding the (client) model
// in ModelLimits, or -1.
func (a *AccountRef) ModelLimitIndex(model string) int {
	if model == "" {
		return -1
	}
	return slices.IndexFunc(a.ModelLimits, func(l ModelLimit) bool { return slices.Contains(l.Models, model) })
}

// ServesModel reports whether the account may serve the (client) model.
func (a *AccountRef) ServesModel(model string) bool {
	if len(a.Models) == 0 {
		return true
	}
	for _, m := range a.Models {
		if m == model {
			return true
		}
	}
	return false
}

// MapModel returns the upstream model for the client model.
func (a *AccountRef) MapModel(model string) string {
	if to, ok := a.ModelMapping[model]; ok && to != "" {
		return to
	}
	return model
}

// Limited reports whether the account has any rate limit, its own or one of
// a model list.
func (a *AccountRef) Limited() bool {
	if a.RPMLimit > 0 || a.TPMLimit > 0 || a.SPMLimit > 0 {
		return true
	}
	for _, l := range a.ModelLimits {
		if l.RPMLimit > 0 || l.TPMLimit > 0 || l.SPMLimit > 0 {
			return true
		}
	}
	return false
}

// RateUsage is the usage of a subject's rate limits: requests and tokens of
// the last 60 seconds. Sessions (accounts only) counts the sessions of the
// session window (CONTRACTS §71); Models (accounts only) holds the usage of
// each model list, in the order of the account's model_limits.
type RateUsage struct {
	RPM      int64            `json:"rpm"`
	TPM      int64            `json:"tpm"`
	Sessions int64            `json:"sessions"`
	Models   []ModelRateUsage `json:"models,omitempty"`
}

// ModelRateUsage is the usage of one model list of an account.
type ModelRateUsage struct {
	Models   []string `json:"models"`
	InUse    int      `json:"in_use"`
	RPM      int64    `json:"rpm"`
	TPM      int64    `json:"tpm"`
	Sessions int64    `json:"sessions"`
}

// Prefixes of the sessions a request or a WebSocket connection is alone
// (RequestSession, ConnectionSession).
const (
	requestSessionPrefix    = "req:"
	connectionSessionPrefix = "conn:"
)

// RequestSession is the session of a request no sticky session matched: the
// request alone is a new session (CONTRACTS §71.2), which never comes back.
func RequestSession(requestID string) string { return requestSessionPrefix + requestID }

// IsRequestSession reports whether session is a RequestSession: the limiter
// counts it once and does not remember it.
func IsRequestSession(session string) bool { return strings.HasPrefix(session, requestSessionPrefix) }

// ConnectionSession is the session of a WebSocket connection no sticky
// session matched: every later turn comes back with it, so the limiter
// remembers it like a sticky session and the connection counts once
// (CONTRACTS §71.3).
func ConnectionSession(requestID string) string { return connectionSessionPrefix + requestID }

// AccountLimiter enforces per-account RPM, TPM and SPM, and those of the
// model list holding the request's (client) model, shared by every node:
// requests and tokens over the last 60 seconds, sessions over each level's
// session window. model "" checks the account's own limits only; any
// other model is also counted apart, uncapped, for GET /accounts/:id/rate-models.
// session is the request's session (CONTRACTS §71: its sticky session key,
// else RequestSession, or ConnectionSession on a WebSocket connection); ""
// (tasks, downloads) is neither checked against SPM nor counted.
type AccountLimiter interface {
	// Exhausted returns the ids among refs that reached a limit for model
	// and session; an account already holding the session passes its SPM.
	// Accounts without limits are never listed.
	Exhausted(ctx context.Context, refs []AccountRef, model, session string) (map[int64]bool, error)
	// TryHit atomically checks the limits for model and session and counts an
	// admitted request (and the session), without reserving tokens.
	TryHit(ctx context.Context, ref AccountRef, model, session string) (bool, error)
	// Reserve is TryHit that also reserves tokens against the TPM caps until
	// Settle (CONTRACTS §78).
	Reserve(ctx context.Context, ref AccountRef, model, session string, tokens int64) (RateHold, bool, error)
	// Settle replaces what hold reserved by the tokens really used; 0 returns
	// the reservation and the request stays counted.
	Settle(ctx context.Context, hold RateHold, actual int64)
	// ReleaseSession takes session off the account's (and its model list's)
	// active sessions: a retry moved the session to another account, which
	// now holds it alone, or brings the request back to this one. at is the
	// time the request was admitted on ref (RateHold.At): a RequestSession,
	// counted once and not remembered, leaves the counts of that time, and
	// stays counted when at is zero. Once per admission; best effort.
	ReleaseSession(ctx context.Context, ref AccountRef, model, session string, at time.Time)
	// AddTokens adds the tokens of a finished response to the account and,
	// when list is not "", to the model list of that key (ModelLimit.Key).
	AddTokens(ctx context.Context, id int64, list string, n int64)
	// Usage returns the counters of the accounts and of their model lists:
	// RateUsage.Models[i] is refs[k].ModelLimits[i], so ModelLimitIndex finds
	// a model's list (CONTRACTS §89 limit load).
	Usage(ctx context.Context, refs []AccountRef) (map[int64]RateUsage, error)
	// ModelUsage returns the uncapped counters of the (client) model on each
	// account (Models left empty), what the traffic balance by model compares
	// (CONTRACTS §89); model "" reads nothing and returns an empty map.
	ModelUsage(ctx context.Context, refs []AccountRef, model string) (map[int64]RateUsage, error)
	// CountModelTokens adds the tokens of a finished response to today's
	// tokens of the client model on account id (CONTRACTS §68.7).
	CountModelTokens(ctx context.Context, id int64, model string, n int64)
	// ModelRates returns the figures of the account, then those of each
	// client model it served since the earlier of midnight and an hour ago,
	// the most tokens today first; InUse is left to the caller. Sessions are
	// counted over the account's session window.
	ModelRates(ctx context.Context, ref AccountRef, midnight time.Time) ([]ModelRates, error)
}

// ModelRates are the figures of one client model of an account ("" = the
// account itself): requests in flight, the last minute's requests, tokens
// and sessions, and the tokens since midnight (TPD).
type ModelRates struct {
	Model    string `json:"model,omitempty"`
	InUse    int    `json:"in_use"`
	RPM      int64  `json:"rpm"`
	TPM      int64  `json:"tpm"`
	TPD      int64  `json:"tpd"`
	Sessions int64  `json:"sessions"`
}

// Account adds decrypted credentials and settings.
type Account struct {
	AccountRef
	Status      string
	Schedulable bool
	AutoDisable bool
	Credentials json.RawMessage
	Settings    json.RawMessage
	// HeaderPassthrough lists the client headers copied onto the upstream
	// request (CONTRACTS §81); empty = none beyond the platform's own.
	HeaderPassthrough []string
	// ParamOverride rewrites the upstream body and headers (CONTRACTS §84);
	// nil = none.
	ParamOverride *ParamOverride
	// HeaderOverride sets, removes and passes through upstream headers
	// (CONTRACTS §108), before ParamOverride; nil = none.
	HeaderOverride *HeaderOverrideRules
}

// PluginAccount is the account as the plugin of its account type receives
// it (CONTRACTS §124): proxy is the proxy this call's requests go out
// through (Egress.Proxy, nil = direct), so what the plugin sees is what the
// host uses.
func (a *Account) PluginAccount(platform string, proxy *ProxyRef) *pluginv1.Account {
	return &pluginv1.Account{Id: a.ID, Name: a.Name, Platform: platform, Type: a.Type, CredentialsJson: string(a.Credentials), SettingsJson: string(a.Settings), Properties: &pluginv1.AccountProperties{
		Status: a.Status, Schedulable: a.Schedulable, AutoDisable: a.AutoDisable,
		Priority: int32(a.Priority), Weight: int32(a.Weight), MaxConcurrency: int32(a.MaxConcurrency),
		RpmLimit: int32(a.RPMLimit), TpmLimit: a.TPMLimit, SpmLimit: int32(a.SPMLimit), Models: a.Models, ModelMapping: a.ModelMapping,
		ModelLimits: PluginModelLimits(a.ModelLimits),
	}, Proxy: PluginProxy(proxy)}
}

// PluginProxy converts a proxy for the plugin protocol; nil stays nil.
func PluginProxy(p *ProxyRef) *pluginv1.AccountProxy {
	if p == nil {
		return nil
	}
	return &pluginv1.AccountProxy{Id: p.ID, Protocol: p.Protocol, Host: p.Host, Port: int32(p.Port), Username: p.Username, Password: p.Password}
}

// PluginModelLimits converts model limits for the plugin protocol.
func PluginModelLimits(in []ModelLimit) []*pluginv1.ModelLimit {
	if len(in) == 0 {
		return nil
	}
	out := make([]*pluginv1.ModelLimit, len(in))
	for i, l := range in {
		out[i] = &pluginv1.ModelLimit{Models: l.Models, MaxConcurrency: int32(l.MaxConcurrency), RpmLimit: int32(l.RPMLimit), TpmLimit: l.TPMLimit, SpmLimit: int32(l.SPMLimit)}
	}
	return out
}

// AccountDirectory is the gateway's view of accounts. Candidates are served
// from a per-node snapshot invalidated by account:changed.
type AccountDirectory interface {
	// Candidates returns active, schedulable accounts of the group whose
	// account type is in types, excluding cooling-down ones.
	Candidates(ctx context.Context, groupID int64, types []AccountTypeKey) ([]AccountRef, error)
	// GroupHasType reports whether the group holds any account (not deleted,
	// whatever its status) of one of types: without one the group cannot serve
	// the endpoint at all, which is not a passing shortage.
	GroupHasType(ctx context.Context, groupID int64, types []AccountTypeKey) (bool, error)
	// Resting returns the accounts of the group, of types, that Candidates
	// leaves out for a while only: cooling down, or automatically disabled
	// until a time that has not come. They are read from the snapshot
	// Candidates is served from, not the database. Only a dispatch whose
	// every candidate is closed by its protocol binding asks, to tell whether
	// an account that serves the request will be back.
	Resting(ctx context.Context, groupID int64, types []AccountTypeKey) ([]AccountRef, error)
	// Unschedulable lists the accounts of the group (not deleted) that
	// Candidates does not offer now, with why: disabled, unschedulable,
	// automatically disabled or cooling down. Only failed dispatches ask.
	//
	// Resting and Unschedulable overlap (both name accounts Candidates
	// leaves out) but answer different questions from different sources.
	// Resting decides a dispatch: it must agree with the snapshot Candidates
	// was served from, so it reads that snapshot, keeps the full AccountRef
	// the gateway judges against the request, and only the accounts that
	// come back on their own. Unschedulable explains a failed request in its
	// record: it reads the database, every account type and every reason,
	// and returns ids with reasons, not refs.
	Unschedulable(ctx context.Context, groupID int64) ([]UnschedulableAccount, error)
	// Load returns one account with decrypted credentials.
	Load(ctx context.Context, id int64) (*Account, error)
	// Egress is how a request on behalf of account id leaves the core: its
	// proxy as the account stands when asked (read from the snapshot Load
	// serves, which account:changed and a reconnected bus invalidate), never
	// one a caller kept. Every outbound request of an existing account made
	// outside the account service takes its client here; the service itself
	// goes by the account row it has just read.
	Egress(ctx context.Context, id int64) (Egress, error)
	IsCoolingDown(ctx context.Context, id int64) (bool, error)
	SetCooldown(ctx context.Context, id int64, until time.Time, reason string) error
	// AutoDisable sets status=auto_disabled with a reason and emits
	// account.status_changed, unless the account opted out of automatic
	// disabling (CONTRACTS §42.3). It reports whether the account is disabled.
	AutoDisable(ctx context.Context, id int64, reason string) (bool, error)
	// AutoDisableByPolicy executes a decision already authorized by the core's
	// request policy snapshot. It still checks the account's own opt-out, but
	// does not reread the global switch from a newer policy revision.
	AutoDisableByPolicy(ctx context.Context, id int64, reason string) (bool, error)
	// AutoDisableUntil sets status=auto_disabled until release, when the
	// core's sweep makes the account active again: a temporary disable a
	// plugin's ClassifyError asked for (automatic_state with a release time,
	// CONTRACTS §69). The account's opt-out and the global switch still
	// apply. It reports whether the account is disabled.
	AutoDisableUntil(ctx context.Context, id int64, reason string, release time.Time) (bool, error)
	TouchLastUsed(ctx context.Context, id int64)
}

// QuotaObserver receives the subscription quota headers of the gateway's
// upstream responses (CONTRACTS §44). The gateway calls it for every
// response (2xx and 429 matter, the observer ignores the rest) of an account
// type declaring manifest quota.headers, with that declaration. It must not
// block: implementations parse the few declared headers and hand the result
// to an asynchronous, throttled writer.
type QuotaObserver interface {
	ObserveQuotaHeaders(accountID int64, headers []manifest.QuotaHeader, status int, h http.Header)
}
