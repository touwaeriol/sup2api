// Mock handlers: plugins (list, detail, lifecycle, rollouts, grants, settings,
// resources, egress, jobs, events) and the plugin market. Reviews, consents
// and installs live in pluginConsent.ts, /ui/plugins and /p/<key>/... in
// pluginui.ts; publishers are not here.
import { fail, noContent, now, on, paginate, type MockRequest } from './router'
import { caller, hasPerm, stepUp, type Identity } from './core'
import { addRoleKeys, bumpAuthz, managesRole } from './roles'
import { pluginPlatforms } from './platforms'
import { pluginAccountCount, uninstallPluginAccounts } from './accounts'
import { invalid } from './common'
import { mailerSettings } from './mailer'

export type Any = Record<string, any>

/** Version of the mock core, reported as host_version by GET /market/plugins. */
export const HOST_VERSION = '0.1.5'
/** First mock core of the current plugin protocol (the refusal's first_core_version). */
const FIRST_CORE_VERSION = '0.1.4'

interface MockPlugin {
  key: string
  name: Any
  description: Any
  status: string
  status_reason: string
  /** Ships with the image: disable-only (CONTRACTS §5.7, §14.1). */
  builtin?: boolean
  active_version: string | null
  desired_version: string | null
  publisher: string
  trust: string
  egress_policy: string
  resources: Any
  versions: Array<{ version: string; consent_status: string }>
  grants: Any[]
  manifest: Any
  settings: Any | null
  /** Exclusive resources the generation did not give the plugin (audit 2026-10-09 P2-2). */
  resource_conflicts?: Array<{ resource: string; id: string; reason: string }>
  /** Version the nodes refuse to load: built for an older plugin contract. */
  refused_version?: string
}

interface MockRollout {
  id: number
  plugin_key: string
  action: 'enable' | 'upgrade' | 'disable'
  from_version: string
  target_version: string
  started: number
  cancelled: boolean
  failNode: boolean
}

const NODES = ['node-1', 'node-2']
let rolloutSeq = 40

// ------------------------------------------------------------------ fixtures

function guardManifest(version: string): Any {
  return {
    key: 'guard',
    version,
    publisher: 'sub2api',
    description: { en: 'Inspects gateway requests and blocks the ones matching rules.', zh: '检查网关请求，拦截命中规则的请求。' },
    hostCompat: '>=0.1.0 <0.2.0',
    capabilities: [{ id: 'gateway.hook.v1' }, { id: 'app.events.v1' }, { id: 'app.jobs.v1' }, { id: 'http.routes.v1' }, { id: 'app.broadcast.v1' }],
    ui: {
      menus: [{ id: 'guard', section: 'plugins', label: { en: 'Guard', zh: '请求守卫' }, page: 'dashboard' }],
      pages: { dashboard: { type: 'native', component: 'GuardDashboard' } },
      slots: [{ slot: 'dashboard.widgets', component: 'BlockedTodayCard' }]
    },
    // As the detail's manifest (a review) has it: the scripts are GET /plugins/:key/database's.
    database: { migrations: guardMigrations(version).map((m) => m.id) },
    resources: { memoryMB: 128, cpu: 0.25, maxProcs: 64, maxOpenFiles: 256 }
  }
}

/**
 * Guard's migration scripts by version (CONTRACTS §130): 0.2.0 changes the
 * second one and adds a third. Tables are named with the plugin's prefix.
 */
function guardMigrations(version: string): Array<{ id: string; checksum: string; sql: string }> {
  const upgraded = version >= '0.2.0'
  const list = [
    {
      id: '0001_init.sql',
      checksum: 'sha256:7d1f0c2a9e',
      sql: `CREATE TABLE IF NOT EXISTS plg_guard__rules (
  id         bigserial PRIMARY KEY,
  pattern    text        NOT NULL,
  action     text        NOT NULL DEFAULT 'block',
  enabled    boolean     NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS plg_guard__rules_enabled_idx ON plg_guard__rules (enabled);`
    },
    {
      id: '0002_stats.sql',
      checksum: upgraded ? 'sha256:c4b81e07aa' : 'sha256:5a9e33d210',
      sql: `CREATE TABLE IF NOT EXISTS plg_guard__stats (
  day      date   NOT NULL,
  rule_id  bigint NOT NULL REFERENCES plg_guard__rules (id) ON DELETE CASCADE,
  blocked  bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (day, rule_id)
);${upgraded ? `
ALTER TABLE plg_guard__stats ADD COLUMN IF NOT EXISTS user_id bigint;
CREATE INDEX IF NOT EXISTS plg_guard__stats_user_idx ON plg_guard__stats (user_id, day);` : ''}`
    }
  ]
  if (upgraded) {
    list.push({
      id: '0003_group_rules.sql',
      checksum: 'sha256:0be2f6c871',
      sql: `ALTER TABLE plg_guard__rules ADD COLUMN IF NOT EXISTS group_id bigint REFERENCES groups (id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS plg_guard__rules_group_idx ON plg_guard__rules (group_id);`
    })
  }
  return list
}

/** Guard's database.access: 0.2.0 asks for writes and a foreign key as well. */
function guardAccess(version: string): Any[] {
  const access: Any[] = [{ table: 'users', columns: ['id', 'email', 'status'], privileges: ['select'] }]
  if (version >= '0.2.0') {
    access.push({ table: 'groups', privileges: ['select', 'references'] }, { table: 'usage_logs', columns: ['status', 'error_message'], privileges: ['select', 'update'] })
  }
  return access
}

/** The db.core_read scope of access, as the core grants and compares it (dbschema.AccessScope). */
function accessScope(access: Any[]): Any {
  return { access: access.flatMap((a) => a.privileges.map((p: string) => (!a.columns?.length || p === 'delete' ? [`${p}:${a.table}.*`] : a.columns.map((c: string) => `${p}:${a.table}.${c}`))).flat()) }
}

/** The installed guard's migrations as review database_migrations would mark them against the target version's. */
function guardReviewMigrations(installed: string | null, version: string): Any[] {
  const ran = new Map(installed ? guardMigrations(installed).map((m) => [m.id, m.checksum]) : [])
  return guardMigrations(version).map((m) => ({ ...m, status: !ran.has(m.id) ? 'new' : ran.get(m.id) === m.checksum ? 'applied' : 'changed' }))
}

function anthropicManifest(version: string): Any {
  return {
    key: 'anthropic',
    version,
    publisher: 'sub2api',
    description: { en: 'Anthropic account types: Claude API keys (the anthropic platform is built into the core).', zh: 'Anthropic 账号类型：Claude API Key（anthropic 平台由核心内置）。' },
    hostCompat: '>=0.1.0 <0.2.0',
    capabilities: [{ id: 'platform.adapter.v1' }],
    // CONTRACTS §13: no platform of its own; its account type supports the
    // built-in anthropic platform.
    platforms: [],
    account_types: [{ id: 'apikey', label: { en: 'API key', zh: 'API Key' }, form_mode: 'native', platforms: ['anthropic'] }],
    gateway_endpoints: [],
    resources: { memoryMB: 256, cpu: 0.5, maxProcs: 128, maxOpenFiles: 1024 }
  }
}

export function grant(permission: string, scope: Any | null = null, version = '0.1.0'): Any {
  return { permission, scope, status: 'active', plugin_version: version, granted_by: 1, granted_by_email: 'admin@example.com', granted_at: now(-86400 * 3) }
}

export const plugins = new Map<string, MockPlugin>()

