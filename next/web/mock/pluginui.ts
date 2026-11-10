import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, normalize } from 'node:path'
import { now, on, paginate } from './router'
import { moderationAsset, moderationUIPlugin } from './moderation'
import { dynweightAsset, dynweightUIPlugin } from './dynweight'
import { codexAsset, codexUIPlugin } from './codex'
import { controlsAsset, controlsUIPlugin } from './controls'

// Mock handlers: GET /ui/plugins, plugin-owned routes (/p/<key>/...) and the
// /plugin-ui/<key>/<hash>/... static files (guard native UI from its dist
// folder, a demo iframe page and a demo iframe account form).

interface MockUIPlugin {
  menus?: unknown[]
  pages?: Record<string, unknown>
  slots?: unknown[]
  account_actions?: unknown[]
  native_entry?: string
}

const GUARD_DIST = fileURLToPath(new URL('../../plugins/guard/ui/native/dist/', import.meta.url))

const uiPlugins = [
  moderationUIPlugin,
  dynweightUIPlugin,
  codexUIPlugin,
  controlsUIPlugin,
  {
    key: 'anthropic',
    version: '0.1.0',
    name: { en: 'Anthropic', zh: 'Anthropic' },
    asset_base: '/plugin-ui/anthropic/0.1.0-dev',
    pages: {
      models: {
        type: 'table',
        title: { en: 'Model catalog', zh: '模型目录' },
        source: 'GET /models',
        search: 'q',
        columns: [
          { key: 'id', label: { en: 'Model', zh: '模型' } },
          { key: 'family', label: { en: 'Family', zh: '系列' }, format: 'badge' },
          { key: 'context_window', label: { en: 'Context', zh: '上下文' }, format: 'number' },
          { key: 'input_price', label: { en: 'Input $/M', zh: '输入 $/M' }, format: 'currency' },
          { key: 'released_at', label: { en: 'Released', zh: '发布时间' }, format: 'datetime' }
        ]
      }
    },
    slots: [],
    native_entry: '',
    trust: 'official'
  },
  {
    key: 'guard',
    version: '0.1.0',
    name: { en: 'Guard', zh: '请求守卫' },
    asset_base: '/plugin-ui/guard/0.1.0-dev',
    menus: [{ id: 'guard', section: 'plugins', label: { en: 'Request guard', zh: '请求守卫' }, page: 'dashboard' }],
    pages: { dashboard: { type: 'native', component: 'GuardDashboard' } },
    slots: [{ slot: 'dashboard.widgets', component: 'BlockedTodayCard', permission: 'stats:read' }],
    native_entry: 'ui/native/entry.js',
    trust: 'official',
    host_ui_compat: '^1.0'
  },
  {
    key: 'demo',
    version: '0.0.1',
    name: { en: 'Demo platform', zh: '演示平台' },
    asset_base: '/plugin-ui/demo/0.0.1-dev',
    menus: [
      { id: 'status', section: 'plugins', label: { en: 'Demo iframe', zh: '演示 iframe' }, page: 'status' },
      { id: 'settings', section: 'plugins', label: { en: 'Demo form', zh: '演示表单' }, page: 'settings' }
    ],
    pages: {
      status: { type: 'iframe', title: { en: 'Demo iframe page', zh: '演示 iframe 页面' }, src: 'ui/iframe/status.html' },
      settings: { type: 'form', title: { en: 'Demo settings', zh: '演示设置' }, schema: 'forms/settings.schema.json', source: 'GET /settings', submit: 'PUT /settings' }
    },
    slots: [],
    native_entry: '',
    trust: 'community'
  }
]

// Like the server (plugin/api/ui.go): a plugin with no menu, page, slot,
// account action or native entry is left out, so a plugin that only has a
// native account form (codex) is listed here exactly when it is in production.
const hasExtension = (p: MockUIPlugin) =>
  !!(p.menus?.length || Object.keys(p.pages || {}).length || p.slots?.length || p.account_actions?.length || p.native_entry)
on('GET', '/ui/plugins', () => uiPlugins.filter(hasExtension))

// The anthropic model catalog is the mock's declarative table. It declares
// `search: 'q'` and the route below really honours ?q= and really paginates
// (it answers with a page envelope), so `npm run dev` exercises both halves of
// the declaration: the box appears because the page declared a parameter, and
// DeclarativeTable reads server pagination off the response instead of
// guessing it from the row count. Giving the demo route only three rows used
// to hide the bug this replaces - the old heuristic said "client paged" until
// the data outgrew one page.
const MOCK_MODELS = [
  { id: 'claude-opus-4-1', family: 'opus', context_window: 200000, input_price: '15', released_at: '2025-08-05T00:00:00Z' },
  { id: 'claude-sonnet-4-5', family: 'sonnet', context_window: 1000000, input_price: '3', released_at: '2025-09-29T00:00:00Z' },
  { id: 'claude-haiku-4-5', family: 'haiku', context_window: 200000, input_price: '1', released_at: '2025-10-15T00:00:00Z' }
]

