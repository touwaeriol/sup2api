import { readFileSync } from 'node:fs'
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from './router'
import { activePlatforms, platformById, platformLabel } from './platforms'
import { agentUser, caller, filterOwned, hasAction, hasPerm, inScope, ownerScope, stepUp, userEmail, type Identity } from './core'
import { resolveProxyURL, validateProxyURL } from './proxies'
import { nextPath } from './paths'
import { codexAccountTypes, codexCredentialErrors } from './codex'
import { groupServedProtocols, protocolFieldErrors, servedProtocols, typeEndpoints } from './protocols'
import { checkControls, controlsAccountTypes } from './controls'
import { placeholderProblem, rowMeaning } from '../src/views/accounts/headerOverride/model'

// Mock handlers: accounts, account types (form modes native + iframe) and
// GET /me/platforms (CONTRACTS §14.1). Scopes (CONTRACTS §21): a narrower
// scope only reaches the accounts in it (own: those the caller created); sign
// in as vendor@example.com to try it (see core.ts).

/** The caller's reach for an account action, e.g. 'read' (account:read at any scope). */
function accountScope(req: MockRequest, action: 'read' | 'create' | 'update' | 'delete' | 'test' | 'credential:view') {
  const who = caller(req)
  return { who, scope: ownerScope(who, `account:${action}`) }
}

/** Guarded settings (CONTRACTS §21.3): base_url of the built-in types is limited to the official address. */
const GUARDED: Record<string, Array<{ field: string; allowed: string[] }>> = {
  anthropic: [{ field: 'base_url', allowed: ['https://api.anthropic.com'] }],
  openai: [{ field: 'base_url', allowed: ['https://api.openai.com'] }],
  gemini: [{ field: 'base_url', allowed: ['https://generativelanguage.googleapis.com'] }]
}

const normURL = (s: unknown) => {
  const v = String(s ?? '').trim().replace(/\/+$/, '')
  return v.replace(/^([a-z]+:\/\/)([^/]+)/i, (_m, scheme: string, host: string) => scheme.toLowerCase() + host.toLowerCase())
}

/** 400 forbidden on a guarded field the caller may not change; null when fine. */
function checkGuarded(who: Identity, pluginKey: string, creds: Record<string, any> | undefined, prev?: Record<string, any>) {
  if (!creds || hasPerm(who, 'account:settings:custom')) return null
  const fields: Array<{ field: string; code: string; message: string }> = []
  for (const g of GUARDED[pluginKey] || []) {
    const v = normURL(creds[g.field])
    if (!v) continue
    if (g.allowed.some((a) => normURL(a) === v)) continue
    if (prev && normURL(prev[g.field]) === v) continue
    fields.push({ field: `credentials.${g.field}`, code: 'forbidden', message: `Only ${g.allowed.join(', ')} is allowed` })
  }
  return fields.length ? fail(400, 'invalid_argument', 'restricted setting', { fields }) : null
}

interface MockAccountType {
  plugin_key: string
  plugin_name: { en: string; zh: string }
  plugin_version: string
  asset_base: string
  trust: string
  type: string
  label: { en: string; zh: string }
  description: { en: string; zh: string }
  form: { mode: string; page?: string; component?: string }
  sensitive_fields: string[]
  creation_group?: string
  auth_method_label?: { en: string; zh: string }
  /** Declared supported platforms (built-in or plugin platforms). */
  supports: string[]
  /** Protocols of the supported platforms served natively (absent: all); the other text ones are adapted (mock/protocols.ts). */
  subset?: string[]
  default_models?: string[]
  default_model_mapping?: Record<string, string>
  default_limits?: Record<string, unknown>
  /** Declared common controls (mock/controls.ts); others come from the plugin manifest. */
  common_controls?: Record<string, any>
}


// Account types (CONTRACTS §13): identified by (plugin_key, type); each
// declares the platforms it supports. GET /account-types adds `platforms`
// (with availability) and `endpoints` (native, adapted, converted: mock/protocols.ts).
const accountTypeDecls: MockAccountType[] = [
  {
    plugin_key: 'anthropic',
    plugin_name: { en: 'Anthropic', zh: 'Anthropic' },
    plugin_version: '0.1.0',
    asset_base: '/plugin-ui/anthropic/0.1.0-dev',
    trust: 'official',
    type: 'apikey',
    label: { en: 'API Key', zh: 'API Key' },
    description: { en: 'Claude API key (x-api-key)', zh: 'Claude API 密钥' },
    form: { mode: 'native', component: 'AnthropicKeyForm' },
    sensitive_fields: ['api_key'],
    supports: ['anthropic']
  },
  {
    plugin_key: 'openai',
    plugin_name: { en: 'OpenAI', zh: 'OpenAI' },
    plugin_version: '0.1.0',
    asset_base: '/plugin-ui/openai/0.1.0-dev',
    trust: 'official',
    type: 'apikey',
    label: { en: 'API Key', zh: 'API Key' },
    description: { en: 'OpenAI API key (Authorization: Bearer)', zh: 'OpenAI API 密钥' },
    form: { mode: 'native', component: 'OpenAIKeyForm' },
    sensitive_fields: ['api_key'],
    supports: ['openai']
  },
  {
    plugin_key: 'gemini',
    plugin_name: { en: 'Gemini', zh: 'Gemini' },
    plugin_version: '0.1.0',
    asset_base: '/plugin-ui/gemini/0.1.0-dev',
    trust: 'official',
    type: 'apikey',
    label: { en: 'API Key', zh: 'API Key' },
    description: { en: 'Google AI Studio API key (x-goog-api-key)', zh: 'Google AI Studio API 密钥' },
    form: { mode: 'native', component: 'GeminiKeyForm' },
    sensitive_fields: ['api_key'],
    supports: ['gemini']
  },
  {
    plugin_key: 'relay',
    plugin_name: { en: 'Relay', zh: '中转' },
    plugin_version: '0.3.0',
    asset_base: '/plugin-ui/relay/0.3.0-dev',
    trust: 'verified',
    type: 'relay_key',
    label: { en: 'Relay key', zh: '中转 Key' },
    description: { en: 'Key of an Anthropic-compatible relay', zh: 'Anthropic 兼容中转站的 Key' },
    form: { mode: 'native', component: 'RelayKeyForm' },
    sensitive_fields: ['api_key'],
    supports: ['anthropic']
  },
  {
    plugin_key: 'videogen',
    plugin_name: { en: 'Video generation', zh: '视频生成' },
    plugin_version: '0.2.0',
    asset_base: '/plugin-ui/videogen/0.2.0-dev',
    trust: 'verified',
    type: 'video_key',
    label: { en: 'MyVideo key', zh: 'MyVideo Key' },
    description: { en: 'Serves the myvideo platform declared by the same plugin', zh: '服务本插件声明的 myvideo 平台' },
    form: { mode: 'native', component: 'VideoKeyForm' },
    sensitive_fields: ['api_key'],
    supports: ['myvideo']
  },
  {
    plugin_key: 'demo',
    plugin_name: { en: 'Demo platform', zh: '演示平台' },
    plugin_version: '0.0.1',
    asset_base: '/plugin-ui/demo/0.0.1-dev',
    trust: 'community',
    type: 'token',
    label: { en: 'Token (iframe form)', zh: 'Token（iframe 表单）' },
    description: { en: 'Sandboxed iframe account form; supports the foo platform (plugin not enabled) and openai', zh: '沙箱 iframe 账号表单；支持 foo 平台（插件未启用）和 openai' },
    form: { mode: 'iframe', page: 'ui/iframe/account.html' },
    sensitive_fields: ['token'],
    supports: ['foo', 'openai']
  },
  // Native account form (plugins/codex/ui/native, mock/codex.ts): one creation card, two auth methods.
  ...codexAccountTypes,
  // Declared common controls and an editor section (mock/controls.ts).
  ...controlsAccountTypes
]

function accountTypeOut(d: MockAccountType) {
  const { supports, subset, ...rest } = d
  const defaults = pluginDefaults(d.plugin_key, d.type)
  const platforms = supports.map((id) => {
    const p = platformById(id)
    return { id, label: p?.label ?? platformLabel(id), builtin: p?.builtin ?? false, available: !!p }
  })
  return { ...rest, ...defaults, guarded_settings: GUARDED[d.plugin_key], common_controls: d.common_controls ?? defaults.common_controls, platforms, endpoints: typeEndpoints(supports.filter((id) => platformById(id)), subset) }
}

