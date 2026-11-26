import { fail, noContent, now, on, type MockHandler, type MockRequest } from './router'
import { canonicalPermission, permissionIndex, rowOwner, scopeAllowsRow, type Scope, type ScopedRow } from '../src/authz/implies'

// auth, me, menus

export const ALL_PERMISSIONS = [
  'user:read', 'user:create', 'user:update', 'user:delete', 'user:password:reset',
  'segment:read', 'segment:manage',
  'role:read', 'role:define', 'role:assign',
  'apikey:self:manage',
  'group:read', 'group:manage',
  // CONTRACTS §21: the account and proxy actions take a scope (account:read:own, account:update:group/12, ...).
  'account:read', 'account:create', 'account:update', 'account:delete', 'account:test', 'account:credential:view',
  'account:settings:custom', 'account:group:bind', 'account:relay', 'account:owner:update',
  'proxy:read', 'proxy:manage',
  'price:read', 'price:manage',
  'balance:self:read', 'balance:all:read', 'balance:adjust', 'balance:adjust:any',
  'redeem:use', 'redeem:manage', 'topup:use', 'topup:order:read', 'topup:manage',
  'usage:self:read', 'usage:all:read', 'usage:account:read', 'usage:settle',
  'sticky:read', 'sticky:manage',
  'plugin:read', 'plugin:install', 'plugin:manage', 'plugin:uninstall', 'plugin:grant:high', 'plugin:grant:critical', 'plugin:grant:db_schema_unconfined', 'plugin:egress:read', 'plugin:market:read',
  'publisher:read', 'publisher:manage',
  'node:read', 'task:read', 'task:run', 'task:manage', 'gateway:use', 'settings:read', 'settings:manage',
  'audit:read', 'system:update:read', 'system:update:execute', 'system:update:recover',
  'plugin.guard:rules:read', 'plugin.guard:rules:manage', 'plugin.guard:stats:read',
  'plugin.dynweight:dynweight:read', 'plugin.dynweight:dynweight:manage',
  'plugin.codex:codex:manage', 'plugin.codex:codex:reset'
]

export interface Identity {
  id: number
  email: string
  display_name: string
  roles: string[]
  permissions: string[]
  superuser: boolean
}

// The built-in roles' defaults (authz/catalog.go): the user role's, the
// supplier's, and the supplier agent's (CONTRACTS §103.1): both of those and
// its team's.
export const USER_KEYS = ['apikey:self:manage', 'balance:self:read', 'redeem:use', 'topup:use', 'usage:self:read', 'gateway:use']
export const SUPPLIER_KEYS = [
  'account:read:own', 'account:create:own', 'account:update:own', 'account:delete:own', 'account:test:own', 'account:credential:view:own',
  'proxy:read:own', 'proxy:manage:own', 'usage:self:read', 'usage:account:read:own'
]
export const SUPPLIER_AGENT_KEYS = [...new Set([
  ...USER_KEYS, ...SUPPLIER_KEYS,
  'account:read:team', 'usage:account:read:team', 'user:read:team', 'user:create:team', 'user:update:team', 'role:read', 'role:assign'
])]

export const me: Identity = {
  id: 1,
  email: 'admin@example.com',
  display_name: 'Admin',
  roles: ['super_admin'],
  permissions: ALL_PERMISSIONS,
  superuser: true
}

/** Sign in as user@example.com (password admin) to get the built-in "user" role. */
const plainUser: Identity = {
  id: 1,
  email: 'user@example.com',
  display_name: 'User',
  roles: ['user'],
  permissions: USER_KEYS,
  superuser: false
}

/**
 * Sign in as vendor@example.com (password admin): the "vendor" role of
 * CONTRACTS §21 (the own scope of the account and proxy actions, no
 * settings:custom). Sees and edits only the accounts it owns and the
 * proxies it created, and reads those accounts' usage.
 */
export const vendorUser: Identity = {
  id: 6,
  email: 'vendor@example.com',
  display_name: 'Vendor',
  roles: ['vendor'],
  permissions: [
    'account:read:own', 'account:create:own', 'account:update:own', 'account:delete:own', 'account:test:own', 'account:credential:view:own',
    'proxy:read:own', 'proxy:manage:own', 'usage:account:read:own',
    'group:read', 'gateway:use'
  ],
  superuser: false
}

