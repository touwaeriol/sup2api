// Mock permission catalog (GET /permissions): the core modules and two plugin
// modules with their keys, the scopes a key may take, and the rows of plugin
// resources the scope pickers offer.
import { SCOPABLE, parsePermission } from '../src/authz/implies'
import { fail, on } from './router'
import { SUPER_ONLY_KEYS, caller, hasPerm } from './core'
import { match } from './common'

export type L = { en: string; zh: string }
export const L = (en: string, zh: string): L => ({ en, zh })

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
export interface ModuleDef {
  module: string
  label: L
  source: 'core' | 'plugin'
  plugin_key?: string | null
  status: string
  groups?: Array<{ key: string; label: L; sort: number }>
  permissions: PermDef[]
}

export const MODULES: ModuleDef[] = [
  { module: 'user', label: L('Users', '用户'), source: 'core', status: 'active', permissions: [
    { key: 'user:read', label: L('View', '查看') }, { key: 'user:create', label: L('Create', '新建') },
    { key: 'user:update', label: L('Edit', '编辑') }, { key: 'user:delete', label: L('Delete', '删除'), risk: 'sensitive' },
    { key: 'user:password:reset', label: L('Reset passwords and 2FA', '重置密码与两步验证'), risk: 'sensitive' }] },
  { module: 'segment', label: L('User groups', '用户分组'), source: 'core', status: 'active', permissions: [
    { key: 'segment:read', label: L('View', '查看') }, { key: 'segment:manage', label: L('Manage', '管理') }] },
  { module: 'role', label: L('Roles', '角色'), source: 'core', status: 'active', permissions: [
    { key: 'role:read', label: L('View', '查看') }, { key: 'role:define', label: L('Define', '定义'), risk: 'sensitive' },
    { key: 'role:assign', label: L('Assign', '分配'), risk: 'sensitive' }] },
  { module: 'apikey', label: L('API keys', 'API Key'), source: 'core', status: 'active', permissions: [
    { key: 'apikey:self:manage', label: L('Manage own', '管理自己的') }] },
  { module: 'group', label: L('Account groups', '账号分组'), source: 'core', status: 'active', permissions: [
    { key: 'group:read', label: L('View', '查看') }, { key: 'group:manage', label: L('Manage', '管理') }] },
  { module: 'account', label: L('Accounts', '账号'), source: 'core', status: 'active', permissions: [
    { key: 'account:read', label: L('View', '查看') }, { key: 'account:create', label: L('Create', '新建') },
    { key: 'account:update', label: L('Edit', '编辑') }, { key: 'account:delete', label: L('Delete', '删除'), risk: 'sensitive' },
    { key: 'account:test', label: L('Test', '测试') }, { key: 'account:credential:view', label: L('View credentials', '查看凭证'), risk: 'sensitive' },
    { key: 'account:settings:custom', label: L('Custom restricted settings', '自定义受限设置') },
    { key: 'account:group:bind', label: L('Bind accounts to account groups (own level)', '将账号绑定到账号分组（own 级）') },
    { key: 'account:relay', label: L('Create relay-type accounts with custom upstream', '创建自定义上游的 relay 类账号'), risk: 'sensitive' }] },
  { module: 'proxy', label: L('Proxies', '代理'), source: 'core', status: 'active', permissions: [
    { key: 'proxy:read', label: L('View', '查看') }, { key: 'proxy:manage', label: L('Manage', '管理') }] },
  { module: 'price', label: L('Prices', '价格'), source: 'core', status: 'active', permissions: [
    { key: 'price:read', label: L('View', '查看') }, { key: 'price:manage', label: L('Manage', '管理') }] },
  { module: 'balance', label: L('Balance', '余额'), source: 'core', status: 'active', permissions: [
    { key: 'balance:self:read', label: L('View own', '查看自己的') }, { key: 'balance:all:read', label: L('View all', '查看全部') },
    { key: 'balance:adjust', label: L('Adjust plain users', '调整普通用户'), risk: 'sensitive' },
    { key: 'balance:adjust:any', label: L('Adjust any managed user', '调整任意可管理用户'), risk: 'sensitive' }] },
  { module: 'redeem', label: L('Redeem codes', '兑换码'), source: 'core', status: 'active', permissions: [
    { key: 'redeem:use', label: L('Redeem a code', '使用兑换码') },
    { key: 'redeem:manage', label: L('Generate and void codes', '生成与作废兑换码'), risk: 'sensitive' }] },
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

export function moduleOut(m: ModuleDef) {
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
export const permDef = (key: string) => {
  const action = parsePermission(key)?.action
  return MODULES.flatMap((m) => m.permissions).find((p) => p.key === action)
}

/**
 * Whether the scope of a valid key is one its permission accepts (authz
 * pluginScope.fits): a plugin key only the resource, own and enum values its
 * manifest declares; core scopes are checked by the parser already.
 */
export function scopeFits(key: string): boolean {
  const p = parsePermission(key)
  const def = permDef(key)
  if (!p || !def) return false
  if (!key.startsWith('plugin.') || p.scope.all) return true
  if (p.scope.own) return !!def.scope?.own
  const [res, value] = (p.scope.ref ?? '').split('/')
  if (!def.scope || res !== def.scope.resource) return false
  return !def.scope.values || def.scope.values.some((v) => v.key === value)
}

export const pluginModule = (plugin: string) => MODULES.find((m) => m.source === 'plugin' && m.plugin_key === plugin)

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