/** The type of an account as GET /account-types lists it; undefined for an orphaned account. */
const typeOutOf = (a: { plugin_key: string; type: string }) => {
  const d = accountTypeDecls.find((x) => x.plugin_key === a.plugin_key && x.type === a.type)
  return d && accountTypeOut(d)
}
/** served_protocols of an account ([] when its type is gone). */
const servedOf = (a: any) => {
  const at = a.orphaned ? undefined : typeOutOf(a)
  return at ? servedProtocols(a, at) : []
}
/** Field errors of the protocol binding a request leaves the account with; null when it sets neither field or they are fine. */
function validateProtocols(body: Record<string, any>, a: { plugin_key: string; type: string; protocols?: string[] | null; model_protocols?: any[] }) {
  if (!('protocols' in body) && !('model_protocols' in body)) return null
  const at = typeOutOf(a)
  if (!at) return fail(503, 'plugin_unavailable', 'the plugin providing this account type is not enabled; its protocols cannot be changed')
  const fields = protocolFieldErrors({ protocols: 'protocols' in body ? body.protocols : a.protocols, model_protocols: body.model_protocols ?? a.model_protocols }, at)
  return fields.length ? fail(400, 'invalid_argument', 'invalid protocols', { fields }) : null
}

/**
 * Default models / mapping (CONTRACTS §41), read from the real plugin
 * manifest so the mock never drifts from what the plugins ship; types
 * without a plugin source in this repository get none.
 */
function pluginDefaults(pluginKey: string, type: string) {
  // Plugin directories use dashes where keys use underscores (azure_openai -> plugins/azure-openai).
  for (const dir of new Set([pluginKey, pluginKey.replace(/_/g, '-')])) {
    try {
      const m = JSON.parse(readFileSync(nextPath('plugins', dir, 'manifest.json'), 'utf8'))
      const at = (m.accountTypes || []).find((x: any) => x.id === type)
      return { default_models: at?.defaultModels || [], default_model_mapping: at?.defaultModelMapping || {}, common_controls: at?.commonControls || {} }
    } catch {
      // try the next directory name
    }
  }
  return { default_models: [], default_model_mapping: {}, common_controls: {} }
}

function applyCommonControls(pluginKey: string, type: string, body: Record<string, any>, current?: Record<string, any>) {
  const declared = accountTypeDecls.find((x) => x.plugin_key === pluginKey && x.type === type)?.common_controls
  if (declared) {
    const bad = checkControls(declared, body, current)
    return bad ? fail(400, 'invalid_argument', 'Invalid common control', { fields: [{ ...bad, message: bad.code === 'required' ? 'This account type needs a proxy' : 'This account type does not allow changing this field' }] }) : undefined
  }
  const controls = pluginDefaults(pluginKey, type).common_controls
  for (const field of ['auto_disable', 'schedulable']) {
    const control = controls[field]
    const expected = current ? current[field] ?? true : control?.default ?? true
    if (control?.configurable === false && body[field] != null && body[field] !== expected) return fail(400, 'invalid_argument', 'Read-only account control', { fields: [{ field, code: 'read_only', message: 'This account type does not allow changing this control' }] })
    if (body[field] == null) {
      if (current) delete body[field]
      else body[field] = expected
    }
  }
}

const typeLabel = (pluginKey: string, type: string) => accountTypeDecls.find((x) => x.plugin_key === pluginKey && x.type === type)?.label || type