/**
 * Sign in as agent@example.com (password admin): the built-in supplier_agent
 * role (CONTRACTS §103.1), a user's and a supplier's keys plus its team's:
 * reads, creates, edits and assigns roles to the users below it
 * (vendor@example.com among them), reads their accounts and those accounts'
 * usage, never their credentials or proxies.
 */
export const agentUser: Identity = {
  id: 11,
  email: 'agent@example.com',
  display_name: 'Agent',
  roles: ['supplier_agent'],
  permissions: SUPPLIER_AGENT_KEYS,
  superuser: false
}

/** Sign in as readonly@example.com (password admin): sees every account and proxy, changes nothing, no credentials. */
export const readonlyUser: Identity = {
  id: 7,
  email: 'readonly@example.com',
  display_name: 'Read only',
  roles: ['readonly'],
  permissions: ['account:read', 'proxy:read', 'group:read', 'gateway:use'],
  superuser: false
}

/**
 * Sign in as moderator@example.com (password admin): runs the moderation
 * plugin and may look up no user, group or account, so its entity pickers are
 * the text boxes of a caller without GET /lookup.
 */
export const moderatorUser: Identity = {
  id: 8,
  email: 'moderator@example.com',
  display_name: 'Moderator',
  roles: ['moderator'],
  permissions: ['plugin.moderation:moderation:read', 'plugin.moderation:moderation:manage', 'gateway:use'],
  superuser: false
}

/**
 * Sign in as pricer@example.com (password admin): reads user groups, groups
 * and prices but not users, so a personal user group goes by "#<user id>",
 * its members cannot be listed and no user may be put in a user group
 * (CONTRACTS §97.6, §97.7).
 */
export const pricerUser: Identity = {
  id: 9,
  email: 'pricer@example.com',
  display_name: 'Pricer',
  roles: ['pricer'],
  permissions: ['segment:read', 'group:read', 'group:manage', 'price:read', 'gateway:use'],
  superuser: false
}

/** The core's super-only keys (authz/catalog.go): critical, and not among admin's defaults. */
export const SUPER_ONLY_KEYS = new Set(['balance:adjust:any', 'plugin:grant:critical', 'plugin:grant:db_schema_unconfined', 'publisher:manage', 'system:update:execute', 'system:update:recover'])

/**
 * The guard keys of the built-in admin role in the mock: rules:manage at the
 * own scope only, no alerts:mute or bypass. The role also holds the disabled
 * foo plugin's key, which /me leaves out and the role editor's grant view
 * keeps grantable.
 */
export const MANAGER_PLUGIN_KEYS = ['plugin.guard:rules:read', 'plugin.guard:rules:manage:own', 'plugin.guard:stats:read']

/**
 * Sign in as manager@example.com (password admin): the built-in admin role,
 * not the super admin. It holds no super-only key, manages the roles admin
 * may assign (operator, supplier, user and the custom ones) but not admin
 * itself, so an install's suggestions to admin go to review (D2), holds
 * rules:manage at the own scope only (no "All" to offer), and lacks the
 * guard's alerts:mute and bypass, which the role editor offers greyed out.
 */
export const managerUser: Identity = {
  id: 10,
  email: 'manager@example.com',
  display_name: 'Manager',
  roles: ['admin'],
  permissions: [...ALL_PERMISSIONS.filter((k) => !SUPER_ONLY_KEYS.has(k) && !k.startsWith('plugin.guard:')), ...MANAGER_PLUGIN_KEYS],
  superuser: false
}

const identities = [me, plainUser, vendorUser, agentUser, readonlyUser, moderatorUser, pricerUser, managerUser]

/** Email of a (possibly deleted) user id, for created_by_email. */
export function userEmail(id: number | null | undefined): string | null {
  if (id == null) return null
  return identities.find((u) => u.id === id)?.email ?? KNOWN_EMAILS[id] ?? null
}
const KNOWN_EMAILS: Record<number, string> = { 2: 'ops@example.com', 3: 'li.si@example.com', 5: 'old@example.com' }

