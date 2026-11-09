import { fail, now, on, type MockRequest } from './router'

// auth, me, menus

export const ALL_PERMISSIONS = [
  'user:read', 'user:create', 'user:update', 'user:delete',
  'role:read', 'role:manage',
  'apikey:self:manage',
  'group:read', 'group:manage',
  'account:read', 'account:create', 'account:update', 'account:delete', 'account:test', 'account:credential:view',
  // CONTRACTS §21.1: own-level keys act on rows the caller created; settings:custom lifts the base_url guard.
  'account:own:read', 'account:own:create', 'account:own:update', 'account:own:delete', 'account:own:test', 'account:own:credential:view',
  'account:settings:custom',
  'proxy:read', 'proxy:manage', 'proxy:own:read', 'proxy:own:manage',
  'price:read', 'price:manage',
  'balance:self:read', 'balance:all:read', 'balance:adjust',
  'usage:self:read', 'usage:all:read',
  'sticky:read', 'sticky:manage',
  'plugin:read', 'plugin:install', 'plugin:manage', 'plugin:uninstall', 'plugin:grant:high', 'plugin:grant:critical', 'plugin:egress:read', 'plugin:market:read',
  'publisher:read', 'publisher:manage',
  'node:read', 'gateway:use', 'settings:read', 'settings:manage',
  'audit:read', 'system:update:read', 'system:update:execute', 'system:update:recover',
  'plugin.guard:rules:read', 'plugin.guard:rules:manage', 'plugin.guard:stats:read',
  'plugin.dynweight:dynweight:read', 'plugin.dynweight:dynweight:manage',
  'plugin.codex:codex:manage',
  'plugin.anthropic:model_catalog:read'
]

export interface Identity {
  id: number
  email: string
  display_name: string
  roles: string[]
  permissions: string[]
  superuser: boolean
}

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
  permissions: ['apikey:self:manage', 'balance:self:read', 'usage:self:read', 'gateway:use'],
  superuser: false
}

/**
 * Sign in as vendor@example.com (password admin): the "vendor" role of
 * CONTRACTS §21.1 (own-level account and proxy keys, no settings:custom).
 * Sees and edits only the accounts / proxies it created.
 */