// claude-main is in three groups (the list shows two and "+1"); with claude-bak
// it makes haiku-only a group of one platform, whose badge takes the vendor tint.
// Protocol binding (mock/protocols.ts): openai-main converts one model for
// Anthropic clients and serves the embedding model on its own protocol only;
// demo-token, a chat-only compatible site, opens one of its own protocols.
export const accounts: any[] = [
  { id: 12, name: 'claude-main', header_override: { 'User-Agent': 'claude-cli/2.1.90 (external, cli)', 'anthropic-beta': '{client_header:anthropic-beta:context-1m-2025-08-07}', 'x-trace-id': '{client_header:x-request-id}', 'x-stainless-retry-count': '{unset}' }, param_override: { operations: [
    { description: '固定 temperature', mode: 'set', path: 'temperature', value: 0.7 },
    { mode: 'delete', path: 'top_p' },
    { description: 'claude- 模型设置 max_tokens', mode: 'set', path: 'max_tokens', value: 8192, keep_origin: true, logic: 'OR', conditions: [{ path: 'model', mode: 'prefix', value: 'claude-' }] },
    { description: 'anthropic-beta 去掉 1M 上下文', mode: 'set_header', path: 'anthropic-beta', value: { 'context-1m-2025-08-07': null } },
    { mode: 'return_error', value: { message: '该账号不提供 opus', status_code: 400 }, logic: 'OR', conditions: [{ path: 'model', mode: 'contains', value: 'opus' }] }
  ] }, plugin_key: 'anthropic', type: 'apikey', created_by: 1, group_ids: [1, 2, 3], proxy_id: null, priority: 1, weight: 3, max_concurrency: 10, schedulable: true, models: ['claude-sonnet-4-5', 'claude-haiku-4-5', 'claude-opus-4-1'], model_mapping: { 'claude-3-5-sonnet-latest': 'claude-sonnet-4-5' }, rpm_limit: 60, tpm_limit: 100000, spm_limit: 8, spm_window_seconds: 300, model_limits: [{ models: ['claude-opus-4-1'], max_concurrency: 2, rpm_limit: 10, tpm_limit: 0, spm_limit: 2, spm_window_seconds: 3600 }, { models: ['claude-haiku-4-5', 'claude-sonnet-4-5'], max_concurrency: 0, rpm_limit: 0, tpm_limit: 50000 }], status: 'active', status_reason: '', in_use: 3, cooldown_until: null, orphaned: false, last_used_at: now(-12), created_at: now(-86400 * 20), credentials: { api_key: '******', base_url: 'https://api.anthropic.com' } },
  { id: 13, name: 'claude-bak', plugin_key: 'anthropic', type: 'apikey', created_by: 6, group_ids: [1, 3], proxy_id: 1, priority: 2, weight: 1, max_concurrency: 10, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: now(600), cooldown_reason: '429', orphaned: false, last_used_at: now(-300), created_at: now(-86400 * 10), credentials: { api_key: '******', base_url: 'https://api.anthropic.com' } },
  { id: 14, name: 'old-key', plugin_key: 'anthropic', type: 'apikey', created_by: 1, group_ids: [1], proxy_id: null, priority: 5, weight: 1, max_concurrency: 10, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'disabled', status_reason: '401 invalid credentials', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: now(-86400), created_at: now(-86400 * 40), credentials: { api_key: '******' } },
  { id: 16, name: 'relay-1', plugin_key: 'relay', type: 'relay_key', created_by: 6, group_ids: [1], proxy_id: null, priority: 3, weight: 1, max_concurrency: 20, schedulable: true, models: [], model_mapping: { 'claude-opus-4-1': 'claude-sonnet-4-5' }, rpm_limit: 120, tpm_limit: 0, spm_limit: 20, spm_window_seconds: 3600, status: 'active', status_reason: '', in_use: 1, cooldown_until: null, orphaned: false, last_used_at: now(-40), created_at: now(-86400 * 2), credentials: { api_key: '******', base_url: 'https://relay.example.com' } },
  { id: 17, name: 'video-1', plugin_key: 'videogen', type: 'video_key', created_by: 1, group_ids: [2], proxy_id: null, priority: 1, weight: 1, max_concurrency: 4, schedulable: true, models: ['myvideo-pro'], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: now(-3600), created_at: now(-86400 * 3), credentials: { api_key: '******', region: 'us' } },
  { id: 18, name: 'demo-token', plugin_key: 'demo', type: 'token', protocols: ['openai.chat'], created_by: 1, group_ids: [1], proxy_id: null, priority: 8, weight: 1, max_concurrency: 2, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: null, created_at: now(-86400), credentials: { token: '******' } },
  { id: 19, name: 'openai-main', plugin_key: 'openai', type: 'apikey', created_by: 1, group_ids: [1, 2], proxy_id: null, priority: 1, weight: 2, max_concurrency: 20, schedulable: true, models: ['gpt-4o', 'gpt-4o-mini', 'text-embedding-3-large', 'claude-sonnet-4-5'], model_mapping: { 'claude-sonnet-4-5': 'gpt-4o' }, model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }, { models: ['text-embedding-3-large'], protocols: ['openai.embeddings'] }], rpm_limit: 0, tpm_limit: 200000, status: 'active', status_reason: '', in_use: 2, cooldown_until: null, orphaned: false, last_used_at: now(-30), created_at: now(-86400 * 2), credentials: { api_key: '******', base_url: 'https://api.openai.com' } },
  { id: 20, name: 'gemini-main', plugin_key: 'gemini', type: 'apikey', created_by: 1, group_ids: [2], proxy_id: null, priority: 1, weight: 1, max_concurrency: 10, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: now(-900), created_at: now(-86400), credentials: { api_key: '******', base_url: 'https://generativelanguage.googleapis.com' } },
  // More Claude API keys: one cooling down after a 429, one not used yet.
  { id: 21, name: 'claude-key-1', plugin_key: 'anthropic', type: 'apikey', created_by: 1, group_ids: [1, 2], proxy_id: 1, priority: 1, weight: 2, max_concurrency: 5, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 1, cooldown_until: null, orphaned: false, last_used_at: now(-8), created_at: now(-86400 * 12), credentials: { api_key: '******' } },
  { id: 22, name: 'claude-key-2', plugin_key: 'anthropic', type: 'apikey', created_by: 1, group_ids: [1], proxy_id: null, priority: 1, weight: 1, max_concurrency: 5, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: now(5400), cooldown_reason: '429 rate_limit_error', orphaned: false, last_used_at: now(-600), created_at: now(-86400 * 30), credentials: { api_key: '******' } },
  { id: 23, name: 'claude-key-3', plugin_key: 'anthropic', type: 'apikey', created_by: 6, group_ids: [2], proxy_id: null, priority: 2, weight: 1, max_concurrency: 3, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: now(-95), created_at: now(-86400 * 6), credentials: { api_key: '******' } },
  { id: 24, name: 'claude-key-new', plugin_key: 'anthropic', type: 'apikey', created_by: 1, group_ids: [1], proxy_id: null, priority: 3, weight: 1, max_concurrency: 5, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: null, created_at: now(-600), credentials: { api_key: '******' } },
  // ChatGPT subscriptions (codex plugin, native form): device code and Sign in with Codex; edit one to see "Re-authorize".
  { id: 29, name: 'chatgpt-plus', plugin_key: 'codex', type: 'codex_device', created_by: 1, group_ids: [1], proxy_id: 1, priority: 2, weight: 1, max_concurrency: 3, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'auto_disabled', status_reason: 'Codex usage limit reached', auto_disabled_until: now(2 * 3600 + 1200), in_use: 0, cooldown_until: now(2 * 3600 + 1200), orphaned: false, last_used_at: now(-1200), created_at: now(-86400 * 5), credentials: { access_token: '******', refresh_token: '******', id_token: '******', expires_at: Math.floor(Date.now() / 1000) + 2400, email: 'owner@example.com', chatgpt_account_id: '5d1c2b9e-7f3a-4e1b-9a0c-2f6d8e4b1a73', plan_type: 'plus' } },
  { id: 30, name: 'chatgpt-codex', plugin_key: 'codex', type: 'codex_oauth', created_by: 1, group_ids: [1], proxy_id: null, priority: 3, weight: 1, max_concurrency: 3, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: null, created_at: now(-86400), credentials: { access_token: '******', refresh_token: '******', id_token: '******', expires_at: Math.floor(Date.now() / 1000) - 600, email: 'owner@example.com', chatgpt_account_id: '8b0e6f2a-1c4d-4e9b-a7f3-5d2c9e1b0a64', plan_type: 'pro' } },
  // Automatically disabled until a release time (yellow badge); the list switch reads off and turning it on enables it by hand.
  { id: 31, name: 'relay-limited', plugin_key: 'relay', type: 'relay_key', created_by: 1, group_ids: [1], proxy_id: null, priority: 4, weight: 1, max_concurrency: 10, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'auto_disabled', status_reason: 'matched auto-disable rule: status 429', auto_disabled_until: now(3 * 3600), in_use: 0, cooldown_until: null, orphaned: false, last_used_at: now(-1800), created_at: now(-86400 * 8), credentials: { api_key: '******', base_url: 'https://relay.example.com' } },
  // Balance (CONTRACTS §52): relay-new was never read; relay-badkey's first read failed.
  { id: 32, name: 'relay-new', plugin_key: 'relay', type: 'relay_key', created_by: 1, group_ids: [1], proxy_id: null, priority: 5, weight: 1, max_concurrency: 10, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: null, created_at: now(-120), credentials: { api_key: '******', base_url: 'https://relay.example.com' } },
  { id: 33, name: 'relay-badkey', plugin_key: 'relay', type: 'relay_key', created_by: 1, group_ids: [1], proxy_id: null, priority: 6, weight: 1, max_concurrency: 10, schedulable: true, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, cooldown_until: null, orphaned: false, last_used_at: null, created_at: now(-3600), credentials: { api_key: '******', base_url: 'https://relay.example.com' } },
  // Its plugin was uninstalled without purge_accounts: kept as an orphaned account.
  { id: 15, name: 'legacy-vendor', plugin_key: 'legacy_vendor', type: 'apikey', created_by: null, type_label: { en: 'API key', zh: 'API Key' }, group_ids: [], proxy_id: null, priority: 10, weight: 1, max_concurrency: 5, schedulable: false, models: [], model_mapping: {}, rpm_limit: 0, tpm_limit: 0, status: 'active', status_reason: '', in_use: 0, orphaned: true, created_at: now(-86400 * 90) }
]
for (const a of accounts) {
  a.type_label ??= typeLabel(a.plugin_key, a.type)
  // The owner (所属, CONTRACTS §103.4) starts as the creator, as migration 0103 fills it.
  a.owner_id ??= a.created_by
}
// One account an administrator gave the agent (agent@example.com), so its creator is not its owner.
accounts.find((a) => a.id === 17).owner_id = agentUser.id
// Latest tests (last_test): fast, slow, failed; the others were never tested.
const seedTest = (id: number, ok: boolean, latency: number, model: string, message: string, ago: number) => {
  const a = accounts.find((x) => x.id === id)
  if (a) a.last_test = { at: now(-ago), ok, latency_ms: latency, model, message }
}
seedTest(12, true, 640, 'claude-haiku-4-5', 'model claude-haiku-4-5 answered', 1800)
seedTest(13, true, 3820, 'claude-sonnet-4-5', 'model claude-sonnet-4-5 answered', 7200)
seedTest(14, false, 212, 'claude-haiku-4-5', 'authentication_error: invalid x-api-key', 86400)
seedTest(19, true, 1450, 'gpt-4o-mini', 'model gpt-4o-mini answered', 600)

// ---------------------------------------------------------------- subscription quota
// Plan windows of subscription accounts (QuotaSnapshot). Types without plan
// limits (API keys, relays, ...) report quota = null. A window resets every
// `period` seconds, first at `firstIn` seconds after the mock started.
// No type here declares a usage query: codex samples its response headers,
// so force returns the snapshot (CONTRACTS §44).
const QUOTA_TYPES = new Set(['codex/codex_device', 'codex/codex_oauth'])
const MOCK_T0 = Date.now()
const H = 3600
const D = 86400
type QuotaSeed = { key: string; utilization: number; status: string; firstIn: number; period: number }
interface QuotaState {
  source: 'passive' | 'active' | ''
  updated_at: string | null
  error: string
  windows: QuotaSeed[]
}
const quotaState = new Map<number, QuotaState>([
  // ChatGPT (Codex), sampled from the x-codex-* response headers: #29's 5 h window used up, the account paused until it resets (mock/codex.ts resets it).
  [29, { source: 'passive', updated_at: now(-300), error: '', windows: [
    { key: '5h', utilization: 100, status: '', firstIn: 2 * H + 1200, period: 5 * H },
    { key: '7d', utilization: 63, status: '', firstIn: 3 * D + 9 * H, period: 7 * D }
  ] }],
  [30, { source: 'passive', updated_at: now(-900), error: '', windows: [
    { key: '5h', utilization: 18, status: '', firstIn: 3 * H + 600, period: 5 * H },
    { key: '7d', utilization: 7.5, status: '', firstIn: 6 * D + 2 * H, period: 7 * D }
  ] }]
])

/**
 * An official Codex usage reset (mock/codex.ts): the paused account resumes
 * (the plugin clears its automatic state). Like the real core, the snapshot
 * keeps the old usage until the account's next request; the answer counts
 * the used-up windows the upstream reset.
 */
export function resetCodexUsage(id: number): number {
  const a = accounts.find((x) => x.id === id)
  if (a?.status === 'auto_disabled') Object.assign(a, { status: 'active', status_reason: '', auto_disabled_until: null, cooldown_until: null })
  return quotaState.get(id)?.windows.filter((w) => w.utilization >= 100).length ?? 0
}

/** Next reset after now: MOCK_T0 + firstIn + k·period. */
function nextReset(w: QuotaSeed): string {
  const first = MOCK_T0 + w.firstIn * 1000
  const period = w.period * 1000
  const k = Date.now() <= first ? 0 : Math.ceil((Date.now() - first) / period)
  return new Date(first + k * period).toISOString()
}

