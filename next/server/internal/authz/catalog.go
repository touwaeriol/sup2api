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

func lt(en, zh string) core.LocalizedText { return core.LocalizedText{"en": en, "zh": zh} }

func req(keys ...perm.Key) []perm.Key { return keys }

// coreModules is the core permission catalog (docs/CONTRACTS.md §4). The order
// here defines the sort order shown in the role editor.
var coreModules = []coreModule{
	{"user", lt("Users", "用户"), []corePerm{
		{key: perm.UserRead, en: "View users", zh: "查看用户",
			desc: lt("List users and open their profiles and roles (balances need balance:all:read).", "查看用户列表与用户详情（资料、角色；余额需要 balance:all:read）。")},
		{key: perm.UserCreate, en: "Create users", zh: "新建用户",
			desc: lt("Create user accounts; giving them roles also needs role:assign.", "新建用户；同时分配角色还需要 role:assign。"), requires: req(perm.UserRead)},
		{key: perm.UserUpdate, en: "Edit users", zh: "编辑用户",
			desc: lt("Edit other users' profiles and status, within the roles you may manage.", "修改他人资料与状态，只限可管理角色的用户。"), requires: req(perm.UserRead)},
		{key: perm.UserDelete, en: "Delete users", zh: "删除用户", sensitive: true, stepUp: true,
			desc: lt("Delete users you may manage; asks for verification each time.", "删除可管理的用户；每次需要二次验证。"), requires: req(perm.UserRead)},
		{key: perm.UserPasswordReset, en: "Reset user passwords and two-factor", zh: "重置他人密码与两步验证", sensitive: true, stepUp: true,
			desc: lt("Reset the password, two-factor and passkeys of users you may manage; asks for verification each time.", "重置可管理用户的密码、两步验证与通行密钥；每次需要二次验证。"), requires: req(perm.UserRead, perm.UserUpdate)},
	}},
	{"role", lt("Roles", "角色"), []corePerm{
		{key: perm.RoleRead, en: "View roles and permissions", zh: "查看角色与权限",
			desc: lt("View roles, their permissions and the permission catalog.", "查看角色、角色权限与权限目录。")},
		{key: perm.RoleDefine, en: "Define roles", zh: "定义角色", sensitive: true,
			desc: lt("Create, edit and delete custom roles, never beyond your own permissions.", "新建、修改、删除自定义角色，授出的权限不能超过自己。"), requires: req(perm.RoleRead)},
		{key: perm.RoleAssign, en: "Assign roles to users", zh: "给用户分配角色", sensitive: true,
			desc: lt("Give or take roles from users, only roles on your assignable list.", "给用户分配或撤销角色，只限自己可分配清单内的角色。"), requires: req(perm.RoleRead, perm.UserRead)},
	}},
	{"apikey", lt("API keys", "API Key"), []corePerm{
		{key: perm.APIKeySelfManage, en: "Manage own API keys", zh: "管理自己的 API Key",
			desc: lt("Create and manage your own gateway API keys.", "新建与管理自己的网关 API Key。")},
	}},
	{"group", lt("Account groups", "账号分组"), []corePerm{
		{key: perm.GroupRead, en: "View account groups", zh: "查看账号分组",
			desc: lt("View account groups and the accounts bound to them.", "查看账号分组及其绑定的账号。")},
		{key: perm.GroupManage, en: "Manage account groups", zh: "管理账号分组", sensitive: true,
			desc: lt("Create, edit and delete account groups with their model prices and rates and which user groups may use them at what rates; also lets restricted account editors change any account's account groups.", "新建、修改、删除账号分组，设置模型价格、模型倍率以及哪些用户分组可用、按什么倍率；账号范围受限的人有它时可改任意账号的账号分组。"), requires: req(perm.GroupRead)},
	}},
	// User groups (segments, CONTRACTS §97): who is in which one, the private
	// groups it may use and what it pays there.
	{"segment", lt("User groups", "用户分组"), []corePerm{
		{key: perm.SegmentRead, en: "View user groups", zh: "查看用户分组",
			desc: lt("View user groups, their members and rates.", "查看用户分组、成员与倍率。")},
		{key: perm.SegmentManage, en: "Manage user groups", zh: "管理用户分组",
			desc: lt("Create, edit and delete user groups: members, private account groups and rates.", "新建、修改、删除用户分组：成员、私有账号分组的开通与倍率。"), requires: req(perm.SegmentRead)},
	}},
	{"account", lt("Accounts", "账号"), []corePerm{
		// Scopable (perm.ScopeType): account:read reaches every account,
		// account:read:own, :group/12, :account/125 … some (CONTRACTS §21).
		{key: perm.AccountRead, en: "View accounts", zh: "查看账号",
			desc: lt("View upstream accounts within the scope granted (all, own, own and the users below, an account group or one account).", "查看授权范围内的上游账号（全部、自己的、自己及下级、某账号分组或单个账号）。")},
		{key: perm.AccountCreate, en: "Create accounts", zh: "新建账号",
			desc: lt("Create upstream accounts within the scope granted.", "在授权范围内新建上游账号。"), requires: req(perm.AccountRead)},
		{key: perm.AccountUpdate, en: "Edit accounts", zh: "编辑账号",
			desc: lt("Edit upstream accounts within the scope granted.", "编辑授权范围内的上游账号。"), requires: req(perm.AccountRead)},
		{key: perm.AccountDelete, en: "Delete accounts", zh: "删除账号", sensitive: true,
			desc: lt("Delete upstream accounts within the scope granted.", "删除授权范围内的上游账号。"), requires: req(perm.AccountRead)},
		{key: perm.AccountTest, en: "Test accounts", zh: "测试账号",
			desc: lt("Send test requests through accounts within the scope granted.", "对授权范围内的账号发起测试请求。"), requires: req(perm.AccountRead)},
		{key: perm.AccountCredentialView, en: "View account credentials", zh: "查看账号凭证", sensitive: true, stepUp: true,
			desc: lt("Reveal the plain credentials of accounts within the scope granted; asks for verification each time.", "明文查看授权范围内账号的凭证；每次需要二次验证。"), requires: req(perm.AccountRead)},
		{key: perm.AccountSettingsCustom, en: "Custom guarded settings (e.g. base URL)", zh: "自定义受限设置（如 Base URL）",
			desc: lt("Set guarded account settings (e.g. base URL) to values other than the official ones.", "把受限的账号设置（如 Base URL）改成非官方值。"), requires: req(perm.AccountUpdate)},
		{key: perm.AccountGroupBind, en: "Bind accounts to account groups (own level)", zh: "将账号绑定到账号分组（own 级）",
			desc: lt("Bind accounts to account groups you can use; only matters to callers with a restricted account scope (own, an account group or one account) and without group:manage.", "把账号绑定到自己可用的账号分组；只对账号范围受限（仅自己、某账号分组或单个账号）且没有 group:manage 的人生效。"), requires: req(perm.AccountUpdate)},
		{key: perm.AccountRelay, en: "Create relay-type accounts with custom upstream", zh: "创建自定义上游的 relay 类账号", sensitive: true,
			desc: lt("Create accounts whose upstream address is free (relay) or re-point it; only matters to callers with a restricted account scope (own, account group or account).", "创建或改动可自定义上游地址的账号（relay）；只对账号范围受限（仅自己、某账号分组或单个账号）的人生效。"), requires: req(perm.AccountCreate)},
		// 所属 (accounts.owner_id) decides whose own and team scopes reach
		// an account (CONTRACTS §103).
		{key: perm.AccountOwnerUpdate, en: "Change account owners", zh: "修改账号所属", sensitive: true,
			desc: lt("Move an account to another user, its owner: whose own and team account scopes reach it.", "把账号的所属改为其他用户；所属决定哪些人的“自己的”“自己及下级”账号范围能看到它。"), requires: req(perm.AccountRead)},
	}},
	{"proxy", lt("Proxies", "代理"), []corePerm{
		{key: perm.ProxyRead, en: "View proxies", zh: "查看代理",
			desc: lt("View proxies within the scope granted.", "查看授权范围内的代理。")},
		{key: perm.ProxyManage, en: "Manage proxies", zh: "管理代理", sensitive: true, stepUp: true,
			desc: lt("Create, edit, delete and reveal proxies within the scope granted; revealing asks for verification.", "在授权范围内新建、修改、删除代理与查看代理地址；查看地址需要二次验证。"), requires: req(perm.ProxyRead)},
	}},
	{"price", lt("Model prices", "模型价格"), []corePerm{
		{key: perm.PriceRead, en: "View model prices", zh: "查看模型价格",
			desc: lt("View model prices and price rules.", "查看模型价格与价格规则。")},
		{key: perm.PriceManage, en: "Manage model prices", zh: "管理模型价格", sensitive: true,
			desc: lt("Edit model prices, price rules and price sync.", "修改模型价格、价格规则与价格同步。"), requires: req(perm.PriceRead)},
	}},
	{"balance", lt("Balance", "余额"), []corePerm{
		{key: perm.BalanceSelfRead, en: "View own balance", zh: "查看自己的余额",
			desc: lt("View your own balance and ledger.", "查看自己的余额与流水。")},
		{key: perm.BalanceAllRead, en: "View all balances", zh: "查看全部余额",
			desc: lt("View every user's balance and the site-wide ledger.", "查看所有用户的余额与全站流水。")},
		// balance:adjust reaches the plain users among those the caller
		// manages (holding no role but user); balance:adjust:any the rest
		// of them too (canActOnUsers, core.ActOnBalance).
		{key: perm.BalanceAdjust, en: "Adjust balances of plain users", zh: "调整普通用户的余额", sensitive: true,
			desc: lt("Add to or deduct from the balance of users you may manage that hold no role but user.", "给可管理、且只持有普通用户角色的用户加减余额。"), requires: req(perm.BalanceAllRead, perm.UserRead)},
		{key: perm.BalanceAdjustAny, en: "Adjust balances of any managed user", zh: "调整任意可管理用户的余额", sensitive: true, superOnly: true,
			desc: lt("With balance:adjust, also adjust the balance of users you may manage that hold other roles (operators, suppliers, custom roles).", "配合 balance:adjust，也能给持有其他角色（运维、供应商、自定义角色）的可管理用户调余额。"), requires: req(perm.BalanceAdjust)},
	}},
	{"redeem", lt("Redeem codes", "兑换码"), []corePerm{
		{key: perm.RedeemUse, en: "Redeem codes", zh: "使用兑换码",
			desc: lt("Redeem a code into your own balance.", "用兑换码给自己的余额充值。"), requires: req(perm.BalanceSelfRead)},
		// Generating codes mints balance, so it is sensitive like adjusting it.
		{key: perm.RedeemManage, en: "Manage redeem codes", zh: "管理兑换码", sensitive: true,
			desc: lt("Generate, list and void redeem codes.", "批量生成、查看与作废兑换码。"), requires: req(perm.BalanceAllRead)},
	}},
	{"usage", lt("Usage", "使用记录"), []corePerm{
		{key: perm.UsageSelfRead, en: "View own usage", zh: "查看自己的用量",
			desc: lt("View your own usage logs and statistics.", "查看自己的使用日志与统计。")},
		{key: perm.UsageAllRead, en: "View all usage", zh: "查看全部使用记录",
			desc: lt("View site-wide usage logs and statistics, user emails included.", "查看全站使用日志与统计，包含用户邮箱。")},
		// Scopable like account:read: the requests served by the accounts
		// of the scope, through the account-side view (/account-usage,
		// CONTRACTS §103), which shows no user, API key or billed amount.
		{key: perm.UsageAccountRead, en: "View account usage", zh: "查看账号的使用记录", sensitive: true,
			desc: lt("View the usage logs and statistics of accounts within the scope granted, without users, API keys or billed amounts.", "查看授权范围内账号的使用记录与统计，不含用户、API Key 与计费金额。"), requires: req(perm.AccountRead)},
		// Reconcile again, or refund, a pre-charged request the core gave up
		// on (CONTRACTS §25.4). It moves money, so it is sensitive and is
		// not implied by reading usage.
		{key: perm.UsageSettle, en: "Reconcile or refund pre-charged usage", zh: "重新核对或退款预扣费用", sensitive: true,
			desc: lt("Reconcile again or refund a pre-charged request the core gave up on.", "对核心放弃结算的预扣费请求重新核对或退款。"), requires: req(perm.UsageAllRead)},
	}},
	{"sticky", lt("Sticky sessions", "粘性会话"), []corePerm{
		{key: perm.StickyRead, en: "View sticky sessions", zh: "查看粘性会话",
			desc: lt("View sticky session bindings.", "查看粘性会话绑定。")},
		{key: perm.StickyManage, en: "Manage sticky sessions", zh: "管理粘性会话",
			desc: lt("Manage sticky rules, clear sticky bindings and edit their settings.", "管理粘性规则、清除粘性绑定并修改其设置。"), requires: req(perm.StickyRead)},
	}},
	{"plugin", lt("Plugins", "插件"), []corePerm{
		{key: perm.PluginRead, en: "View plugins", zh: "查看插件",
			desc: lt("View installed plugins, versions and settings.", "查看已安装插件、版本与设置。")},
		{key: perm.PluginInstall, en: "Install plugins", zh: "安装插件", sensitive: true, stepUp: true,
			desc: lt("Install and upgrade plugins; asks for verification each time.", "安装与升级插件；每次需要二次验证。"), requires: req(perm.PluginRead, perm.PluginMarketRead)},
		{key: perm.PluginManage, en: "Manage plugins", zh: "管理插件", sensitive: true,
			desc: lt("Enable, disable and configure installed plugins.", "启用、停用与配置已安装插件。"), requires: req(perm.PluginRead)},
		{key: perm.PluginUninstall, en: "Uninstall plugins", zh: "卸载插件", sensitive: true,
			desc: lt("Uninstall plugins and drop their data.", "卸载插件并删除其数据。"), requires: req(perm.PluginRead)},
		{key: perm.PluginGrantHigh, en: "Grant high-risk plugin permissions", zh: "授予插件高风险权限", sensitive: true, stepUp: true,
			desc: lt("Approve high-risk plugin permissions at install time.", "安装时批准插件的高风险权限。"), requires: req(perm.PluginInstall)},
		{key: perm.PluginGrantCritical, en: "Grant critical plugin permissions", zh: "授予插件关键权限", sensitive: true, superOnly: true, stepUp: true,
			desc: lt("Approve critical plugin permissions at install time.", "安装时批准插件的关键权限。"), requires: req(perm.PluginInstall)},
		{key: perm.PluginGrantDBSchemaUnconfined, en: "Grant db.schema to non-official plugins", zh: "授予非官方插件 db.schema", sensitive: true, superOnly: true, stepUp: true,
			desc: lt("Approve db.schema for a non-official plugin: it gets a database identity of its own, its tables in the core's database and its own connections to it. Tables outside the plugin take db.core_read on top.", "批准非官方插件的 db.schema：插件在核心的数据库里获得自己的数据库身份、自己的表和自己的连接；访问插件以外的表另需 db.core_read。"), requires: req(perm.PluginInstall)},
		{key: perm.PluginEgressRead, en: "View plugin egress traffic", zh: "查看插件出口流量",
			desc: lt("View the outbound traffic of plugins.", "查看插件的出站流量。"), requires: req(perm.PluginRead)},
		{key: perm.PluginMarketRead, en: "Browse the plugin market", zh: "浏览插件市场",
			desc: lt("Browse the plugin market.", "浏览插件市场。")},
	}},
	{"publisher", lt("Publishers", "发布者"), []corePerm{
		{key: perm.PublisherRead, en: "View publishers", zh: "查看发布者",
			desc: lt("View trusted publishers and their keys.", "查看受信任的发布者与密钥。")},
		{key: perm.PublisherManage, en: "Manage publishers and keys", zh: "管理发布者与密钥", sensitive: true, superOnly: true, stepUp: true,
			desc: lt("Add, edit and revoke publishers and their signing keys.", "新增、修改、吊销发布者及其签名密钥。"), requires: req(perm.PublisherRead)},
	}},
	{"node", lt("Cluster", "集群"), []corePerm{
		{key: perm.NodeRead, en: "View cluster nodes", zh: "查看集群节点",
			desc: lt("View cluster nodes and their health.", "查看集群节点与健康状态。")},
	}},
	{"audit", lt("Audit", "审计"), []corePerm{
		{key: perm.AuditRead, en: "View audit logs", zh: "查看审计日志",
			desc: lt("View the audit log.", "查看审计日志。")},
	}},
	{"task", lt("Scheduled tasks", "定时任务"), []corePerm{
		{key: perm.TaskRead, en: "View scheduled tasks", zh: "查看定时任务",
			desc: lt("View scheduled tasks and their runs.", "查看定时任务与运行记录。")},
		{key: perm.TaskRun, en: "Run scheduled tasks manually", zh: "手动运行定时任务", sensitive: true,
			desc: lt("Run a scheduled task now.", "立即运行定时任务。"), requires: req(perm.TaskRead)},
		{key: perm.TaskManage, en: "Switch plugin scheduled tasks on and off", zh: "启用或停用插件定时任务", sensitive: true,
			desc: lt("Switch plugin scheduled tasks on and off.", "启用或停用插件定时任务。"), requires: req(perm.TaskRead)},
	}},
	{"gateway", lt("Gateway", "网关"), []corePerm{
		{key: perm.GatewayUse, en: "Call the gateway", zh: "调用网关",
			desc: lt("Call the gateway with your API keys.", "用自己的 API Key 调用网关。")},
	}},
	{"settings", lt("Settings", "设置"), []corePerm{
		{key: perm.SettingsRead, en: "View settings", zh: "查看设置",
			desc: lt("View system settings.", "查看系统设置。")},
		{key: perm.SettingsManage, en: "Manage settings", zh: "管理设置", sensitive: true, stepUp: true,
			desc: lt("Edit system settings; installing or reaching host code asks for verification.", "修改系统设置；安装或连接主机端程序需要二次验证。"), requires: req(perm.SettingsRead)},
	}},
	{"system_update", lt("Core updates", "核心升级"), []corePerm{
		{key: perm.SystemUpdateRead, en: "View core updates", zh: "查看核心升级",
			desc: lt("View core versions and update status.", "查看核心版本与升级状态。")},
		{key: perm.SystemUpdateExecute, en: "Execute core updates", zh: "执行核心升级", sensitive: true, superOnly: true, stepUp: true,
			desc: lt("Start a core update; asks for verification each time.", "执行核心升级；每次需要二次验证。"), requires: req(perm.SystemUpdateRead)},
		{key: perm.SystemUpdateRecover, en: "Recover core updates", zh: "恢复核心升级", sensitive: true, superOnly: true, stepUp: true,
			desc: lt("Recover a failed core update; asks for verification each time.", "恢复失败的核心升级；每次需要二次验证。"), requires: req(perm.SystemUpdateRead)},
	}},
}

// userRolePermissions are granted to the built-in "user" role.
var userRolePermissions = []string{"apikey:self:manage", "balance:self:read", "usage:self:read", "gateway:use"}

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
