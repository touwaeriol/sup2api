// Mock roles: the built-in and custom roles, their grants and the roles each
// may assign, a plugin's permissions × roles matrix, and the authz version.
import { canonicalPermission, parsePermission, permissionIndex } from '../src/authz/implies'
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from './router'
import { ALL_PERMISSIONS, MANAGER_PLUGIN_KEYS, SUPER_ONLY_KEYS, SUPPLIER_AGENT_KEYS, SUPPLIER_KEYS, USER_KEYS, caller, moderatorUser, pricerUser, readonlyUser, stepUpRequired, vendorUser, type Identity } from './core'
import { invalid } from './common'
import { L, MODULES, moduleOut, permDef, pluginModule, scopeFits, type ModuleDef } from './permissions'
import { users } from './users'

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
export const roles: MockRole[] = [
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
  custom(10, 'group_ops', L('Account group operator', '账号分组运维'), 'Runs the accounts of account group 1', [
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

on('GET', '/permissions/plugins/:plugin', (req) => {
  const m = pluginModule(req.params.plugin)
  return m ? pluginRoleMatrix(req, m) : fail(404, 'not_found', 'plugin not found')
})
interface PluginRoleWrite {
  r: MockRole
  next: string[]
  critical: boolean
}
/**
 * The checked write of one role's keys of plugin module m (PUT
 * /permissions/plugins/:plugin), or the refusal. Only m's keys of the role
 * are touched: a removed key stays while kept and goes when left out; a new
 * one is dropped.
 */
function pluginRoleWrite(who: Identity, m: ModuleDef, key: string, sent: unknown, revision: unknown): PluginRoleWrite | ReturnType<typeof fail> {
  const r = roles.find((x) => x.key === key)
  if (!r) return invalid(`roles.${key}`, `unknown role ${key}`, 'unknown')
  if (r.superuser || !editableBy(who, r)) return fail(403, 'permission_denied', 'you cannot change this role', { role: key })
  const conflict = revisionConflict(r, revision)
  if (conflict) return conflict
  const prefix = `${m.module}:`
  const keys: string[] = Array.isArray(sent) ? sent : []
  const foreign = keys.find((k) => !k.startsWith(prefix))
  if (foreign) return invalid(`roles.${key}`, `${foreign} is not a permission of ${m.plugin_key}`, 'foreign')
  const bad = keys.find((k) => !scopeFits(k))
  if (bad) return invalid(`roles.${key}`, `invalid permission key ${bad}`)
  const held = r.permission_keys.filter((k) => k.startsWith(prefix))
  const next = canonicalSet(keys).filter((k) => held.includes(k) || !removedAction(k))
  const had = permissionIndex(held)
  const missing = next.filter((k) => !had.has(k) && !mayGrant(who, k))
  if (missing.length) return fail(403, 'permission_denied', 'you can only grant permissions you hold', { missing, role: key })
  return { r, next, critical: newCritical(held, next).length > 0 }
}

on('PUT', '/permissions/plugins/:plugin', (req) => {
  const m = pluginModule(req.params.plugin)
  if (!m) return fail(404, 'not_found', 'plugin not found')
  const who = caller(req)
  const revisions: Record<string, number> = req.body?.revisions ?? {}
  const body: Record<string, unknown> = req.body?.roles ?? {}
  // Every role is checked before any is written: one refusal leaves them all as they were.
  const writes: PluginRoleWrite[] = []
  for (const key of Object.keys(body).sort()) {
    const w = pluginRoleWrite(who, m, key, body[key], revisions[key])
    if (!('next' in w)) return w
    writes.push(w)
  }
  if (writes.some((w) => w.critical)) {
    const verify = stepUpRequired(req, 'plugin.grant')
    if (verify) return verify
  }
  for (const { r, next } of writes) {
    r.permission_keys = [...r.permission_keys.filter((k) => !k.startsWith(`${m.module}:`)), ...next].sort()
    revise(r)
  }
  if (writes.length) bumpAuthz()
  return pluginRoleMatrix(req, m)
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