function quotaStateOf(a: any): QuotaState | null {
  if (!QUOTA_TYPES.has(`${a.plugin_key}/${a.type}`)) return null
  let st = quotaState.get(a.id)
  if (!st) quotaState.set(a.id, (st = { source: '', updated_at: null, error: '', windows: [] }))
  return st
}

/** QuotaSnapshot of an account, or null for types without plan limits. */
function quotaOf(a: any) {
  const st = quotaStateOf(a)
  if (!st) return null
  return {
    supported: true,
    source: st.source,
    updated_at: st.updated_at,
    error: st.error,
    windows: st.windows.map((w) => ({ key: w.key, utilization: w.utilization, resets_at: nextReset(w), status: w.status }))
  }
}

/** Credential refresh state (CONTRACTS §48): OAuth-style types only. */
function refreshOf(a: any) {
  if (!QUOTA_TYPES.has(`${a.plugin_key}/${a.type}`)) return null
  return a.refresh_state || {
    expires_at: now(5 * 3600),
    last_attempt_at: now(-3 * 3600),
    last_success_at: now(-3 * 3600),
    error_type: '',
    error: ''
  }
}

/** Last-60-seconds counters (CONTRACTS §18.1, §68, §71); mocked as a fraction of the limit, or from in_use without one. */
function rateUsage(a: any) {
  const used = (limit: number, ratio: number, busy: number) => (limit ? Math.min(limit, Math.round(limit * ratio)) : busy)
  const inUse = a.in_use || 0
  return {
    rpm: used(a.rpm_limit, 0.2, inUse * 4),
    tpm: used(a.tpm_limit, 0.032, inUse * 2100),
    sessions: used(a.spm_limit, 0.5, inUse),
    models: (a.model_limits || []).map((l: any, i: number) => ({ models: l.models, in_use: i === 0 ? Math.min(inUse, l.max_concurrency || inUse) : 0, rpm: used(l.rpm_limit, 0.9, 2), tpm: used(l.tpm_limit, 0.85, 1800), sessions: used(l.spm_limit, 0.5, i === 0 ? 1 : 0) }))
  }
}

// Success rate of the last minute and the usage record (CONTRACTS §73), made up
// from the account id so the list is stable between reloads.
function outcomesOf(a: any) {
  const success = (a.in_use || 0) * 37 + ((a.id * 13) % 50)
  const failed = a.id % 5 === 0 ? 6 : a.id % 3 === 0 ? 1 : 0
  return { success, failed }
}
const usageLimits = new Map<number, any>()
/** The limit of an account that never saved one: timezone "" (the console then uses the browser's zone). */
const defaultLimit = () => ({ raw_cost_accounting: false, enabled: false, amount: '0', period: 'month', period_count: 1, anchor: null, timezone: '' })
/** Largest period_count per period: one period spans at most 366 days. */
const PERIOD_COUNT_MAX: Record<string, number> = { hour: 8784, day: 366, week: 52, month: 12, none: 1000 }
function usageFigures(a: any, scale: number) {
  const cost = Math.round(a.id * 97.31 * scale) / 100
  return { cost: cost.toFixed(8), raw_cost: (cost / 1.2).toFixed(8), requests: Math.round(a.id * 41 * scale), tokens: Math.round(a.id * 1_234_567 * scale) }
}
function usageOf(a: any) {
  const l = usageLimits.get(a.id) || defaultLimit()
  const total = usageFigures(a, 10)
  const used = (l.raw_cost_accounting ? Number(total.raw_cost) : Number(total.cost)) / 3
  const end = new Date()
  end.setMonth(end.getMonth() + 1, 1)
  end.setHours(0, 0, 0, 0)
  const start = new Date(end)
  start.setMonth(start.getMonth() - 1)
  return {
    raw_cost_accounting: l.raw_cost_accounting,
    total,
    today: usageFigures(a, 0.3),
    limit: l.enabled ? { amount: l.amount, used: used.toFixed(8), period: l.period, period_count: l.period_count, window_start: l.period === 'none' ? null : start.toISOString(), window_end: l.period === 'none' ? null : end.toISOString(), waived: Boolean(l.waived) } : null,
    // The mock does not disable accounts: the status poll of the server does.
    limit_disabled: a.status === 'auto_disabled' && String(a.status_reason || '').startsWith('usage limit reached')
  }
}

// ---------------------------------------------------------------- balance
// Balance of the account types that read one (CONTRACTS §52): the relay keys
// and the demo token here. Types without one have no balance field. A read
// (forced, or the first) asks the "upstream": relay-badkey's key is rejected,
// the others succeed. The amount and the unit are the upstream's as they are:
// relay-1 reads like a New API site (a quota without a unit), relay-limited
// like a Sub2API one (unit "USD").
const BALANCE_TYPES = new Set(['relay/relay_key', 'demo/token'])
interface BalanceState { amount?: string; currency?: string; unlimited?: boolean; updated_at?: string; error?: string; error_at?: string }
const balanceState = new Map<number, BalanceState>([
  [16, { amount: '1250000.000000', updated_at: now(-240) }],
  [18, { unlimited: true, updated_at: now(-90) }],
  [31, { amount: '7.500000', currency: 'USD', updated_at: now(-7200), error: 'transient: upstream answered 503', error_at: now(-300) }],
  [33, { error: 'auth_rejected: the upstream rejected the key (401)', error_at: now(-600) }]
])

/** The account's balance view; undefined when its type reads none. */
function balanceOf(a: any) {
  if (!BALANCE_TYPES.has(`${a.plugin_key}/${a.type}`)) return undefined
  return { supported: true, ...balanceState.get(a.id) }
}

/** One read of the balance of a: stored on success, the failure on top of the last balance otherwise. */
function readBalance(a: any) {
  const st = balanceState.get(a.id) || {}
  if (a.id === 33) {
    balanceState.set(a.id, { ...st, error: 'auth_rejected: the upstream rejected the key (401)', error_at: now(0) })
    return
  }
  const amount = st.unlimited ? {} : { amount: (Number(st.amount ?? 20) * 0.98).toFixed(6), currency: st.currency }
  balanceState.set(a.id, { unlimited: st.unlimited, ...amount, updated_at: now(0) })
}

const withGroups = (a: any) => ({
  ...a,
  models: a.models || [],
  model_mapping: a.model_mapping || {},
  model_limits: (a.model_limits || []).map((l: any) => ({ spm_limit: 0, spm_window_seconds: 60, ...l })),
  spm_limit: a.spm_limit ?? 0,
  spm_window_seconds: a.spm_window_seconds ?? 60,
  header_passthrough: a.header_passthrough ?? [],
  protocols: a.protocols ?? null,
  model_protocols: a.model_protocols ?? [],
  header_override: a.header_override ?? null,
  param_override: a.param_override ?? null,
  weight: a.weight ?? 1,
  owner_id: a.owner_id ?? null,
  owner_email: userEmail(a.owner_id),
  created_by: a.created_by ?? null,
  created_by_email: userEmail(a.created_by),
  rate_usage: rateUsage(a),
  outcomes: outcomesOf(a),
  usage: usageOf(a),
  quota: quotaOf(a),
  balance: balanceOf(a),
  refresh: refreshOf(a),
  last_test: a.last_test ?? null,
  groups: (a.group_ids || []).map((id: number) => ({ id, name: GROUP_NAMES[id] ?? `group-${id}` }))
})

/** Names of the groups in groups.ts (which imports this file), for the `groups` of an account. */
const GROUP_NAMES: Record<number, string> = { 1: 'default', 2: 'vip', 3: 'haiku-only', 4: 'gemini-trial' }

// The console's three statuses, as the core filters them (GET /accounts ?status=).
const statusKey = (a: { status: string; schedulable: boolean }) =>
  a.status === 'disabled' ? 'disabled' : a.status === 'active' && a.schedulable ? 'active' : 'auto_disabled'

/** Accounts in a group (any status). */
export function groupAccountCount(gid: number): number {
  return accounts.filter((a) => (a.group_ids || []).includes(gid)).length
}

/** Accounts referencing a proxy (every owner, CONTRACTS §21.2). */
export function proxyAccountCount(pid: number): number {
  return accounts.filter((a) => a.proxy_id === pid).length
}

/** served_protocols of GET /groups/:id: what the accounts of the group open, with how many serve each protocol. */
export function groupServed(gid: number) {
  return groupServedProtocols(accounts.filter((a) => (a.group_ids || []).includes(gid)).map(servedOf))
}

/** Platforms a group serves: supported by its accounts' types and existing now (sorted). */
export function groupPlatforms(gid: number): string[] {
  const out = new Set<string>()
  for (const a of accounts) {
    if (a.orphaned || !(a.group_ids || []).includes(gid)) continue
    const d = accountTypeDecls.find((x) => x.plugin_key === a.plugin_key && x.type === a.type)
    for (const id of d?.supports || []) if (platformById(id)) out.add(id)
  }
  return [...out].sort()
}