on('GET', '/p/anthropic/models', (req) => {
  const q = String(req.query.q || '')
    .trim()
    .toLowerCase()
  const items = q ? MOCK_MODELS.filter((m) => m.id.toLowerCase().includes(q) || m.family.includes(q)) : MOCK_MODELS
  return paginate(items, req.query)
})

let demoSettings: Record<string, unknown> = { endpoint: 'https://demo.example.com', retries: 3, verbose: false }
on('GET', '/p/demo/settings', () => demoSettings)
on('PUT', '/p/demo/settings', (req) => (demoSettings = req.body || {}))
on('GET', '/p/demo/ping', () => ({ pong: true, at: now() }))

let guardRules = [
  { id: 1, name: 'ID number', kind: 'regex', pattern: '\\d{17}[\\dXx]', enabled: true, updated_at: now(-86400) },
  { id: 2, name: 'Forbidden word', kind: 'keyword', pattern: 'xxx', enabled: true, updated_at: now(-3600) }
]
on('GET', '/p/guard/rules', () => guardRules)
on('PUT', '/p/guard/rules', (req) => {
  const list = Array.isArray(req.body) ? req.body : req.body?.rules || []
  let id = Math.max(0, ...guardRules.map((r) => r.id))
  guardRules = list.map((r: any) => ({ ...r, id: r.id ?? ++id, updated_at: now() }))
  return guardRules
})
on('GET', '/p/guard/stats', (req) => {
  const range = req.query.range || 'today'
  const day = range === '30d'
  const points = range === '30d' ? 30 : range === '7d' ? 7 * 24 : 24
  const step = day ? 86400000 : 3600000
  const start = Date.now() - points * step
  const trend = Array.from({ length: points }, (_, i) => {
    const total = 200 + Math.round(150 * Math.sin(i / 3) + Math.random() * 80)
    return { ts: new Date(start + i * step).toISOString(), blocked: Math.round(Math.random() * 12 * (day ? 20 : 1)), total: total * (day ? 20 : 1) }
  })
  const blocked = trend.reduce((a, p) => a + p.blocked, 0)
  return {
    from: new Date(start).toISOString(),
    to: now(),
    bucket: day ? 'day' : 'hour',
    blocked_total: req.query.from ? Math.round(blocked * 0.8) : blocked,
    requests_total: trend.reduce((a, p) => a + p.total, 0),
    trend,
    top_rules: [
      { rule_id: 2, name: 'Forbidden word', kind: 'keyword', pattern: 'xxx', hits: Math.round(blocked * 0.6) },
      { rule_id: 1, name: 'ID number', kind: 'regex', pattern: '\\d{17}[\\dXx]', hits: Math.round(blocked * 0.4) }
    ],
    recent: Array.from({ length: 5 }, (_, i) => ({
      occurred_at: now(-i * 420),
      rule_id: 2,
      rule_name: 'Forbidden word',
      request_id: 'req_' + Math.random().toString(16).slice(2, 14),
      user_id: 7,
      group_id: 1,
      model: 'claude-sonnet-4-5',
      snippet: '... xxx ...'
    }))
  }
})

// ------------------------------------------------------------------ static plugin assets

const BRIDGE = `
const pending = new Map(); let seq = 0; let state = {};
function post(m) { parent.postMessage(Object.assign({ s2a: 1 }, m), '*') }
function call(method, params) { const id = 'f' + (++seq); post({ kind: 'request', id, method, params }); return new Promise((res, rej) => pending.set(id, { res, rej })) }
function resize() { post({ kind: 'resize', height: document.documentElement.scrollHeight }) }
function applyTheme(t) { document.body.className = t === 'dark' ? 'dark' : '' }
window.addEventListener('message', (e) => {
  if (e.source !== parent) return; const m = e.data; if (!m || m.s2a !== 1) return;
  if (m.kind === 'init') { state = m; applyTheme(m.theme); window.onInit && window.onInit(m); resize() }
  else if (m.kind === 'theme') { applyTheme(m.theme) }
  else if (m.kind === 'locale') { state.locale = m.locale; window.onInit && window.onInit(state) }
  else if (m.kind === 'response') { const p = pending.get(m.id); if (p) { pending.delete(m.id); m.error ? p.rej(m.error) : p.res(m.result) } }
  else if (m.kind === 'request') { Promise.resolve(window.onRequest ? window.onRequest(m.method, m.params) : undefined).then((r) => post({ kind: 'response', id: m.id, result: r }), (err) => post({ kind: 'response', id: m.id, error: { code: 'internal', message: String(err) } })) }
});
new ResizeObserver(resize).observe(document.documentElement);
post({ kind: 'ready' });
`

const STYLE = `body{font:14px system-ui,sans-serif;margin:0;padding:12px;color:#111827;background:transparent}body.dark{color:#e5e7eb}
input{width:100%;box-sizing:border-box;padding:8px 10px;border:1px solid #d1d5db;border-radius:10px;background:transparent;color:inherit}
label{display:block;margin:10px 0 4px;font-weight:500}.err{color:#ef4444;font-size:12px}button{padding:6px 12px;border-radius:8px;border:1px solid #d1d5db;background:transparent;color:inherit;cursor:pointer;margin-right:6px}pre{background:rgba(148,163,184,.15);padding:8px;border-radius:8px;white-space:pre-wrap}`

