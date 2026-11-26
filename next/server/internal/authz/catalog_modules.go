package authz

import (
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

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
		// Generating codes mints balance a holder can redeem for anyone,
		// itself included, so it is as strong as balance:adjust:any.
		{key: perm.RedeemManage, en: "Manage redeem codes", zh: "管理兑换码", sensitive: true, superOnly: true,
			desc: lt("Generate, list and void redeem codes.", "批量生成、查看与作废兑换码。"), requires: req(perm.BalanceAllRead)},
	}},
	{"topup", lt("Top-up & payment", "充值/支付"), []corePerm{
		{key: perm.TopupUse, en: "Top up online", zh: "在线充值",
			desc: lt("Top up your own balance with the enabled payment methods and see your own orders.", "用已启用的支付方式给自己充值，查看自己的充值记录。"), requires: req(perm.BalanceSelfRead)},
		{key: perm.TopupOrderRead, en: "View top-up orders", zh: "查看充值订单",
			desc: lt("View every user's top-up orders and the top-up statistics (read only).", "查看全站充值订单与统计（只读）。"), requires: req(perm.BalanceAllRead)},
		// Payment credentials and promo bonuses decide where money goes and how much balance is minted.
		{key: perm.TopupManage, en: "Manage top-up", zh: "管理充值",
			desc: lt("Edit the top-up settings, payment methods, built-in gateway credentials and promo codes.", "修改充值配置、支付方式、内置网关密钥与优惠码。"), sensitive: true, requires: req(perm.TopupOrderRead)},
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