/**
 * Uninstall hook (CONTRACTS §14.3): with purge, deletes the accounts of the
 * plugin's account types and returns how many; otherwise marks them orphaned.
 */
/** Accounts still belonging to a plugin key (kept orphaned after an uninstall). */
export function pluginAccountCount(pluginKey: string): number {
  return accounts.filter((a) => a.plugin_key === pluginKey).length
}

export function uninstallPluginAccounts(pluginKey: string, purge: boolean): number {
  let n = 0
  for (let i = accounts.length - 1; i >= 0; i--) {
    if (accounts[i].plugin_key !== pluginKey) continue
    n++
    if (purge) accounts.splice(i, 1)
    else accounts[i].orphaned = true
  }
  return purge ? n : 0
}

// Any signed-in user: available platforms and endpoints (CONTRACTS §14.1).
on('GET', '/me/platforms', () => activePlatforms().map((p) => ({ id: p.id, label: p.label, builtin: p.builtin, endpoints: p.endpoints })))
const autoDisableSettings = {
  enabled: true,
  status_codes: '401',
  keywords: ['your credit balance is too low', 'this organization has been disabled.', 'you exceeded your current quota', 'permission denied', 'the security token included in the request is invalid', 'operation not allowed', 'your account is not authorized']
}
on('GET', '/accounts/auto-disable', (req) => {
  const who = caller(req)
  if (!hasAction(who, ['account:create', 'account:update'])) return fail(403, 'permission_denied', 'Permission denied')
  return { enabled: autoDisableSettings.enabled }
})
on('GET', '/settings/auto-disable', (req) => hasPerm(caller(req), 'settings:read') ? autoDisableSettings : fail(403, 'permission_denied', 'Permission denied'))
on('PUT', '/settings/auto-disable', (req) => {
  if (!hasPerm(caller(req), 'settings:manage')) return fail(403, 'permission_denied', 'Permission denied')
  const b = req.body || {}
  if (typeof b.enabled !== 'boolean' || typeof b.status_codes !== 'string' || !Array.isArray(b.keywords) || !b.keywords.every((value: unknown) => typeof value === 'string')) return fail(400, 'invalid_argument', 'Invalid automatic disabling settings')
  Object.assign(autoDisableSettings, { enabled: b.enabled, status_codes: b.status_codes, keywords: b.keywords })
  return autoDisableSettings
})

on('GET', '/account-types', () => accountTypeDecls.map(accountTypeOut))

/** A complete model id (CONTRACTS §16): no wildcards. */
const MODEL_RE = /^[A-Za-z0-9._:/@+-]{1,200}$/
const badModel = (s: unknown) => typeof s !== 'string' || !MODEL_RE.test(s) || s.includes('*')

/** Validates the §18.1 scheduling / limit / model fields of a create or patch body. */
function validateScheduling(b: Record<string, any>) {
  const fields: Array<{ field: string; code: string; message: string }> = []
  if (Array.isArray(b.models)) {
    if (b.models.length > 500) fields.push({ field: 'models', code: 'too_many', message: 'At most 500 models' })
    b.models.forEach((m: unknown, i: number) => {
      if (badModel(m)) fields.push({ field: `models[${i}]`, code: 'invalid', message: 'Not a complete model id' })
      else if (b.models.indexOf(m) !== i) fields.push({ field: `models[${i}]`, code: 'duplicate', message: 'Duplicate model' })
    })
  }
  if (b.model_mapping && typeof b.model_mapping === 'object' && !Array.isArray(b.model_mapping)) {
    const entries = Object.entries(b.model_mapping as Record<string, unknown>)
    if (entries.length > 500) fields.push({ field: 'model_mapping', code: 'too_many', message: 'At most 500 entries' })
    for (const [from, to] of entries) {
      if (badModel(from) || badModel(to)) fields.push({ field: `model_mapping.${from}`, code: 'invalid', message: 'Not a complete model id' })
    }
  }
  const range: Array<[string, number, number]> = [
    ['priority', 0, 1000000],
    ['weight', 1, 1000],
    ['rpm_limit', 0, 10000000],
    ['tpm_limit', 0, 1e12],
    ['spm_limit', 0, 10000000],
  ]
  for (const [k, min, max] of range) {
    if (b[k] === undefined) continue
    const n = Number(b[k])
    if (!Number.isFinite(n) || n < min || n > max) fields.push({ field: k, code: 'invalid', message: `Must be between ${min} and ${max}` })
  }
  // Session windows (CONTRACTS §71): whole seconds, 1 minute to 24 hours; absent = 60.
  const badWindow = (v: unknown) => v !== undefined && (!Number.isInteger(v) || (v as number) < 60 || (v as number) > 86400)
  if (badWindow(b.spm_window_seconds)) fields.push({ field: 'spm_window_seconds', code: 'invalid', message: 'invalid' })
  if (Array.isArray(b.model_limits)) b.model_limits.forEach((l: any, i: number) => badWindow(l?.spm_window_seconds) && fields.push({ field: `model_limits[${i}].spm_window_seconds`, code: 'invalid', message: 'invalid' }))
  return fields.length ? fail(400, 'invalid_argument', 'invalid argument', { fields }) : null
}

/** Resolves the proxy of a create / patch body (CONTRACTS §21.4): proxy_id xor proxy_url. Returns the fields to set, or an error. */
function resolveProxy(req: MockRequest, b: Record<string, any>): { proxy_id?: number | null; proxy_created?: boolean } | { __status: number; body: unknown } {
  if (b.proxy_url !== undefined && b.proxy_id !== undefined) {
    return fail(400, 'invalid_argument', 'proxy_id and proxy_url are exclusive', { fields: [{ field: 'proxy_url', code: 'conflict', message: 'Give proxy_id or proxy_url, not both' }] })
  }
  if (typeof b.proxy_url === 'string' && b.proxy_url.trim()) {
    const r = resolveProxyURL(req, b.proxy_url)
    if ('__status' in r) return r
    return { proxy_id: r.id, proxy_created: r.created }
  }
  if (b.proxy_id !== undefined) return { proxy_id: b.proxy_id === null ? null : Number(b.proxy_id), proxy_created: false }
  return { proxy_created: false }
}

// Accounts in the caller's scope by status (T81): available = active and schedulable.
on('GET', '/accounts/summary', (req) => {
  const { who, scope } = accountScope(req, 'read')
  const list = filterOwned(accounts, scope, who, {})
  const available = list.filter((a) => a.status === 'active' && a.schedulable).length
  const disabled = list.filter((a) => a.status === 'disabled').length
  return { total: list.length, available, auto_disabled: list.length - available - disabled, disabled }
})
on('GET', '/accounts', (req) => {
  const { who, scope } = accountScope(req, 'read')
  let list = filterOwned(accounts, scope, who, req.query)
  const q = req.query
  // Accounts of disabled plugins are hidden unless orphaned=true|all.
  if (q.orphaned === 'true') list = list.filter((a) => a.orphaned)
  else if (q.orphaned !== 'all') list = list.filter((a) => !a.orphaned)
  if (q.plugin_key) list = list.filter((a) => a.plugin_key === q.plugin_key)
  if (q.type) list = list.filter((a) => a.type === q.type)
  if (q.group_id) list = list.filter((a) => a.group_ids.includes(Number(q.group_id)))
  if (q.status) list = list.filter((a) => statusKey(a) === q.status)
  // ?model=: accounts that can serve it (empty `models` = all models, §18.3).
  if (q.model) list = list.filter((a) => !(a.models || []).length || (a.models || []).includes(q.model))
  if (q.q) list = list.filter((a) => a.name.includes(q.q))
  // ?ids=1,2: at most 500 integers (MAX_ACCOUNT_BATCH in @sub2api/host, which the
  // mock does not import; 400 otherwise), ANDed with the other filters.
  if (q.ids) {
    const ids = String(q.ids).split(',').map(Number)
    if (ids.length > 500 || ids.some((id) => !Number.isInteger(id))) return fail(400, 'invalid_argument', 'ids must be up to 500 integers')
    list = list.filter((a) => ids.includes(a.id))
  }
  // Newest first (§18.3).
  list = [...list].sort((x, y) => y.created_at.localeCompare(x.created_at) || y.id - x.id)
  return paginate(list.map(({ credentials: _c, ...a }) => withGroups(a)), q)
})
/**
 * The account of :id within the caller's scope for `action`: 403 without the
 * action at any scope, 404 when missing or out of it (the two are not told apart).
 */
