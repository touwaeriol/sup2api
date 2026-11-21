// Mock handlers: users, roles, permissions, API keys, groups, proxies, nodes,
// publishers. (Accounts and account types live in their own mock module.)
import { SCOPABLE, canonicalPermission, parsePermission, permissionIndex } from '../src/authz/implies'
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from './router'
import { ALL_PERMISSIONS, MANAGER_PLUGIN_KEYS, SUPER_ONLY_KEYS, SUPPLIER_AGENT_KEYS, SUPPLIER_KEYS, USER_KEYS, agentUser, caller, filterOwned, hasPerm, inScope, moderatorUser, ownerScope, pricerUser, readonlyUser, resolveTeams, stepUp, stepUpRequired, userEmail, vendorUser, type Identity } from './core'
import { accounts, groupAccountCount, groupPlatforms, groupServed, proxyAccountCount } from './accounts'
import { pricedModels } from './billing'
import { accessOf, findSegment, forgetGroup, forgetUser, groupAccess, moveUser, rateOf, rulesWhere, segmentOfUser, segmentRef } from './pricingStore'

type L = { en: string; zh: string }
const L = (en: string, zh: string): L => ({ en, zh })

export function match(q: string | undefined, ...fields: Array<string | undefined | null>) {
  if (!q) return true
  const s = q.toLowerCase()
  return fields.some((f) => (f || '').toLowerCase().includes(s))
}

export function invalid(field: string, message: string, code = 'invalid') {
  return fail(400, 'invalid_argument', message, { fields: [{ field, code, message }] })
}

// ------------------------------------------------------------------ permissions

type Risk = 'low' | 'sensitive' | 'critical'
/** A plugin permission's manifest scope (manifest.PermissionScope; resource "value" for an enum). */
interface ScopeDecl {
  resource: string
  own?: boolean
  options?: string
  values?: Array<{ key: string; label: L }>
}
interface PermDef {
  key: string
  label: L
  /** Core keys: the super-only ones are critical whatever this says (corePerm.risk). */
  risk?: Risk
  status?: string
  // Plugin keys (UI 3.16).
  group?: string
  requires?: string[]
  scope?: ScopeDecl
  /** Built-in role → full canonical keys, as GET /permissions sends them. */
  defaults?: Record<string, string[]>
}
interface ModuleDef {
  module: string
  label: L
  source: 'core' | 'plugin'
  plugin_key?: string | null
  status: string
  groups?: Array<{ key: string; label: L; sort: number }>
  permissions: PermDef[]
}

const MODULES: ModuleDef[] = [
  { module: 'user', label: L('Users', '用户'), source: 'core', status: 'active', permissions: [
    { key: 'user:read', label: L('View', '查看') }, { key: 'user:create', label: L('Create', '新建') },
    { key: 'user:update', label: L('Edit', '编辑') }, { key: 'user:delete', label: L('Delete', '删除'), risk: 'sensitive' },
    { key: 'user:password:reset', label: L('Reset passwords and 2FA', '重置密码与两步验证'), risk: 'sensitive' }] },
  { module: 'segment', label: L('User groups', '用户组'), source: 'core', status: 'active', permissions: [
    { key: 'segment:read', label: L('View', '查看') }, { key: 'segment:manage', label: L('Manage', '管理') }] },
  { module: 'role', label: L('Roles', '角色'), source: 'core', status: 'active', permissions: [
    { key: 'role:read', label: L('View', '查看') }, { key: 'role:define', label: L('Define', '定义'), risk: 'sensitive' },
    { key: 'role:assign', label: L('Assign', '分配'), risk: 'sensitive' }] },
  { module: 'apikey', label: L('API keys', 'API Key'), source: 'core', status: 'active', permissions: [
    { key: 'apikey:self:manage', label: L('Manage own', '管理自己的') }] },
  { module: 'group', label: L('Groups', '分组'), source: 'core', status: 'active', permissions: [
    { key: 'group:read', label: L('View', '查看') }, { key: 'group:manage', label: L('Manage', '管理') }] },
  { module: 'account', label: L('Accounts', '账号'), source: 'core', status: 'active', permissions: [
    { key: 'account:read', label: L('View', '查看') }, { key: 'account:create', label: L('Create', '新建') },
    { key: 'account:update', label: L('Edit', '编辑') }, { key: 'account:delete', label: L('Delete', '删除'), risk: 'sensitive' },
    { key: 'account:test', label: L('Test', '测试') }, { key: 'account:credential:view', label: L('View credentials', '查看凭证'), risk: 'sensitive' },
    { key: 'account:settings:custom', label: L('Custom restricted settings', '自定义受限设置') },
    { key: 'account:group:bind', label: L('Bind accounts to groups (own level)', '将账号绑定到分组（own 级）') },
    { key: 'account:relay', label: L('Create relay-type accounts with custom upstream', '创建自定义上游的 relay 类账号'), risk: 'sensitive' }] },
  { module: 'proxy', label: L('Proxies', '代理'), source: 'core', status: 'active', permissions: [
    { key: 'proxy:read', label: L('View', '查看') }, { key: 'proxy:manage', label: L('Manage', '管理') }] },
  { module: 'price', label: L('Prices', '价格'), source: 'core', status: 'active', permissions: [
    { key: 'price:read', label: L('View', '查看') }, { key: 'price:manage', label: L('Manage', '管理') }] },
  { module: 'balance', label: L('Balance', '余额'), source: 'core', status: 'active', permissions: [
    { key: 'balance:self:read', label: L('View own', '查看自己的') }, { key: 'balance:all:read', label: L('View all', '查看全部') },
    { key: 'balance:adjust', label: L('Adjust plain users', '调整普通用户'), risk: 'sensitive' },
    { key: 'balance:adjust:any', label: L('Adjust any managed user', '调整任意可管理用户'), risk: 'sensitive' }] },
  { module: 'usage', label: L('Usage', '使用记录'), source: 'core', status: 'active', permissions: [
    { key: 'usage:self:read', label: L('View own', '查看自己的') }, { key: 'usage:all:read', label: L('View all', '查看全部') },
    { key: 'usage:settle', label: L('Settle usage', '结算用量'), risk: 'sensitive' }] },
  { module: 'sticky', label: L('Sticky sessions', '粘性会话'), source: 'core', status: 'active', permissions: [
    { key: 'sticky:read', label: L('View', '查看') }, { key: 'sticky:manage', label: L('Manage', '管理') }] },
  { module: 'plugin', label: L('Plugins', '插件管理'), source: 'core', status: 'active', permissions: [
    { key: 'plugin:read', label: L('View', '查看') }, { key: 'plugin:install', label: L('Install', '安装'), risk: 'sensitive' },
    { key: 'plugin:manage', label: L('Manage', '管理') }, { key: 'plugin:uninstall', label: L('Uninstall', '卸载'), risk: 'sensitive' },
    { key: 'plugin:grant:high', label: L('Grant high risk', '授予高风险权限') },
    { key: 'plugin:grant:critical', label: L('Grant critical risk', '授予极高风险权限'), risk: 'sensitive' },
    { key: 'plugin:grant:db_schema_unconfined', label: L('Grant db.schema to non-official plugins', '授予非官方插件 db.schema'), risk: 'sensitive' },
    { key: 'plugin:egress:read', label: L('View egress', '查看出口流量') }, { key: 'plugin:market:read', label: L('Browse market', '浏览市场') }] },
  { module: 'publisher', label: L('Publishers', '发布者'), source: 'core', status: 'active', permissions: [
    { key: 'publisher:read', label: L('View', '查看') }, { key: 'publisher:manage', label: L('Manage', '管理'), risk: 'sensitive' }] },
  { module: 'node', label: L('Cluster', '集群'), source: 'core', status: 'active', permissions: [{ key: 'node:read', label: L('View nodes', '查看节点') }] },
  { module: 'audit', label: L('Audit logs', '审计日志'), source: 'core', status: 'active', permissions: [{ key: 'audit:read', label: L('View audit logs', '查看审计日志') }] },
  { module: 'task', label: L('Scheduled tasks', '定时任务'), source: 'core', status: 'active', permissions: [{ key: 'task:read', label: L('View scheduled tasks', '查看定时任务') }, { key: 'task:run', label: L('Run scheduled tasks manually', '手动运行定时任务') }, { key: 'task:manage', label: L('Manage scheduled tasks', '管理定时任务'), risk: 'sensitive' }] },
  { module: 'gateway', label: L('Gateway', '网关'), source: 'core', status: 'active', permissions: [{ key: 'gateway:use', label: L('Call gateway', '调用网关') }] },
  { module: 'settings', label: L('Settings', '系统设置'), source: 'core', status: 'active', permissions: [
    { key: 'settings:read', label: L('View', '查看') }, { key: 'settings:manage', label: L('Manage', '管理') }] },
  { module: 'system', label: L('Core updates', '核心升级'), source: 'core', status: 'active', permissions: [
    { key: 'system:update:read', label: L('View', '查看') }, { key: 'system:update:execute', label: L('Upgrade', '升级'), risk: 'sensitive' },
    { key: 'system:update:recover', label: L('Recover', '恢复'), risk: 'sensitive' }] },
  // Groups, a resource scope, an enum scope and a critical key: the permission matrix (UI 3.16).
  { module: 'plugin.guard', label: L('Guard', 'Guard'), source: 'plugin', plugin_key: 'guard', status: 'active',
    groups: [{ key: 'rules', label: L('Rules', '拦截规则'), sort: 10 }, { key: 'ops', label: L('Operations', '运行'), sort: 20 }], permissions: [
      { key: 'plugin.guard:rules:read', label: L('View rules', '查看规则'), group: 'rules',
        defaults: { admin: ['plugin.guard:rules:read'], operator: ['plugin.guard:rules:read'] } },
      { key: 'plugin.guard:rules:manage', label: L('Manage rules', '管理规则'), risk: 'sensitive', group: 'rules', requires: ['plugin.guard:rules:read'],
        scope: { resource: 'ruleset', own: true, options: '/scopes/ruleset' },
        defaults: { admin: ['plugin.guard:rules:manage'], operator: ['plugin.guard:rules:manage:own'] } },
      // No longer declared by the running version (0.2.0 brings it back): kept on the roles holding it until taken off.
      { key: 'plugin.guard:rules:test', label: L('Dry-run rules', '试运行规则'), group: 'rules', status: 'removed' },
      { key: 'plugin.guard:stats:read', label: L('View stats', '查看统计'), group: 'ops',
        defaults: { admin: ['plugin.guard:stats:read'], operator: ['plugin.guard:stats:read'], supplier: ['plugin.guard:stats:read'] } },
      { key: 'plugin.guard:alerts:mute', label: L('Mute alerts', '静音告警'), group: 'ops',
        scope: { resource: 'value', values: [{ key: 'webhook', label: L('Webhook', 'Webhook') }, { key: 'email', label: L('Email', '邮件') }] },
        defaults: { admin: ['plugin.guard:alerts:mute'] } },
      { key: 'plugin.guard:bypass', label: L('Bypass the rules', '免于拦截'), risk: 'critical', group: 'ops' }] },
  { module: 'plugin.foo', label: L('Foo Platform', 'Foo 平台'), source: 'plugin', plugin_key: 'foo', status: 'disabled', permissions: [
    { key: 'plugin.foo:dashboard:read', label: L('View dashboard', '查看面板'), status: 'disabled' }] }
]