plugins.set('guard', {
  key: 'guard',
  name: { en: 'Guard', zh: '请求守卫' },
  description: guardManifest('0.1.0').description,
  status: 'enabled',
  status_reason: '',
  active_version: '0.1.0',
  desired_version: '0.1.0',
  publisher: 'sub2api',
  trust: 'official',
  egress_policy: 'allowlist',
  resources: { memory_mb: 128, cpu: 0.25, max_threads: 64, max_open_files: 256 },
  // 0.2.0 waits for consent: the upgrade's permission matrix (new, revived and removed user permissions).
  versions: [
    { version: '0.1.0', consent_status: 'approved' },
    { version: '0.2.0', consent_status: 'pending' }
  ],
  grants: [
    grant('kv'),
    grant('db.schema'),
    grant('db.core_read'),
    grant('gateway.hook', { points: ['gateway.request'], fields: ['model', 'prompt_text'] }),
    grant('events', { subscribe: ['usage.recorded'] }),
    grant('jobs'),
    grant('routes.admin'),
    grant('ui.native'),
    grant('broadcast'),
    grant('net', { domains: ['hooks.example.com'] })
  ],
  manifest: guardManifest('0.1.0'),
  settings: {
    mode: 'native',
    component: 'GuardSettings',
    secret_fields: [],
    values: { webhook_url: 'https://hooks.example.com/guard', record_snippets: false, alert_cooldown_sec: 60 }
  }
})

plugins.set('anthropic', {
  key: 'anthropic',
  name: { en: 'Anthropic', zh: 'Anthropic' },
  description: anthropicManifest('0.1.0').description,
  status: 'enabled',
  status_reason: '',
  active_version: '0.1.0',
  desired_version: '0.1.0',
  publisher: 'sub2api',
  trust: 'official',
  egress_policy: 'allow_all',
  resources: { memory_mb: 256, cpu: 0.5, max_threads: 128, max_open_files: 1024 },
  versions: [
    { version: '0.1.0', consent_status: 'approved' },
    { version: '0.2.0', consent_status: 'pending' }
  ],
  grants: [grant('kv'), grant('platform.register'), grant('net')],
  manifest: anthropicManifest('0.1.0'),
  settings: null,
  builtin: true
})

// Settings of its own and nothing else to show: native settings without a
// page, mounted on the settings tab (mailer.ts has its native module).
plugins.set('mailer', {
  key: 'mailer',
  name: { en: 'Mailer', zh: '邮件' },
  description: { en: 'Sends notifications by email through an SMTP server.', zh: '通过 SMTP 服务器发送邮件通知。' },
  status: 'enabled',
  status_reason: '',
  builtin: true,
  active_version: '0.2.0',
  desired_version: '0.2.0',
  publisher: 'sub2api',
  trust: 'official',
  egress_policy: 'allow_all',
  resources: { memory_mb: 64, cpu: 0.1 },
  versions: [{ version: '0.2.0', consent_status: 'approved' }],
  grants: [grant('net', { domains: ['*'] }, '0.2.0'), grant('ui.native', null, '0.2.0')],
  manifest: {
    key: 'mailer',
    version: '0.2.0',
    publisher: 'sub2api',
    description: { en: 'Sends notifications by email through an SMTP server.', zh: '通过 SMTP 服务器发送邮件通知。' },
    hostCompat: '>=0.1.0 <0.2.0',
    hostUICompat: '^4.0',
    capabilities: [{ id: 'notify.channel.v1' }],
    ui: { native: { entry: 'ui/native/entry.js' }, settings: { mode: 'native', component: 'MailerSettings', secrets: [{ path: 'password' }] } }
  },
  settings: mailerSettings
})

// Built-in openai / gemini plugins (CONTRACTS §14.1): one account type each
// for the built-in platform of the same name.
function builtinAdapterManifest(key: 'openai' | 'gemini', version: string): Any {
  const label = key === 'openai' ? 'OpenAI' : 'Gemini'
  return {
    key,
    version,
    publisher: 'sub2api',
    description: {
      en: `${label} account type: API keys (the ${key} platform is built into the core).`,
      zh: `${label} 账号类型：API Key（${key} 平台由核心内置）。`
    },
    hostCompat: '>=0.1.0 <0.2.0',
    capabilities: [{ id: 'platform.adapter.v1' }],
    platforms: [],
    account_types: [{ id: 'apikey', label: { en: 'API key', zh: 'API Key' }, form_mode: 'native', platforms: [key] }],
    gateway_endpoints: [],
    resources: { memoryMB: 128, cpu: 0.5, maxProcs: 128, maxOpenFiles: 1024 }
  }
}

for (const key of ['openai', 'gemini'] as const) {
  const m = builtinAdapterManifest(key, '0.1.0')
  plugins.set(key, {
    key,
    name: { en: key === 'openai' ? 'OpenAI' : 'Gemini', zh: key === 'openai' ? 'OpenAI' : 'Gemini' },
    description: m.description,
    status: 'enabled',
    status_reason: '',
    builtin: true,
    active_version: '0.1.0',
    desired_version: '0.1.0',
    publisher: 'sub2api',
    trust: 'official',
    egress_policy: 'allow_all',
    resources: { memory_mb: 128, cpu: 0.5, max_threads: 128, max_open_files: 1024 },
    versions: [{ version: '0.1.0', consent_status: 'approved' }],
    grants: [grant('kv'), grant('platform.register'), grant('net')],
    manifest: m,
    settings: null
  })
}

plugins.set('foo_platform', {
  key: 'foo_platform',
  name: { en: 'Foo Platform', zh: 'Foo 平台' },
  description: { en: 'Community adapter for the Foo API.', zh: 'Foo API 的社区适配器。' },
  status: 'awaiting_consent',
  status_reason: '',
  active_version: null,
  desired_version: '1.0.0',
  publisher: 'foo-labs',
  trust: 'community',
  egress_policy: 'allow_all',
  resources: {},
  versions: [{ version: '1.0.0', consent_status: 'pending' }],
  grants: [],
  manifest: { key: 'foo_platform', version: '1.0.0', publisher: 'foo-labs', hostCompat: '>=0.1.0' },
  settings: null
})

// Declares only an account type (no platform of its own): relay_key supports
// the built-in anthropic platform.
plugins.set('relay', {
  key: 'relay',
  name: { en: 'Relay', zh: '中转' },
  description: { en: 'Keys of Anthropic-compatible relays.', zh: 'Anthropic 兼容中转站的 Key。' },
  status: 'enabled',
  status_reason: '',
  active_version: '0.3.0',
  desired_version: '0.3.0',
  publisher: 'relay-labs',
  trust: 'verified',
  egress_policy: 'allow_all',
  resources: { memory_mb: 64, cpu: 0.1 },
  versions: [{ version: '0.3.0', consent_status: 'approved' }],
  // Built for the older plugin contract: the nodes refuse to load it.
  refused_version: '0.3.0',
  grants: [grant('kv', null, '0.3.0'), grant('platform.register', null, '0.3.0'), grant('net', null, '0.3.0')],
  manifest: {
    key: 'relay',
    version: '0.3.0',
    publisher: 'relay-labs',
    description: { en: 'Keys of Anthropic-compatible relays.', zh: 'Anthropic 兼容中转站的 Key。' },
    hostCompat: '>=0.1.0',
    capabilities: [{ id: 'platform.adapter.v1' }],
    platforms: [],
    gateway_endpoints: [],
    account_types: [{ id: 'relay_key', label: { en: 'Relay key', zh: '中转 Key' }, form_mode: 'native', platforms: ['anthropic'] }]
  },
  settings: null
})

// Declares a new platform (myvideo) with its endpoints and an account type for it.
function videogenManifest(version: string): Any {
  return {
    key: 'videogen',
    version,
    publisher: 'video-labs',
    description: { en: 'Adds the myvideo platform (video generation endpoints) and its account type.', zh: '新增 myvideo 平台（视频生成端点）及其账号类型。' },
    hostCompat: '>=0.1.0',
    capabilities: [{ id: 'gateway.platform.v1' }, { id: 'platform.adapter.v1' }],
    platforms: [
      {
        id: 'myvideo',
        label: { en: 'MyVideo', zh: 'MyVideo 视频' },
        endpoints: [
          { method: 'POST', path: '/v1/video/generations', protocol: 'myvideo.generate', billing: 'usage' },
          { method: 'GET', path: '/v1/video/generations/:id', protocol: 'myvideo.status', billing: 'free' }
        ]
      }
    ],
    gateway_endpoints: [
      { method: 'POST', path: '/v1/video/generations', protocol: 'myvideo.generate' },
      { method: 'GET', path: '/v1/video/generations/:id', protocol: 'myvideo.status' }
    ],
    account_types: [{ id: 'video_key', label: { en: 'MyVideo key', zh: 'MyVideo Key' }, form_mode: 'native', platforms: ['myvideo'] }]
  }
}