const adminLocked = () => fail(403, 'account_locked_by_admin', 'this account was disabled by an administrator; only an administrator can enable it')
function scopedAccount(req: MockRequest, action: 'read' | 'update' | 'delete' | 'test' | 'credential:view'): { a: any } | { __status: number; body: unknown } {
  const { who, scope } = accountScope(req, action)
  if (!scope) return fail(403, 'permission_denied', `account:${action} is required`, { permission: `account:${action}` })
  const a = accounts.find((x) => x.id === Number(req.params.id))
  return a && inScope(scope, who, a) ? { a } : fail(404, 'not_found', 'account not found')
}
// The request header override (CONTRACTS §108), read as the console's editor
// reads it (rowMeaning). The mock refuses credentials, client addresses,
// gateway headers, {api_key} and placeholders it would not read (placeholderProblem); the server checks the rest.
const HEADER_BLOCKED = /^(authorization|proxy-authorization|x-api-key|cookie|host|x-forwarded-for|x-real-ip|x-sub2api-.*|x-kin-.*)$/i
const PLACEHOLDER_PROBLEMS: Record<string, { code: string; message: string }> = {
  noSource: { code: 'required', message: 'client_header needs a header name' },
  emptySuffix: { code: 'invalid', message: 'the client_header append value must not be empty' },
  partialPlaceholder: { code: 'invalid', message: 'a placeholder must be the whole value' }
}
function headerEntryProblem(k: string, val: unknown): { code: string; message: string } | null {
  if (typeof val !== 'string') return { code: 'invalid', message: `${k}: the value must be a string` }
  if (val.includes('{api_key}')) return { code: 'unsupported', message: `${k}: {api_key} is not supported, the credential comes from the account type's auth` }
  const m = rowMeaning(k, val)
  const placeholder = m.kind === 'client' || m.kind === 'clientAppend'
  if (HEADER_BLOCKED.test(k.trim()) || (placeholder && HEADER_BLOCKED.test(m.name ?? ''))) return { code: 'blocked', message: `${k}: this header cannot be overridden` }
  const problem = PLACEHOLDER_PROBLEMS[placeholderProblem(k, val) ?? '']
  return problem ? { code: problem.code, message: `${k}: ${problem.message}` } : null
}
/** Validates the header override of a create or patch body; {} is stored as null. */
function validateHeaderOverride(b: Record<string, any>) {
  const v = b.header_override
  if (v == null) return null
  const problem = typeof v !== 'object' || Array.isArray(v) ? { code: 'invalid', message: 'header_override must be an object' } : Object.entries(v).map(([k, val]) => headerEntryProblem(k, val)).find(Boolean)
  if (problem) return fail(400, 'invalid_argument', 'invalid argument', { fields: [{ field: 'header_override', ...problem }] })
  if (!Object.keys(v).length) b.header_override = null
  return null
}
/** What the header override sets and deletes (lower-case names) for these client headers; its passthrough keys are not resolved here. */
function resolveHeaderOverride(ho: Record<string, string> | null | undefined, headers: Record<string, string>) {
  const set: Record<string, string> = {}
  const del: string[] = []
  const client = (name: string) => Object.entries(headers).find(([k]) => k.toLowerCase() === name.toLowerCase())?.[1]
  for (const [k, v] of Object.entries(ho || {})) {
    const name = k.trim().toLowerCase()
    const m = rowMeaning(k, v)
    if (m.kind === 'unset') del.push(name)
    else if (m.kind === 'static') set[name] = v.trim()
    else if (m.kind === 'client' || m.kind === 'clientAppend') {
      const value = [client(m.name ?? '')?.trim(), m.suffix].filter(Boolean).join(',')
      if (value) set[name] = value
    }
  }
  return { set, del }
}

