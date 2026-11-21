import { readFileSync, existsSync } from 'node:fs'
import { join, normalize } from 'node:path'
import { randomBytes, randomUUID } from 'node:crypto'
import { nextPath } from './paths'
import { fail, on } from './router'

// Mock of the codex plugin (docs/PLUGIN-CODEX.md §4): its admin routes
// (/p/codex/auth/*) and its native account form from
// plugins/codex/ui/native/dist (build it first). Nothing leaves the mock:
//   device code   the code is "approved" on the third poll (about 6 s);
//                 a session the mock does not know reads as expired.
//   Sign in with Codex
//                 the authorize link is the real OpenAI address shape (it
//                 does not work); paste back any
//                 http://localhost:1455/auth/callback?code=<anything>&state=<state of the link>.
//                 error=access_denied, a missing code or another state fail
//                 as the plugin would.
// Answers have the plugin's shapes (CONTRACTS §3.1): {"data": ...} on
// success (pluginsdk.DataResponse); 400 invalid_argument with details.fields
// for a malformed request (pluginsdk.BadRequest); the plugin's own codes
// (state_mismatch, session_expired, ...) for a refused flow; every message
// "English / 中文".

const CODEX_DIR = nextPath('plugins', 'codex')
const CODEX_DIST = join(CODEX_DIR, 'ui/native/dist')

export function codexAsset(path: string): { type: string; body: Buffer } | null {
  // The package icon (manifest icon), shown on the account type card.
  if (path === manifest.icon && /^assets\/[\w.-]+\.svg$/.test(path) && existsSync(join(CODEX_DIR, path))) return { type: 'image/svg+xml', body: readFileSync(join(CODEX_DIR, path)) }
  if (!path.startsWith('ui/native/')) return null
  const rel = normalize(path.slice('ui/native/'.length))
  if (rel.startsWith('..')) return null
  const file = join(CODEX_DIST, rel)
  if (!existsSync(file)) return null
  return { type: rel.endsWith('.css') ? 'text/css' : 'text/javascript', body: readFileSync(file) }
}

/**
 * The two account types as GET /account-types lists them (before platforms
 * and endpoints), read from plugins/codex/manifest.json so the mock follows
 * the plugin; the values below stand in while the manifest is missing.
 */
const CODEX_MANIFEST = join(CODEX_DIR, 'manifest.json')
function readManifest(): any {
  try {
    return JSON.parse(readFileSync(CODEX_MANIFEST, 'utf8'))
  } catch {
    return {}
  }
}
const manifest = readManifest()
const declared = (id: string): any => (manifest.accountTypes || []).find((a: any) => a.id === id) || {}
const NAME = manifest.name || { en: 'ChatGPT (Codex)', zh: 'ChatGPT（Codex）' }
const VERSION = manifest.version || '0.1.0'
const FALLBACK = {
  codex_device: { authMethodLabel: { en: 'Device Code', zh: '设备码' }, description: { en: 'Your own ChatGPT subscription, authorized with the Codex device code login', zh: '你自己的 ChatGPT 订阅，用 Codex 设备码登录授权' } },
  codex_oauth: { authMethodLabel: { en: 'Codex', zh: 'Codex' }, description: { en: "Your own ChatGPT subscription, authorized with the Codex CLI's browser sign-in", zh: '你自己的 ChatGPT 订阅，用 Codex CLI 的浏览器登录授权' } }
}
export const codexAccountTypes = (['codex_device', 'codex_oauth'] as const).map((type) => {
  const d = declared(type)
  return {
    plugin_key: 'codex',
    plugin_name: NAME,
    plugin_version: VERSION,
    asset_base: `/plugin-ui/codex/${VERSION}-dev`,
    trust: 'official',
    ...(manifest.icon ? { icon: manifest.icon } : {}),
    type,
    label: d.label || NAME,
    description: d.description || FALLBACK[type].description,
    creation_group: d.creationGroup || 'codex',
    auth_method_label: d.authMethodLabel || FALLBACK[type].authMethodLabel,
    form: { mode: d.form?.mode || 'native', component: d.form?.component || 'CodexAuthForm' },
    sensitive_fields: d.sensitiveFields || ['access_token', 'refresh_token', 'id_token'],
    supports: ['openai'],
    // Codex speaks Responses only: chat requests are adapted to it (CONTRACTS §83.3).
    subset: ['openai.responses_ws', 'openai.responses']
  }
})

/** GET /ui/plugins entry: no menu or page, only the native entry holding CodexAuthForm. */
export const codexUIPlugin = {
  key: 'codex',
  version: VERSION,
  name: NAME,
  asset_base: `/plugin-ui/codex/${VERSION}-dev`,
  menus: [],
  pages: {},
  slots: manifest.ui?.slots || [],
  // manifest ui.accountActions as /ui/plugins hands it down (CONTRACTS §23.3, §91b).
  account_actions: manifest.ui?.accountActions || [],
  native_entry: 'ui/native/entry.js',
  trust: 'official',
  host_ui_compat: manifest.hostUICompat || '*'
}