function moduleOut(m: ModuleDef) {
  return {
    ...m,
    plugin_key: m.plugin_key ?? null,
    // A staged key (a version approved, not running yet) is no editor's business.
    permissions: m.permissions.filter((p) => p.status !== 'staged').map((p) => ({
      ...p,
      risk: SUPER_ONLY_KEYS.has(p.key) ? 'critical' : (p.risk ?? 'low'),
      // The account and proxy actions take a scope (CONTRACTS §21); a plugin's scoped key ranges over "plugin:<resource>".
      scope_type: SCOPABLE[p.key] ?? (p.scope ? `plugin:${p.scope.resource}` : ''),
      status: p.status || m.status
    }))
  }
}

on('GET', '/permissions', () => MODULES.map(moduleOut))

/** The catalog entry of a key's action, if the key names one. */
const permDef = (key: string) => {
  const action = parsePermission(key)?.action
  return MODULES.flatMap((m) => m.permissions).find((p) => p.key === action)
}

/**
 * Whether the scope of a valid key is one its permission accepts (authz
 * pluginScope.fits): a plugin key only the resource, own and enum values its
 * manifest declares; core scopes are checked by the parser already.
 */
function scopeFits(key: string): boolean {
  const p = parsePermission(key)
  const def = permDef(key)
  if (!p || !def) return false
  if (!key.startsWith('plugin.') || p.scope.all) return true
  if (p.scope.own) return !!def.scope?.own
  const [res, value] = (p.scope.ref ?? '').split('/')
  if (!def.scope || res !== def.scope.resource) return false
  return !def.scope.values || def.scope.values.some((v) => v.key === value)
}

// ------------------------------------------------------------------ roles

interface MockRole {
  id: number
  key: string
  name: L
  description: string
  builtin: boolean
  superuser: boolean
  permission_keys: string[]
  assignable_role_keys: string[]
  assign_custom: boolean
  created_at: string
  updated_at: string
  /** Moved by every change to the role (roles.revision, C1). */
  revision?: number
}

const CORE_KEYS = ALL_PERMISSIONS.filter((k) => !k.startsWith('plugin.'))
// Built-in defaults (CONTRACTS §4, authz/catalog.go): admin has every core
// permission but the super-only ones; operator, supplier, supplier_agent and
// user are explicit lists (core.ts holds those an identity signs in with).
const OPERATOR_KEYS = [
  'user:read', 'user:create', 'user:update', 'role:read', 'role:assign', 'group:read', 'group:manage', 'segment:read', 'segment:manage',
  'account:read', 'account:create', 'account:update', 'account:delete', 'account:test', 'account:credential:view:own', 'proxy:read', 'proxy:manage', 'price:read',
  'balance:all:read', 'usage:all:read', 'usage:account:read', 'node:read', 'audit:read', 'task:read', 'task:run', ...USER_KEYS
]
const ADMIN_KEYS = CORE_KEYS.filter((k) => !SUPER_ONLY_KEYS.has(k))
const BUILTIN_DEFAULTS: Record<string, { name: L; description: string; keys: string[]; assignable: string[]; assignCustom: boolean }> = {
  admin: { name: L('Admin', '管理员'), description: 'Every core permission except the super-only ones', keys: ADMIN_KEYS, assignable: ['operator', 'supplier', 'supplier_agent', 'user'], assignCustom: true },
  operator: { name: L('Operator', '运维'), description: 'Users, accounts, proxies and operations', keys: OPERATOR_KEYS, assignable: ['supplier', 'user'], assignCustom: false },
  supplier: { name: L('Supplier', '供应商'), description: 'Own accounts, proxies and usage (an add-on to user)', keys: SUPPLIER_KEYS, assignable: [], assignCustom: false },
  supplier_agent: { name: L('Supplier agent', '代理供应商'), description: "A user's and a supplier's permissions, and managing the users below it and reading their accounts and usage", keys: SUPPLIER_AGENT_KEYS, assignable: ['supplier', 'supplier_agent'], assignCustom: false },
  user: { name: L('User', '普通用户'), description: 'Own API keys, usage and balance; gateway access', keys: USER_KEYS, assignable: [], assignCustom: false }
}
const at = (days: number) => now(-86400 * days)
const builtin = (id: number, key: string, extra: string[] = []): MockRole => {
  const d = BUILTIN_DEFAULTS[key]
  return { id, key, name: d.name, description: d.description, builtin: true, superuser: false, permission_keys: [...d.keys, ...extra], assignable_role_keys: [...d.assignable], assign_custom: d.assignCustom, created_at: at(90), updated_at: at(10) }
}
/** Keys canonical, without duplicates (perm.CanonicalSet); a key that does not parse is dropped. */
const canonicalSet = (keys: string[]) => [...new Set(keys.map(canonicalPermission).filter((k): k is string => !!k))].sort()
const custom = (id: number, key: string, name: L, description: string, keys: string[]): MockRole => ({ id, key, name, description, builtin: false, superuser: false, permission_keys: canonicalSet(keys), assignable_role_keys: [], assign_custom: false, created_at: at(5), updated_at: at(1) })
const roles: MockRole[] = [
  { id: 1, key: 'super_admin', name: L('Super admin', '超级管理员'), description: 'Has every permission', builtin: true, superuser: true, permission_keys: [], assignable_role_keys: [], assign_custom: false, created_at: at(90), updated_at: at(90) },
  // The manager's role: some guard keys (rules:manage at the own scope) and the disabled foo plugin's key.
  builtin(2, 'admin', [...MANAGER_PLUGIN_KEYS, 'plugin.foo:dashboard:read']),
  builtin(3, 'user'),
  builtin(4, 'operator', ['plugin.guard:stats:read', 'plugin.guard:rules:test']),
  builtin(9, 'supplier'),
  builtin(11, 'supplier_agent'),
  // CONTRACTS §21.1 typical roles: vendor (own-level only, no settings:custom) and read-only.
  custom(5, 'vendor', L('Vendor', '供应商（自定义）'), 'Maintains only the accounts and proxies it created', vendorUser.permissions),
  custom(6, 'readonly', L('Read only', '只读'), 'Sees every account and proxy, changes nothing', readonlyUser.permissions),
  custom(7, 'moderator', L('Moderator', '审核员'), 'Runs the moderation plugin; looks up no user, group or account', moderatorUser.permissions),
  // Group pricing without users (CONTRACTS §97.6): personal user groups go by "#<user id>".
  custom(8, 'pricer', L('Pricer', '定价员'), 'Prices groups and user groups, sees no users', pricerUser.permissions),
  // Resource scopes (CONTRACTS §21): the accounts of group 1, one more account of group 2, one proxy.
  custom(10, 'group_ops', L('Group operator', '分组运维'), 'Runs the accounts of group 1', [
    'account:read:group/1', 'account:create:group/1', 'account:update:group/1', 'account:test:group/1', 'account:update:group/2/account/13',
    'proxy:read:proxy/1', 'proxy:manage:proxy/1', 'group:read', 'gateway:use'
  ])
]

/** The global authz version (authz_meta): every role or grant write bumps it. */
export let authzVersion = 1
export function bumpAuthz() {
  authzVersion++
}
/**
 * A role's revision (roles.revision, C1): every change to the role moves
 * it; roles and each role of the plugin matrix carry it, and a write given
 * another one fails with 409 version_conflict (without one it is not
 * checked).
 */
function revisionConflict(r: MockRole, rev: unknown) {
  if (rev === undefined || rev === (r.revision ?? 0)) return null
  return fail(409, 'version_conflict', 'the role changed since it was read', { revision: r.revision ?? 0 })
}
function revise(r: MockRole) {
  r.revision = (r.revision ?? 0) + 1
  r.updated_at = now()
}

