package core

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// ============================================================ identity & authz (owner: A core-data)

// TokenVerifier validates console session tokens (s2s_, CONTRACTS §100):
// the user and the login session of the token.
type TokenVerifier interface {
	VerifySession(ctx context.Context, token string) (userID int64, session string, err error)
}

// PermissionSet is a user's effective permissions at a given authz version.
// In the grant view (what roles carry, authz.RolesPermissions) it also holds
// the roles the holder may assign: Assignable never holds a superuser role,
// and AssignCustom ("and every custom role") is already expanded into it.
//
// Keys holds the unscoped keys, a scope of everything included (canonical,
// perm.Canonical); Scoped the narrower scopes per action (CONTRACTS §21).
// Build sets with Add or PermissionSetOf, which canonicalize.
//
// A user's set as the Authorizer resolves it (PermissionSet) is decided:
// its lists are what Casbin lists for the user, and Has is Casbin's
// Enforce (CONTRACTS §99.3). A set built from keys (a role's grants, an
// access key's list) compares keys.
type PermissionSet struct {
	Superuser    bool
	Keys         map[string]struct{}
	Scoped       map[perm.Key][]perm.Scope
	Assignable   map[string]struct{}
	AssignCustom bool
	Version      int64
	// decide answers Has for a decided set (Decided), nil for one built
	// from keys.
	decide func(string) bool
}

// Decided returns s answering Has with decide: the authorizer's decision
// for the holder, whose grants s lists.
func (s PermissionSet) Decided(decide func(string) bool) PermissionSet {
	s.decide = decide
	return s
}

// Add grants k (any spelling: trailing wildcards included). A key that does
// not parse grants nothing.
func (s *PermissionSet) Add(k string) {
	p, err := perm.Parse(k)
	if err != nil {
		return
	}
	s.add(p)
}

func (s *PermissionSet) add(p perm.Pattern) {
	if p.Scope.All {
		if s.Keys == nil {
			s.Keys = map[string]struct{}{}
		}
		s.Keys[string(p.Action)] = struct{}{}
		return
	}
	if s.Scoped == nil {
		s.Scoped = map[perm.Key][]perm.Scope{}
	}
	if !slices.Contains(s.Scoped[p.Action], p.Scope) {
		s.Scoped[p.Action] = append(s.Scoped[p.Action], p.Scope)
	}
}

// Has reports whether the set implies k: for a decided set, the
// authorizer's decision; else a superuser, k held unscoped, or a held scope
// implying k's. An unscoped k asks for every row, so Has("account:read") is
// false for a holder of account:read:own only.
func (s PermissionSet) Has(k string) bool {
	if s.decide != nil {
		return s.decide(k)
	}
	return s.lists(k)
}

// lists reports whether the lists of the set imply k (Has of a set built
// from keys).
func (s PermissionSet) lists(k string) bool {
	if s.Superuser {
		return true
	}
	if _, ok := s.Keys[k]; ok {
		return true
	}
	p, err := perm.Parse(k)
	if err != nil {
		return false
	}
	if _, ok := s.Keys[string(p.Action)]; ok || p.Scope.All {
		return ok
	}
	for _, sc := range s.Scoped[p.Action] {
		if perm.Implies(perm.Pattern{Action: p.Action, Scope: sc}, p) {
			return true
		}
	}
	return false
}

// HasAction reports whether the set holds action with any scope: what a
// route of rows the caller may reach some of asks (Router.PermScoped).
func (s PermissionSet) HasAction(action perm.Key) bool {
	_, ok := s.Keys[string(action)]
	return s.Superuser || ok || len(s.Scoped[action]) > 0
}

// DataScope is the rows uid, the holder, reaches with action.
func (s PermissionSet) DataScope(uid int64, action perm.Key) DataScope {
	if _, ok := s.Keys[string(action)]; ok || s.Superuser {
		return AllScope
	}
	return Compile(uid, s.Scoped[action])
}