export const vendorUser: Identity = {
  id: 6,
  email: 'vendor@example.com',
  display_name: 'Vendor',
  roles: ['vendor'],
  permissions: [
    'account:own:read', 'account:own:create', 'account:own:update', 'account:own:delete', 'account:own:test', 'account:own:credential:view',
    'proxy:own:read', 'proxy:own:manage',
    'group:read', 'gateway:use'
  ],
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

const identities = [me, plainUser, vendorUser, readonlyUser]

/** Email of a (possibly deleted) user id, for created_by_email. */
export function userEmail(id: number | null | undefined): string | null {
  if (id == null) return null
  return identities.find((u) => u.id === id)?.email ?? KNOWN_EMAILS[id] ?? null
}
const KNOWN_EMAILS: Record<number, string> = { 2: 'ops@example.com', 3: 'li.si@example.com', 5: 'old@example.com' }

export function hasPerm(who: Identity, key: string | string[]): boolean {
  if (who.superuser) return true
  const list = Array.isArray(key) ? key : [key]
  return list.some((k) => who.permissions.includes(k))
}

/**
 * Owner scope of a caller (CONTRACTS §21.1, core.OwnerScope): `all` with the
 * all-level key, `own` with only the own-level key, null with neither.
 */
export function ownerScope(who: Identity, allKey: string, ownKey: string): 'all' | 'own' | null {
  if (hasPerm(who, allKey)) return 'all'
  if (hasPerm(who, ownKey)) return 'own'
  return null
}

/** Whether `row` (created_by) is reachable by the caller under the scope; null owners need the all level. */
export function inScope(scope: 'all' | 'own' | null, who: Identity, row: { created_by?: number | null }): boolean {
  if (scope === 'all') return true
  if (scope === 'own') return row.created_by != null && row.created_by === who.id
  return false
}

/** Applies the ?mine=true and ?created_by= list filters (the latter only at the all level). */
export function filterOwned<T extends { created_by?: number | null }>(rows: T[], scope: 'all' | 'own' | null, who: Identity, q: Record<string, string>): T[] {
  let out = rows.filter((r) => inScope(scope, who, r))
  if (q.mine === 'true') out = out.filter((r) => r.created_by === who.id)
  if (scope === 'all' && q.created_by) out = out.filter((r) => r.created_by === Number(q.created_by))
  return out
}

// Sessions (CONTRACTS §14.2). Refresh tokens belong to a family created at
// login and rotate on every refresh; presenting a rotated (or revoked) token
// again revokes the whole family. Access tokens carry their family, so a
// revoked family also fails with 401 on the next request.
interface Family {
  current: string
  revoked: boolean
  seq: number
  user: Identity
}
const families = new Map<string, Family>()
let familySeq = 0

function issue(fam: string) {
  const f = families.get(fam)!
  f.seq++
  f.current = `mock-refresh-${fam}-${f.seq}`
  return { access_token: `mock-access-${fam}-${f.seq}-${Date.now()}`, refresh_token: f.current, expires_in: 7200, user: f.user }
}

function newFamily(user: Identity = me) {
  const fam = `f${++familySeq}`
  families.set(fam, { current: '', revoked: false, seq: 0, user })
  return issue(fam)
}

/** The signed-in identity of a request (admin for tokens of unknown families). */
function whoAmI(auth: string | undefined): Identity {
  const m = /^Bearer\s+mock-access-(f\d+)-/i.exec(auth || '')
  return (m && families.get(m[1])?.user) || me
}

/** The signed-in identity of a mock request. */
export function caller(req: MockRequest): Identity {
  return whoAmI(req.headers.authorization)
}

/**
 * Checks the bearer token of a console request: "mock-access-<family>-..."
 * tokens are rejected once their family is revoked; tokens of an unknown
 * family (issued before a mock restart) stay valid; "expired" always fails.
 */
export function accessTokenValid(auth: string | undefined): boolean {
  if (!auth) return true
  const tok = auth.replace(/^Bearer\s+/i, '')
  if (tok === 'expired') return false
  const m = /^mock-access-(f\d+)-/.exec(tok)
  if (!m) return true
  const f = families.get(m[1])
  return !f || !f.revoked
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
  return newFamily(identities.find((u) => u.email === email) || me)
})
on('POST', '/auth/refresh', (req) => {
  const tok = String(req.body?.refresh_token || '')
  // Legacy tokens from before this mock version: start a new family.
  if (tok === 'mock-refresh') return newFamily()
  const m = /^mock-refresh-(f\d+)-\d+$/.exec(tok)
  const f = m ? families.get(m[1]) : undefined
  if (!f) return fail(401, 'unauthenticated', 'invalid refresh token')
  if (f.revoked) return fail(401, 'unauthenticated', 'refresh token revoked')
  if (f.current !== tok) {
    // Reuse of a rotated token: revoke the whole login.
    f.revoked = true
    return fail(401, 'unauthenticated', 'refresh token reuse detected; session revoked')
  }
  return issue(m![1])
})
on('POST', '/auth/logout', (req) => {
  const m = /^mock-refresh-(f\d+)-\d+$/.exec(String(req.body?.refresh_token || ''))
  const f = m ? families.get(m[1]) : undefined
  if (f) f.revoked = true
  return {}
})
on('GET', '/me', (req) => whoAmI(req.headers.authorization))
on('PUT', '/me', (req) => ({ ...whoAmI(req.headers.authorization), display_name: String(req.body?.display_name || '') }))
// Account security (CONTRACTS §79). Mock code for every step: 123456.
const twoFactor = { enabled: false, left: 0 }
const backupCodes = () => Array.from({ length: 10 }, (_, i) => `MOCK${i}-CODE${i}`)
on('GET', '/me/two-factor', () => ({ enabled: twoFactor.enabled, enabled_at: twoFactor.enabled ? now() : undefined, backup_codes_left: twoFactor.left }))
on('POST', '/me/two-factor/setup', (req) =>
  req.body?.password === 'admin'
    ? { flow_token: 'mock-totp', expires_in: 300, secret: 'MOCKSECRETMOCKSECRET', otpauth_url: 'otpauth://totp/mock', qr_code: 'data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw==' }
    : fail(400, 'invalid_argument', 'incorrect password', { fields: [{ field: 'password', code: 'incorrect', message: 'incorrect password' }] })
)
const codeError = () => fail(400, 'invalid_argument', 'incorrect code', { fields: [{ field: 'code', code: 'incorrect', message: 'incorrect verification code' }] })
on('POST', '/me/two-factor/enable', (req) => {
  if (req.body?.code !== '123456') return codeError()
  twoFactor.enabled = true
  twoFactor.left = 10
  return { backup_codes: backupCodes() }
})
on('POST', '/me/two-factor/disable', (req) => {
  if (req.body?.code !== '123456') return codeError()
  twoFactor.enabled = false
  twoFactor.left = 0
  return {}
})
on('POST', '/me/two-factor/backup-codes', (req) => (req.body?.code === '123456' ? { backup_codes: backupCodes() } : codeError()))
on('GET', '/me/passkeys', () => [{ id: 1, name: 'MacBook', rp_id: 'localhost', created_at: now(), last_used_at: null }])
on('DELETE', '/me/passkeys/:id', () => ({}))
on('GET', '/me/sessions', () => [
  { id: 'a', method: 'passkey', ip: '203.0.113.10', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/131.0 Safari/537.36', created_at: now(), last_used_at: now(), current: true },
  { id: 'b', method: 'two_factor', ip: '203.0.113.11', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Version/18.0 Mobile/15E148 Safari/604.1', created_at: now(), last_used_at: now(), current: false }
])
on('DELETE', '/me/sessions/:id', () => ({}))
on('POST', '/me/sessions/revoke-others', () => ({}))
on('DELETE', '/users/:id/security', () => ({}))
// Access keys (CONTRACTS §80): what the mock user holds is what can be chosen.
const accessKeys: any[] = []
on('GET', '/me/access-keys', () => accessKeys)
on('GET', '/me/access-keys/permissions', () => [
  {
    module: 'usage',
    label: { en: 'Usage', zh: '用量' },
    source: 'core',
    status: 'active',
    permissions: [
      { key: 'usage:self:read', label: { en: 'View own usage', zh: '查看自己的用量' }, description: {}, sensitive: false, status: 'active' },
      { key: 'balance:self:read', label: { en: 'View own balance', zh: '查看自己的余额' }, description: {}, sensitive: false, status: 'active' }
    ]
  }
])
on('POST', '/me/access-keys', (req) => {
  if (req.body?.password !== 'admin') return fail(400, 'invalid_argument', 'incorrect password', { fields: [{ field: 'password', code: 'incorrect', message: 'incorrect password' }] })
  const key = { id: accessKeys.length + 1, name: req.body.name, hint: 'mock', permissions: req.body.permissions, lost: [], expires_at: req.body.expires_at, last_used_at: null, last_used_ip: '', created_at: now() }
  accessKeys.unshift(key)
  return { ...key, key: 'sak_mockmockmockmockmockmockmockmockmockmockmock' }
})
on('PATCH', '/me/access-keys/:id', (req) => {
  const key = accessKeys.find((k) => String(k.id) === req.params.id)
  if (!key) return fail(404, 'not_found', 'not found')
  Object.assign(key, { name: req.body?.name ?? key.name, permissions: req.body?.permissions ?? key.permissions, lost: [] })
  return key
})
on('DELETE', '/me/access-keys/:id', (req) => {
  const i = accessKeys.findIndex((k) => String(k.id) === req.params.id)
  if (i >= 0) accessKeys.splice(i, 1)
  return {}
})
on('PUT', '/me/password', (req) => (req.body?.old_password === 'admin' ? {} : fail(400, 'invalid_argument', 'wrong password', { fields: [{ field: 'old_password', code: 'mismatch', message: 'Current password is wrong' }] })))

// Permission of each core menu path (CONTRACTS §21.2 for accounts / platforms / proxies); plugin items need their plugin permission.
const MENU_PERMS: Record<string, string | string[]> = {
  '/groups': 'group:read',
  '/accounts': ['account:read', 'account:own:read', 'account:own:create'],
  '/proxies': ['proxy:read', 'proxy:own:read', 'proxy:own:manage'],
  '/prices': 'price:read',
  '/usage': 'usage:all:read',
  '/usage/overview': 'usage:all:read',
  '/ledger': 'balance:all:read',
  '/users': 'user:read',
  '/roles': 'role:read',
  '/platforms': ['account:read', 'account:own:read', 'account:own:create'],
  '/plugins': 'plugin:read',
  '/market': 'plugin:market:read',
  '/publishers': 'publisher:read',
  '/nodes': 'node:read',
  '/settings': ['settings:read', 'sticky:read'],
  '/ccgateway': 'settings:read',
  '/system/upgrades': 'system:update:read',
  '/system/audit': 'audit:read',
  '/system/tasks': 'task:read',
  '/me/api-keys': 'apikey:self:manage',
  '/me/models': 'apikey:self:manage',
  '/me/usage': 'usage:self:read',
  '/me/usage/overview': 'usage:self:read',
  '/me/wallet': 'balance:self:read',
  '/p/anthropic/models': 'plugin.anthropic:model_catalog:read',
  '/p/guard/dashboard': 'plugin.guard:stats:read',
  '/p/dynweight/dashboard': 'plugin.dynweight:dynweight:read'
}

on('GET', '/me/menus', (req) => {
  const who = caller(req)
  return MENUS.map((s) => ({ ...s, items: s.items.filter((i) => !MENU_PERMS[i.path] || hasPerm(who, MENU_PERMS[i.path])) })).filter((s) => s.items.length)
})

// Mirrors server/internal/authz/menus.go (sections, labels and order, CONTRACTS
// §90) with the bundled plugins' menus placed where their manifests put them.
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
      { id: 'usage', label: L('Site usage logs', '全站日志'), icon: 'usage', path: '/usage' }
    ]
  },
  {
    section: 'gateway',
    label: L('Gateway', '网关'),
    items: [
      { id: 'accounts', label: L('Accounts', '账号'), icon: 'account', path: '/accounts' },
      { id: 'groups', label: L('Groups', '分组'), icon: 'group', path: '/groups' },
      { id: 'platforms', label: L('Platforms', '平台'), icon: 'globe', path: '/platforms' },
      { id: 'proxies', label: L('Proxies', '代理'), icon: 'proxy', path: '/proxies' },
      { id: 'prices', label: L('Model prices', '模型价格'), icon: 'price', path: '/prices' },
      // The server lists it only while the ccgateway plugin is enabled.
      ...(process.env.SUB2API_MOCK_CCGATEWAY ? [{ id: 'ccgateway', label: L('CCGateway', 'CCGateway'), icon: 'code', path: '/ccgateway' }] : []),
      { id: 'anthropic:anthropic-models', label: L('Model catalog', '模型目录'), icon: 'list', path: '/p/anthropic/models', plugin_key: 'anthropic' },
      { id: 'dynweight:dynweight', label: L('Dynamic Weight', '动态权重'), icon: 'chart', path: '/p/dynweight/dashboard', plugin_key: 'dynweight' }
    ]
  },
  {
    section: 'security',
    label: L('Security', '安全'),
    items: [{ id: 'guard:guard', label: L('Guard', '请求守卫'), icon: 'shield', path: '/p/guard/dashboard', plugin_key: 'guard' }]
  },
  { section: 'finance', label: L('Finance', '财务'), items: [{ id: 'ledger', label: L('Balance ledger', '余额流水'), icon: 'ledger', path: '/ledger' }] },
  {
    section: 'users',
    label: L('Users', '用户'),
    items: [
      { id: 'users', label: L('Users', '用户'), icon: 'user', path: '/users' },
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
    failureRule('rate-limit', 'Account quota and gateway concurrency (429)', '429', ["exceed your account's rate limit", 'gateway concurrency limit reached'], false)
  ],
  account_rules: [
    failureRule('login-invalid', 'Login invalid (401)', '401', ['oauth access token has been revoked', 'oauth token has been revoked', 'oauth refresh token is no longer valid'], true),
    failureRule('account-on-hold', 'Account on hold (403)', '403', ['oauth_account_on_hold', 'oauth authentication is currently not allowed for this organization'], true),
    failureRule('migrated-disable', 'Existing automatic disable', '', ['your credit balance is too low', 'this organization has been disabled.', 'you exceeded your current quota'], true)
  ]
}
on('GET', '/settings/failure-policy', () => ({ configured: true, policy: failurePolicy }))
on('PUT', '/settings/failure-policy', (req) => {
  failurePolicy = { ...req.body.policy, revision: failurePolicy.revision + 1 }
  return { configured: true, policy: failurePolicy }
})