/** The roles the caller manages (CONTRACTS §43.6): all but super_admin for the super admin; else what its roles may assign, the custom ones with assign_custom. */
function managedRoles(who: Identity): MockRole[] {
  if (who.superuser) return roles.filter((r) => !r.superuser)
  const mine = roles.filter((r) => who.roles.includes(r.key))
  const keys = new Set(mine.flatMap((r) => r.assignable_role_keys))
  const anyCustom = mine.some((r) => r.assign_custom)
  return roles.filter((r) => !r.superuser && (keys.has(r.key) || (anyCustom && !r.builtin)))
}
export const managesRole = (who: Identity, key: string) => managedRoles(who).some((r) => r.key === key)

const removedAction = (key: string) => {
  const action = parsePermission(key)?.action
  return MODULES.some((m) => m.permissions.some((p) => p.key === action && (p.status || m.status) === 'removed'))
}
/**
 * What the caller may grant (the grant view): its own keys and its roles'
 * keys, a disabled plugin's included, a removed permission's left out.
 */
export function mayGrant(who: Identity, key: string): boolean {
  if (who.superuser) return true
  const keys = [...who.permissions, ...roles.filter((r) => who.roles.includes(r.key)).flatMap((r) => r.permission_keys)].filter((k) => !removedAction(k))
  return permissionIndex(keys).has(key)
}
/** Whether the caller may change the role (editableRole): managed, and nothing it holds beyond the caller (§43.6). */
function editableBy(who: Identity, r: MockRole): boolean {
  return managesRole(who, r.key) && r.permission_keys.every((k) => removedAction(k) || mayGrant(who, k))
}
/** The keys of next that newly grant a critical plugin permission: a write with one asks for step-up (C3). */
function newCritical(held: string[], next: string[]): string[] {
  const had = permissionIndex(held)
  return next.filter((k) => k.startsWith('plugin.') && permDef(k)?.risk === 'critical' && !had.has(k))
}

/** Adds keys to a role (a confirmed review of held role grants). */
export function addRoleKeys(roleKey: string, keys: string[]) {
  const r = roles.find((x) => x.key === roleKey)
  if (!r) return
  r.permission_keys = canonicalSet([...r.permission_keys, ...keys])
  revise(r)
}

function roleOut(r: MockRole) {
  return { ...r, user_count: users.filter((u) => u.roles.includes(r.key)).length, revision: r.revision ?? 0 }
}

on('GET', '/roles', (req) => paginate(roles.map(roleOut), req.query))
on('GET', '/roles/:id', (req) => {
  const r = roles.find((x) => x.id === Number(req.params.id))
  return r ? roleOut(r) : fail(404, 'not_found', 'role not found')
})
on('POST', '/roles', (req) => {
  const b = req.body || {}
  if (!/^[a-z][a-z0-9_]{1,49}$/.test(b.key || '')) return invalid('key', 'invalid role key')
  if (BUILTIN_DEFAULTS[b.key] || b.key === 'super_admin') return invalid('key', 'key is reserved for a built-in role', 'reserved')
  if (roles.some((r) => r.key === b.key)) return fail(409, 'conflict', 'role key already exists')
  const r: MockRole = { id: nextId(), key: b.key, name: b.name || L(b.key, b.key), description: b.description || '', builtin: false, superuser: false, permission_keys: [], assignable_role_keys: [], assign_custom: false, created_at: now(), updated_at: now() }
  roles.push(r)
  bumpAuthz()
  return roleOut(r)
})
on('PATCH', '/roles/:id', (req) => {
  const r = roles.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'role not found')
  if (r.superuser) return fail(403, 'permission_denied', 'superuser role cannot be edited')
  const conflict = revisionConflict(r, req.body?.revision)
  if (conflict) return conflict
  if (req.body?.name) r.name = req.body.name
  if (req.body?.description !== undefined) r.description = req.body.description
  revise(r)
  bumpAuthz()
  return roleOut(r)
})
on('DELETE', '/roles/:id', (req) => {
  const r = roles.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'role not found')
  if (r.builtin) return fail(409, 'conflict', 'built-in roles cannot be deleted')
  roles.splice(roles.indexOf(r), 1)
  users.forEach((u) => (u.roles = u.roles.filter((k) => k !== r.key)))
  bumpAuthz()
  return noContent()
})
on('PUT', '/roles/:id/permissions', (req) => {
  const r = roles.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'role not found')
  if (r.superuser) return fail(403, 'permission_denied', 'superuser role cannot be edited')
  const who = caller(req)
  if (!editableBy(who, r)) return fail(403, 'permission_denied', 'you cannot change this role', { role: r.key })
  const conflict = revisionConflict(r, req.body?.revision)
  if (conflict) return conflict
  const keys: string[] = Array.isArray(req.body?.permission_keys) ? req.body.permission_keys : []
  // Any spelling (aliases, trailing /*) is taken and stored canonical; the actions must be in the catalog, at a scope they take.
  const bad = keys.find((k) => !scopeFits(k))
  if (bad) return invalid('permission_keys', `invalid permission key ${bad}`)
  const canonical = canonicalSet(keys)
  // Only keys the editor's grant view implies can be added (Missing: the canonical keys refused); what the role's own keys imply is no addition (notImplied).
  const had = permissionIndex(r.permission_keys)
  const missing = canonical.filter((k) => !had.has(k) && !mayGrant(who, k))
  if (missing.length) return fail(403, 'permission_denied', 'you can only grant permissions you hold', { missing })
  if (newCritical(r.permission_keys, canonical).length) {
    const verify = stepUpRequired(req, 'plugin.grant')
    if (verify) return verify
  }
  r.permission_keys = canonical
  revise(r)
  bumpAuthz()
  return roleOut(r)
})
on('PUT', '/roles/:id/assignable', (req) => {
  const r = roles.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'role not found')
  if (r.superuser) return fail(409, 'conflict', 'superuser role cannot be edited')
  const conflict = revisionConflict(r, req.body?.revision)
  if (conflict) return conflict
  const keys: string[] = Array.isArray(req.body?.role_keys) ? req.body.role_keys : []
  const bad = keys.find((k) => !roles.some((x) => x.key === k && !x.superuser))
  if (bad) return invalid('role_keys', `unknown role ${bad}`, 'unknown')
  r.assignable_role_keys = [...new Set(keys)].sort()
  r.assign_custom = !!req.body?.assign_custom
  revise(r)
  bumpAuthz()
  return roleOut(r)
})
// A plugin's permissions × roles (GET/PUT /permissions/plugins/:plugin, UI 3.16):
// the roles the caller manages are editable, the others read-only; each with
// the revision the PUT checks (C1).
function pluginRoleMatrix(req: MockRequest, m: ModuleDef) {
  const prefix = `${m.module}:`
  const who = caller(req)
  return {
    plugin_key: m.plugin_key,
    module: moduleOut(m),
    roles: roles.map((r) => {
      const editable = !r.superuser && editableBy(who, r)
      return {
        id: r.id,
        key: r.key,
        name: r.name,
        builtin: r.builtin,
        superuser: r.superuser,
        editable,
        denied: r.superuser ? 'superuser_role' : editable ? undefined : 'not_assignable',
        revision: r.revision ?? 0,
        permission_keys: r.permission_keys.filter((k) => k.startsWith(prefix))
      }
    })
  }
}
const pluginModule = (plugin: string) => MODULES.find((m) => m.source === 'plugin' && m.plugin_key === plugin)

on('GET', '/permissions/plugins/:plugin', (req) => {
  const m = pluginModule(req.params.plugin)
  return m ? pluginRoleMatrix(req, m) : fail(404, 'not_found', 'plugin not found')
})
on('PUT', '/permissions/plugins/:plugin', (req) => {
  const m = pluginModule(req.params.plugin)
  if (!m) return fail(404, 'not_found', 'plugin not found')
  const who = caller(req)
  const revisions: Record<string, number> = req.body?.revisions ?? {}
  const removed = new Set(m.permissions.filter((p) => (p.status || m.status) === 'removed').map((p) => p.key))
  const body: Record<string, string[]> = req.body?.roles ?? {}
  // Every role is checked before any is written: one refusal leaves them all as they were.
  const writes: Array<[MockRole, string[]]> = []
  let critical = false
  for (const key of Object.keys(body).sort()) {
    const r = roles.find((x) => x.key === key)
    if (!r) return invalid(`roles.${key}`, `unknown role ${key}`, 'unknown')
    if (r.superuser || !editableBy(who, r)) return fail(403, 'permission_denied', 'you cannot change this role', { role: key })
    const conflict = revisionConflict(r, revisions[key])
    if (conflict) return conflict
    const keys = Array.isArray(body[key]) ? body[key] : []
    const foreign = keys.find((k) => !k.startsWith(`${m.module}:`))
    if (foreign) return invalid(`roles.${key}`, `${foreign} is not a permission of ${m.plugin_key}`, 'foreign')
    const bad = keys.find((k) => !scopeFits(k))
    if (bad) return invalid(`roles.${key}`, `invalid permission key ${bad}`)
    const held = r.permission_keys.filter((k) => k.startsWith(`${m.module}:`))
    // A removed key stays while kept and goes when left out; a new one is dropped.
    const next = canonicalSet(keys).filter((k) => held.includes(k) || !removed.has(parsePermission(k)!.action))
    const had = permissionIndex(held)
    const missing = next.filter((k) => !had.has(k) && !mayGrant(who, k))
    if (missing.length) return fail(403, 'permission_denied', 'you can only grant permissions you hold', { missing, role: key })
    critical ||= newCritical(held, next).length > 0
    writes.push([r, next])
  }
  if (critical) {
    const verify = stepUpRequired(req, 'plugin.grant')
    if (verify) return verify
  }
  for (const [r, next] of writes) {
    r.permission_keys = [...r.permission_keys.filter((k) => !k.startsWith(`${m.module}:`)), ...next].sort()
    revise(r)
  }
  if (writes.length) bumpAuthz()
  return pluginRoleMatrix(req, m)
})