// ScopeGroups lists the groups its scopes name (group/G, group/G/account/X)
// under any action, sorted: the groups whose names the holder's own grants
// show (lookup's entity.named).
func (s PermissionSet) ScopeGroups() []int64 {
	var out []int64
	for _, scopes := range s.Scoped {
		for _, sc := range scopes {
			if sc.Group > 0 {
				out = append(out, sc.Group)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// KeyList lists the canonical keys of the set, sorted (not the superuser
// flag), without the scopes of an action it holds for every row.
func (s PermissionSet) KeyList() []string {
	out := make([]string, 0, len(s.Keys))
	for k := range s.Keys {
		out = append(out, k)
	}
	for a, scopes := range s.Scoped {
		if _, all := s.Keys[string(a)]; all {
			continue
		}
		for _, sc := range scopes {
			out = append(out, string(perm.Pattern{Action: a, Scope: sc}.Key()))
		}
	}
	slices.Sort(out)
	return out
}

// CanAssign reports whether the holder may assign or revoke role roleKey. A
// superuser may assign any; callers exclude the superuser role themselves.
func (s PermissionSet) CanAssign(roleKey string) bool {
	if s.Superuser {
		return true
	}
	_, ok := s.Assignable[roleKey]
	return ok
}

// AccessKeyPrefix starts every access key, so a bearer token can be told from
// a console session token without a lookup.
const AccessKeyPrefix = "sak_"

// AccessKeyIdentity is who an access key acts for and what it was given.
type AccessKeyIdentity struct {
	UserID      int64
	KeyID       int64
	Permissions []string
}

// AccessKeyVerifier validates access keys (CONTRACTS §80).
type AccessKeyVerifier interface {
	VerifyAccessKey(ctx context.Context, raw, ip string) (AccessKeyIdentity, error)
}

// AuthenticateBearer resolves a console bearer token to its user: a login
// session, whose id then travels in the returned context, or an access key,
// whose limits do. The client's ip goes in the context first (ClientIP): a
// session records where it is used from.
func AuthenticateBearer(ctx context.Context, tokens TokenVerifier, token, ip string) (context.Context, int64, error) {
	ctx = WithClientIP(ctx, ip)
	if !strings.HasPrefix(token, AccessKeyPrefix) {
		uid, session, err := tokens.VerifySession(ctx, token)
		if err != nil {
			return ctx, 0, err
		}
		return WithSession(WithUserID(ctx, uid), session), uid, nil
	}
	keys, ok := tokens.(AccessKeyVerifier)
	if !ok {
		return ctx, 0, ErrUnauthenticated
	}
	id, err := keys.VerifyAccessKey(ctx, token, ip)
	if err != nil {
		return ctx, 0, err
	}
	return WithAccessKey(WithUserID(ctx, id.UserID), id), id.UserID, nil
}

// PermissionSetOf is the set holding exactly keys, not a superuser.
func PermissionSetOf(keys ...string) PermissionSet {
	set := PermissionSet{Keys: make(map[string]struct{}, len(keys))}
	for _, k := range keys {
		set.Add(k)
	}
	return set
}

// Missing is what a wanted set holds beyond the set it is compared to: the
// superuser flag, permission keys and assignable roles (both sorted), and
// the "every custom role" flag.
type Missing struct {
	Superuser    bool
	Keys         []string
	Assign       []string
	AssignCustom bool
}

// Empty reports whether nothing is missing.
func (m Missing) Empty() bool {
	return !m.Superuser && len(m.Keys) == 0 && len(m.Assign) == 0 && !m.AssignCustom
}

// Details is m as the details of a permission_denied error; roles are kept
// apart from permission keys.
func (m Missing) Details() map[string]any {
	return map[string]any{
		"missing":               append([]string{}, m.Keys...),
		"missing_assignable":    append([]string{}, m.Assign...),
		"missing_assign_custom": m.AssignCustom,
		"superuser":             m.Superuser,
	}
}

// Missing returns what want holds that s does not: each key of want that s
// does not imply. A superuser misses nothing; anyone else misses the
// superuser flag when want has it.
func (s PermissionSet) Missing(want PermissionSet) Missing {
	if s.Superuser {
		return Missing{}
	}
	keys := slices.DeleteFunc(want.KeyList(), s.Has)
	if len(keys) == 0 {
		keys = nil
	}
	return Missing{
		Superuser:    want.Superuser,
		Keys:         keys,
		Assign:       missingKeys(s.Assignable, want.Assignable),
		AssignCustom: want.AssignCustom && !s.AssignCustom,
	}
}

// missingKeys lists the keys of want not in have, sorted.
func missingKeys(have, want map[string]struct{}) []string {
	var out []string
	for k := range want {
		if _, ok := have[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Restrict returns the part of the set that keys allow, pattern by pattern:
// a key the set implies is kept as is; a key wider than the set keeps the
// set's narrower scopes of that action it implies (a key of account:read
// on a holder of account:read:own gives account:read:own); scopes neither
// implies, such as group/12 and own, are dropped rather than intersected.
// Never more than the set itself, and no longer a superuser, whose Has is
// true for anything. The loss of the flag is deliberate: an access key never
// acts as a superuser, so it cannot grant or act on what only a superuser
// may (Missing). Assignable roles are not copied, also deliberately: an
// access key never assigns roles. On a decided set, Has of the result is
// the narrowed lists' and the holder's decision both.
func (s PermissionSet) Restrict(keys []string) PermissionSet {
	out := s.restrict(keys)
	if s.decide != nil {
		lists, decide := out, s.decide
		out.decide = func(k string) bool { return lists.lists(k) && decide(k) }
	}
	return out
}

// restrict is Restrict on the lists of the set.
func (s PermissionSet) restrict(keys []string) PermissionSet {
	out := PermissionSet{Keys: make(map[string]struct{}, len(keys)), Version: s.Version}
	for _, k := range keys {
		p, err := perm.Parse(k)
		switch {
		case err != nil:
		case s.lists(k):
			out.add(p)
		default:
			for _, sc := range s.Scoped[p.Action] {
				if held := (perm.Pattern{Action: p.Action, Scope: sc}); perm.Implies(p, held) {
					out.add(held)
				}
			}
		}
	}
	return out
}

// Authorizer answers permission questions; results are cached per node and
// invalidated through the authz:changed broadcast.
type Authorizer interface {
	Can(ctx context.Context, userID int64, permission string) (bool, error)
	PermissionSet(ctx context.Context, userID int64) (PermissionSet, error)
	// CanGrant validates the actor holds all permissions being granted (CONTRACTS §4.1, SEC-H2).
	CanGrant(ctx context.Context, actorID int64, perms []string) error
}

// AuthzVersioner is optionally implemented by an Authorizer: the newest
// version of the roles and permissions it knows of, without reading the
// database. Every change of roles, grants or the catalog raises it; a session
// that ends does not. The console compares it (response header
// X-Authz-Version) with the authz_version of its GET /me to know when to
// fetch the caller's permissions again.
type AuthzVersioner interface {
	Version() int64
}

// UserAction is what an action on users changes (UserGuard).
type UserAction int

const (
	// ActOnData changes what users have on the platform: balance, user
	// group.
	ActOnData UserAction = iota
	// ActOnIdentity changes the account itself: profile, email, password,
	// second factors and passkeys, status, deletion, roles. A superuser's
	// account changes through an access key only with a step-up proof
	// (期 4 S3, replacing decision 1's session-only rule).
	ActOnIdentity
	// ActOnBalance adjusts balances: ActOnData, and a target holding a role
	// besides the default one also takes balance:adjust:any, in the actor's
	// roles and its access key (plain users only with balance:adjust alone).
	ActOnBalance
)

// UserGuard decides whom a caller may act on (authz implements it); what the
// caller may do is the route's permission, checked before (FU6).
type UserGuard interface {
	// CanActOnUsers fails unless actorID may manage every target, itself
	// included: by the actor's roles, not narrowed by its access key, every
	// role a target holds, the default user role aside, is one the actor may
	// assign, and what the targets' roles grant is within the actor's (a
	// superuser manages everyone). An actor whose roles reach users by a
	// team alone (user:read, user:create, user:update, none of them for
	// every user) manages only the users below it (CONTRACTS §103). For
	// ActOnIdentity a superuser target
	// through an access key needs a step-up proof; for ActOnBalance a target
	// holding a role besides user needs balance:adjust:any, which an access
	// key must name. Actor 0 (the system) passes.
	CanActOnUsers(ctx context.Context, actorID int64, action UserAction, targetIDs ...int64) error
}

// OwnerGuard decides and writes the owner of a user (users.owner_id and
// users.owner_path, UserTree; CONTRACTS §103.3): authz implements it, the
// one writer of both; iam calls it inside the transaction that creates or
// updates the user, on a create once the user is inserted (without an
// owner). SetOwner takes the authz lock itself, so the owners it reads
// cannot move under it.
type OwnerGuard interface {
	// SetOwner gives userID the owner actorID names and returns it (0:
	// none). owner is the requested owner_id, 0 for none. On a create
	// (create) nil is the actor when it creates users for its team only,
	// else none; on an update nil, or the current owner, is no change and
	// passes as is. Naming an owner, or none, needs user:create (create) or
	// user:update (update) for every user; a holder for its team only may
	// name only itself (403 otherwise), decided before the owner is read.
	// It refuses (400 owner_id) an owner that is userID (self) or below it
	// (cycle), would put userID, or a user below it, more than
	// MaxTeamDepth levels below the top of its chain (too_deep), is missing
	// or deleted (not_found), or may not manage userID by its roles
	// (owner_cannot_manage; on a create the roles come after, and
	// assigning them, but for the system's, checks it), and an actor that
	// may not act (ActOnIdentity), on an update, on userID as it stands
	// under the lock, before the move (after it a team holder naming itself
	// holds userID in its team), or, after the move, on every user of the
	// old and the new owner's chains (the owner and those above it, whose
	// teams the user leaves or joins); the actor itself and those above it
	// in a chain aside. A change is audited as user.owner (from, to). Actor
	// 0 (the system) needs no permission.
	SetOwner(ctx context.Context, tx *gorm.DB, actorID, userID int64, create bool, owner *int64) (int64, error)
}

// GuardOrDeny is g, or a guard refusing everything with ErrInternal when g
// is nil (not wired): failing closed instead of panicking.
func GuardOrDeny(g UserGuard) UserGuard {
	if g == nil {
		return denyAllUsers{}
	}
	return g
}

type denyAllUsers struct{}

func (denyAllUsers) CanActOnUsers(context.Context, int64, UserAction, ...int64) error {
	return ErrInternal
}

// PluginScopeTypePrefix starts the scope_type of a scoped plugin permission:
// "plugin:" + its manifest scope's ResourceName().
const PluginScopeTypePrefix = "plugin:"

// The one encoding of a plugin permission's scope in the catalog: written
// as StoredPluginScope with PluginScopeType, read back with ReadPluginScope.

// StoredPluginScope is manifest scope s as the catalog stores it and the
// console reads it (PermissionDef.Scope, GET /permissions, the review):
// Resource set to ResourceName(), the values resource for a values scope;
// nil for nil.
func StoredPluginScope(s *manifest.PermissionScope) *manifest.PermissionScope {
	if s == nil {
		return nil
	}
	out := *s
	out.Resource = s.ResourceName()
	return &out
}

// PluginScopeType is the scope_type of a plugin permission of manifest
// scope s, "" for none.
func PluginScopeType(s *manifest.PermissionScope) string {
	if s == nil {
		return ""
	}
	return PluginScopeTypePrefix + s.ResourceName()
}

// ReadPluginScope reads a stored plugin permission's scope_type and scope
// as the scope its grants may name (manifest.PermissionScope.Takes): the
// zero scope (every row only) for an unscoped one, the resource always
// that of scope_type.
func ReadPluginScope(scopeType string, s *manifest.PermissionScope) manifest.PermissionScope {
	res, ok := strings.CutPrefix(scopeType, PluginScopeTypePrefix)
	if !ok {
		return manifest.PermissionScope{}
	}
	var out manifest.PermissionScope
	if s != nil {
		out = *s
	}
	out.Resource = res
	return out
}

// PluginScopeSuffix is the canonical form of sfx, a scope suffix of the
// plugin permission key ("own", "flow/3", "value/input"); ok is false when
// key:sfx is not a scope of key. Whether the permission takes it is
// ReadPluginScope's Takes.
func PluginScopeSuffix(key, sfx string) (string, bool) {
	p, err := perm.Parse(key + ":" + sfx)
	if err != nil || string(p.Action) != key {
		return "", false
	}
	return p.Suffix(), true
}

// PermissionDef describes one permission in the catalog.
type PermissionDef struct {
	Key    string
	Module string
	// ScopeType is the resource the action's scope ranges over: core
	// "account"/"proxy" (perm.ScopeType), plugin PluginScopeTypePrefix + the
	// manifest scope's ResourceName(); "" for an action that takes none.
	ScopeType string
	// Scope is a plugin permission's manifest scope (Resource set to
	// ResourceName()); nil for core and unscoped ones.
	Scope       *manifest.PermissionScope
	Label       LocalizedText
	Description LocalizedText
	Risk        string // manifest.RiskLow (also when empty) | RiskSensitive | RiskCritical
	Sort        int
	// Group of a plugin permission (manifest permissionGroups[]).
	Group      string
	GroupLabel LocalizedText
	GroupSort  int
	// Requires are soft dependencies, qualified keys; never authorization.
	Requires []string
	// Defaults is the manifest's suggestion, role -> canonical scope
	// suffixes ("" every row): stored for the console's "restore plugin
	// suggestion", never granted by itself. Nil for a critical permission.
	Defaults map[string][]string
	// GrantRoles are what the catalog grants when it first creates the
	// permission, role -> suffixes as Defaults (super admin roles skipped):
	// the consent's decision, not the manifest's.
	GrantRoles map[string][]string
	// Staged: declared only by a version approved while another runs. The
	// catalog creates it with status staged (granting nothing, untouched by
	// SetPluginActive) and leaves it removed, scope grants unpruned, when it
	// is, so it means nothing before that version's activation syncs it
	// again.
	Staged bool
}

// PluginPermission is a plugin permission as the catalog holds it
// (PermissionCatalog.PluginPermissions).
type PluginPermission struct {
	Key    string // qualified
	Label  LocalizedText
	Status string // active | disabled | removed
	Risk   string // manifest.RiskLow | RiskSensitive | RiskCritical
	// ScopeType and Scope as stored; Scope read back with ReadPluginScope
	// (the zero scope for an unscoped key).
	ScopeType string
	Scope     manifest.PermissionScope
}

// PermissionHolder is one grant of a permission
// (PermissionCatalog.PermissionHolders): the role and the scope.
type PermissionHolder struct {
	Role  string
	Scope string // canonical suffix; "" every row
}

// PluginGrantees is which roles an actor may hand new plugin permissions
// (PermissionCatalog.PluginGrantees): Denied maps a role to why not.
type PluginGrantees struct {
	Superuser bool              `json:"superuser"` // may grant critical ones
	Denied    map[string]string `json:"denied"`    // a GrantDeny* reason
}

// Why an actor may not change a role or hand it new plugin permissions:
// PluginGrantees.Denied, the role matrix's denied, the consent review's
// reasons and a critical refusal's details.reason. The console reads them.
const (
	GrantDenySuperuserRole = "superuser_role" // a super admin role: frozen, it holds everything
	GrantDenyNotEditable   = "not_editable"   // the role holds what the actor lacks
	GrantDenyNotAssignable = "not_assignable" // not on the actor's assignable list
	GrantDenyHeld          = "held"           // the actor holds the role
	GrantDenyRoleDefine    = "role_define"    // the actor lacks role:define
	GrantDenyCritical      = "critical"       // a critical permission, the actor no superuser
)

// PermissionCatalog is used by the plugin runtime to manage plugin-owned
// permissions ("plugin.<key>:<local>"). Every mutation bumps the authz version
// and broadcasts authz:changed.
type PermissionCatalog interface {
	// SyncPlugin upserts the plugin's permissions (install/upgrade),
	// revives removed ones it declares again (grants as they were, no
	// GrantRoles), grants newly created ones their GrantRoles (scopes
	// included; the consent resolved inheritance into them), drops scope
	// grants the declared scope no longer takes, and marks keys no longer
	// declared removed (their grants stay, granting nothing).
	SyncPlugin(ctx context.Context, tx *gorm.DB, pluginKey string, defs []PermissionDef) error
	// CanGrantToRoles fails unless actorID may change every role of
	// roleKeys (super admin roles never), as handing them a plugin's new
	// permissions of risk at most risk does, and each is on its assignable
	// list and, but for a superuser, not held by actorID. A critical risk
	// also needs a superuser actor.
	CanGrantToRoles(ctx context.Context, tx *gorm.DB, actorID int64, roleKeys []string, risk string) error
	// PluginGrantees is CanGrantToRoles read-only: no version bump, no
	// locks, a refusal is a Denied entry rather than an error.
	PluginGrantees(ctx context.Context, tx *gorm.DB, actorID int64, roleKeys []string) (PluginGrantees, error)
	// GrantPluginPermission grants the existing plugin permission key to
	// roles (role -> scope suffixes as PermissionDef.GrantRoles; removed
	// keys and super admin roles skipped), each grant audited as
	// authz.sync.grant: the role grants an upgrade withheld until someone
	// confirmed them. Callers check the rights (CanGrantToRoles).
	GrantPluginPermission(ctx context.Context, tx *gorm.DB, key string, roles map[string][]string) error
	// PluginPermissions lists the plugin's permissions, removed ones
	// included, by their sort.
	PluginPermissions(ctx context.Context, q *gorm.DB, pluginKey string) ([]PluginPermission, error)
	// PermissionHolders maps each key of keys to its grants, every row and
	// scoped ones, by role then scope; super admin roles aside, removed
	// keys keeping theirs (D6).
	PermissionHolders(ctx context.Context, q *gorm.DB, keys []string) (map[string][]PermissionHolder, error)
	// SetPluginActive flips status active/disabled (enable/disable); removed
	// and staged permissions stay as they are.
	SetPluginActive(ctx context.Context, tx *gorm.DB, pluginKey string, active bool) error
	// DiscardStaged deletes the plugin's staged permissions (PermissionDef.
	// Staged) and their grants: a consent supersedes what an earlier one,
	// of a version not activated, created for it.
	DiscardStaged(ctx context.Context, tx *gorm.DB, pluginKey string) error
	// DeletePlugin removes the plugin's permissions and grants (uninstall).
	DeletePlugin(ctx context.Context, tx *gorm.DB, pluginKey string) error
}

// ============================================================ api keys & groups (owner: A)

type GroupInfo struct {
	ID              int64
	Name            string
	Status          string
	RateMultiplier  decimal.Decimal
	ModelAllowlist  []string // globs; empty = all
	ModelFilterMode string   // whitelist (default) or blacklist
	// Environment is the group layer of the environment variables (§74.3).
	Environment EnvironmentValues
	// ConversionLossPolicy is the group's conversion loss policy (§88), ""
	// to leave it to the accounts (default allow).
	ConversionLossPolicy string
}

// APIKeyPrincipal is the result of gateway authentication. Group.RateMultiplier
// is the group's base rate.
type APIKeyPrincipal struct {
	KeyID  int64
	UserID int64
	// SegmentID is the segment of the key's owner and RateOverride its own
	// rate in the key's group (nil = none).
	SegmentID    int64
	RateOverride *decimal.Decimal
	// KeyLimits and KeyQuota are the caps the key's owner set on it.
	KeyLimits KeyLimits
	KeyQuota  KeyQuota
	Group     GroupInfo
}

// Rates are the rate levels of the key's owner in the key's group, the
// model aside: Pricer.ModelRates adds those of a model.
func (p *APIKeyPrincipal) Rates() RateLevels {
	return RateLevels{Segment: p.RateOverride, Group: p.Group.RateMultiplier}
}

// KeyQuota is an API key's spend cap in USD (Limit 0 = unlimited) and what
// its requests were charged so far (CONTRACTS §61).
type KeyQuota struct {
	Limit decimal.Decimal `json:"quota_limit"`
	Used  decimal.Decimal `json:"quota_used"`
}

// Exhausted reports whether a capped key has spent its quota.
func (q KeyQuota) Exhausted() bool { return q.Limit.IsPositive() && q.Used.GreaterThanOrEqual(q.Limit) }

// KeyLimits caps one API key (0 = unlimited): requests and tokens per minute
// and requests in flight, all counted across the nodes.
type KeyLimits struct {
	RPM         int   `json:"rpm_limit"`
	TPM         int64 `json:"tpm_limit"`
	Concurrency int   `json:"max_concurrency"`
}

// KeyUsageReporter answers GET /v1/usage for an authenticated API key: what
// it can still spend, its caps and what it used (CONTRACTS §62). The answer
// is written as JSON.
type KeyUsageReporter interface {
	KeyUsage(ctx context.Context, p *APIKeyPrincipal) (any, error)
}

// KeySlots is the Slots kind of the API keys' requests in flight.
const KeySlots = "apikey"

// AccountWaitSlots is the Slots kind of the requests queueing for an account
// (docs/GATEWAY-CONCURRENCY-DESIGN.md §3).
const AccountWaitSlots = "account-wait"

// RateHold is one admitted request in the rate windows: what it reserved in
// each set of counters of its subject, until Settle replaces that by what the
// request really used. At is the Redis time it was admitted at: the
// reservations sit in the window's segment of that time (CONTRACTS §68.3). A
// hold never settled returns by itself when that segment leaves the window.
type RateHold struct {
	Subject int64
	At      time.Time
	Items   []RateHoldItem
}

// RateHoldItem is what a hold reserved in the counters Sub ("" = the
// subject's own).
type RateHoldItem struct {
	Sub    string
	Tokens int64
}

// RateWindows counts requests and tokens of one kind of subject (API keys)
// over the last 60 seconds, shared by every node. Admit counts one request
// when the subject is under rpm and tpm (0 = unlimited), else returns the time
// until it would pass; Reserve also reserves tokens until Settle; AddTokens
// counts a finished response admitted without a reservation.
type RateWindows interface {
	Admit(ctx context.Context, id int64, rpm int, tpm int64) (ok bool, retry time.Duration, err error)
	Reserve(ctx context.Context, id int64, rpm int, tpm, tokens int64) (hold RateHold, ok bool, retry time.Duration, err error)
	Settle(ctx context.Context, hold RateHold, actual int64)
	AddTokens(ctx context.Context, id, n int64)
	Usage(ctx context.Context, ids []int64) (map[int64]RateUsage, error)
}

// APIKeyAuthenticator resolves a raw client key. Errors: ErrUnauthenticated
// (unknown/disabled/expired key or disabled user), ErrPermissionDenied (user
// lacks gateway:use or the group is not available to the user).
type APIKeyAuthenticator interface {
	Authenticate(ctx context.Context, rawKey string) (*APIKeyPrincipal, error)
}

// ============================================================ accounts & proxies (owner: A)

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

// Egress is the way out of one outbound request: the HTTP client (the
// proxy's, or the direct one) and whether it goes through a proxy, which
// the SSRF guard needs (netguard.CheckURLVia). Proxied is set even when the
// proxy's client cannot be had (disabled, gone): the request then fails, it
// never goes out directly. Proxy is the proxy the client goes through,
// password included, from the same cache entry as Client: what a plugin is
// shown as Account.proxy (CONTRACTS §124). It is nil for a direct
// connection, and nil whenever the client cannot be had.
type Egress struct {
	Client  *http.Client
	Proxied bool
	Proxy   *ProxyRef
}

// ProxyRef is a proxy: its id and address, password included. ID is 0 for a
// proxy URL an account form pasted, which is not stored.
type ProxyRef struct {
	ID int64
	ProxySpec
}

// String describes the proxy without its password.
func (r ProxyRef) String() string { return fmt.Sprintf("proxy %d %s", r.ID, r.ProxySpec) }

// GoString keeps %#v from printing the password.
func (r ProxyRef) GoString() string { return r.String() }

// Format keeps every verb (%d, %x ...) from printing the password.
func (r ProxyRef) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// LogValue keeps slog from printing the password.
func (r ProxyRef) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", r.ID), slog.Any("spec", r.ProxySpec))
}

// ProxyClient is the HTTP client of a proxy and the proxy it goes through;
// Proxy is nil for the direct client.
type ProxyClient struct {
	Client *http.Client
	Proxy  *ProxyRef
}

// ProxyDirectory hands out HTTP clients per proxy (nil = direct), cached and
// rebuilt when a proxy changes.
type ProxyDirectory interface {
	HTTPClient(ctx context.Context, proxyID *int64) (ProxyClient, error)
	// HTTPClientFor builds a transient client for a proxy that is not (or
	// not yet) stored, e.g. a proxy_url given to a models/fetch request
	// (CONTRACTS §21.4). Nothing is cached or written; the caller should
	// CloseIdleConnections when done. spec must already be validated
	// (proxy.ParseURL).
	HTTPClientFor(ctx context.Context, spec ProxySpec) (*http.Client, error)
}

// ProxySpec is a parsed proxy address (proxy.ParseURL, CONTRACTS §21.4).
// Protocol is http, https or socks5 (socks5h is normalised to socks5);
// Host is lower-case without IPv6 brackets. String, GoString, Format and
// LogValue leave the password out, so a logged spec never shows it.
type ProxySpec struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
}

// String describes the proxy without its password.
func (s ProxySpec) String() string {
	return fmt.Sprintf("%s://%s username=%q password=%t", s.Protocol, net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), s.Username, s.Password != "")
}

// GoString keeps %#v from printing the password.
func (s ProxySpec) GoString() string { return s.String() }

// Format keeps every verb (%d, %x ...) from printing the password.
func (s ProxySpec) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, s.String()) }

// LogValue keeps slog from printing the password.
func (s ProxySpec) LogValue() slog.Value {
	return slog.GroupValue(slog.String("protocol", s.Protocol), slog.String("host", s.Host), slog.Int("port", s.Port),
		slog.String("username", s.Username), slog.Bool("password", s.Password != ""))
}

// ProxyResolver finds or creates the proxy row matching a parsed proxy URL
// when an account is saved with proxy_url (CONTRACTS §21.4). Implemented by
// proxy.Service, injected into account.Deps.
type ProxyResolver interface {
	// FindOrCreate returns the id of the enabled proxy equal to spec in
	// protocol, host, port, username and password among the rows of scope
	// (the caller's proxy:manage, CONTRACTS §21.0), or, when scope may
	// create (every row or own), inserts a new one owned by ownerID (name
	// "<protocol>://<host>:<port>", status active). It must
	// run inside the caller's transaction (tx) together with the account
	// write; it takes pg_advisory_xact_lock on the match key first so two
	// concurrent saves of the same URL never create duplicates.
	//
	// It writes no audit row and sends no broadcast: the account id the
	// audit detail needs does not exist yet when the account is being
	// created. After the account row is written, callers pass created ==
	// true rows to AuditAutoCreate in the same transaction.
	FindOrCreate(ctx context.Context, tx *gorm.DB, spec ProxySpec, ownerID int64, scope DataScope) (id int64, created bool, err error)
	// AuditAutoCreate records the audit row proxy.create {auto:true,
	// account_id} (actor = ownerID, ip from audit.WithClientIP on ctx) for a
	// proxy FindOrCreate just inserted, and announces the new proxy on
	// config:changed. Call it once per created proxy, in the same
	// transaction, after the account id is known.
	AuditAutoCreate(ctx context.Context, tx *gorm.DB, proxyID, ownerID, accountID int64) error
}