/** The plugin's ValidateCredentials as far as the mock goes: the tokens both logins return. */
export function codexCredentialErrors(type: string, creds: Record<string, unknown>) {
  const need = ['access_token', 'refresh_token', 'chatgpt_account_id']
  const missing = need.filter((k) => typeof creds[k] !== 'string' || !creds[k])
  if (!missing.length) return null
  return fail(400, 'invalid_argument', 'invalid credentials', { fields: missing.map((k) => ({ field: `credentials.${k}`, code: 'required', message: k === 'access_token' ? 'Authorize the account first' : `${k} is required for ${type}` })) })
}

/** pluginsdk.BadRequest: 400 invalid_argument with one field error. */
const badRequest = (field: string, code: string, message: string) =>
  fail(400, 'invalid_argument', 'invalid request / 请求参数不正确', { fields: [{ field, code, message }] })
const noSession = () => badRequest('session_id', 'required', 'session_id is required / 缺少 session_id')

/**
 * The query of a pasted callback, read like the plugin's parseCallback: the
 * whole address, the address without its scheme, or only the query
 * ("?code=..." / "code=..."); the fragment when the query has none.
 */
function callbackQuery(raw: string): URLSearchParams {
  let s = raw.trim().replace(/^["'<>`\s]+|["'<>`\s]+$/g, '').replace(/&amp;/g, '&')
  let fragment = ''
  const hash = s.indexOf('#')
  if (hash >= 0) [s, fragment] = [s.slice(0, hash), s.slice(hash + 1)]
  const q = s.indexOf('?')
  const query = q >= 0 ? s.slice(q + 1) : s.includes('://') || (!s.includes('=') && s.includes('/')) ? '' : s
  const params = new URLSearchParams(query)
  return !params.get('code') && !params.get('error') && fragment ? new URLSearchParams(fragment) : params
}

const token = (prefix: string) => `${prefix}.${randomBytes(24).toString('base64url')}`
const inAnHour = () => Math.floor(Date.now() / 1000) + 3600

// ------------------------------------------------------------------ device code

const deviceSessions = new Map<string, { polls: number; expires: number }>()

// Both logins start with the proxy ticket of the account form (CONTRACTS §117).
const noTicket = (req: { body?: any }) =>
  String(req.body?.proxy_ticket || '').trim()
    ? null
    : badRequest('proxy_ticket', 'required', "proxy_ticket is required: the account form's proxy choice / 缺少 proxy_ticket：账号表单的代理选择")

on('POST', '/p/codex/auth/device/start', (req) => {
  const refused = noTicket(req)
  if (refused) return refused
  const session_id = randomUUID()
  deviceSessions.set(session_id, { polls: 0, expires: Date.now() + 900_000 })
  const letters = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'
  const pick = () => Array.from({ length: 4 }, () => letters[Math.floor(Math.random() * letters.length)]).join('')
  return { data: { session_id, verification_url: 'https://auth.openai.com/codex/device', user_code: `${pick()}-${pick()}`, interval: 2, expires_in: 900 } }
})

on('POST', '/p/codex/auth/device/poll', (req) => {
  const id = String(req.body?.session_id || '').trim()
  if (!id) return noSession()
  const s = deviceSessions.get(id)
  if (!s || Date.now() > s.expires) {
    deviceSessions.delete(id)
    return { data: { status: 'expired', message: 'the device code expired; start again / 设备码已过期，请重新开始' } }
  }
  if (++s.polls < 3) return { data: { status: 'pending', interval: 2 } }
  deviceSessions.delete(id)
  return { data: {
    status: 'done',
    credentials: {
      access_token: token('eyJhbGciOiJSUzI1NiJ9.mock-access'),
      refresh_token: token('rt'),
      id_token: token('eyJhbGciOiJSUzI1NiJ9.mock-id'),
      expires_at: inAnHour(),
      email: 'codex-owner@example.com',
      chatgpt_account_id: randomUUID(),
      plan_type: 'plus'
    }
  } }
})

// ------------------------------------------------------------------ Sign in with Codex

const oauthSessions = new Map<string, string>() // session id -> state

on('POST', '/p/codex/auth/codex/start', (req) => {
  const refused = noTicket(req)
  if (refused) return refused
  const state = randomBytes(12).toString('base64url')
  const session_id = randomUUID()
  oauthSessions.set(session_id, state)
  const q = new URLSearchParams({
    client_id: 'app_EMoamEEZ73f0CkXaXp7hrann',
    response_type: 'code',
    redirect_uri: 'http://localhost:1455/auth/callback',
    scope: 'openid profile email offline_access',
    state,
    code_challenge_method: 'S256',
    code_challenge: randomBytes(32).toString('base64url'),
    id_token_add_organizations: 'true',
    codex_cli_simplified_flow: 'true'
  })
  return { data: { session_id, authorize_url: `https://auth.openai.com/oauth/authorize?${q}` } }
})

on('POST', '/p/codex/auth/codex/exchange', (req) => {
  const id = String(req.body?.session_id || '').trim()
  if (!id) return noSession()
  const p = callbackQuery(String(req.body?.callback_url || ''))
  if (!p.get('code') && !p.get('error'))
    return fail(400, 'invalid_callback', 'the callback address has no code: paste the whole address of the page that failed to open / 回调网址中没有 code：请粘贴打不开的那个页面的完整地址')
  const state = oauthSessions.get(id)
  if (!state) return fail(404, 'session_expired', 'the authorization session expired; generate a new link / 授权会话已过期，请重新生成链接')
  if (p.get('error') === 'access_denied') {
    oauthSessions.delete(id)
    return fail(400, 'access_denied', `authorization was denied / 授权被拒绝 (${p.get('error')})`)
  }
  if (p.get('error')) return fail(400, 'authorization_failed', `authorization failed / 授权失败 (${p.get('error')})`)
  if (p.get('state') !== state)
    return fail(400, 'state_mismatch', 'the callback does not belong to this authorization (state mismatch); paste the address from the latest link / 回调与本次授权不匹配（state 不一致），请粘贴最新链接的回调地址')
  oauthSessions.delete(id)
  return { data: {
    credentials: {
      access_token: token('eyJhbGciOiJSUzI1NiJ9.mock-oauth'),
      refresh_token: token('rt'),
      id_token: token('eyJhbGciOiJSUzI1NiJ9.mock-id'),
      expires_at: inAnHour(),
      email: 'codex-owner@example.com',
      chatgpt_account_id: randomUUID(),
      plan_type: 'pro'
    }
  } }
})

// ------------------------------------------------------------------ official usage resets
// POST /reset-credits/list, /summary and /consume as the plugin answers
// them (docs/PLUGIN-CODEX.md, official usage resets). Account 29 starts with
// two resets and a used-up 5 h window; other codex accounts have none.

const day = 86400
const unix = (offsetSec: number) => Math.floor(Date.now() / 1000) + offsetSec
const resetCredits = new Map<number, any[]>([
  [29, [
    { id: 'rlrc_mock_referral', title: 'Referral reset', description: 'Thanks for inviting a friend to Codex', status: 'available', granted_at: unix(-6 * day), expires_at: unix(20 * day) },
    { id: 'rlrc_mock_welcome', title: null, description: null, status: 'available', granted_at: unix(-30 * day), expires_at: null },
    { id: 'rlrc_mock_used', title: 'Full reset', description: null, status: 'redeemed', granted_at: unix(-60 * day), expires_at: null }
  ]]
])

// accounts.ts imports this file at load time, so it is imported lazily here.
const accountsMock = () => import('./accounts')

async function resetAccount(id: number) {
  const { accounts } = await accountsMock()
  const a = accounts.find((x) => x.id === id)
  if (!a || a.plugin_key !== 'codex') return fail(404, 'not_found', 'account not found / 账号不存在')
  return a
}

const available = (list: any[]) => list.filter((c) => c.status === 'available')

on('POST', '/p/codex/reset-credits/list', async (req) => {
  const a = await resetAccount(Number(req.body?.account_id))
  if ('__status' in a) return a
  await new Promise((ok) => setTimeout(ok, 400))
  const list = resetCredits.get(a.id) || []
  return { data: { available_count: available(list).length, credits: list } }
})

// Only the cache: accounts whose count /reset-credits/list has written (here every codex account with resets).
on('POST', '/p/codex/reset-credits/summary', (req) => {
  const ids: number[] = Array.isArray(req.body?.account_ids) ? req.body.account_ids : []
  const out: Record<string, { available_count: number; updated_at: number }> = {}
  for (const id of ids) {
    const list = resetCredits.get(Number(id))
    if (list) out[String(id)] = { available_count: available(list).length, updated_at: unix(-300) }
  }
  return { data: { accounts: out } }
})

on('POST', '/p/codex/reset-credits/consume', async (req) => {
  const ids = Array.isArray(req.body?.account_ids) ? req.body.account_ids : []
  if (ids.length !== 1) return badRequest('account_ids', 'invalid', 'exactly one account / 只能选择一个账号')
  const a = await resetAccount(Number(ids[0]))
  if ('__status' in a) return a
  await new Promise((ok) => setTimeout(ok, 800))
  const list = available(resetCredits.get(a.id) || [])
  const credit = req.body?.credit_id ? list.find((c) => c.id === req.body.credit_id) : list[0]
  if (!credit) return fail(409, 'no_credit', 'The account has no reset left / 账号没有剩余重置次数')
  if (a.status !== 'auto_disabled') return fail(409, 'nothing_to_reset', 'No usage window can be reset right now; no reset was used / 当前没有可重置的用量窗口，未消耗次数')
  credit.status = 'redeemed'
  const { resetCodexUsage } = await accountsMock()
  return { data: { outcome: 'reset', windows_reset: resetCodexUsage(a.id), account_restored: true } }
})