/** One of the keys is held: implied by a held key (CONTRACTS §21, the console's implies.ts). */
export function hasPerm(who: Identity, key: string | string[]): boolean {
  if (who.superuser) return true
  const held = permissionIndex(who.permissions)
  return [key].flat().some((k) => held.has(k))
}

/** One of the scoped actions is held at any scope. */
export function hasAction(who: Identity, action: string | string[]): boolean {
  return who.superuser || [action].flat().some((a) => permissionIndex(who.permissions).hasAction(a))
}

/**
 * The reach of a caller for a scoped action (core.DataScope): `all` every
 * row, else `own` (the restricted tier: own, group and id scopes) with the
 * scopes held; null without the action.
 */
export interface Reach {
  level: 'all' | 'own'
  scopes: Scope[]
}
export function ownerScope(who: Identity, action: string): Reach | null {
  if (hasPerm(who, action)) return { level: 'all', scopes: [] }
  const scopes = permissionIndex(who.permissions).scopes(action)
  return scopes.length ? { level: 'own', scopes } : null
}

/**
 * The users below uid, the whole subtree (users.owner_id, CONTRACTS §103.2:
 * reads do not truncate, the server bounds the depth on write);
 * resources.ts, which holds the users, answers it.
 */
let teamOf: (uid: number) => number[] = () => []
export const resolveTeams = (resolve: (uid: number) => number[]) => {
  teamOf = resolve
}
/** The team a held scope reaches beyond the caller's own rows (the server's UserTree().Below subquery inside ScopeTable.Scope). */
const teamReaches = (s: Scope, who: Identity, row: ScopedRow) => !!s.team && teamOf(who.id).includes(rowOwner(row) ?? 0)

/** Whether `row` is in the caller's reach (core.DataScope.AllowsRow); null owners need the all level or an id or group scope. */
export function inScope(scope: Reach | null, who: Identity, row: ScopedRow): boolean {
  if (scope?.level === 'all') return true
  return !!scope && scope.scopes.some((s) => scopeAllowsRow(s, row, who.id) || teamReaches(s, who, row))
}

/**
 * Applies the ?mine=true, ?owner_id= and ?subordinates=true list filters
 * (the owner: an account's owner_id, a proxy's creator) and ?created_by=
 * (only at the all level).
 */
export function filterOwned<T extends ScopedRow>(rows: T[], scope: Reach | null, who: Identity, q: Record<string, string>): T[] {
  let out = rows.filter((r) => inScope(scope, who, r))
  if (q.mine === 'true') out = out.filter((r) => rowOwner(r) === who.id)
  if (q.owner_id) out = out.filter((r) => rowOwner(r) === Number(q.owner_id))
  if (q.subordinates === 'true') out = out.filter((r) => scope?.scopes.some((s) => teamReaches(s, who, r)))
  if (scope?.level === 'all' && q.created_by) out = out.filter((r) => r.created_by === Number(q.created_by))
  return out
}

// Sessions (CONTRACTS §100): a login is a session the server renews while it
// is used. Its token names it ("s2s_mock-<id>-..."), so a revoked session
// (logout) fails with 401 on the next request.
interface MockSession {
  revoked: boolean
  user: Identity
}
const sessions = new Map<string, MockSession>()
let sessionSeq = 0

function newSession(user: Identity = me) {
  const id = `m${++sessionSeq}`
  sessions.set(id, { revoked: false, user })
  return { access_token: `s2s_mock-${id}-${Date.now()}`, expires_in: 604800, user }
}

/** The session of a bearer token; undefined for one issued before a mock restart. */
function sessionOf(auth: string | undefined): MockSession | undefined {
  const m = /^Bearer\s+s2s_mock-(m\d+)-/i.exec(auth || '')
  return m ? sessions.get(m[1]) : undefined
}

/** The signed-in identity of a request (admin for tokens of unknown sessions). */
function whoAmI(auth: string | undefined): Identity {
  return sessionOf(auth)?.user || me
}

