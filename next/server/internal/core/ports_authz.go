package core

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// ============================================================ authz (owner: A core-data)

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