plugins.set('videogen', {
  key: 'videogen',
  name: { en: 'Video generation', zh: '视频生成' },
  description: videogenManifest('0.2.0').description,
  status: 'enabled',
  status_reason: '',
  // A newer core reserved one of its paths: the platform is not served.
  resource_conflicts: [
    { resource: 'platform', id: 'videogen', reason: 'endpoint GET /dashboard/videos would replace the console path /dashboard' }
  ],
  active_version: '0.2.0',
  desired_version: '0.2.0',
  publisher: 'video-labs',
  trust: 'verified',
  egress_policy: 'allow_all',
  resources: { memory_mb: 128, cpu: 0.25 },
  versions: [{ version: '0.2.0', consent_status: 'approved' }],
  grants: [
    grant('kv', null, '0.2.0'),
    grant('platform.register', null, '0.2.0'),
    grant('gateway.endpoint', null, '0.2.0'),
    grant('net', null, '0.2.0')
  ],
  manifest: videogenManifest('0.2.0'),
  settings: null
})

// ------------------------------------------------------------------ reviews

/** An item of review.diff (review.go DiffItem). */
export const diffItem = (id: string, risk: string) => ({ id, risk })

// The guard's user permissions as the catalog has them (resources.ts plugin.guard):
// local keys, defaults as canonical suffixes ("" every row), as the review sends them.
const GUARD_PERMISSIONS: Any[] = [
  { key: 'rules:read', label: { en: 'View rules', zh: '查看规则' }, risk: 'low', group: 'rules', defaults: { admin: [''], operator: [''] } },
  {
    key: 'rules:manage', label: { en: 'Manage rules', zh: '管理规则' }, risk: 'sensitive', group: 'rules', requires: ['rules:read'],
    scope: { resource: 'ruleset', own: true, options: '/scopes/ruleset' }, defaults: { admin: [''], operator: ['own'] }
  },
  { key: 'stats:read', label: { en: 'View stats', zh: '查看统计' }, risk: 'low', group: 'ops', defaults: { admin: [''], operator: [''], supplier: [''] } },
  {
    key: 'alerts:mute', label: { en: 'Mute alerts', zh: '静音告警' }, risk: 'low', group: 'ops',
    scope: { resource: 'value', values: [{ key: 'webhook', label: { en: 'Webhook', zh: 'Webhook' } }, { key: 'email', label: { en: 'Email', zh: '邮件' } }] },
    defaults: { admin: ['value/webhook', 'value/email'] }
  },
  { key: 'bypass', label: { en: 'Bypass the rules', zh: '免于拦截' }, risk: 'critical', group: 'ops', defaults: {} }
]
const GUARD_GROUPS = [{ key: 'rules', label: { en: 'Rules', zh: '拦截规则' }, sort: 10 }, { key: 'ops', label: { en: 'Operations', zh: '运行' }, sort: 20 }]

/**
 * The user permission part of a guard review: a first install creates every
 * permission; an upgrade keeps them, adds two (one sensitive, one suggested
 * to a custom role: both need confirmation), brings rules:test back with its
 * grants (D6) and renames alerts:mute to alerts:silence (inherit_from).
 */
function guardUserPermissions(upgrade: boolean): Any {
  if (!upgrade) return { user_permissions: GUARD_PERMISSIONS.map((p) => ({ ...p, new: true })), permission_groups: GUARD_GROUPS }
  const kept = GUARD_PERMISSIONS.filter((p) => p.key !== 'alerts:mute').map((p) => ({ ...p, new: false }))
  return {
    user_permissions: [
      ...kept,
      { key: 'rules:publish', label: { en: 'Publish rules', zh: '发布规则' }, risk: 'sensitive', group: 'rules', requires: ['rules:manage'], defaults: { admin: [''] }, new: true },
      { key: 'reports:export', label: { en: 'Export reports', zh: '导出报表' }, risk: 'low', group: 'ops', defaults: { admin: [''], readonly: [''] }, new: true },
      { key: 'rules:test', label: { en: 'Dry-run rules', zh: '试运行规则' }, risk: 'low', group: 'rules', defaults: { operator: [''] }, revived: true },
      // alerts:mute renamed: it starts at the old key's holders, with their scopes (C2).
      {
        key: 'alerts:silence', label: { en: 'Silence alerts', zh: '屏蔽告警' }, risk: 'low', group: 'ops',
        scope: { resource: 'value', values: [{ key: 'webhook', label: { en: 'Webhook', zh: 'Webhook' } }, { key: 'email', label: { en: 'Email', zh: '邮件' } }] },
        new: true, renamed: true, inherit_from: ['alerts:mute'], inherited: { admin: ['value/webhook', 'value/email'] }
      }
    ],
    permission_groups: GUARD_GROUPS,
    removed_user_permissions: [{ key: 'alerts:mute', label: { en: 'Mute alerts', zh: '静音告警' }, holders: ['admin'] }]
  }
}

export function guardReview(version: string, diff?: Any): Any {
  return {
    plugin_key: 'guard',
    version,
    name: { en: 'Guard', zh: '请求守卫' },
    publisher: 'sub2api',
    trust: 'official',
    signature_status: 'valid',
    host_compat_ok: true,
    host_compat: '>=0.1.0 <0.2.0',
    capabilities: [{ id: 'gateway.hook.v1' }, { id: 'app.events.v1' }, { id: 'app.jobs.v1' }, { id: 'http.routes.v1' }, { id: 'app.broadcast.v1' }],
    gateway_endpoints: [],
    platforms: [],
    account_types: [],
    hooks: [
      {
        point: 'gateway.request',
        id: 'inspect',
        match: { protocols: ['anthropic.messages'], models: ['*'], groups: ['*'] },
        needs: ['model', 'prompt_text'],
        maxPromptBytes: 32768,
        timeoutMs: 300,
        failure: 'open'
      }
    ],
    jobs: [
      { id: 'rollup', schedule: '@every 5m' },
      { id: 'cleanup', schedule: '0 3 * * *' }
    ],
    events: ['usage.recorded'],
    routes: [
      { method: 'GET', path: '/rules', scope: 'admin', permission: 'rules:read' },
      { method: 'PUT', path: '/rules', scope: 'admin', permission: 'rules:manage' },
      { method: 'GET', path: '/stats', scope: 'admin', permission: 'stats:read' }
    ],
    menus: [{ id: 'guard', label: { en: 'Guard', zh: '请求守卫' }, type: 'native' }],
    ...guardUserPermissions(!!diff),
    database: { migrations: guardMigrations(version).map((m) => m.id) },
    database_access: guardAccess(version),
    // An upgrade runs against the scripts of the installed 0.1.0.
    database_migrations: guardReviewMigrations(diff ? '0.1.0' : null, version),
    resources: { memoryMB: 128, cpu: 0.25, maxProcs: 64, maxOpenFiles: 256 },
    external_services: ['hooks.example.com'],
    host_permissions: [
      { id: 'kv', risk: 'low' },
      { id: 'broadcast', risk: 'low', reason: { en: 'Reload rules on every node right after they change', zh: '规则修改后通知各节点立即重载' } },
      { id: 'db.schema', risk: 'high', reason: { en: 'Store rules and statistics', zh: '保存拦截规则和统计' }, requires: 'plugin:grant:high' },
      {
        id: 'db.core_read',
        risk: 'critical',
        scope: accessScope(guardAccess(version)),
        reason: { en: 'Match rules against users and groups', zh: '按用户和分组匹配规则' },
        requires: 'plugin:grant:critical',
        // An upgrade from 0.1.0, which holds its access.
        ...(diff ? { current_status: 'granted', current_scope: accessScope(guardAccess('0.1.0')) } : {})
      },
      {
        id: 'gateway.hook',
        risk: 'high',
        scope: { points: ['gateway.request'], fields: ['model', 'prompt_text'] },
        reason: { en: 'Inspect request content', zh: '检查请求内容' },
        requires: 'plugin:grant:high'
      },
      { id: 'events', risk: 'medium', scope: { subscribe: ['usage.recorded'] } },
      { id: 'jobs', risk: 'medium' },
      { id: 'routes.admin', risk: 'medium' },
      { id: 'ui.native', risk: 'critical', reason: { en: 'Provides the statistics dashboard', zh: '提供统计大盘' }, requires: 'plugin:grant:critical' },
      {
        id: 'net',
        risk: 'high',
        scope: { domains: ['hooks.example.com'] },
        optional: true,
        reason: { en: 'Send alerts when a rule matches (effective in allowlist mode)', zh: '命中规则时发送告警（出口策略为白名单时生效）' },
        requires: 'plugin:grant:high'
      }
    ],
    diff: diff ?? null
  }
}