// The rows of plugin resources the scope pickers offer (GET /plugins/:key/scope-options), by "<plugin>:<resource>".
const PLUGIN_SCOPE_ROWS: Record<string, Array<{ id: string; name: string; hint?: string }>> = {
  'guard:ruleset': [
    { id: '1', name: 'Default rules', hint: '12 rules' },
    { id: '2', name: 'Prompt injection', hint: '5 rules' },
    { id: '3', name: 'PII', hint: '8 rules' }
  ]
}
on('GET', '/plugins/:key/scope-options', (req) => {
  const who = caller(req)
  if (!hasPerm(who, ['role:read', 'plugin:read'])) return fail(403, 'permission_denied', 'role:read or plugin:read required')
  const m = pluginModule(req.params.key)
  const action = `plugin.${req.params.key}:${req.query.permission ?? ''}`
  const def = m?.permissions.find((p) => p.key === action)
  if (!m || !def?.scope) return fail(404, 'not_found', 'no scoped permission of that key')
  const { resource, values } = def.scope
  // An enum is answered from the manifest; a resource asks the plugin, which must be running.
  if (!values && m.status !== 'active') return fail(503, 'plugin_unavailable', 'the plugin is not running')
  const rows = values ? values.map((v) => ({ id: v.key, name: v.label.en })) : (PLUGIN_SCOPE_ROWS[`${req.params.key}:${resource}`] ?? [])
  // Only the values the caller may grant: its own scopes of the action.
  const mine = rows.filter((x) => hasPerm(who, `${action}:${resource}/${x.id}`))
  if (req.query.ids) {
    const ids = req.query.ids.split(',')
    return { items: mine.filter((x) => ids.includes(x.id)), more: false }
  }
  const limit = Math.min(Math.max(Number(req.query.limit) || 20, 1), 50)
  const found = mine.filter((x) => match(req.query.q, x.name, x.id))
  return { items: found.slice(0, limit), more: found.length > limit }
})

// The caller's managed roles (CONTRACTS §43.6), each saying what of it the caller could not grant.
on('GET', '/me/assignable-roles', (req) => {
  const who = caller(req)
  return managedRoles(who)
    .sort((a, b) => Number(b.builtin) - Number(a.builtin))
    .map((r) => {
      const missing = r.permission_keys.filter((k) => !removedAction(k) && !mayGrant(who, k))
      return { key: r.key, name: r.name, builtin: r.builtin, grantable: !missing.length, missing, missing_assignable: [], missing_assign_custom: false }
    })
})
on('POST', '/roles/:id/reset', (req) => {
  const r = roles.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'role not found')
  const d = BUILTIN_DEFAULTS[r.key]
  if (!r.builtin || (!d && !r.superuser)) return fail(409, 'conflict', 'only built-in roles can be reset')
  const conflict = revisionConflict(r, req.body?.revision)
  if (conflict) return conflict
  if (d) {
    // Plugin permissions on the role are kept.
    r.permission_keys = [...d.keys, ...r.permission_keys.filter((k) => k.startsWith('plugin.'))]
    r.assignable_role_keys = [...d.assignable]
    r.assign_custom = d.assignCustom
    r.name = d.name
    r.description = d.description
  }
  revise(r)
  bumpAuthz()
  return roleOut(r)
})

// ------------------------------------------------------------------ groups

export interface MockGroup {
  id: number
  name: string
  description: string
  status: string
  rate_multiplier: string
  visibility: 'public' | 'restricted'
  model_allowlist: string[]
  model_filter_mode?: 'whitelist' | 'blacklist'
  account_count: number
  created_at: string
}

export const groups: MockGroup[] = [
  { id: 1, name: 'default', description: 'Default group for everyone', status: 'active', rate_multiplier: '1', visibility: 'public', model_allowlist: [], account_count: 8, created_at: now(-86400 * 60) },
  { id: 2, name: 'vip', description: 'Discounted Claude access', status: 'active', rate_multiplier: '0.8', visibility: 'restricted', model_allowlist: ['claude-*'], account_count: 3, created_at: now(-86400 * 30) },
  { id: 3, name: 'haiku-only', description: 'Cheap models for batch jobs', status: 'disabled', rate_multiplier: '0.5', visibility: 'restricted', model_allowlist: ['claude-3-5-haiku*', 'claude-haiku-4*'], account_count: 1, created_at: now(-86400 * 5) },
  { id: 4, name: 'gemini-trial', description: 'Planned Gemini access (no accounts yet)', status: 'active', rate_multiplier: '1', visibility: 'public', model_allowlist: ['gemini-*'], account_count: 0, created_at: now(-86400 * 1) }
]

// account_count and platforms come from the accounts mock (CONTRACTS §13);
// the key count uses the server spelling api_key_count.
function groupOut(g: MockGroup) {
  return { ...g, account_count: groupAccountCount(g.id), platforms: groupPlatforms(g.id), api_key_count: keys.filter((k) => k.group_id === g.id).length, access: groupAccess(g), price_rule_count: rulesWhere({ group_id: g.id }).length, model_count: groupModels(g).length }
}

const GLOB_SPECIALS = /[.+^${}()|[\]\\]/g