// Dry run of a parameter override (CONTRACTS §84), the headers starting from
// the header override's (§108). The mock only knows the common modes (set,
// delete, set_header, delete_header, return_error) and conditions on
// top-level fields; the server runs the real engine.
on('POST', '/accounts/param-override/preview', (req) => {
  const { param_override: po, header_override: ho, body = {}, model = '', headers = {} } = req.body || {}
  const out: Record<string, any> = JSON.parse(JSON.stringify(body))
  const ctx: Record<string, any> = { model, original_model: model, request_headers: headers }
  const { set, del } = resolveHeaderOverride(ho, headers)
  const compare = (v: any, mode: string, want: any) => {
    const s = String(v ?? ''), w = String(want ?? '')
    return ({ full: s === w, prefix: s.startsWith(w), suffix: s.endsWith(w), contains: s.includes(w), gt: +s > +w, gte: +s >= +w, lt: +s < +w, lte: +s <= +w } as Record<string, boolean>)[mode] ?? false
  }
  const holds = (op: any) => {
    const conds: any[] = op.conditions || []
    if (!conds.length) return true
    const results = conds.map(c => {
      const v = c.path in out ? out[c.path] : ctx[c.path]
      if (v === undefined) return !!c.pass_missing_key
      return compare(v, c.mode, c.value) !== !!c.invert
    })
    return String(op.logic).toUpperCase() === 'AND' ? results.every(Boolean) : results.some(Boolean)
  }
  for (const op of po?.operations || []) {
    if (!holds(op)) continue
    if (op.mode === 'set' && !(op.keep_origin && op.path in out)) out[op.path] = op.value
    else if (op.mode === 'delete') delete out[op.path]
    else if (op.mode === 'set_header') set[String(op.path).toLowerCase()] = typeof op.value === 'string' ? op.value : JSON.stringify(op.value)
    else if (op.mode === 'delete_header') del.push(String(op.path).toLowerCase())
    else if (op.mode === 'return_error') {
      const v = typeof op.value === 'string' ? { message: op.value } : op.value || {}
      return { body: out, set_headers: set, delete_headers: del, error: null, return_error: { status: v.status_code ?? 400, code: v.code ?? 'invalid_request', type: v.type ?? 'invalid_request_error', message: v.message, skip_retry: v.skip_retry !== false } }
    }
  }
  return { body: out, set_headers: set, delete_headers: del, return_error: null, error: null }
})
on('GET', '/accounts/:id', (req) => {
  const r = scopedAccount(req, 'read')
  // served_protocols comes with a single account only.
  return 'a' in r ? { ...withGroups(r.a), served_protocols: servedOf(r.a) } : r
})
on('POST', '/accounts', (req) => {
  const who = caller(req)
  const b = req.body || {}
  // CONTRACTS §21: a restricted creator needs the own scope or one of its groups on the new account.
  const reach = ownerScope(who, 'account:create')
  if (!reach) return fail(403, 'permission_denied', 'account:create is required', { permission: 'account:create' })
  if (!inScope(reach, who, { id: 0, created_by: who.id, group_ids: (b.group_ids || []).map(Number) })) {
    return fail(400, 'invalid_argument', 'invalid group_ids', { fields: [{ field: 'group_ids', code: 'not_in_scope', message: 'bind the account to a group you manage' }] })
  }
  const creds = b.credentials || {}
  if ('platform' in b) return fail(400, 'invalid_argument', 'unknown field "platform"')
  const at = accountTypeDecls.find((x) => x.plugin_key === b.plugin_key && x.type === b.type)
  if (!at) return fail(400, 'invalid_argument', 'unknown account type', { fields: [{ field: 'type', code: 'not_found', message: 'Unknown account type' }] })
  const bad = validateScheduling(b) || validateHeaderOverride(b) || validateProtocols(b, at) || applyCommonControls(at.plugin_key, at.type, b)
  if (bad) return bad
  if (b.plugin_key === 'codex') {
    const invalid = codexCredentialErrors(b.type, creds)
    if (invalid) return invalid
  }
  if (b.plugin_key === 'anthropic' && !String(creds.api_key || '').startsWith('sk-')) {
    return fail(400, 'invalid_argument', 'invalid credentials', { fields: [{ field: 'credentials.api_key', code: 'invalid', message: 'API key must start with sk-' }] })
  }
  const guarded = checkGuarded(who, b.plugin_key, creds)
  if (guarded) return guarded
  if (accounts.some((a) => a.name === b.name)) return fail(400, 'invalid_argument', 'name taken', { fields: [{ field: 'name', code: 'conflict', message: 'Name already used' }] })
  const proxy = resolveProxy(req, b)
  if ('__status' in proxy) return proxy
  const masked = { ...creds }
  for (const f of at.sensitive_fields) if (masked[f]) masked[f] = '******'
  const { proxy_url: _u, ...fields } = b
  const a = {
    id: nextId(),
    status: 'active',
    status_reason: '',
    in_use: 0,
    orphaned: false,
    created_at: now(),
    priority: 10,
    weight: 1,
    max_concurrency: 10,
    schedulable: true,
    models: [],
    model_mapping: {},
    rpm_limit: 0,
    tpm_limit: 0,
    spm_limit: 0,
    spm_window_seconds: 60,
    ...fields,
    proxy_id: proxy.proxy_id ?? null,
    owner_id: who.id,
    created_by: who.id,
    type_label: at.label,
    credentials: masked
  }
  accounts.unshift(a)
  return { ...withGroups(a), proxy_created: !!proxy.proxy_created }
})
on('PATCH', '/accounts/:id', (req) => {
  const r = scopedAccount(req, 'update')
  if (!('a' in r)) return r
  const a = r.a
  const body = req.body || {}
  const bad = validateScheduling(body) || validateHeaderOverride(body) || validateProtocols(body, a) || applyCommonControls(a.plugin_key, a.type, body, a)
  if (bad) return bad
  const { credentials, platform: _p, plugin_key: _k, type: _t, proxy_id: _pid, proxy_url: _purl, ...rest } = req.body || {}
  const guarded = checkGuarded(caller(req), a.plugin_key, credentials, a.credentials)
  if (guarded) return guarded
  const { who, scope } = accountScope(req, 'update')
  // A restricted caller may not move the account out of its scope (CONTRACTS §21).
  if (!inScope(scope, who, { ...a, group_ids: (rest.group_ids ?? a.group_ids ?? []).map(Number) })) {
    return fail(403, 'would_leave_scope', 'the change would move the account out of your range')
  }
  const proxy = resolveProxy(req, req.body || {})
  if ('__status' in proxy) return proxy
  // Enabling restores scheduling; a status change clears the release time and words the reason as the core does.
  if (rest.status === 'active' && rest.schedulable === undefined) rest.schedulable = true
  // The administrator lock (CONTRACTS §15.9 admin_locked): the full scope locks an account somebody else created when it
  // disables it and unlocks when it enables; an own-level caller cannot enable a locked account.
  if (scope?.level === 'own' && a.admin_locked && rest.status === 'active' && a.status !== 'active') return adminLocked()
  if (rest.status !== undefined && rest.status !== a.status) Object.assign(a, { auto_disabled_until: null, status_reason: rest.status !== 'disabled' ? '' : scope?.level === 'own' ? 'disabled by owner' : 'disabled by administrator' })
  if (scope?.level === 'all' && rest.status === 'active') a.admin_locked = false
  if (scope?.level === 'all' && rest.status === 'disabled' && a.owner_id !== who.id) Object.assign(a, { admin_locked: true, status_reason: 'disabled by administrator' })
  // `models` and `model_mapping` are replaced as a whole (§18.3).
  Object.assign(a, rest)
  if (proxy.proxy_id !== undefined) a.proxy_id = proxy.proxy_id
  if (credentials) {
    a.credentials ??= {}
    const sensitive = accountTypeDecls.find((x) => x.plugin_key === a.plugin_key && x.type === a.type)?.sensitive_fields ?? []
    for (const [k, v] of Object.entries(credentials)) if (v !== '******') a.credentials[k] = k === 'api_key' || k === 'token' || (sensitive.includes(k) && v) ? '******' : v
  }
  return { ...withGroups(a), proxy_created: !!proxy.proxy_created }
})
// PUT /accounts/:id/owner (CONTRACTS §103.4): moves the account to another user.
on('PUT', '/accounts/:id/owner', (req) => {
  if (!hasPerm(caller(req), 'account:owner:update')) return fail(403, 'permission_denied', 'account:owner:update is required', { permission: 'account:owner:update' })
  const a = accounts.find((x) => x.id === Number(req.params.id))
  if (!a) return fail(404, 'not_found', 'account not found')
  const owner = Number(req.body?.owner_id)
  if (!Number.isInteger(owner) || owner <= 0) return fail(400, 'invalid_argument', 'owner_id must be a positive integer', { fields: [{ field: 'owner_id', code: 'invalid', message: 'a positive integer' }] })
  const email = userEmail(owner)
  if (!email) return fail(400, 'invalid_argument', 'no such user', { fields: [{ field: 'owner_id', code: 'not_found', message: 'no such user' }] })
  a.owner_id = owner
  return { id: a.id, owner_id: owner, owner_email: email }
})
on('DELETE', '/accounts/:id', (req) => {
  const { who, scope } = accountScope(req, 'delete')
  const i = accounts.findIndex((x) => x.id === Number(req.params.id))
  if (i < 0 || !inScope(scope, who, accounts[i])) return fail(404, 'not_found', 'account not found')
  accounts.splice(i, 1)
  return noContent()
})
on('POST', '/accounts/:id/test', async (req) => {
  const r = scopedAccount(req, 'test')
  if (!('a' in r)) return r
  const a = r.a
  // The model goes through the account's model_mapping first (§18.3); no model = the plugin default.
  const requested = typeof req.body?.model === 'string' ? req.body.model : ''
  const asked = requested || (a.plugin_key === 'openai' ? 'gpt-4o-mini' : a.plugin_key === 'gemini' ? 'gemini-2.5-flash' : 'claude-haiku-4-5')
  const model = (a.model_mapping || {})[asked] || asked
  // The protocol the plugin tested (§122): the first of the model's model_protocols entry, else chat / messages.
  const protocol = (a.model_protocols || []).find((e: { models: string[] }) => e.models.includes(asked))?.protocols[0] || (a.plugin_key === 'openai' ? 'openai.chat' : 'anthropic.messages')
  const path = ({ 'openai.chat': '/v1/chat/completions', 'openai.responses': '/v1/responses', 'openai.embeddings': '/v1/embeddings', 'openai.images': '/v1/images/generations' } as Record<string, string>)[protocol] || '/v1/messages'
  const upstream = `${String(a.credentials?.base_url || 'https://api.anthropic.com').replace(/\/+$/, '')}${path}`
  // Mocked outcomes: disabled = 401 (would disable), *opus* = 429 (would cool down),
  // *unknown* / *gpt-5* = 404 model not found, otherwise OK with a random latency.
  const latency = a.status === 'disabled' ? 212 : 300 + Math.round(Math.random() * (model.includes('pro') ? 6000 : 2500))
  await new Promise((ok) => setTimeout(ok, Math.min(1500, latency / 3)))
  let out: Record<string, unknown>
  if (a.status === 'disabled') {
    out = { ok: false, status: 401, latency_ms: latency, message: 'authentication_error: invalid x-api-key', reason: 'auth_rejected', effect: 'disable', body: '{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}' }
  } else if (model.includes('opus')) {
    out = { ok: false, status: 429, latency_ms: latency, message: 'rate_limit_error: Number of request tokens has exceeded your per-minute rate limit', reason: 'rate_limited', effect: 'cooldown', body: '{"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}' }
  } else if (model.includes('unknown') || model.startsWith('gpt-5')) {
    out = { ok: false, status: 404, latency_ms: latency, message: `not_found_error: model: ${model}`, reason: 'model_not_found', effect: '', body: `{"type":"error","error":{"type":"not_found_error","message":"model: ${model}"}}` }
  } else {
    out = {
      ok: true,
      status: 200,
      latency_ms: latency,
      message: `model ${model} answered`,
      reason: '',
      effect: '',
      usage: { input_tokens: 12, output_tokens: 8, cache_read_tokens: 0, cache_creation_tokens: 0 },
      body: JSON.stringify({ id: 'msg_mock', type: 'message', role: 'assistant', model, content: [{ type: 'text', text: 'pong' }], stop_reason: 'end_turn', usage: { input_tokens: 12, output_tokens: 8 } })
    }
  }
  out = { ...out, model, requested_model: requested, protocol, upstream }
  a.last_test = { at: now(), ok: out.ok, latency_ms: latency, model, protocol, message: String(out.message || '') }
  return out
})
// Fetch models from the upstream (CONTRACTS §19). Per plugin a fixed list; a
// key containing "bad" mimics an upstream 401, gemini lists resource names.
const upstreamModels: Record<string, string[]> = {
  anthropic: ['claude-sonnet-4-5', 'claude-opus-4-1', 'claude-haiku-4-5', 'claude-sonnet-4-5-20250929'],
  openai: ['gpt-4.1', 'gpt-4o', 'gpt-4o-mini', 'o3', 'text-embedding-3-small'],
  gemini: ['gemini-2.5-pro', 'gemini-2.5-flash', 'gemini-2.0-flash'],
  relay: ['claude-sonnet-4-5', 'claude-opus-4-1']
}
function fetchUpstreamModels(pluginKey: string, creds: Record<string, any> | undefined) {
  const key = String(creds?.api_key || '')
  if (key.includes('bad')) return fail(503, 'unavailable', 'upstream returned 401: {"error":"invalid api key"}', { status: 401 })
  const models = upstreamModels[pluginKey]
  if (!models) return fail(501, 'unsupported', 'this account type cannot list models from the upstream')
  return { models: [...models].sort(), skipped: 0, status: 200 }
}
on('POST', '/account-types/:plugin_key/:type/models/fetch', (req) => {
  // A proxy_url is only parsed and used for this request (CONTRACTS §21.2): nothing is looked up or created.
  const b = req.body || {}
  if (b.proxy_url !== undefined && b.proxy_id !== undefined) {
    return fail(400, 'invalid_argument', 'proxy_id and proxy_url are exclusive', { fields: [{ field: 'proxy_url', code: 'conflict', message: 'Give proxy_id or proxy_url, not both' }] })
  }
  if (typeof b.proxy_url === 'string' && b.proxy_url.trim()) {
    const err = validateProxyURL(b.proxy_url)
    if (err) return err
  }
  return fetchUpstreamModels(req.params.plugin_key, b.credentials)
})
// The proxy of a native authorization flow (CONTRACTS §117): checked like
// models/fetch, sealed by the core; the mock's ticket is opaque base64.
on('POST', '/account-types/:plugin_key/:type/proxy-ticket', (req) => {
  const b = req.body || {}
  const url = typeof b.proxy_url === 'string' ? b.proxy_url.trim() : ''
  if (url && b.proxy_id != null) {
    return fail(400, 'invalid_argument', 'proxy_id and proxy_url are exclusive', { fields: [{ field: 'proxy_url', code: 'conflict', message: 'Give proxy_id or proxy_url, not both' }] })
  }
  if (url) {
    const err = validateProxyURL(url)
    if (err) return err
  }
  const choice = url ? { proxy_url: url } : b.proxy_id != null ? { proxy_id: b.proxy_id } : {}
  return { ticket: Buffer.from(JSON.stringify({ plugin: req.params.plugin_key, ...choice })).toString('base64url') }
})
on('POST', '/accounts/:id/models/fetch', (req) => {
  const r = scopedAccount(req, 'test')
  if (!('a' in r)) return r
  const a = r.a
  return fetchUpstreamModels(a.plugin_key, req.body?.credentials || a.credentials)
})
// Subscription quota (QuotaSnapshot). Explicit refreshes have no cooldown;
// the page coalesces in-flight clicks. A rejected window stays rejected.
on('GET', '/accounts/:id/usage', (req) => {
  const r = scopedAccount(req, 'read')
  if (!('a' in r)) return r
  const u = usageOf(r.a)
  return { ...u, last_7_days: usageFigures(r.a, 2), last_30_days: usageFigures(r.a, 7) }
})
// Figures by client model (CONTRACTS §68.7): the account's rate_usage split
// over its models by a fixed share, jittered so the open card visibly polls.
on('GET', '/accounts/:id/rate-models', (req) => {
  const r = scopedAccount(req, 'read')
  if (!('a' in r)) return r
  const a = r.a
  const u = a.rate_usage || { rpm: 0, tpm: 0, sessions: 0 }
  const models: string[] = a.models?.length ? a.models.slice(0, 6) : ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5']
  const jitter = () => 0.9 + Math.random() * 0.2
  const rows = models.map((model, i) => {
    const share = 1 / 2 ** (i + 1)
    return {
      model,
      in_use: Math.round((a.in_use || 0) * share),
      rpm: Math.round(u.rpm * share * jitter()),
      tpm: Math.round(u.tpm * share * jitter()),
      tpd: Math.round(u.tpm * 600 * share),
      sessions: Math.round((u.sessions || 0) * share)
    }
  })
  const sum = (k: 'tpd') => rows.reduce((n, row) => n + row[k], 0)
  return { total: { in_use: a.in_use || 0, rpm: u.rpm, tpm: u.tpm, tpd: sum('tpd'), sessions: u.sessions || 0 }, models: rows }
})
on('GET', '/accounts/:id/usage-limit', (req) => {
  const r = scopedAccount(req, 'read')
  if (!('a' in r)) return r
  const { waived: _waived, ...l } = usageLimits.get(r.a.id) || defaultLimit()
  return { ...l, configured: usageLimits.has(r.a.id) }
})
// Same checks and error shape as the core's validateUsageLimit: 400
// invalid_argument with one field error per problem. A switched-off limit may
// have no amount; "none" has no period length to check.
on('PUT', '/accounts/:id/usage-limit', (req) => {
  const r = scopedAccount(req, 'update')
  if (!('a' in r)) return r
  const b = req.body || {}
  const fields: { field: string; code: string; message: string }[] = []
  const invalid = (field: string, message: string) => fields.push({ field, code: 'invalid', message })
  const max = PERIOD_COUNT_MAX[b.period]
  if (!max) invalid('period', 'period must be hour, day, week, month or none')
  else if (b.period !== 'none' && !(b.period_count >= 1 && b.period_count <= max)) invalid('period_count', `period count must be 1-${max} (one period lasts at most 366 days)`)
  const raw = String(b.amount ?? '').trim()
  const amount = raw === '' && !b.enabled ? 0 : Number(raw)
  const decimals = (raw.split('.')[1] || '').replace(/0+$/, '').length
  if (raw === '' && b.enabled) invalid('amount', 'set an amount above 0 to enable the usage limit')
  else if (!(amount >= 0 && amount <= 1e9) || decimals > 8) invalid('amount', 'amount must be 0-1000000000 USD with at most 8 decimals')
  else if (b.enabled && !(amount > 0)) invalid('amount', 'set an amount above 0 to enable the usage limit')
  const year = b.anchor ? new Date(b.anchor).getUTCFullYear() : 2024
  if (!(year >= 2000 && year < 2100)) invalid('anchor', 'anchor must be between 2000 and 2100')
  if (!b.timezone) invalid('timezone', 'timezone must be an IANA time zone name')
  if (fields.length) return fail(400, 'invalid_argument', 'invalid argument', { fields })
  usageLimits.set(r.a.id, { ...defaultLimit(), ...b, amount: raw || '0', period_count: b.period === 'none' ? Math.max(1, Number(b.period_count) || 1) : b.period_count, waived: false })
  const u = usageOf(r.a)
  return { ...u, last_7_days: usageFigures(r.a, 2), last_30_days: usageFigures(r.a, 7) }
})