function anthropicReview(version: string): Any {
  return {
    plugin_key: 'anthropic',
    version,
    name: { en: 'Anthropic', zh: 'Anthropic' },
    publisher: 'sub2api',
    trust: 'official',
    signature_status: 'valid',
    host_compat_ok: true,
    host_compat: '>=0.1.0 <0.2.0',
    capabilities: [{ id: 'platform.adapter.v1' }],
    gateway_endpoints: anthropicManifest(version).gateway_endpoints,
    platforms: anthropicManifest(version).platforms,
    account_types: anthropicManifest(version).account_types,
    hooks: [],
    jobs: [{ id: 'refresh_oauth', schedule: '@every 10m' }],
    events: [],
    routes: [{ method: 'POST', path: '/oauth/callback', scope: 'webhook' }],
    menus: [],
    user_permissions: [],
    database: null,
    resources: { memoryMB: 256, cpu: 0.5 },
    external_services: ['api.anthropic.com', 'console.anthropic.com'],
    host_permissions: [
      { id: 'kv', risk: 'low' },
      { id: 'platform.register', risk: 'high', requires: 'plugin:grant:high' },
      { id: 'jobs', risk: 'medium' },
      { id: 'routes.webhook', risk: 'high', scope: { paths: ['/oauth/callback'] }, requires: 'plugin:grant:high' },
      { id: 'net', risk: 'high', scope: { domains: ['api.anthropic.com', 'console.anthropic.com'] }, requires: 'plugin:grant:high' }
    ],
    diff: version === '0.1.0' ? null : { added: [diffItem('jobs', 'medium'), diffItem('routes.webhook', 'high')], widened: [diffItem('net', 'high')], removed: [] }
  }
}

function genericReview(key: string, version: string, name: Any, publisher: string, trust: string): Any {
  // foo_platform declares the "foo" platform (bar_platform "bar"); other generic plugins use their key.
  const pid = key.replace(/_platform$/, '')
  return {
    plugin_key: key,
    version,
    name,
    publisher,
    trust,
    signature_status: trust === 'unsigned' ? 'unsigned' : 'valid',
    host_compat_ok: key !== 'legacy_tool',
    host_compat: key === 'legacy_tool' ? '>=0.0.1 <0.1.0' : '>=0.1.0',
    capabilities: [{ id: 'gateway.platform.v1' }, { id: 'platform.adapter.v1' }],
    gateway_endpoints: [{ method: 'POST', path: `/v1/${pid}/chat`, protocol: `${pid}.chat` }],
    platforms: [
      {
        id: pid,
        label: name,
        endpoints: [
          { method: 'POST', path: `/v1/${pid}/chat`, protocol: `${pid}.chat`, billing: 'usage' },
          { method: 'POST', path: `/v1/${pid}/tokens`, protocol: `${pid}.tokens`, billing: 'free' }
        ]
      }
    ],
    account_types: [
      // manifest-shaped platform entries are accepted too
      { id: 'apikey', label: { en: 'API key', zh: 'API Key' }, form_mode: 'native', platforms: [{ platform: pid }, 'openai'] }
    ],
    hooks: [],
    jobs: [],
    events: [],
    routes: [],
    menus: [],
    user_permissions: [],
    database: null,
    resources: { memoryMB: 64, cpu: 0.1 },
    external_services: [`api.${pid}.dev`],
    host_permissions: [
      { id: 'kv', risk: 'low' },
      { id: 'log', risk: 'low' },
      { id: 'platform.register', risk: 'high', requires: 'plugin:grant:high' },
      { id: 'gateway.endpoint', risk: 'high', requires: 'plugin:grant:high' },
      { id: 'net', risk: 'high', scope: { domains: ['api.foo.dev'] }, optional: true, requires: 'plugin:grant:high' }
    ]
  }
}

/** The role columns of a review (super admin first, built-in roles, then custom ones). */
const REVIEW_ROLES: Any[] = [
  { key: 'super_admin', name: { en: 'Super admin', zh: '超级管理员' }, builtin: true, superuser: true },
  { key: 'admin', name: { en: 'Admin', zh: '管理员' }, builtin: true, superuser: false },
  { key: 'operator', name: { en: 'Operator', zh: '运维' }, builtin: true, superuser: false },
  { key: 'supplier', name: { en: 'Supplier', zh: '供应商' }, builtin: true, superuser: false },
  { key: 'user', name: { en: 'User', zh: '普通用户' }, builtin: true, superuser: false },
  { key: 'vendor', name: { en: 'Vendor', zh: '供应商（自定义）' }, builtin: false, superuser: false },
  { key: 'readonly', name: { en: 'Read only', zh: '只读' }, builtin: false, superuser: false }
]
/** Roles an upgrade grants a low-risk permission without asking (install autoGrantRoles). */
const AUTO_GRANT_ROLES = new Set(['admin', 'operator', 'supplier'])

/**
 * Why the viewer may not name a role for a permission of the risk; '' when it
 * may. A non-super caller (manager@example.com) names the roles it manages,
 * whether or not it holds the new permission, but not its own admin role, so
 * those suggestions go to review (D2); a critical row says so (D3).
 */
function denial(who: Identity, role: Any, risk: string): string {
  if (role.superuser) return 'superuser_role'
  if (who.superuser) return ''
  if (risk === 'critical') return 'critical'
  return managesRole(who, role.key) ? '' : 'not_assignable'
}

/**
 * A review as the viewer sees it (install Review(actorID)): the matrix
 * columns and cells, and the role grants of the permissions the consent
 * creates. A cell starts at the suggestion, a renamed row's at the old key's
 * holders; one the viewer may not name keeps it and goes to review (D2, C2).
 * Who holds the old keys is left out for a viewer without role:read (C4):
 * the removed rows' holders, the revived and renamed rows' cell grants and
 * the inherited role grants.
 */