function globMatch(pattern: string, s: string): boolean {
  return new RegExp('^' + pattern.replace(GLOB_SPECIALS, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.') + '$').test(s)
}

/** The model policy of a group (core.GroupInfo.AllowsModel): an empty list allows every model. */
function allowsModel(g: Pick<MockGroup, 'model_allowlist' | 'model_filter_mode'>, model: string): boolean {
  if (!g.model_allowlist.length) return true
  const listed = g.model_allowlist.some((p) => globMatch(p, model))
  return g.model_filter_mode === 'blacklist' ? !listed : listed
}

/**
 * The models a group provides: those its accounts declare, through its model
 * policy. An account that declares none serves every model, which here is
 * every model with a global price.
 */
export function groupModels(g: Pick<MockGroup, 'id' | 'model_allowlist' | 'model_filter_mode'>): string[] {
  const members = accounts.filter((a) => a.group_ids.includes(g.id))
  const declared: string[] = members.flatMap((a) => a.models || [])
  const open = members.some((a) => !a.models?.length)
  return [...new Set([...declared, ...(open ? pricedModels() : [])])].filter((m) => allowsModel(g, m)).sort()
}


function validateGroup(b: any) {
  if (b.name !== undefined && !String(b.name).trim()) return invalid('name', 'name is required')
  if (b.rate_multiplier !== undefined && !(Number(b.rate_multiplier) >= 0)) return invalid('rate_multiplier', 'must be a non-negative number')
  if (b.visibility !== undefined && !['public', 'restricted'].includes(b.visibility)) return invalid('visibility', 'invalid visibility')
  return null
}

// ?model=<name> keeps the groups that provide that model.
on('GET', '/groups', (req) => {
  const { q, model } = req.query
  const rows = groups.filter((g) => match(q, g.name, g.description) && (!model || groupModels(g).includes(model)))
  return paginate(rows.map(groupOut), req.query)
})
on('GET', '/groups/:id/models', req => {
 const gid = Number(req.params.id)
 if (!groups.some(g => g.id === gid)) return fail(404, 'not_found', 'group not found')
 const members = accounts.filter(a => a.group_ids.includes(gid))
 return { models: [...new Set(members.flatMap(a => a.models || []))].sort(), unrestricted_accounts: members.filter(a => !a.models?.length).length }
})
on('GET', '/groups/:id', (req) => {
  const g = groups.find((x) => x.id === Number(req.params.id))
  // served_protocols comes with a single group only.
  return g ? { ...groupOut(g), served_protocols: groupServed(g.id) } : fail(404, 'not_found', 'group not found')
})
on('POST', '/groups', (req) => {
  const b = req.body || {}
  const err = validateGroup({ name: b.name ?? '', ...b })
  if (err) return err
  if (groups.some((g) => g.name === b.name)) return invalid('name', 'name already in use')
  const g: MockGroup = {
    id: nextId(),
    name: b.name,
    description: b.description || '',
    status: b.status || 'active',
    rate_multiplier: String(b.rate_multiplier ?? '1'),
    visibility: b.visibility || 'public',
    model_allowlist: b.model_allowlist || [],
    model_filter_mode: b.model_filter_mode || 'blacklist',
    account_count: 0,
    created_at: now()
  }
  groups.push(g)
  return groupOut(g)
})
on('PATCH', '/groups/:id', (req) => {
  const g = groups.find((x) => x.id === Number(req.params.id))
  if (!g) return fail(404, 'not_found', 'group not found')
  const err = validateGroup(req.body || {})
  if (err) return err
  const b = req.body || {}
  for (const k of ['name', 'description', 'status', 'visibility', 'model_allowlist', 'model_filter_mode'] as const) if (b[k] !== undefined) (g as any)[k] = b[k]
  if (b.rate_multiplier !== undefined) g.rate_multiplier = String(b.rate_multiplier)
  return groupOut(g)
})
on('DELETE', '/groups/:id', (req) => {
  const g = groups.find((x) => x.id === Number(req.params.id))
  if (!g) return fail(404, 'not_found', 'group not found')
  // Keys are only loosely bound: they move to move_keys_to, or stay unbound.
  let to: number | null = null
  if (req.query.move_keys_to !== undefined && req.query.move_keys_to !== '') {
    to = Number(req.query.move_keys_to)
    if (to === g.id || !groups.some((x) => x.id === to)) return invalid('move_keys_to', 'pick another existing group')
  }
  for (const k of keys) if (k.group_id === g.id) k.group_id = to
  groups.splice(groups.indexOf(g), 1)
  forgetGroup(g.id)
  return noContent()
})

// ------------------------------------------------------------------ users

export interface MockUser {
  id: number
  email: string
  display_name: string
  status: string
  roles: string[]
  balance: string
  /** The user above (CONTRACTS §103.3). */
  owner_id: number | null
  last_login_at: string | null
  created_at: string
  updated_at: string
}

export const users: MockUser[] = [
  { id: 1, email: 'admin@example.com', display_name: 'Admin', status: 'active', roles: ['super_admin'], balance: '12.34000000', owner_id: null, last_login_at: now(-60), created_at: now(-86400 * 90), updated_at: now(-86400) },
  { id: 2, email: 'ops@example.com', display_name: '张三', status: 'active', roles: ['operator', 'user'], balance: '85.50000000', owner_id: null, last_login_at: now(-3600 * 5), created_at: now(-86400 * 40), updated_at: now(-86400 * 3) },
  { id: 3, email: 'li.si@example.com', display_name: '李四', status: 'active', roles: ['operator'], balance: '3.21000000', owner_id: null, last_login_at: now(-86400 * 2), created_at: now(-86400 * 20), updated_at: now(-86400 * 2) },
  { id: 4, email: 'dev@acme.io', display_name: 'Acme Dev', status: 'active', roles: ['user'], balance: '250.00000000', owner_id: null, last_login_at: now(-600), created_at: now(-86400 * 10), updated_at: now(-86400) },
  { id: 5, email: 'old@example.com', display_name: 'Former user', status: 'disabled', roles: ['user'], balance: '0.00000000', owner_id: null, last_login_at: null, created_at: now(-86400 * 80), updated_at: now(-86400 * 30) },
  { id: agentUser.id, email: agentUser.email, display_name: agentUser.display_name, status: 'active', roles: ['supplier_agent'], balance: '0.00000000', owner_id: null, last_login_at: now(-1800), created_at: now(-86400 * 6), updated_at: now(-86400) },
  // The vendor and the first two generated users are the agent's team (CONTRACTS §103).
  { id: vendorUser.id, email: vendorUser.email, display_name: vendorUser.display_name, status: 'active', roles: ['vendor'], balance: '0.00000000', owner_id: agentUser.id, last_login_at: now(-7200), created_at: now(-86400 * 5), updated_at: now(-86400) },
  { id: readonlyUser.id, email: readonlyUser.email, display_name: readonlyUser.display_name, status: 'active', roles: ['readonly'], balance: '0.00000000', owner_id: null, last_login_at: now(-86400), created_at: now(-86400 * 5), updated_at: now(-86400) },
  { id: pricerUser.id, email: pricerUser.email, display_name: pricerUser.display_name, status: 'active', roles: ['pricer'], balance: '0.00000000', owner_id: null, last_login_at: now(-86400), created_at: now(-86400 * 5), updated_at: now(-86400) },
  { id: moderatorUser.id, email: moderatorUser.email, display_name: moderatorUser.display_name, status: 'active', roles: ['moderator'], balance: '0.00000000', owner_id: null, last_login_at: now(-3600), created_at: now(-86400 * 2), updated_at: now(-86400) }
]
for (let i = 0; i < 18; i++) {
  users.push({ id: 100 + i, email: `user${i + 1}@example.org`, display_name: `User ${i + 1}`, status: i % 7 === 6 ? 'disabled' : 'active', roles: ['user'], balance: (Math.random() * 50).toFixed(8), owner_id: i < 2 ? agentUser.id : null, last_login_at: i % 3 ? now(-3600 * i) : null, created_at: now(-86400 * (i + 1)), updated_at: now(-3600 * i) })
}

function findUser(req: MockRequest) {
  return users.find((u) => u.id === Number(req.params.id))
}

/**
 * The users below uid, the whole subtree (users.owner_id, CONTRACTS §103.2:
 * reads do not truncate, the server bounds the depth on write).
 */
function teamOf(uid: number): number[] {
  const out: number[] = []
  let frontier = [uid]
  while (frontier.length) {
    frontier = users.filter((u) => u.owner_id !== null && frontier.includes(u.owner_id) && !out.includes(u.id)).map((u) => u.id)
    out.push(...frontier)
  }
  return out
}
resolveTeams(teamOf)

/** The users a user action reaches (CONTRACTS §103.3): every user (null), the caller's team, or none (undefined). */
function userReach(who: Identity, action: string): number[] | null | undefined {
  if (hasPerm(who, action)) return null
  return hasPerm(who, `${action}:team`) ? teamOf(who.id) : undefined
}
const reaches = (reach: number[] | null, id: number) => reach === null || reach.includes(id)
const noUserAction = (action: string) => fail(403, 'permission_denied', `${action} is required`, { permission: action })

/**
 * The owner_id of POST /users or PATCH /users/:id for user `id` (0 when new):
 * a team holder names only itself; anyone else any user but the user itself
 * and those below it, or 0 for none. A response when refused.
 */
function ownerFor(who: Identity, reach: number[] | null, id: number, want: unknown): { owner: number | null } | ReturnType<typeof fail> {
  if (reach !== null) return want == null || Number(want) === who.id ? { owner: who.id } : fail(403, 'permission_denied', 'a team holder names only itself as the owner')
  const owner = Number(want) || null
  if (owner === null) return { owner }
  if (owner === id) return invalid('owner_id', 'a user cannot own itself', 'self')
  if (!users.some((u) => u.id === owner)) return invalid('owner_id', 'no such user', 'not_found')
  return teamOf(id).includes(owner) ? invalid('owner_id', 'the owner is below the user', 'cycle') : { owner }
}

/** A user as the API returns it: with its segment and its owner's email. */
export function userOut(u: MockUser) {
  return { ...u, owner_email: users.find((x) => x.id === u.owner_id)?.email ?? null, segment: segmentRef(segmentOfUser(u.id)) }
}

/** The ?segment= filter of the users list: a segment id, or "personal" for every personal segment. */
function inSegment(u: MockUser, want: string | undefined) {
  if (!want) return true
  const s = segmentOfUser(u.id)
  return want === 'personal' ? s.kind === 'personal' : s.id === Number(want)
}

/** ?owner_id=<id>|none: directly under that user, or under none; ?direct=true: directly under the caller. */
function ownerMatch(u: MockUser, q: Record<string, string>, who: Identity) {
  if (q.owner_id && u.owner_id !== (q.owner_id === 'none' ? null : Number(q.owner_id))) return false
  return q.direct !== 'true' || u.owner_id === who.id
}
on('GET', '/users', (req) => {
  const who = caller(req)
  const reach = userReach(who, 'user:read')
  if (reach === undefined) return noUserAction('user:read')
  const { q, status, role, segment } = req.query
  const items = users.filter((u) => reaches(reach, u.id) && match(q, u.email, u.display_name) && (!status || u.status === status) && (!role || u.roles.includes(role)) && inSegment(u, segment) && ownerMatch(u, req.query, who))
  return paginate(items.map(userOut), req.query)
})
on('GET', '/users/:id', (req) => {
  const reach = userReach(caller(req), 'user:read')
  if (reach === undefined) return noUserAction('user:read')
  const u = findUser(req)
  return u && reaches(reach, u.id) ? userOut(u) : fail(404, 'not_found', 'user not found')
})
on('POST', '/users', (req) => {
  const who = caller(req)
  const reach = userReach(who, 'user:create')
  if (reach === undefined) return noUserAction('user:create')
  const b = req.body || {}
  const roleKeys: string[] = Array.isArray(b.role_keys) && b.role_keys.length ? b.role_keys : ['user']
  // Roles other than the default are a role assignment, the user group a
  // segment assignment: each needs its key as well, checked before the body.
  if ((roleKeys.length > 1 || roleKeys[0] !== 'user') && !hasPerm(who, 'role:assign')) return noUserAction('role:assign')
  if (b.segment_id != null && !hasPerm(who, 'segment:manage')) return noUserAction('segment:manage')
  if (!/^[^@\s]+@[^@\s]+$/.test(b.email || '')) return invalid('email', 'invalid email address')
  if (String(b.password || '').length < 8) return invalid('password', 'at least 8 characters', 'too_short')
  if (Number(b.owner_id) < 0) return invalid('owner_id', 'must be a user id, or 0 for none')
  // segment_id: the named user group the user starts in; without it, the default one.
  const segment = b.segment_id == null ? null : findSegment(Number(b.segment_id))
  if (segment !== null && segment?.kind !== 'named') return invalid('segment_id', 'unknown user group')
  if (users.some((u) => u.email === b.email)) return fail(409, 'conflict', 'email already in use', { fields: [{ field: 'email', code: 'taken', message: 'email already in use' }] })
  const owner = ownerFor(who, reach, 0, b.owner_id)
  if (!('owner' in owner)) return owner
  // Every role must be one the caller assigns, but the default one (authz checkAssignment).
  const unknown = roleKeys.filter((k) => !roles.some((r) => r.key === k))
  if (unknown.length) return invalid('role_keys', `unknown roles: ${unknown.join(', ')}`, 'unknown')
  const refused = roleKeys.find((k) => k !== 'user' && !managesRole(who, k))
  if (refused) return fail(403, 'permission_denied', 'you cannot assign or revoke this role', { role: refused })
  const u: MockUser = { id: nextId(), email: b.email, display_name: b.display_name || '', status: 'active', roles: roleKeys, balance: '0.00000000', owner_id: owner.owner, last_login_at: null, created_at: now(), updated_at: now() }
  users.unshift(u)
  if (segment) moveUser(u.id, segment)
  return userOut(u)
})
on('PATCH', '/users/:id', (req) => {
  const who = caller(req)
  const reach = userReach(who, 'user:update')
  if (reach === undefined) return noUserAction('user:update')
  const u = findUser(req)
  if (!u || !reaches(reach, u.id)) return fail(404, 'not_found', 'user not found')
  const b = req.body || {}
  if (b.owner_id != null) {
    const owner = ownerFor(who, reach, u.id, b.owner_id)
    if (!('owner' in owner)) return owner
    u.owner_id = owner.owner
  }
  if (b.display_name !== undefined) u.display_name = b.display_name
  if (b.status !== undefined) u.status = b.status
  u.updated_at = now()
  return userOut(u)
})
on('DELETE', '/users/:id', stepUp('user.delete', (req) => {
  const u = findUser(req)
  if (!u) return fail(404, 'not_found', 'user not found')
  if (u.roles.includes('super_admin') && users.filter((x) => x.roles.includes('super_admin')).length <= 1) return fail(409, 'conflict', 'at least one super_admin must remain')
  users.splice(users.indexOf(u), 1)
  forgetUser(u.id)
  return noContent()
}))
// role:assign, on a user in reach of user:update; every role added or taken
// away must be one the caller manages (authz checkAssignment).
on('PUT', '/users/:id/roles', (req) => {
  const who = caller(req)
  if (!hasPerm(who, 'role:assign')) return noUserAction('role:assign')
  const reach = userReach(who, 'user:update')
  const u = findUser(req)
  if (!u || reach === undefined || !reaches(reach, u.id)) return fail(404, 'not_found', 'user not found')
  const keys: string[] = Array.isArray(req.body?.role_keys) ? req.body.role_keys : []
  const bad = keys.find((k) => !roles.some((r) => r.key === k))
  if (bad) return invalid('role_keys', `unknown role ${bad}`)
  const changed = [...keys.filter((k) => !u.roles.includes(k)), ...u.roles.filter((k) => !keys.includes(k))]
  const refused = changed.find((k) => !managesRole(who, k))
  if (refused) return fail(403, 'permission_denied', `${refused} is not a role you may assign`, { role: refused })
  u.roles = keys
  return userOut(u)
})
on('POST', '/users/:id/balance/adjust', (req) => {
  const u = findUser(req)
  if (!u) return fail(404, 'not_found', 'user not found')
  const amount = String(req.body?.amount ?? '')
  if (!/^\d+(\.\d{1,8})?$/.test(amount) || Number(amount) <= 0) return invalid('amount', 'amount must be a positive decimal')
  const delta = (req.body?.credit ? 1 : -1) * Number(amount)
  const next = Number(u.balance) + delta
  if (next < 0) return fail(402, 'insufficient_balance', 'balance would become negative')
  u.balance = next.toFixed(8)
  return { id: nextId(), user_id: u.id, delta: delta.toFixed(8), balance_after: u.balance, kind: 'admin_adjust', ref_type: 'admin', ref_id: '', note: req.body?.note || '', created_at: now() }
})

// ------------------------------------------------------------------ API keys

interface MockKey {
  allowed_ips?: string[]
  quota_limit?: string
  quota_used?: string
  rpm_limit?: number
  tpm_limit?: number
  max_concurrency?: number
  id: number
  user_id: number
  group_id: number | null
  name: string
  key_prefix: string
  status: string
  expires_at: string | null
  last_used_at: string | null
  created_at: string
}

export const ME_ID = 1
export const keys: MockKey[] = [
  { id: 1, user_id: 1, group_id: 1, name: 'laptop', key_prefix: 'sk-s2a-a1b2c', status: 'active', expires_at: null, last_used_at: now(-42), created_at: now(-86400 * 30) },
  { id: 2, user_id: 1, group_id: 2, rpm_limit: 60, tpm_limit: 200000, max_concurrency: 2, quota_limit: '20', quota_used: '17.3', name: 'ci-runner', key_prefix: 'sk-s2a-c3d4e', status: 'active', expires_at: now(86400 * 60), last_used_at: now(-3600 * 3), created_at: now(-86400 * 12) },
  { id: 3, user_id: 1, group_id: 1, name: 'old-script', key_prefix: 'sk-s2a-e5f6g', status: 'active', expires_at: now(-86400), last_used_at: now(-86400 * 3), created_at: now(-86400 * 50) },
  { id: 4, user_id: 2, group_id: 1, name: 'ops-tools', key_prefix: 'sk-s2a-g7h8i', status: 'active', expires_at: null, last_used_at: now(-300), created_at: now(-86400 * 8) },
  { id: 5, user_id: 4, group_id: 2, name: 'acme-prod', key_prefix: 'sk-s2a-i9j0k', status: 'disabled', expires_at: null, last_used_at: now(-86400 * 4), created_at: now(-86400 * 9) },
  // Its group was deleted: the key is kept, unbound.
  { id: 6, user_id: 1, group_id: null, name: 'orphan', key_prefix: 'sk-s2a-k1l2m', status: 'active', expires_at: null, last_used_at: now(-86400 * 2), created_at: now(-86400 * 20) },
  // Its group has no accounts yet: it serves no platform.
  { id: 7, user_id: 1, group_id: 4, name: 'split-test-relay', key_prefix: 'sk-s2a-A9p0q', status: 'active', expires_at: null, last_used_at: null, created_at: now(-86400 * 2) },
  { id: 8, user_id: 1, group_id: 2, quota_limit: '50', quota_used: '50', allowed_ips: ['10.0.0.0/8', '203.0.113.7'], name: 'office-gateway', key_prefix: 'sk-s2a-m3n4o', status: 'active', expires_at: now(86400 * 7), last_used_at: now(-600), created_at: now(-86400 * 15) },
  { id: 9, user_id: 1, group_id: 1, name: 'paused-bot', key_prefix: 'sk-s2a-p5q6r', status: 'disabled', expires_at: null, last_used_at: now(-86400 * 6), created_at: now(-86400 * 40) }
]

const LIMIT_FIELDS = ['rpm_limit', 'tpm_limit', 'max_concurrency'] as const

/** Mock live use: a steady share of each cap (ci-runner close to its RPM). */
function liveOf(k: MockKey) {
  return { rpm: k.rpm_limit ? Math.round(k.rpm_limit * 0.85) : k.id * 3, tpm: k.tpm_limit ? Math.round(k.tpm_limit * 0.4) : k.id * 1200, concurrency: k.max_concurrency ? 1 : 0 }
}

/** How the key's group serves it, as the server reports group_status. */
function keyGroupStatus(k: MockKey) {
  const g = groups.find((x) => x.id === k.group_id)
  if (!g) return 'deleted'
  if (g.status !== 'active') return 'disabled'
  return accessOf(segmentOfUser(k.user_id), g).accessible ? 'active' : 'unavailable'
}

/** How much of a key the server keeps readable (key_prefix). */
const KEY_PREFIX_LENGTH = 12
/** Keys kept only as a hash (old-script stands for one): they cannot be revealed until regenerated. */
const legacyKeyIds = new Set([3])
/** The plaintext of the keys issued in this session; a seeded key's is made up from its prefix. */
const issuedKeys = new Map<number, string>()
const plaintextOf = (k: MockKey) => issuedKeys.get(k.id) ?? k.key_prefix + 'X'.repeat(47 - k.key_prefix.length)

/** Gives the key a new secret (on create and on rotate) and returns its plaintext. */
function issueKey(k: MockKey) {
  const token = 'sk-s2a-' + randomToken(40)
  issuedKeys.set(k.id, token)
  legacyKeyIds.delete(k.id)
  k.key_prefix = token.slice(0, KEY_PREFIX_LENGTH)
  return token
}

function keyOut(k: MockKey) {
  const g = groups.find((x) => x.id === k.group_id)
  return { copyable: !legacyKeyIds.has(k.id), rpm_limit: 0, tpm_limit: 0, max_concurrency: 0, quota_limit: '0', quota_used: k.id === 1 ? '3.2' : '0', ...k, group_id: g ? g.id : null, live: liveOf(k), user_email: users.find((u) => u.id === k.user_id)?.email, group_name: g?.name ?? '', group_status: keyGroupStatus(k), platforms: g ? groupPlatforms(g.id) : [] }
}

function randomToken(n: number) {
  const abc = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
  let s = ''
  for (let i = 0; i < n; i++) s += abc[Math.floor(Math.random() * abc.length)]
  return s
}

/** The active groups the segment of a user may use, each with the rate that user gets in it. */
export function myGroups(userId: number) {
  const s = segmentOfUser(userId)
  return groups
    .filter((g) => g.status === 'active' && accessOf(s, g).accessible)
    .map((g) => {
      const rate = rateOf(s, g)
      return { ...groupOut(g), effective_rate: rate.value, rate_source: rate.source, exclusive: g.visibility === 'restricted' }
    })
}

/** A row of GET /me/groups: the fields the server's group.MyGroup has, no more; served_protocols without the account counts. */
function myGroupOut(g: ReturnType<typeof myGroups>[number]) {
  const { id, name, description, rate_multiplier, model_allowlist, model_filter_mode, platforms, effective_rate, rate_source, exclusive } = g
  const served_protocols = groupServed(id).map(({ protocol }) => ({ protocol }))
  return { id, name, description, rate_multiplier, model_allowlist, model_filter_mode, platforms, served_protocols, effective_rate, rate_source, exclusive }
}
on('GET', '/me/groups', (req) => myGroups(caller(req).id).map(myGroupOut))
/** A key under the status filter: one that is switched off is "disabled" even past its expiry. */
function keyListStatus(k: MockKey) {
  if (k.status !== 'active') return 'disabled'
  return k.expires_at && new Date(k.expires_at).getTime() <= Date.now() ? 'expired' : 'active'
}

/**
 * The list filters of GET /me/api-keys (CONTRACTS §92.1), ignoring case: q is
 * in the name, or starts the key prefix when it starts as a key and says more
 * than "sk-s2a-", which every key starts with (a whole key is compared by
 * its prefix); status and group_id match exactly.
 */
function keyMatches(k: MockKey, q: Record<string, string>) {
  const text = String(q.q || '').trim().toLowerCase()
  const prefix = text.startsWith('sk-s2a-') && text.length > 'sk-s2a-'.length ? text.slice(0, KEY_PREFIX_LENGTH) : ''
  if (text && !k.name.toLowerCase().includes(text) && !(prefix && k.key_prefix.toLowerCase().startsWith(prefix))) return false
  if (q.status && q.status !== keyListStatus(k)) return false
  return !q.group_id || k.group_id === Number(q.group_id)
}
on('GET', '/me/api-keys', (req) => paginate(keys.filter((k) => k.user_id === ME_ID && keyMatches(k, req.query)).map(keyOut), req.query))
on('POST', '/me/api-keys', (req) => {
  const b = req.body || {}
  if (!String(b.name || '').trim()) return invalid('name', 'name is required')
  if (!myGroups(caller(req).id).some((g) => g.id === Number(b.group_id))) return invalid('group_id', 'group not available')
  for (const f of LIMIT_FIELDS) if (b[f] !== undefined && !(Number(b[f]) >= 0)) return invalid(f, 'must be between 0 (unlimited) and the maximum')
  const k: MockKey = { id: nextId(), user_id: ME_ID, group_id: Number(b.group_id), rpm_limit: Number(b.rpm_limit) || 0, tpm_limit: Number(b.tpm_limit) || 0, max_concurrency: Number(b.max_concurrency) || 0, quota_limit: String(Number(b.quota_limit) || 0), quota_used: '0', allowed_ips: b.allowed_ips || [], name: b.name, key_prefix: '', status: 'active', expires_at: b.expires_at || null, last_used_at: null, created_at: now() }
  const key = issueKey(k)
  keys.unshift(k)
  return { ...keyOut(k), key }
})
on('PATCH', '/me/api-keys/:id', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  const b = req.body || {}
  if (b.status === undefined && b.group_id === undefined && LIMIT_FIELDS.every((f) => b[f] === undefined) && b.quota_limit === undefined && !b.reset_quota && b.name === undefined && b.expires_at === undefined && b.allowed_ips === undefined) return fail(400, 'invalid_argument', 'nothing to update')
  for (const f of LIMIT_FIELDS) if (b[f] !== undefined && !(Number(b[f]) >= 0)) return invalid(f, 'must be between 0 (unlimited) and the maximum')
  if (b.status !== undefined && b.status !== 'active' && b.status !== 'disabled') return invalid('status', 'status must be active or disabled')
  if (b.group_id !== undefined && !myGroups(caller(req).id).some((g) => g.id === Number(b.group_id))) return invalid('group_id', 'group not available')
  if (b.status !== undefined) k.status = b.status
  if (b.group_id !== undefined) k.group_id = Number(b.group_id)
  for (const f of LIMIT_FIELDS) if (b[f] !== undefined) k[f] = Number(b[f])
  if (b.quota_limit !== undefined) {
    if (!(Number(b.quota_limit) >= 0)) return invalid('quota_limit', 'must be between 0 (unlimited) and 1000000000 USD')
    k.quota_limit = String(Number(b.quota_limit))
  }
  if (b.reset_quota) k.quota_used = '0'
  if (b.name !== undefined) {
    if (!String(b.name).trim()) return invalid('name', 'name must be 1-100 characters')
    k.name = String(b.name).trim()
  }
  if (b.expires_at !== undefined) k.expires_at = b.expires_at
  if (b.allowed_ips !== undefined) k.allowed_ips = b.allowed_ips
  return keyOut(k)
})
on('POST', '/me/api-keys/:id/reveal', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  if (legacyKeyIds.has(k.id)) return fail(409, 'conflict', 'This legacy key cannot be recovered. Create a new key to enable copying.')
  return { key: plaintextOf(k) }
})
on('POST', '/me/api-keys/:id/rotate', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  const key = issueKey(k)
  return { ...keyOut(k), key }
})
on('DELETE', '/me/api-keys/:id', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  keys.splice(keys.indexOf(k), 1)
  return noContent()
})