/** The signed-in identity of a mock request. */
export function caller(req: MockRequest): Identity {
  return whoAmI(req.headers.authorization)
}

/**
 * Checks the bearer token of a console request: a revoked session's token
 * is rejected; other tokens (issued before a mock restart) stay valid;
 * "expired" always fails.
 */
export function accessTokenValid(auth: string | undefined): boolean {
  if (!auth) return true
  if (auth.replace(/^Bearer\s+/i, '') === 'expired') return false
  return !sessionOf(auth)?.revoked
}

// Login rate limit (CONTRACTS §14.2, shortened for the mock): 5 failures for
// an email lock it for 30 seconds -> 429 rate_limited + retry_after_seconds.
const LOGIN_MAX_FAILURES = 5
const LOGIN_LOCK_SECONDS = 30
const loginFailures = new Map<string, { count: number; lockedUntil: number }>()

on('POST', '/auth/login', (req) => {
  const email = String(req.body?.email || '').toLowerCase()
  const st = loginFailures.get(email)
  if (st && st.lockedUntil > Date.now()) {
    const retry = Math.ceil((st.lockedUntil - Date.now()) / 1000)
    return fail(429, 'rate_limited', 'too many failed sign-in attempts', { retry_after_seconds: retry })
  }
  if (!email || req.body?.password !== 'admin') {
    const s = st && st.lockedUntil <= Date.now() && st.count >= LOGIN_MAX_FAILURES ? { count: 0, lockedUntil: 0 } : st || { count: 0, lockedUntil: 0 }
    s.count++
    if (s.count >= LOGIN_MAX_FAILURES) s.lockedUntil = Date.now() + LOGIN_LOCK_SECONDS * 1000
    loginFailures.set(email, s)
    return fail(401, 'unauthenticated', 'invalid email or password (mock password: admin)')
  }
  loginFailures.delete(email)
  return newSession(identities.find((u) => u.email === email) || me)
})
on('POST', '/auth/logout', (req) => {
  const ended = sessionOf(req.headers.authorization)
  if (ended) ended.revoked = true
  return {}
})
on('GET', '/me', (req) => whoAmI(req.headers.authorization))
on('PUT', '/me', (req) => ({ ...whoAmI(req.headers.authorization), display_name: String(req.body?.display_name || '') }))
// Account security (CONTRACTS §79). Mock code for every step: 123456.
const twoFactor = { enabled: false, left: 0 }
const backupCodes = () => Array.from({ length: 10 }, (_, i) => `MOCK${i}-CODE${i}`)
on('GET', '/me/two-factor', () => ({ enabled: twoFactor.enabled, enabled_at: twoFactor.enabled ? now() : undefined, backup_codes_left: twoFactor.left }))
const codeError = () => fail(400, 'invalid_argument', 'incorrect code', { fields: [{ field: 'code', code: 'incorrect', message: 'incorrect verification code' }] })
on('POST', '/me/two-factor/enable', (req) => {
  if (req.body?.code !== '123456') return codeError()
  twoFactor.enabled = true
  twoFactor.left = 10
  return { backup_codes: backupCodes() }
})
// Step-up verification (CONTRACTS §80.2): the code is 123456, the password admin.
const proofs = new Set<string>()
const passwordTooScopes = new Set(['superadmin.transfer', 'self.password.change'])
/** The password goes with the factor for these scopes, and for the super admin removing its last factor (iam lastSuperFactor; the mock user has no passkey). */
const passwordToo = (scope: string, context = '') =>
  passwordTooScopes.has(scope) ||
  (!!me.superuser && ((scope === 'self.2fa.disable' && twoFactor.enabled) || (scope === 'user.security.reset' && context === `DELETE /api/v1/users/${me.id}/security`)))