export function forViewer(r: Any, who: Identity): Any {
  const perms: Any[] = r.user_permissions ?? []
  if (!perms.length) return r
  const upgrade = !!r.diff
  const seesHolders = hasPerm(who, 'role:read')
  const created = (p: Any) => p.new && !p.revived
  const cellOf = (p: Any, role: Any) => {
    const reason = denial(who, role, p.risk)
    const grant: string[] = created(p) && !role.superuser ? ((p.renamed ? p.inherited : p.defaults)?.[role.key] ?? []) : []
    const hidden = (p.renamed || p.revived) && !seesHolders
    // Held for review all the same, but not told to a viewer the holders are hidden from (N10).
    const cell = { grant, nameable: !reason, ...(reason ? { reason } : {}), withheld: !hidden && !!reason && grant.length > 0 }
    if (hidden) delete (cell as Any).grant
    return cell
  }
  const user_permissions: Any[] = perms.map(({ inherited, ...p }) => ({ ...p, cells: Object.fromEntries(REVIEW_ROLES.map((role) => [role.key, cellOf({ ...p, inherited }, role)])) }))
  const new_role_grants = user_permissions.filter(created).flatMap((p) =>
    REVIEW_ROLES.flatMap((role) => {
      const c = p.cells[role.key]
      const needs = upgrade && (p.risk !== 'low' || !AUTO_GRANT_ROLES.has(role.key))
      if (p.renamed && !seesHolders) return []
      return (c.grant ?? []).map((scope: string) => ({ permission: `plugin.${r.plugin_key}:${p.key}`, role: role.key, scope, risk: p.risk, via: p.renamed ? 'inherit' : 'default', needs_consent: needs, nameable: c.nameable, withheld: c.withheld }))
    })
  )
  const role_columns = REVIEW_ROLES.map((role) => {
    const reason = denial(who, role, 'low')
    return { ...role, nameable: !reason, ...(reason ? { reason } : {}) }
  })
  const removed_user_permissions = seesHolders ? r.removed_user_permissions : r.removed_user_permissions?.map(({ holders: _, ...x }: Any) => x)
  return { ...r, user_permissions, removed_user_permissions, role_columns, new_role_grants, role_grants_need_consent: new_role_grants.some((g) => g.needs_consent) }
}

/**
 * Checks a consent's new_permission_roles (local key → role → true | scopes)
 * against the viewer's review; returns the error, or the grants held for
 * review (the defaults of cells the viewer may not name, overridden or not).
 */
export function settleRoleGrants(r: Any, who: Identity, overrides: Any): { error: Any } | { withheld: Any[] } {
  const v = forViewer(r, who)
  for (const [local, roles] of Object.entries<Any>(overrides ?? {})) {
    const p = v.user_permissions?.find((x: Any) => x.key === local && x.new && !x.revived)
    if (!p) return { error: invalid(`new_permission_roles.${local}`, 'not a permission this consent creates', 'unknown') }
    for (const [role, grant] of Object.entries<unknown>(roles ?? {})) {
      if (grant !== true && !(Array.isArray(grant) && grant.every((x) => typeof x === 'string'))) {
        return { error: invalid(`new_permission_roles.${local}.${role}`, 'a grant is true or a list of scopes') }
      }
      const c = p.cells[role]
      if (!c) return { error: invalid(`new_permission_roles.${local}`, 'unknown role', 'unknown') }
      if (c.reason === 'critical') return { error: fail(403, 'permission_denied', 'only the super admin grants critical permissions', { reason: 'critical', superuser: true, permissions: [local] }) }
      if (!c.nameable) return { error: fail(403, 'permission_denied', 'you may not name this role', { role }) }
    }
  }
  return { withheld: (v.new_role_grants ?? []).filter((g: Any) => g.withheld) }
}

export const reviews = new Map<string, Any>()
// guard: a first install (as after an uninstall) and the pending 0.2.0 upgrade.
reviews.set('guard@0.1.0', guardReview('0.1.0'))
// 0.2.0 asks for more tables and privileges than the db.core_read grant of 0.1.0 holds: a wider scope.
reviews.set('guard@0.2.0', guardReview('0.2.0', { added: [diffItem('net', 'high')], widened: [diffItem('db.core_read', 'critical')], removed: [] }))
reviews.set('foo_platform@1.0.0', genericReview('foo_platform', '1.0.0', { en: 'Foo Platform', zh: 'Foo 平台' }, 'foo-labs', 'community'))
reviews.set('anthropic@0.2.0', anthropicReview('0.2.0'))

// ------------------------------------------------------------------ market

const sources = [
  { id: 1, name: 'Official market', url: 'https://market.sub2api.dev/index.json', enabled: true },
  { id: 2, name: 'Community', url: 'https://example.com/s2a/index.json', enabled: true }
]

const market: Record<number, Any[]> = {
  1: [
    {
      key: 'anthropic',
      name: { en: 'Anthropic', zh: 'Anthropic' },
      description: { en: 'Claude API keys and OAuth accounts.', zh: 'Claude API Key 与 OAuth 账号。' },
      publisher: 'sub2api',
      trust: 'official',
      categories: ['platform'],
      versions: [
        { version: '0.1.0', host_compat: '>=0.1.0 <0.2.0', size: 9_830_400 },
        { version: '0.2.0', host_compat: '>=0.1.0 <0.2.0', size: 10_223_616 },
        // needs a newer core than HOST_VERSION
        { version: '0.3.0', host_compat: '>=0.2.0 <0.3.0', size: 10_485_760 }
      ]
    },
    {
      key: 'guard',
      name: { en: 'Guard', zh: '请求守卫' },
      description: { en: 'Request inspection and blocking.', zh: '请求检查与拦截。' },
      publisher: 'sub2api',
      trust: 'official',
      categories: ['security'],
      versions: [{ version: '0.1.0', host_compat: '>=0.1.0 <0.2.0', size: 7_340_032 }]
    },
    {
      key: 'openai',
      name: { en: 'OpenAI', zh: 'OpenAI' },
      description: { en: 'OpenAI API keys for the built-in openai platform.', zh: '内置 openai 平台的 OpenAI API Key。' },
      publisher: 'sub2api',
      trust: 'official',
      categories: ['platform'],
      versions: [{ version: '0.1.0', host_compat: '>=0.1.0 <0.2.0', size: 8_912_896 }]
    },
    {
      key: 'gemini',
      name: { en: 'Gemini', zh: 'Gemini' },
      description: { en: 'Google AI Studio API keys for the built-in gemini platform.', zh: '内置 gemini 平台的 Google AI Studio API Key。' },
      publisher: 'sub2api',
      trust: 'official',
      categories: ['platform'],
      versions: [{ version: '0.1.0', host_compat: '>=0.1.0 <0.2.0', size: 8_650_752 }]
    }
  ],
  2: [
    // Not installed: a first install from the market.
    {
      key: 'bar_platform',
      name: { en: 'Bar Platform', zh: 'Bar 平台' },
      description: { en: 'Community adapter for the Bar API.', zh: 'Bar API 的社区适配器。' },
      publisher: 'bar-labs',
      trust: 'community',
      categories: ['platform'],
      versions: [{ version: '1.0.0', host_compat: '>=0.1.0', size: 6_291_456 }]
    },
    {
      key: 'foo_platform',
      name: { en: 'Foo Platform', zh: 'Foo 平台' },
      description: { en: 'Community adapter for the Foo API.', zh: 'Foo API 的社区适配器。' },
      publisher: 'foo-labs',
      trust: 'community',
      categories: ['platform'],
      versions: [{ version: '1.0.0', host_compat: '>=0.1.0' }]
    },
    {
      key: 'legacy_tool',
      name: { en: 'Legacy tool', zh: '旧版工具' },
      description: { en: 'Only works with old hosts.', zh: '只兼容旧版核心。' },
      publisher: 'someone',
      trust: 'unsigned',
      versions: [{ version: '0.0.3', host_compat: '>=0.0.1 <0.1.0' }]
    }
  ]
}

export function reviewFor(key: string, version: string): Any {
  const existing = reviews.get(`${key}@${version}`)
  if (existing) return existing
  let r: Any
  if (key === 'guard') r = guardReview(version)
  else if (key === 'anthropic') r = anthropicReview(version)
  else {
    const m = Object.values(market)
      .flat()
      .find((x) => x.key === key)
    r = genericReview(key, version, m?.name ?? { en: key }, m?.publisher ?? 'unknown', m?.trust ?? 'unsigned')
  }
  reviews.set(`${key}@${version}`, r)
  return r
}