// ------------------------------------------------------------------ proxies

interface MockProxy {
  id: number
  name: string
  protocol: 'http' | 'https' | 'socks5'
  host: string
  port: number
  username: string
  password: string
  status: string
  /** Owner (CONTRACTS §21.2); null for rows created before ownership existed. */
  created_by: number | null
  created_at: string
}

export const proxies: MockProxy[] = [
  { id: 1, name: 'us-west-1', protocol: 'http', host: '10.0.1.20', port: 3128, username: '', password: '', status: 'active', created_by: 1, created_at: now(-86400 * 40) },
  { id: 2, name: 'jp-residential', protocol: 'socks5', host: 'jp.proxy.example.net', port: 1080, username: 'acme', password: 's3cret', status: 'active', created_by: vendorUser.id, created_at: now(-86400 * 15) },
  { id: 3, name: 'eu-backup', protocol: 'https', host: 'eu.proxy.example.net', port: 443, username: 'backup', password: 'pw', status: 'disabled', created_by: null, created_at: now(-86400 * 3) }
]

// CONTRACTS §15.4: the password is never returned, only has_password.
function proxyOut(p: MockProxy) {
  const { password, ...rest } = p
  return { ...rest, has_password: !!password, created_by_email: userEmail(p.created_by), account_count: proxyAccountCount(p.id) }
}