on('GET', '/accounts/:id/quota', (req) => {
  const r = scopedAccount(req, 'read')
  if (!('a' in r)) return r
  return quotaOf(r.a) || { supported: false, source: '', updated_at: null, error: '', windows: [] }
})
on('GET', '/accounts/:id/balance', (req) => {
  const r = scopedAccount(req, 'read')
  if (!('a' in r)) return r
  const b = balanceOf(r.a)
  if (!b) return fail(404, 'unsupported', 'account type does not support balance')
  if (req.query.force === 'true' || (!b.updated_at && !b.error)) readBalance(r.a)
  return balanceOf(r.a)
})
// Renews the credentials of an OAuth account now (CONTRACTS §48).
on('POST', '/accounts/:id/refresh-credentials', (req) => {
  const r = scopedAccount(req, 'update')
  if (!('a' in r)) return r
  const a = r.a
  if (!refreshOf(a)) return fail(400, 'invalid_argument', 'this account type does not refresh its credentials')
  if (!a.credentials?.refresh_token) {
    a.refresh_state = { ...refreshOf(a), last_attempt_at: now(0), error_type: 'auth_rejected', error: 'the account has no refresh_token; authorize it again' }
    return { refreshed: false, error_type: 'auth_rejected', error: a.refresh_state.error, refresh: refreshOf(a) }
  }
  a.refresh_state = { expires_at: now(8 * 3600), last_attempt_at: now(0), last_success_at: now(0), error_type: '', error: '' }
  return { refreshed: true, refresh: refreshOf(a) }
})
// Restores active status and clears the reason and cooldown (needs update on it).
on('POST', '/accounts/:id/reset-status', (req) => {
  const r = scopedAccount(req, 'update')
  if (!('a' in r)) return r
  if (r.a.admin_locked && r.a.status !== 'active' && accountScope(req, 'update').scope?.level === 'own') return adminLocked()
  r.a.admin_locked = false
  // Releasing an account the usage limit disabled waives the limit for that period (§73.4).
  const l = usageLimits.get(r.a.id)
  if (l && usageOf(r.a).limit_disabled) l.waived = true
  r.a.status = 'active'
  r.a.status_reason = ''
  r.a.auto_disabled_until = null
  r.a.cooldown_until = null
  r.a.cooldown_reason = ''
  return withGroups(r.a)
})
on('POST', '/accounts/:id/credentials/reveal', stepUp('credential.reveal', (req) => {
  const r = scopedAccount(req, 'credential:view')
  if (!('a' in r)) return r
  const a = r.a
  return { api_key: 'sk-ant-api03-mock-plaintext-key', base_url: a.credentials?.base_url || 'https://api.anthropic.com' }
}))