/** Registers an uploaded / downloaded version and returns its review. */
export function stageVersion(review: Any): Any {
  const key = review.plugin_key
  let p = plugins.get(key)
  if (!p) {
    p = {
      key,
      name: review.name,
      description: {},
      status: 'awaiting_consent',
      status_reason: '',
      active_version: null,
      desired_version: review.version,
      publisher: review.publisher,
      trust: review.trust,
      egress_policy: 'allow_all',
      resources: {},
      versions: [],
      grants: [],
      manifest: { key, version: review.version, publisher: review.publisher, hostCompat: review.host_compat, resources: review.resources },
      settings: null
    }
    plugins.set(key, p)
  }
  const v = p.versions.find((x) => x.version === review.version)
  if (v) v.consent_status = 'pending'
  else p.versions.push({ version: review.version, consent_status: 'pending' })
  reviews.set(`${key}@${review.version}`, review)
  return review
}

// ------------------------------------------------------------------ rollouts

const rollouts = new Map<string, MockRollout>()

function rolloutView(r: MockRollout): Any {
  const s = (Date.now() - r.started) / 1000
  const p = plugins.get(r.plugin_key)
  let phase: string
  let error = ''
  const nodeStates = NODES.map((n, i) => {
    if (r.action === 'disable') return s > 1 ? 'active' : 'pending'
    if (s < 1.5 + i * 1.5) return 'pending'
    if (r.failNode && i === 1) return 'failed'
    return s > 6 ? 'active' : 'ready'
  })
  if (r.cancelled) phase = 'cancelled'
  else if (nodeStates.includes('failed') && s > 3.5) {
    phase = 'failed'
    error = 'node-2: health check failed: plugin exited with status 2'
  } else if (r.action === 'disable') phase = s > 2 ? 'active' : 'activating'
  else if (s < 4.5) phase = 'preparing'
  else if (s < 6) phase = 'activating'
  else phase = 'active'

  if (p && (phase === 'active' || phase === 'failed' || phase === 'cancelled')) {
    if (phase === 'active') {
      if (r.action === 'disable') p.status = 'disabled'
      else {
        p.status = 'enabled'
        p.active_version = r.target_version
        p.desired_version = r.target_version
        if (p.key === 'guard') p.manifest = guardManifest(r.target_version)
        if (p.key === 'anthropic') p.manifest = anthropicManifest(r.target_version)
      }
    } else if (p.status === 'enabling') p.status = p.active_version ? 'disabled' : 'installed'
    else if (p.status === 'upgrading') {
      p.status = 'enabled'
      p.desired_version = p.active_version
    }
    if (phase === 'failed') p.status_reason = error
    // Plugin platforms exist only while their plugin is enabled.
    for (const pp of pluginPlatforms) if (pp.plugin_key === p.key) pp.enabled = p.status === 'enabled'
  }
  return {
    id: r.id,
    plugin_key: r.plugin_key,
    action: r.action,
    from_version: r.from_version,
    target_version: r.target_version,
    phase,
    coordinator: 'node-2',
    error,
    nodes: NODES.map((n, i) => ({ node_id: n, boot_id: `b00t${i}a7c9e1f2`, state: nodeStates[i], error: nodeStates[i] === 'failed' ? 'health check failed' : '' })),
    migrations: r.action === 'disable' || s < 1 ? [] : [{ id: '0002_stats.sql', node_id: 'node-2', duration_ms: 120 }]
  }
}

function startRollout(p: MockPlugin, action: MockRollout['action'], target: string) {
  const r: MockRollout = {
    id: ++rolloutSeq,
    plugin_key: p.key,
    action,
    from_version: p.active_version || '',
    target_version: target,
    started: Date.now(),
    cancelled: false,
    failNode: p.key === 'foo_platform'
  }
  rollouts.set(p.key, r)
  p.status_reason = ''
  p.status = action === 'upgrade' ? 'upgrading' : action === 'enable' ? 'enabling' : p.status
  if (action !== 'disable') p.desired_version = target
  return rolloutView(r)
}

// ------------------------------------------------------------------ views

/** The role grants a consent held back for a super admin's review (D2), by plugin, each with its version. */
export const reviewQueue = new Map<string, Any[]>()

function summary(p: MockPlugin): Any {
  const running = p.status === 'enabled' || p.status === 'upgrading'
  return {
    key: p.key,
    // Like the server (plugins.go): the manifest declares ui.settings.
    has_settings: !!p.settings,
    name: p.name,
    description: p.description,
    status: p.status,
    status_reason: p.status_reason,
    resource_conflicts: p.resource_conflicts || [],
    role_grants_to_review: reviewQueue.get(p.key) ?? [],
    builtin: !!p.builtin,
    active_version: p.active_version,
    desired_version: p.desired_version,
    publisher: p.publisher,
    trust: p.trust,
    node_summary: {
      total: NODES.length,
      states: p.refused_version ? { failed: NODES.length } : running ? { active: NODES.length } : { stopped: NODES.length }
    },
    refusal: p.refused_version ? refusalOf(p.key, p.refused_version) : null
  }
}

/** The nodes' refusal of a plugin version built for the older plugin contract. */
function refusalOf(key: string, version: string): Any {
  return {
    code: 'outdated_contract',
    version,
    host_version: HOST_VERSION,
    protocol_version: 2,
    first_core_version: FIRST_CORE_VERSION,
    message: `plugin ${key}@${version} was built for an older plugin protocol and cannot be loaded by this core (${HOST_VERSION}, plugin protocol 2); install a version built for core ${FIRST_CORE_VERSION} or later`
  }
}

function hooksOf(p: MockPlugin): Any[] {
  if (p.key !== 'guard') return []
  return [
    {
      point: 'gateway.request',
      id: 'inspect',
      order: 100,
      failure: 'open',
      timeout_ms: 300,
      needs: ['model', 'prompt_text'],
      stats: p.status === 'enabled' ? { calls: 12345, denied: 37, timeouts: 2, p99_ms: 4, breaker_open: false } : null
    }
  ]
}

function jobsOf(p: MockPlugin): Any[] {
  if (p.key === 'guard') {
    return [
      { id: 'rollup', scope: 'cluster', task: 'plugin.guard.rollup', schedule: '@every 5m', timeout_sec: 60, effective_schedule: '@every 5m0s', effective_timeout_sec: 60,
        last_run: { status: 'succeeded', node_id: 'node-2', started_at: now(-120), finished_at: now(-119.2), message: 'rolled up 1,204 rows' } },
      { id: 'cleanup', scope: 'cluster', task: 'plugin.guard.cleanup', schedule: '0 3 * * *', timeout_sec: 7200, effective_schedule: '0 3 * * *', effective_timeout_sec: 3600,
        adjusted: ['timeout 2h0m0s cut to 1h0m0s'],
        last_run: { status: 'continued', node_id: 'node-1', started_at: now(-40000), finished_at: now(-39996.8), message: 'continues in 1m0s' } }
    ]
  }
  if (p.key === 'anthropic') return [{ id: 'refresh_oauth', schedule: '@every 10m', last_run: null }]
  return []
}

function eventsOf(p: MockPlugin): Any {
  if (p.key !== 'guard') return { subscribe: [], cursor: null, backlog: 0, deadletters: [] }
  return {
    subscribe: ['usage.recorded'],
    cursor: 884201,
    backlog: 12,
    deadletters: [{ id: 17, event_id: 880012, event_type: 'usage.recorded', attempts: 5, error: 'handler returned: pq: deadlock detected', failed_at: now(-7200) }]
  }
}

function detailOf(p: MockPlugin): Any {
  const running = p.status === 'enabled' || p.status === 'upgrading' || p.status === 'enabling'
  return {
    ...summary(p),
    manifest: p.manifest,
    grants: p.grants,
    nodes: running
      ? NODES.map((n, i) => ({
          node_id: n,
          boot_id: `b00t${i}a7c9e1f2`,
          addr: `10.0.0.${11 + i}:7070`,
          last_heartbeat: now(-2 - i),
          state: { state: 'active', serving: p.active_version, instances: [{ version: p.active_version, state: 'ready', restarts: i }], pid: 4200 + i, memory_mb: 42 - i * 3, memory_limit_mb: p.resources.memory_mb ?? 128, cpu_percent: 3 - i, threads: 18, restart_reason: i ? 'oom: rss above limit' : '' }
        }))
      : [],
    hooks: hooksOf(p),
    jobs: jobsOf(p),
    events: eventsOf(p),
    resources: p.resources,
    egress_policy: p.egress_policy,
    versions: p.versions
  }
}