function validateProxy(b: any, partial: boolean) {
  if ((!partial || b.name !== undefined) && !String(b.name || '').trim()) return invalid('name', 'name is required')
  if ((!partial || b.protocol !== undefined) && !['http', 'https', 'socks5'].includes(b.protocol)) return invalid('protocol', 'invalid protocol')
  if ((!partial || b.host !== undefined) && !String(b.host || '').trim()) return invalid('host', 'host is required')
  if ((!partial || b.port !== undefined) && !(Number(b.port) >= 1 && Number(b.port) <= 65535)) return invalid('port', 'port out of range')
  return null
}

/** The caller's reach for a proxy action: proxy:read or proxy:manage at any scope. */
function proxyScope(req: MockRequest, action: 'read' | 'manage') {
  const who = caller(req)
  return { who, scope: ownerScope(who, `proxy:${action}`) }
}

/** The proxy of :id within the caller's manage scope: 403 without proxy:manage at any scope, 404 when missing or out of it. */
function managedProxy(req: MockRequest): { p: MockProxy } | { __status: number; body: unknown } {
  const { who, scope } = proxyScope(req, 'manage')
  if (!scope) return fail(403, 'permission_denied', 'proxy:manage is required', { permission: 'proxy:manage' })
  const p = proxies.find((x) => x.id === Number(req.params.id))
  return p && inScope(scope, who, p) ? { p } : fail(404, 'not_found', 'proxy not found')
}

// CONTRACTS §21.4: proxy.ParseURL. scheme://user:pass@host:port, scheme
// http / https / socks5 / socks5h (-> socks5); no path, query or fragment.
interface ProxySpec {
  protocol: MockProxy['protocol']
  host: string
  port: number
  username: string
  password: string
}
const PROXY_SCHEMES: Record<string, MockProxy['protocol']> = { http: 'http', https: 'https', socks5: 'socks5', socks5h: 'socks5' }

function parseProxyURL(raw: string): ProxySpec | null {
  const s = String(raw).trim()
  const m = /^([a-z0-9+.-]+):\/\//i.exec(s)
  const protocol = m ? PROXY_SCHEMES[m[1].toLowerCase()] : undefined
  if (!m || !protocol) return null
  let u: URL
  try {
    u = new URL('proxy-url://' + s.slice(m[0].length))
  } catch {
    return null
  }
  if ((u.pathname && u.pathname !== '/') || u.search || u.hash) return null
  const host = u.hostname.replace(/^\[(.*)\]$/, '$1').toLowerCase()
  const port = Number(u.port)
  if (!host || !u.port || !Number.isInteger(port) || port < 1 || port > 65535) return null
  const dec = (x: string) => {
    try {
      return decodeURIComponent(x)
    } catch {
      return x
    }
  }
  return { protocol, host, port, username: dec(u.username), password: dec(u.password) }
}

/** 400 invalid on a bad proxy_url (the message never echoes the string: it may hold a password); null when fine. */
export function validateProxyURL(raw: string) {
  return parseProxyURL(raw) ? null : fail(400, 'invalid_argument', 'invalid proxy_url', { fields: [{ field: 'proxy_url', code: 'invalid', message: 'Expected scheme://user:pass@host:port with http, https, socks5 or socks5h' }] })
}

/**
 * CONTRACTS §21.4 find-or-create for POST/PATCH /accounts proxy_url: needs
 * proxy:manage at any scope, and its own scope or more to create one; matches protocol/host/port/username/
 * password among the proxies the caller can see (skipping disabled ones) or
 * creates one owned by the caller.
 */
export function resolveProxyURL(req: MockRequest, raw: string): { id: number; created: boolean } | { __status: number; body: unknown } {
  const who = caller(req)
  const reach = ownerScope(who, 'proxy:manage')
  if (!reach) return fail(403, 'permission_denied', 'proxy:manage is required to attach a proxy by URL', { permission: 'proxy:manage:own' })
  const spec = parseProxyURL(raw)
  if (!spec) return validateProxyURL(raw)!
  const hit = proxies
    .filter((p) => inScope(reach, who, p) && p.status !== 'disabled')
    .filter((p) => p.protocol === spec.protocol && p.host.toLowerCase() === spec.host && p.port === spec.port && p.username === spec.username && p.password === spec.password)
    .sort((a, b) => a.id - b.id)[0]
  if (hit) return { id: hit.id, created: false }
  // Creating one takes the own scope or more: proxy/<id> reaches existing proxies only.
  if (!hasPerm(who, 'proxy:manage:own')) {
    return fail(403, 'permission_denied', 'creating a proxy requires proxy:manage for every proxy or your own', { permission: 'proxy:manage:own' })
  }
  const p: MockProxy = {
    id: nextId(),
    name: `${spec.protocol}://${spec.host}:${spec.port}`.slice(0, 100),
    protocol: spec.protocol,
    host: spec.host,
    port: spec.port,
    username: spec.username,
    password: spec.password,
    status: 'active',
    created_by: who.id,
    created_at: now()
  }
  proxies.push(p)
  return { id: p.id, created: true }
}