const verifyMethods = (scope: string, context = '') => ({
  scope,
  password_required: passwordToo(scope, context),
  methods: [
    { method: 'passkey', available: false, reason: 'not_enrolled' },
    { method: 'totp', available: twoFactor.enabled, ...(twoFactor.enabled ? {} : { reason: 'not_enrolled' }) },
    { method: 'password', available: !twoFactor.enabled, ...(twoFactor.enabled ? { reason: 'strong_method' } : {}) }
  ]
})
on('GET', '/me/verify/methods', (req) => verifyMethods(req.query.scope, req.query.context))
on('POST', '/me/verify', (req) => {
  const { scope, context, method, password, code } = req.body || {}
  if ((passwordToo(scope, context) || method === 'password') && password !== 'admin') return fail(400, 'invalid_argument', 'incorrect password', { fields: [{ field: 'password', code: 'incorrect', message: 'incorrect password' }] })
  if (method === 'totp' && code !== '123456') return codeError()
  const proof = `mock-proof-${scope}-${Date.now()}`
  proofs.add(proof)
  return { proof, expires_at: now(300), method, scope, context }
})
/** The 403 verification_required of a request without a valid one-time proof of scope (spent when valid); null when it has one. */
export function stepUpRequired(req: MockRequest, scope: string): ReturnType<typeof fail> | null {
  const proof = req.headers['x-verify-proof']
  if (proof && proofs.delete(proof)) return null
  const context = `${req.method} /api/v1${req.path}`
  return fail(403, 'verification_required', "verify it's you to continue", { ...verifyMethods(scope, context), context })
}
/** handler behind a one-time proof of scope, as Router.StepUp(scope). */
export const stepUp = (scope: string, handler: MockHandler): MockHandler => (req) => stepUpRequired(req, scope) ?? handler(req)
on('POST', '/me/two-factor/setup', stepUp('self.2fa.enable', () => ({
  flow_token: 'mock-totp', expires_in: 300, secret: 'MOCKSECRETMOCKSECRET', otpauth_url: 'otpauth://totp/mock',
  qr_code: 'data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw=='
})))
on('POST', '/me/two-factor/disable', stepUp('self.2fa.disable', () => {
  twoFactor.enabled = false
  twoFactor.left = 0
  return {}
}))
on('POST', '/me/two-factor/backup-codes', stepUp('self.2fa.backup_codes', () => ({ backup_codes: backupCodes() })))
const passkeys = [{ id: 1, name: 'MacBook', rp_id: 'localhost', created_at: now(), last_used_at: null }]
on('GET', '/me/passkeys', () => passkeys)
on('PATCH', '/me/passkeys/:id', (req) => {
  const pk = passkeys.find((p) => String(p.id) === req.params.id)
  if (!pk) return fail(404, 'not_found', 'not found')
  pk.name = String(req.body?.name || '')
  return pk
})
on('DELETE', '/me/passkeys/:id', stepUp('self.passkey.delete', (req) => {
  const i = passkeys.findIndex((p) => String(p.id) === req.params.id)
  if (i >= 0) passkeys.splice(i, 1)
  return noContent()
}))
on('GET', '/me/sessions', () => [
  { id: 'a', method: 'passkey', ip: '203.0.113.10', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/131.0 Safari/537.36', created_at: now(), last_used_at: now(), current: true },
  { id: 'b', method: 'two_factor', ip: '203.0.113.11', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Version/18.0 Mobile/15E148 Safari/604.1', created_at: now(), last_used_at: now(), current: false }
])
on('DELETE', '/me/sessions/:id', () => ({}))
on('POST', '/me/sessions/revoke-others', () => ({}))
on('DELETE', '/users/:id/security', stepUp('user.security.reset', () => noContent()))
on('POST', '/system/super-admin/transfer', stepUp('superadmin.transfer', () => noContent()))
// Access keys (CONTRACTS §80): what the mock user holds is what can be chosen; stored canonical (§21).
const canonicalKeys = (keys: string[] = []) => [...new Set(keys.map((k) => canonicalPermission(k) ?? k))]
const accessKeys: any[] = []
on('GET', '/me/access-keys', () => accessKeys)
on('GET', '/me/access-keys/permissions', () => [
  {
    module: 'self',
    label: { en: 'My account', zh: '我的账号' },
    source: 'core',
    status: 'active',
    permissions: [
      { key: 'self:profile', label: { en: 'Edit own profile', zh: '修改自己的资料' }, description: {}, sensitive: false, step_up: false, status: 'active' },
      { key: 'self:security', label: { en: 'Own sign-in security', zh: '自己的登录安全设置' }, description: {}, sensitive: true, step_up: true, status: 'active' }
    ]
  },
  {
    module: 'usage',
    label: { en: 'Usage', zh: '用量' },
    source: 'core',
    status: 'active',
    permissions: [
      { key: 'usage:self:read', label: { en: 'View own usage', zh: '查看自己的用量' }, description: {}, sensitive: false, status: 'active' },
      { key: 'balance:self:read', label: { en: 'View own balance', zh: '查看自己的余额' }, description: {}, sensitive: false, status: 'active' }
    ]
  },
  {
    // Scoped actions (CONTRACTS §21): the key's scope stays within the user's.
    module: 'account',
    label: { en: 'Accounts', zh: '账号' },
    source: 'core',
    status: 'active',
    permissions: [
      { key: 'account:read', label: { en: 'View', zh: '查看' }, description: {}, sensitive: false, scope_type: 'account', status: 'active' },
      { key: 'account:test', label: { en: 'Test', zh: '测试' }, description: {}, sensitive: false, scope_type: 'account', status: 'active' }
    ]
  }
])
on('POST', '/me/access-keys', stepUp('self.access_key.write', (req) => {
  const key = { id: accessKeys.length + 1, name: req.body.name, hint: 'mock', permissions: canonicalKeys(req.body.permissions), lost: [], expires_at: req.body.expires_at, last_used_at: null, last_used_ip: '', created_at: now() }
  accessKeys.unshift(key)
  return { ...key, key: 'sak_mockmockmockmockmockmockmockmockmockmockmock' }
}))
on('PATCH', '/me/access-keys/:id', stepUp('self.access_key.write', (req) => {
  const key = accessKeys.find((k) => String(k.id) === req.params.id)
  if (!key) return fail(404, 'not_found', 'not found')
  Object.assign(key, { name: req.body?.name ?? key.name, permissions: req.body?.permissions ? canonicalKeys(req.body.permissions) : key.permissions, lost: [] })
  return key
}))
on('DELETE', '/me/access-keys/:id', (req) => {
  const i = accessKeys.findIndex((k) => String(k.id) === req.params.id)
  if (i >= 0) accessKeys.splice(i, 1)
  return {}
})
on('PUT', '/me/password', (req) =>
  String(req.body?.new_password || '').length < 8
    ? fail(400, 'invalid_argument', 'too short', { fields: [{ field: 'new_password', code: 'too_short', message: 'at least 8 characters' }] })
    : stepUp('self.password.change', () => noContent())(req)
)

// Scoped action of a core menu path, held at any scope (CONTRACTS §21, menus.go HasAction).
const MENU_ACTIONS: Record<string, string> = {
  '/accounts': 'account:read',
  '/proxies': 'proxy:read',
  '/users': 'user:read',
  '/account-usage': 'usage:account:read',
  '/account-usage/overview': 'usage:account:read'
}
// Core menu paths hidden from a holder of the key (menus.go hideWith): the site's usage shows instead.
const MENU_HIDE_WITH: Record<string, string> = { '/account-usage': 'usage:all:read', '/account-usage/overview': 'usage:all:read' }
// Permission of each other core menu path; plugin items need their plugin permission.
const MENU_PERMS: Record<string, string | string[]> = {
  '/usage': 'usage:all:read',
  '/usage/overview': 'usage:all:read',
  '/segments': 'segment:read',
  '/roles': 'role:read',
  '/plugins': 'plugin:read',
  '/market': 'plugin:market:read',
  '/publishers': 'publisher:read',
  '/nodes': 'node:read',
  '/groups': 'group:read',
  '/settings': ['settings:read', 'sticky:read', 'price:read', 'topup:manage'],
  '/system/upgrades': 'system:update:read',
  '/system/audit': 'audit:read',
  '/system/tasks': 'task:read',
  '/me/api-keys': 'apikey:self:manage',
  '/me/models': 'apikey:self:manage',
  '/me/usage': 'usage:self:read',
  '/me/usage/overview': 'usage:self:read',
  '/me/wallet': 'balance:self:read',
  '/ledger': 'balance:all:read',
  '/redeem-codes': 'redeem:manage',
  '/p/guard/dashboard': 'plugin.guard:stats:read',
  '/p/dynweight/dashboard': 'plugin.dynweight:dynweight:read',
  '/topup/orders': 'topup:order:read',
  '/promo-codes': 'topup:manage',
  '/me/topup-orders': 'topup:use'
}

on('GET', '/me/menus', (req) => {
  const who = caller(req)
  const held = (path: string) => (MENU_ACTIONS[path] ? hasAction(who, MENU_ACTIONS[path]) : !MENU_PERMS[path] || hasPerm(who, MENU_PERMS[path]))
  const allowed = (path: string) => held(path) && !(MENU_HIDE_WITH[path] && hasPerm(who, MENU_HIDE_WITH[path]))
  return MENUS.map((s) => ({ ...s, items: s.items.filter((i) => allowed(i.path)) })).filter((s) => s.items.length)
})

// Mirrors server/internal/authz/menus.go (sections, labels and order, CONTRACTS
// §90, §132) with the bundled plugins' menus placed where their manifests put them.
const L = (en: string, zh: string) => ({ en, zh })
const MENUS = [
  {
    section: 'overview',
    label: L('Console', '控制台'),
    items: [
      { id: 'dashboard', label: L('Dashboard', '仪表盘'), icon: 'dashboard', path: '/dashboard' },
      { id: 'my-api-keys', label: L('API keys', 'API 密钥'), icon: 'key', path: '/me/api-keys' },
      { id: 'my-models', label: L('Models & prices', '模型与价格'), icon: 'price', path: '/me/models' },
      { id: 'my-usage-overview', label: L('Usage overview', '用量概览'), icon: 'chart', path: '/me/usage/overview' },
      { id: 'my-usage', label: L('Usage logs', '使用日志'), icon: 'usage', path: '/me/usage' },
      { id: 'usage-overview', label: L('Site overview', '全站概览'), icon: 'chart', path: '/usage/overview' },
      { id: 'usage', label: L('Site usage logs', '全站日志'), icon: 'usage', path: '/usage' },
      { id: 'account-usage-overview', label: L('Account usage overview', '账号用量概览'), icon: 'chart', path: '/account-usage/overview' },
      { id: 'account-usage', label: L('Account usage logs', '账号日志'), icon: 'usage', path: '/account-usage' }
    ]
  },
  {
    section: 'gateway',
    label: L('Accounts & proxies', '账号/代理'),
    items: [
      { id: 'accounts', label: L('Accounts', '账号'), icon: 'account', path: '/accounts' },
      { id: 'groups', label: L('Account groups', '账号分组'), icon: 'group', path: '/groups' },
      { id: 'proxies', label: L('Proxies', '代理'), icon: 'proxy', path: '/proxies' },
      { id: 'dynweight:dynweight', label: L('Dynamic Weight', '动态权重'), icon: 'chart', path: '/p/dynweight/dashboard', plugin_key: 'dynweight' }
    ]
  },
  {
    section: 'security',
    label: L('Security', '安全'),
    items: [
      { id: 'guard:guard', label: L('Guard', '请求守卫'), icon: 'shield', path: '/p/guard/dashboard', plugin_key: 'guard' },
      { id: 'moderation:moderation', label: L('Prompt Moderation', '提示词审核'), icon: 'shield', path: '/p/moderation/dashboard', plugin_key: 'moderation' }
    ]
  },
  {
    section: 'finance',
    label: L('Finance', '财务'),
    items: [
      { id: 'ledger', label: L('Balance ledger', '余额流水'), icon: 'balance', path: '/ledger' }
    ]
  },
  {
    section: 'topup',
    label: L('Top-up & payment', '充值/支付'),
    items: [
      { id: 'topup-orders', label: L('Top-up orders', '充值订单'), icon: 'balance', path: '/topup/orders' },
      { id: 'redeem-codes', label: L('Redeem codes', '兑换码'), icon: 'key', path: '/redeem-codes' },
      { id: 'promo-codes', label: L('Promo codes', '优惠码'), icon: 'price', path: '/promo-codes' }
    ]
  },
  {
    section: 'users',
    label: L('Users', '用户'),
    items: [
      { id: 'users', label: L('Users', '用户'), icon: 'user', path: '/users' },
      { id: 'segments', label: L('User groups', '用户分组'), icon: 'publisher', path: '/segments' },
      { id: 'roles', label: L('Roles & permissions', '角色与权限'), icon: 'role', path: '/roles' }
    ]
  },
  {
    section: 'plugins',
    label: L('Plugins', '插件'),
    items: [
      { id: 'plugins', label: L('Installed plugins', '已安装插件'), icon: 'plugin', path: '/plugins' },
      { id: 'market', label: L('Plugin market', '插件市场'), icon: 'market', path: '/market' },
      { id: 'publishers', label: L('Publishers', '发布者'), icon: 'publisher', path: '/publishers' }
    ]
  },
  {
    section: 'system',
    label: L('System', '系统'),
    items: [
      { id: 'nodes', label: L('Cluster nodes', '集群节点'), icon: 'node', path: '/nodes' },
      { id: 'tasks', label: L('Scheduled tasks', '定时任务'), icon: 'clock', path: '/system/tasks' },
      { id: 'upgrades', label: L('Core updates', '核心升级'), icon: 'refresh', path: '/system/upgrades' },
      { id: 'audit', label: L('Audit logs', '审计日志'), icon: 'clock', path: '/system/audit' },
      { id: 'settings', label: L('System settings', '系统设置'), icon: 'settings', path: '/settings' }
    ]
  },
  {
    section: 'me',
    label: L('Personal', '个人中心'),
    items: [
      { id: 'my-wallet', label: L('Wallet', '钱包'), icon: 'balance', path: '/me/wallet' },
      { id: 'my-topup-orders', label: L('Top-up history', '充值记录'), icon: 'list', path: '/me/topup-orders' },
      { id: 'my-profile', label: L('Profile', '个人设置'), icon: 'user', path: '/me/profile' }
    ]
  }
]

on('GET', '/me/balance', () => ({ balance: '12.34000000', updated_at: now() }))

// Failure policy (CONTRACTS §69): the production policy of 2026-11-04.
const failureRule = (id: string, name: string, codes: string, keywords: string[], account: boolean) => ({
  id, name, enabled: true,
  match: { source: 'upstream', status_codes: codes, keywords, keyword_regex: false, mode: 'all', groups: [], models: [], protocols: [] },
  action: account ? 'disable' : 'retry', same_account_retries: 0, exhausted_action: '', egress: '', cooldown_seconds: 0
})
let failurePolicy = {
  schema_version: 4, revision: 4, retry_enabled: true, max_retries: 3, retries_per_account: 1, switch_account: true, reset_sticky: true,
  egress: 'account_configured', backoff_ms: 200, max_wait_ms: 0, respect_retry_after: false, account_enabled: true,
  retry_rules: [
    failureRule('http-transient', 'HTTP transient errors', '408,500-599', [], false),
    failureRule('rate-limit', 'Account quota (429)', '429', ["exceed your account's rate limit"], false)
  ],
  account_rules: [
    failureRule('login-invalid', 'Login invalid (401)', '401', ['oauth access token has been revoked', 'oauth token has been revoked', 'invalid x-api-key'], true),
    failureRule('account-on-hold', 'Account on hold (403)', '403', ['oauth authentication is currently not allowed for this organization'], true),
    failureRule('migrated-disable', 'Existing automatic disable', '', ['your credit balance is too low', 'this organization has been disabled.', 'you exceeded your current quota'], true)
  ]
}
on('GET', '/settings/failure-policy', () => ({ configured: true, policy: failurePolicy }))
on('PUT', '/settings/failure-policy', (req) => {
  failurePolicy = { ...req.body.policy, revision: failurePolicy.revision + 1 }
  return { configured: true, policy: failurePolicy }
})