export function find(key: string): MockPlugin | undefined {
  return plugins.get(key)
}

export const notFound = (key: string) => fail(404, 'not_found', `plugin ${key} not found`)

// ------------------------------------------------------------------ routes

on('GET', '/plugins', (req) => paginate([...plugins.values()].map(summary), req.query))

// CONTRACTS §55.1: an uninstalled plugin's key stays with its publisher.
interface RetiredKeyRow { key: string; publisher: string; retired_at: string; accounts: number; schema: boolean }
const retiredKeys = new Map<string, RetiredKeyRow>([
  ['acme_tool', { key: 'acme_tool', publisher: 'acme', retired_at: '2026-10-08T09:30:00Z', accounts: 2, schema: true }]
])
on('GET', '/plugins/retired-keys', () => ({
  items: [...retiredKeys.values()].map((r) => ({ ...r, installed: plugins.has(r.key) }))
}))
on('POST', '/plugins/retired-keys/:key/release', (req) => {
  const r = retiredKeys.get(req.params.key)
  if (!r) return fail(404, 'not_found', 'the plugin key is not retired')
  if (plugins.has(r.key)) return fail(409, 'conflict', 'the plugin key is installed again; uninstall it first')
  const body = (req.body || {}) as { purge?: boolean; purge_accounts?: boolean }
  retiredKeys.delete(r.key)
  return { accounts_deleted: body.purge_accounts ? r.accounts : 0, kv_deleted: body.purge ? 1 : 0 }
})

on('GET', '/plugins/:key', (req) => {
  const p = find(req.params.key)
  return p ? detailOf(p) : notFound(req.params.key)
})

on('DELETE', '/plugins/:key', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  if (p.builtin) return fail(403, 'permission_denied', 'built-in plugins cannot be uninstalled', { reason: 'builtin' })
  if (p.status === 'enabled') return fail(409, 'conflict', 'disable the plugin before uninstalling it')
  plugins.delete(p.key)
  rollouts.delete(p.key)
  // CONTRACTS §14.3: accounts are kept (orphaned) unless purge_accounts=true.
  const purgeAccounts = req.query.purge_accounts === 'true'
  const deleted = uninstallPluginAccounts(p.key, purgeAccounts)
  retiredKeys.set(p.key, { key: p.key, publisher: p.publisher || '', retired_at: now(), accounts: pluginAccountCount(p.key), schema: req.query.purge !== 'true' })
  return { accounts_deleted: deleted }
})

on('GET', '/plugins/:key/database', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const guard = p.key === 'guard' && p.active_version
  return {
    prefix: `plg_${p.key}__`,
    role: `plg713988_${p.key}`,
    access: guard ? guardAccess(guard) : [],
    migrations: guard ? guardMigrations(guard).map((m) => ({ ...m, applied_at: now(-86400 * 3) })) : []
  }
})

/** The review queue of a version: confirmed (written to the roles, step-up) or rejected whole. */
function settleQueue(req: MockRequest, confirm: boolean) {
  const who = caller(req)
  if (!who.superuser && !hasPerm(who, ['plugin:install', 'role:define'])) return fail(403, 'permission_denied', 'plugin:install and role:define required')
  const all = reviewQueue.get(req.params.key) ?? []
  const mine = all.filter((g) => g.version === req.params.version)
  if (!mine.length) return fail(404, 'not_found', 'nothing to review for this version')
  if (confirm) for (const g of mine) addRoleKeys(g.role, [g.scope ? `${g.permission}:${g.scope}` : g.permission])
  const rest = all.filter((g) => g.version !== req.params.version)
  if (rest.length) reviewQueue.set(req.params.key, rest)
  else reviewQueue.delete(req.params.key)
  bumpAuthz()
  return { grants: mine, confirmed: confirm }
}
on('POST', '/plugins/:key/versions/:version/role-grants/confirm', stepUp('plugin.grant', (req) => settleQueue(req, true)))
on('POST', '/plugins/:key/versions/:version/role-grants/reject', (req) => settleQueue(req, false))

on('POST', '/plugins/:key/versions/:version/reject', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  p.versions = p.versions.filter((x) => x.version !== req.params.version)
  reviews.delete(`${p.key}@${req.params.version}`)
  if (!p.active_version && p.versions.length === 0) plugins.delete(p.key)
  return { ok: true }
})

on('POST', '/plugins/:key/enable', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  if (p.status !== 'installed' && p.status !== 'disabled') return fail(409, 'conflict', `cannot enable a plugin in state ${p.status}`)
  const target = p.active_version || p.versions.filter((v) => v.consent_status === 'approved').map((v) => v.version).pop() || p.desired_version || '0.0.0'
  return startRollout(p, 'enable', target)
})

on('POST', '/plugins/:key/disable', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  if (p.status !== 'enabled') return fail(409, 'conflict', `cannot disable a plugin in state ${p.status}`)
  return startRollout(p, 'disable', p.active_version || '')
})

on('POST', '/plugins/:key/upgrade', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const version = req.body?.version
  const v = p.versions.find((x) => x.version === version)
  if (!v || v.consent_status !== 'approved') return fail(409, 'conflict', 'version is not approved')
  return startRollout(p, 'upgrade', version)
})

on('GET', '/plugins/:key/rollouts/current', (req) => {
  const r = rollouts.get(req.params.key)
  return r ? rolloutView(r) : null
})

on('POST', '/plugins/:key/rollouts/:id/cancel', (req) => {
  const r = rollouts.get(req.params.key)
  if (!r || String(r.id) !== req.params.id) return fail(404, 'not_found', 'rollout not found')
  const v = rolloutView(r)
  if (v.phase !== 'preparing') return fail(409, 'conflict', 'rollout already passed the commit point')
  r.cancelled = true
  rolloutView(r)
  return { ok: true }
})

on('GET', '/plugins/:key/settings', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  return p.settings ?? { mode: 'none', values: {}, secret_fields: [] }
})

on('PUT', '/plugins/:key/settings', (req) => {
  const p = find(req.params.key)
  if (!p?.settings) return notFound(req.params.key)
  const values = { ...(req.body?.values || {}) }
  if (values.alert_cooldown_sec !== undefined && values.alert_cooldown_sec < 0) {
    return fail(400, 'invalid_argument', 'invalid settings', { fields: [{ field: 'values.alert_cooldown_sec', code: 'range', message: 'must be 0 or more' }] })
  }
  // "******" (or no value) keeps the stored secret, '' clears it; the mock stores it masked anyway.
  for (const k of p.settings.secret_fields ?? []) {
    if (values[k] === '******' || values[k] === undefined) values[k] = p.settings.values?.[k]
    else if (values[k]) values[k] = '******'
  }
  p.settings.values = values
  return p.settings
})

on('GET', '/plugins/:key/grants', (req) => {
  const p = find(req.params.key)
  return p ? p.grants : notFound(req.params.key)
})

on('DELETE', '/plugins/:key/grants/:permission', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const g = p.grants.find((x) => x.permission === req.params.permission)
  if (!g) return fail(404, 'not_found', 'grant not found')
  g.status = 'revoked'
  return noContent()
})

// The scope each grant had before it was first narrowed: what the manifest asks for, never exceeded.
const requestedScopes = new WeakMap<Any, Any | null>()

/** Whether scope is within requested (pkg.ScopeWithin): the same keys or fewer, each list a non-empty subset, the rest equal. */
function scopeWithin(scope: Any | null, requested: Any | null): boolean {
  if (!scope || !requested) return !scope || !Object.keys(scope).length
  return Object.entries(scope).every(([k, v]) => {
    const r = requested[k]
    if (Array.isArray(v) && Array.isArray(r)) return v.length > 0 && v.every((x) => r.includes(x))
    return JSON.stringify(v) === JSON.stringify(r)
  })
}