const STATUS_PAGE = `<!doctype html><html><head><meta charset="utf-8"><style>${STYLE}</style></head><body>
<h3 id="title">Demo iframe page</h3><p>This page runs in a sandbox (origin null) and talks to the console via postMessage.</p>
<button id="ping">api.call GET /ping</button><button id="toast">toast</button><button id="nav">navigate /dashboard</button><button id="bad">api.call /api/v1/users (blocked)</button>
<pre id="out">waiting for init…</pre>
<script>${BRIDGE}
const out = document.getElementById('out');
window.onInit = (m) => { out.textContent = JSON.stringify({ plugin: m.plugin, page: m.page, theme: m.theme, locale: m.locale }, null, 2) };
document.getElementById('ping').onclick = () => call('api.call', { method: 'GET', path: '/ping' }).then((r) => out.textContent = JSON.stringify(r, null, 2), (e) => out.textContent = 'error: ' + JSON.stringify(e));
document.getElementById('toast').onclick = () => call('toast', { message: 'Hello from the iframe', kind: 'success' });
document.getElementById('nav').onclick = () => call('navigate', { path: '/dashboard' });
document.getElementById('bad').onclick = () => call('api.call', { method: 'GET', path: '/api/v1/users' }).then((r) => out.textContent = JSON.stringify(r), (e) => out.textContent = 'rejected as expected: ' + JSON.stringify(e));
</script></body></html>`

const ACCOUNT_FORM = `<!doctype html><html><head><meta charset="utf-8"><style>${STYLE}</style></head><body>
<label>Token *</label><input id="token" type="password" autocomplete="off"><div class="err" id="e_token"></div>
<label>Region</label><input id="region" placeholder="us-east-1"><div class="err" id="e_region"></div>
<script>${BRIDGE}
const f = { token: document.getElementById('token'), region: document.getElementById('region') };
function value() { return { token: f.token.value || (state.value && state.value.token) || '', region: f.region.value } }
function showErrors(errs) { for (const k of ['token', 'region']) document.getElementById('e_' + k).textContent = (errs && errs[k]) || '' }
window.onInit = (m) => { const v = m.value || {}; f.region.value = v.region || ''; f.token.placeholder = v.token === '******' ? '****** (unchanged)' : '' };
for (const el of Object.values(f)) el.addEventListener('input', () => post({ kind: 'change', value: value() }));
window.onRequest = (method, params) => {
  if (method === 'getValue') return value();
  if (method === 'setValue') { f.region.value = (params.value || {}).region || ''; return true }
  if (method === 'setErrors') { showErrors(params.errors); return true }
  if (method === 'validate') { const v = value(); const errors = {}; if (!v.token) errors.token = 'Token is required'; showErrors(errors); return { ok: !Object.keys(errors).length, errors } }
};
</script></body></html>`

const DEMO_SETTINGS_SCHEMA = {
  type: 'object',
  required: ['endpoint'],
  properties: {
    endpoint: { type: 'string', format: 'uri', title: 'Endpoint' },
    retries: { type: 'integer', minimum: 0, maximum: 10, title: 'Retries' },
    verbose: { type: 'boolean', title: 'Verbose logging' },
    tags: { type: 'array', items: { type: 'string' }, title: 'Tags' }
  }
}

/** Serves /plugin-ui/<key>/<hash>/<path>; returns null when unknown. */
export function pluginAsset(pathname: string): { type: string; body: string | Buffer } | null {
  const m = /^\/plugin-ui\/([^/]+)\/[^/]+\/(.+)$/.exec(pathname)
  if (!m) return null
  const [, key, path] = m
  if (key === 'moderation') return moderationAsset(path)
  if (key === 'dynweight') return dynweightAsset(path)
  if (key === 'codex') return codexAsset(path)
  if (key === 'controls_demo') return controlsAsset(path)
  if (key === 'guard' && path.startsWith('ui/native/')) {
    const rel = normalize(path.slice('ui/native/'.length))
    if (rel.startsWith('..')) return null
    const file = join(GUARD_DIST, rel)
    if (!existsSync(file)) return null
    return { type: rel.endsWith('.css') ? 'text/css' : 'text/javascript', body: readFileSync(file) }
  }
  if (key === 'demo' && path === 'ui/iframe/status.html') return { type: 'text/html', body: STATUS_PAGE }
  if (key === 'demo' && path === 'ui/iframe/account.html') return { type: 'text/html', body: ACCOUNT_FORM }
  if (key === 'demo' && path === 'forms/settings.schema.json') return { type: 'application/json', body: JSON.stringify(DEMO_SETTINGS_SCHEMA) }
  if (key === 'demo' && path === 'forms/settings.ui.json') {
    return { type: 'application/json', body: JSON.stringify({ endpoint: { 'ui:widget': 'url-presets', 'ui:options': { presets: ['https://demo.example.com'] } } }) }
  }
  return null
}
