package authz

import (
	"slices"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// Built-in role keys.
const (
	RoleSuperAdmin = "super_admin"
	RoleAdmin      = "admin"
	RoleOperator   = "operator"
	// RoleSupplier is an add-on role: a supplier holds it next to RoleUser
	// and brings its own accounts and proxies (CONTRACTS §21).
	RoleSupplier = "supplier"
	// RoleSupplierAgent is an add-on role: a supplier that also manages the
	// users below it (CONTRACTS §103).
	RoleSupplierAgent = "supplier_agent"
	RoleUser          = "user"
)

type coreModule struct {
	key   string
	label core.LocalizedText
	perms []corePerm
}

type corePerm struct {
	key    perm.Key
	en, zh string
	// desc explains what the permission allows, for the role editor and
	// the access key selector (permissions.description).
	desc core.LocalizedText
	// requires are soft dependencies: keys the permission is of little use
	// without (an edit without the read of the same rows). The role editor
	// suggests them; they have no bearing on authorization. A scoped action
	// requires the same scope of the keys it lists.
	requires []perm.Key
	// sensitive flags credentials, money, trust roots, code execution and
	// identity or authorization in the role editor; display only, it has no
	// bearing on access keys.
	sensitive bool
	// superOnly: the permission is in no built-in role's defaults, whatever
	// the role's grants say (builtinRole.accepts), so no role gets it
	// automatically. By default only the super admin holds it; a grant needs
	// a holder, so delegating it starts with the super admin (decision D2).
	superOnly bool
	// stepUp marks a permission some of whose routes need a step-up proof
	// (httpapi.Router.StepUp, where the rule is); display only, for the
	// access key selector. Always sensitive (TestCatalogFlagInvariants).
	stepUp bool
}

// userRolePermissions are granted to the built-in "user" role.
var userRolePermissions = []string{"apikey:self:manage", "balance:self:read", "usage:self:read", "gateway:use", "topup:use", "redeem:use"}

// supplierPermissions are granted to the built-in "supplier" role: its own
// accounts and proxies, its own usage and that of its accounts, nothing
// else. account:group:bind is left out for now (decision D27), so a
// supplier binds no groups.
var supplierPermissions = []string{
	"account:read:own", "account:create:own", "account:update:own", "account:delete:own", "account:test:own", "account:credential:view:own",
	"proxy:read:own", "proxy:manage:own",
	"usage:self:read", "usage:account:read:own",
}

// supplierAgentPermissions are granted to the built-in "supplier_agent"
// role (CONTRACTS §103.1): a user's and a supplier's, so that the role
// alone is enough (D8) and covers the users it manages, and for the users
// below it (team) reading their accounts and the usage of those, and
// reading, creating, editing and assigning roles to those users. Left out
// (D4, D5): deleting users and resetting their passwords, balances, user
// groups, group binding, defining roles, and writing, testing or revealing
// their accounts or anything of their proxies. The team keys imply the
// own ones they widen, which SyncCore stores once.
var supplierAgentPermissions = slices.Concat(userRolePermissions, supplierPermissions, []string{
	"account:read:team", "usage:account:read:team",
	"user:read:team", "user:create:team", "user:update:team",
	"role:read", "role:assign",
})

// operatorPermissions are granted to the built-in "operator" role, an
// explicit list (decision D3), so that a new core permission reaches the
// role only by being added here: day-to-day users, groups, accounts and
// proxies, read access to money and observability, and what a user has.
// Left out on purpose: role:define, balance:adjust, usage:settle,
// user:delete, user:password:reset, account:credential:view,
// account:settings:custom (the one account key that restricts a full-level
// caller; group:bind and relay only bind restricted callers and mean nothing
// next to the full-level keys), price:manage, task:manage, sticky sessions,
// settings, plugins, publishers and core updates.
//
// account:credential:view:own shows the credentials of the accounts the
// operator owns itself; with the full-level account, proxy and account
// usage keys it covers the supplier defaults, so an operator may assign
// that role.
var operatorPermissions = slices.Concat([]string{
	"user:read", "user:create", "user:update",
	"role:read", "role:assign",
	"group:read", "group:manage",
	"segment:read", "segment:manage",
	"account:read", "account:create", "account:update", "account:delete", "account:test",
	"account:credential:view:own",
	"proxy:read", "proxy:manage",
	"price:read",
	"balance:all:read",
	"usage:all:read", "usage:account:read",
	"node:read",
	"audit:read",
	"task:read", "task:run",
}, userRolePermissions)

type builtinRole struct {
	key         string
	name        core.LocalizedText
	description core.LocalizedText
	superuser   bool
	// rev is the revision of the defaults below: a database whose role
	// has an older roles.defaults_rev gets the defaults it lacks once
	// (SyncCore), nothing taken away, so later restarts never bring back
	// what an administrator removed. Raise it when an existing permission
	// or role joins the defaults; a newly created one reaches the role
	// without it.
	rev int32
	// grants reports whether a core permission is in the role's defaults,
	// superOnly ones aside (accepts); nil means none. New core permissions
	// are granted to the role when it accepts them, and a reset restores
	// exactly this set.
	grants func(p corePerm) bool
	// scoped are the role's scoped defaults (canonical keys, e.g.
	// account:read:own), granted when the role is created and on a reset.
	scoped []string
	// assignable lists the built-in roles its holders may assign by
	// default, and assignCustom adds every custom role (decision 5). A
	// role created later is added to the lists naming it.
	assignable   []string
	assignCustom bool
}

// oneOf is the grants of a role whose unscoped defaults are those of keys.
func oneOf(keys []string) func(corePerm) bool {
	return func(p corePerm) bool { return slices.Contains(keys, string(p.key)) }
}

// scopedOf lists the scoped keys of keys.
func scopedOf(keys []string) []string {
	return slices.DeleteFunc(slices.Clone(keys), func(k string) bool {
		p, err := perm.Parse(k)
		return err != nil || p.Scope.All
	})
}

// builtinRoles are the roles every database has, with their defaults: the
// one definition SyncCore seeds and fills in and ResetRole restores.
var builtinRoles = []builtinRole{
	{key: RoleSuperAdmin, name: lt("Super admin", "超级管理员"), description: lt("Has every permission, including future ones", "拥有全部权限（包括以后新增的）"), superuser: true, rev: 1},
	{key: RoleAdmin, name: lt("Admin", "管理员"), description: lt("All core permissions by default, except those reserved for the super admin", "默认拥有除超级管理员专属权限以外的全部核心权限"), rev: 1,
		grants:     func(corePerm) bool { return true },
		assignable: []string{RoleOperator, RoleSupplier, RoleSupplierAgent, RoleUser}, assignCustom: true},
	{key: RoleOperator, name: lt("Operator", "运维"), description: lt("Day-to-day users, groups, accounts and proxies; read-only billing and monitoring", "日常管理用户、分组、账号和代理，查看余额、用量、节点、审计和定时任务"), rev: 1,
		grants: oneOf(operatorPermissions), scoped: scopedOf(operatorPermissions),
		assignable: []string{RoleSupplier, RoleUser}},
	{key: RoleSupplier, name: lt("Supplier", "供应商"), description: lt("Add-on role: own accounts and proxies, own usage", "附加角色：管理自己的账号和代理，查看自己的用量"), rev: 2,
		grants: oneOf(supplierPermissions), scoped: scopedOf(supplierPermissions)},
	{key: RoleSupplierAgent, name: lt("Supplier agent", "代理供应商"), description: lt("A user's and a supplier's permissions, and managing the users below it and reading their accounts and usage", "拥有普通用户和供应商的全部权限，并管理下级用户、查看下级的账号和用量"), rev: 1,
		grants: oneOf(supplierAgentPermissions), scoped: scopedOf(supplierAgentPermissions),
		assignable: []string{RoleSupplier, RoleSupplierAgent}},
	{key: RoleUser, name: lt("User", "普通用户"), description: lt("Own API keys, usage and balance; gateway access", "管理自己的 API Key、查看自己的用量和余额、调用网关"), rev: 1,
		grants: oneOf(userRolePermissions)},
}

// reservedRoleKey reports whether key belongs to a built-in role, so that no
// custom role may take it. A custom role holding one stops SyncCore
// (builtinKeyConflict).
func reservedRoleKey(key string) bool {
	_, ok := builtinRoleByKey(key)
	return ok
}

func builtinRoleByKey(key string) (builtinRole, bool) {
	for _, r := range builtinRoles {
		if r.key == key {
			return r, true
		}
	}
	return builtinRole{}, false
}

// accepts reports whether p is in r's defaults: never a superOnly one.
func (r builtinRole) accepts(p corePerm) bool {
	return r.grants != nil && !p.superOnly && r.grants(p)
}

// defaultPermissions returns the core permissions r has by default, the
// scoped ones included.
func (r builtinRole) defaultPermissions() []string {
	return append(r.unscopedDefaults(), r.scoped...)
}

// unscopedDefaults returns the catalog permissions r has by default.
func (r builtinRole) unscopedDefaults() []string {
	var out []string
	for _, m := range coreModules {
		for _, p := range m.perms {
			if r.accepts(p) {
				out = append(out, string(p.key))
			}
		}
	}
	return out
}

// CorePermissions returns the core catalog as permission definitions.
func CorePermissions() []core.PermissionDef {
	var out []core.PermissionDef
	sort := 0
	for _, m := range coreModules {
		for _, p := range m.perms {
			sort += 10
			out = append(out, core.PermissionDef{
				Key: string(p.key), Module: m.key, Label: lt(p.en, p.zh),
				Description: p.description(), Risk: p.risk(), Requires: p.requiredKeys(), Sort: sort,
				ScopeType: string(perm.ScopeType(p.key)),
			})
		}
	}
	return out
}

// description is p's desc, empty rather than nil.
func (p corePerm) description() core.LocalizedText {
	if p.desc == nil {
		return core.LocalizedText{}
	}
	return p.desc
}

// risk is p's level in the catalog: superOnly ones are critical (only a
// holder grants them, first the super admin), sensitive ones sensitive.
func (p corePerm) risk() string {
	switch {
	case p.superOnly:
		return manifest.RiskCritical
	case p.sensitive:
		return manifest.RiskSensitive
	}
	return manifest.RiskLow
}

// requiredKeys is p's requires as strings, nil when none.
func (p corePerm) requiredKeys() []string {
	if len(p.requires) == 0 {
		return nil
	}
	return perm.Strings(p.requires)
}

// StepUpKey reports whether key is a core permission flagged stepUp (the
// route check holds every step-up route to one).
func StepUpKey(key string) bool {
	p, ok := corePermByKey(key)
	return ok && p.stepUp
}

// KnownKey reports whether key is a listed core permission (catalog or self
// module), for the start-up route check.
func KnownKey(key string) bool {
	_, ok := corePermByKey(key)
	return ok
}

// corePermByKey finds a permission of the core catalog or the self module.
func corePermByKey(key string) (corePerm, bool) {
	for _, mods := range [][]coreModule{coreModules, {selfModule}} {
		for _, m := range mods {
			for _, p := range m.perms {
				if string(p.key) == key {
					return p, true
				}
			}
		}
	}
	return corePerm{}, false
}

var (
	coreModuleLabels = map[string]core.LocalizedText{}
	coreModuleOrder  = map[string]int{}
)

func init() {
	for i, m := range coreModules {
		coreModuleLabels[m.key] = m.label
		coreModuleOrder[m.key] = i
	}
}