on('GET', '/proxies', (req) => {
  const { who, scope } = proxyScope(req, 'read')
  return paginate(filterOwned(proxies, scope, who, req.query).map(proxyOut), req.query)
})
on('GET', '/proxies/:id', (req) => {
  const { who, scope } = proxyScope(req, 'read')
  const p = proxies.find((x) => x.id === Number(req.params.id))
  return p && inScope(scope, who, p) ? proxyOut(p) : fail(404, 'not_found', 'proxy not found')
})
on('POST', '/proxies', (req) => {
  const b = req.body || {}
  // proxy/<id> scopes reach existing proxies only (CONTRACTS §21).
  if (!hasPerm(caller(req), 'proxy:manage:own')) {
    return fail(403, 'permission_denied', 'creating a proxy requires proxy:manage for every proxy or your own', { permission: 'proxy:manage:own' })
  }
  const err = validateProxy(b, false)
  if (err) return err
  const p: MockProxy = { id: nextId(), name: b.name, protocol: b.protocol, host: b.host, port: Number(b.port), username: b.username || '', password: b.password || '', status: 'active', created_by: caller(req).id, created_at: now() }
  proxies.push(p)
  return proxyOut(p)
})
on('PATCH', '/proxies/:id', (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  const b = req.body || {}
  const err = validateProxy(b, true)
  if (err) return err
  for (const k of ['name', 'protocol', 'host', 'username', 'status'] as const) if (b[k] !== undefined) (p as any)[k] = b[k]
  if (b.port !== undefined) p.port = Number(b.port)
  // No mask convention: omitted / null keeps, "" clears, any other string is the new password.
  if (b.password !== undefined && b.password !== null) p.password = String(b.password)
  return proxyOut(p)
})
on('DELETE', '/proxies/:id', (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  // Still referenced: 409 with the count over every account (§21.2), whoever owns them.
  const n = proxyAccountCount(p.id)
  if (n) return fail(409, 'conflict', 'proxy is used by accounts', { account_count: n })
  proxies.splice(proxies.indexOf(p), 1)
  return noContent()
})
on('POST', '/proxies/:id/reveal-url', stepUp('proxy.reveal', (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  const host = p.host.includes(':') ? `[${p.host}]` : p.host
  const cred = p.username || p.password ? `${encodeURIComponent(p.username)}:${encodeURIComponent(p.password)}@` : ''
  return { url: `${p.protocol}://${cred}${host}:${p.port}` }
}))
on('POST', '/proxies/:id/test', async (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  await new Promise((r) => setTimeout(r, 400 + Math.random() * 600))
  if (p.status !== 'active' || p.host.startsWith('eu.')) return { ok: false, latency_ms: 5000, message: 'dial tcp: i/o timeout' }
  return { ok: true, latency_ms: Math.round(80 + Math.random() * 300), ip: p.host.startsWith('jp') ? '203.0.113.' + Math.floor(Math.random() * 250) : '198.51.100.7', message: 'HTTP 200 from https://api.ipify.org' }
})

// ------------------------------------------------------------------ nodes

const bootA = 'b7f1c2d4-8e9a-4b0c-a1d2-3e4f5a6b7c8d'
const bootB = 'c0ffee00-1234-4abc-9def-0123456789ab'
const startedA = now(-86400 * 2 - 3600 * 5)
const startedB = now(-3600 * 7)

on('GET', '/nodes', () => [
  {
    node_id: 'node-1',
    boot_id: bootA,
    addr: '10.0.0.11:8080',
    host_version: '0.1.0',
    started_at: startedA,
    last_heartbeat: now(-Math.floor(Math.random() * 4)),
    plugins: {
      anthropic: JSON.stringify({ serving: '0.2.1', state: 'active', instances: [{ version: '0.2.1', state: 'ready', restarts: 0 }] }),
      guard: { serving: '0.1.0', state: 'active', instances: [{ version: '0.1.0', state: 'ready', restarts: 0 }] }
    }
  },
  {
    node_id: 'node-2',
    boot_id: bootB,
    addr: '10.0.0.12:8080',
    host_version: '0.1.0',
    started_at: startedB,
    last_heartbeat: now(-Math.floor(Math.random() * 4)),
    plugins: {
      anthropic: JSON.stringify({ serving: '0.2.0', standby: '0.2.1', fallback: '0.2.0', state: 'pending', instances: [{ version: '0.2.0', state: 'ready', restarts: 1 }] }),
      guard: JSON.stringify({ serving: '0.1.0', standby: '0.2.0', state: 'pending' })
    }
  },
  {
    node_id: 'node-3',
    boot_id: 'dead0000-0000-4000-8000-000000000003',
    addr: '10.0.0.13:8080',
    host_version: '0.0.9',
    started_at: now(-86400 * 9),
    last_heartbeat: now(-95),
    plugins: {
      anthropic: JSON.stringify({ serving: '0.2.0', fallback: '0.2.0', state: 'failed', error: 'health check timeout' })
    }
  }
])

// ------------------------------------------------------------------ publishers

interface MockPublisherKey {
  key_id: string
  public_key: string
  status: string
  not_before: string | null
  not_after: string | null
  created_at: string
}
interface MockPublisher {
  id: number
  name: string
  trust_level: string
  status: string
  created_at: string
  revoked_at: string | null
  keys: MockPublisherKey[]
}

const publishers: MockPublisher[] = [
  { id: 1, name: 'sub2api', trust_level: 'official', status: 'active', created_at: now(-86400 * 120), revoked_at: null, keys: [
    { key_id: 'sub2api-2026', public_key: 'Hn0l9pJ3vWqg1H1Zb7xg6Yx0cF5m3r2Lk8QwTzA4uYs=', status: 'active', not_before: now(-86400 * 120), not_after: now(86400 * 365), created_at: now(-86400 * 120) },
    { key_id: 'sub2api-2025', public_key: 'q8J2k5L9m1N4b7V0c3X6z9A2s5D8f1G4h7J0k3L6m9Q=', status: 'revoked', not_before: now(-86400 * 500), not_after: now(-86400 * 100), created_at: now(-86400 * 500) }
  ] },
  { id: 2, name: 'acme', trust_level: 'verified', status: 'active', created_at: now(-86400 * 30), revoked_at: null, keys: [
    { key_id: 'acme-1', public_key: 'Zm9vYmFyYmF6cXV4cXV1eGNvcmdlZ3JhdWx0Z2FycGw=', status: 'active', not_before: null, not_after: null, created_at: now(-86400 * 30) }
  ] },
  { id: 3, name: 'shady-plugins', trust_level: 'community', status: 'revoked', created_at: now(-86400 * 60), revoked_at: now(-86400 * 10), keys: [] }
]

function b64Len(s: string) {
  try {
    return Buffer.from(s, 'base64').length
  } catch {
    return 0
  }
}

on('GET', '/publishers', (req) => paginate(publishers, req.query))
on('POST', '/publishers', stepUp('publisher.manage', (req) => {
  const b = req.body || {}
  if (!String(b.name || '').trim()) return invalid('name', 'name is required')
  if (!['official', 'verified', 'community'].includes(b.trust_level)) return invalid('trust_level', 'invalid trust level')
  if (publishers.some((p) => p.name === b.name)) return fail(409, 'conflict', 'publisher already exists')
  const p: MockPublisher = { id: nextId(), name: b.name, trust_level: b.trust_level, status: 'active', created_at: now(), revoked_at: null, keys: [] }
  publishers.push(p)
  return p
}))
on('POST', '/publishers/:id/keys', stepUp('publisher.manage', (req) => {
  const p = publishers.find((x) => x.id === Number(req.params.id))
  if (!p) return fail(404, 'not_found', 'publisher not found')
  const b = req.body || {}
  if (!String(b.key_id || '').trim()) return invalid('key_id', 'key_id is required')
  if (publishers.some((x) => x.keys.some((k) => k.key_id === b.key_id))) return invalid('key_id', 'key_id already exists')
  if (b64Len(String(b.public_key || '')) !== 32) return invalid('public_key', 'public key must be 32 bytes of base64')
  const k: MockPublisherKey = { key_id: b.key_id, public_key: b.public_key, status: 'active', not_before: b.not_before || null, not_after: b.not_after || null, created_at: now() }
  p.keys.push(k)
  return k
}))
on('POST', '/publishers/:id/revoke', stepUp('publisher.manage', (req) => {
  const p = publishers.find((x) => x.id === Number(req.params.id))
  if (!p) return fail(404, 'not_found', 'publisher not found')
  p.status = 'revoked'
  p.revoked_at = now()
  p.keys.forEach((k) => (k.status = 'revoked'))
  return p
}))
on('POST', '/publisher-keys/:key_id/revoke', stepUp('publisher.manage', (req) => {
  for (const p of publishers) {
    const k = p.keys.find((x) => x.key_id === req.params.key_id)
    if (k) {
      k.status = 'revoked'
      return k
    }
  }
  return fail(404, 'not_found', 'key not found')
}))