// Narrows a host permission grant, or grants a revoked one again (body {scope?}; none: the scope requested).
on('PUT', '/plugins/:key/grants/:permission', stepUp('plugin.grant', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const g = p.grants.find((x) => x.permission === req.params.permission)
  if (!g) return fail(404, 'not_found', 'the active version does not request this permission')
  // Granting again is an install's right; narrowing is not.
  if ((g.status === 'revoked' || g.status === 'denied') && !hasPerm(caller(req), 'plugin:install')) {
    return fail(403, 'permission_denied', 'plugin:install required to grant again', { permission: 'plugin:install' })
  }
  if (!requestedScopes.has(g)) requestedScopes.set(g, g.scope)
  const requested = requestedScopes.get(g) ?? null
  const scope = req.body?.scope ?? requested
  if (!scopeWithin(scope, requested)) return invalid('scope', 'the scope is wider than the manifest requests', 'scope_too_wide')
  Object.assign(g, { scope, status: 'active', granted_at: now() })
  return g
}))

on('PUT', '/plugins/:key/resources', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const b = req.body || {}
  if (b.memory_mb && b.memory_mb > 2048) {
    return fail(400, 'invalid_argument', 'above global limit', { fields: [{ field: 'memory_mb', code: 'max', message: 'global limit is 2048 MB' }] })
  }
  p.resources = { ...b }
  // CONTRACTS §14.3: enabled plugins restart their instances on every node.
  const running = p.status === 'enabled' || p.status === 'enabling' || p.status === 'upgrading'
  return {
    ...p.resources,
    restart_notified: true,
    message: running
      ? { en: 'Resource limits saved. Every node has been notified to restart the plugin instances with the new limits.', zh: '资源限制已保存，已通知各节点按新限制重启插件实例。' }
      : { en: 'Resource limits saved. They apply when the plugin is enabled.', zh: '资源限制已保存，将在启用插件时生效。' }
  }
})

on('PUT', '/plugins/:key/egress-policy', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const policy = req.body?.policy
  if (policy !== 'allow_all' && policy !== 'allowlist') return fail(400, 'invalid_argument', 'policy must be allow_all or allowlist')
  p.egress_policy = policy
  return { policy }
})

on('GET', '/plugins/:key/egress', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  const hosts: Array<[string, number, string]> =
    p.key === 'guard'
      ? [['hooks.example.com', 443, 'ok'], ['evil.example.net', 443, 'denied'], ['telemetry.new-vendor.io', 443, 'ok']]
      : [['api.anthropic.com', 443, 'ok'], ['console.anthropic.com', 443, 'ok'], ['statsig.anthropic.com', 443, 'ok']]
  const items: Any[] = []
  for (let i = 0; i < 40; i++) {
    const [host, port, result] = hosts[i % 7 === 0 ? 1 : i % 11 === 3 ? 2 : 0]
    // The two newest connections are still open (CONTRACTS §14.2: logged when opened).
    const open = i < 2
    items.push({
      id: 9000 + i,
      node_id: NODES[i % 2],
      network: 'tcp',
      host,
      port,
      started_at: now(-i * 600 - 5),
      duration_ms: open ? 0 : 80 + ((i * 37) % 400),
      bytes_in: open ? 0 : 2048 + i * 13,
      bytes_out: open ? 0 : 900 + i * 7,
      result: open ? 'open' : i === 5 ? 'error' : result,
      error: i === 5 ? 'dial tcp: i/o timeout' : '',
      closed_at: open ? null : now(-i * 600)
    })
  }
  if (p.key !== 'guard') {
    // A request the core was to send for the plugin, refused by plugin network access (CONTRACTS §113).
    items.push({
      id: 8999, node_id: NODES[0], network: 'https', host: 'relay.example.org', port: 443, started_at: now(-905),
      duration_ms: 0, bytes_in: 0, bytes_out: 0, result: 'denied', closed_at: null,
      error: 'relay.example.org is not on the allowed list of plugin network access'
    })
  }
  const from = req.query.from ? new Date(req.query.from).getTime() : 0
  const inRange = items.filter((x) => new Date(x.started_at).getTime() >= from)
  const summary = new Map<string, Any>()
  for (const l of inRange) {
    const k = `${l.host}:${l.port}`
    const s = summary.get(k) || { host: l.host, port: l.port, count: 0, ok: 0, denied: 0, errors: 0, open: 0, bytes_in: 0, bytes_out: 0, last_at: l.started_at }
    s.count++
    if (l.result === 'ok') s.ok++
    else if (l.result === 'denied') s.denied++
    else if (l.result === 'open') s.open++
    else s.errors++
    s.bytes_in += l.bytes_in
    s.bytes_out += l.bytes_out
    if (l.started_at > s.last_at) s.last_at = l.started_at
    summary.set(k, s)
  }
  // Every host ever seen (not limited to the range); `new` = first seen within 24h.
  const firstSeen: Record<string, number> = {
    'hooks.example.com': 86400 * 30,
    'evil.example.net': 86400 * 12,
    'telemetry.new-vendor.io': 3 * 3600,
    'api.anthropic.com': 86400 * 60,
    'console.anthropic.com': 86400 * 60,
    'statsig.anthropic.com': 40 * 60
  }
  const domains = hosts.map(([host]) => {
    const own = items.filter((x) => x.host === host)
    const age = firstSeen[host] ?? 86400
    return {
      host,
      first_seen_at: now(-age),
      last_seen_at: own[0]?.started_at ?? now(-age),
      connections: own.length + Math.floor(age / 3600),
      new: age < 86400
    }
  })
  return { from: req.query.from ?? null, to: req.query.to ?? null, summary: [...summary.values()].sort((a, b) => b.count - a.count), domains, items: inRange }
})

on('GET', '/plugins/:key/jobs', (req) => {
  const p = find(req.params.key)
  // Same shape as the core: {jobs, recent_runs}.
  return p ? { jobs: jobsOf(p), recent_runs: [] } : notFound(req.params.key)
})

on('POST', '/plugins/:key/jobs/:job_id/run', (req) => {
  const p = find(req.params.key)
  if (!p) return notFound(req.params.key)
  if (!jobsOf(p).some((j) => j.id === req.params.job_id)) return fail(404, 'not_found', 'job not found')
  return { ok: true }
})

on('GET', '/plugins/:key/events', (req) => {
  const p = find(req.params.key)
  return p ? eventsOf(p) : notFound(req.params.key)
})

on('GET', '/market/sources', () => sources)

on('GET', '/market/plugins', (req) => {
  const id = Number(req.query.source_id || 1)
  const list = (market[id] || []).map((m) => ({
    ...m,
    installed_version: plugins.get(m.key)?.active_version ?? undefined,
    latest_version: m.versions[m.versions.length - 1]?.version,
    versions: m.versions.map((v: Any) => ({ ...v, compatible: satisfies(HOST_VERSION, v.host_compat) }))
  }))
  // host_version sits next to data and page (CONTRACTS §14.3).
  return { ...paginate(list, req.query), host_version: HOST_VERSION }
})

/** Minimal semver range check: space-separated >=, >, <=, <, = comparators. */
function satisfies(version: string, range: string | undefined): boolean {
  if (!range) return true
  const cmp = (a: string, b: string) => {
    const pa = a.split('.').map(Number)
    const pb = b.split('.').map(Number)
    for (let i = 0; i < 3; i++) if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) - (pb[i] || 0)
    return 0
  }
  return range
    .trim()
    .split(/\s+/)
    .every((c) => {
      const m = /^(>=|<=|>|<|=)?v?(\d+(?:\.\d+){0,2})$/.exec(c)
      if (!m) return true
      const d = cmp(version, m[2])
      switch (m[1]) {
        case '>=':
          return d >= 0
        case '>':
          return d > 0
        case '<=':
          return d <= 0
        case '<':
          return d < 0
        default:
          return d === 0
      }
    })
}
