import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, normalize } from 'node:path'
import { accounts } from './accounts'
import { fail, on, paginate } from './router'

// Mock of the dynweight plugin routes (/p/dynweight/*, CONTRACTS §74.5) and
// its native UI from plugins/dynweight/ui/native/dist (build it first). The
// configuration and a few degraded keys are kept in memory; validation only
// checks what the console cannot (version conflicts), the plugin itself does
// the rest. The account list, detail tab, editor card and the plugin page
// can all be tried: accounts 12 and 21 are degraded, 13 has an override.

const DW_DIST = fileURLToPath(new URL('../../plugins/dynweight/ui/native/dist/', import.meta.url))

export const dynweightUIPlugin = {
  key: 'dynweight',
  version: '0.5.0',
  name: { en: 'Dynamic Weight', zh: '动态权重' },
  asset_base: '/plugin-ui/dynweight/0.5.0-dev',
  menus: [{ id: 'dynweight', section: 'gateway', label: { en: 'Dynamic Weight', zh: '动态权重' }, page: 'dashboard', icon: 'chart' }],
  pages: { dashboard: { type: 'native', title: { en: 'Dynamic Weight', zh: '动态权重' }, component: 'DynweightDashboard' } },
  slots: [
    { slot: 'account.list.weight', component: 'DynweightAccountWeight', permission: 'dynweight:read' },
    { slot: 'account.list.toolbar', component: 'DynweightDegradedBadge', permission: 'dynweight:read' },
    { slot: 'account.detail.tabs', component: 'DynweightAccountTab', permission: 'dynweight:read' }
  ],
  // manifest ui.accountActions as /ui/plugins hands it down (CONTRACTS §23.3 "UI 3.6").
  account_actions: [
    {
      id: 'reset',
      label: { en: 'Reset dynamic weight', zh: '重置动态权重' },
      icon: 'refresh',
      permission: 'dynweight:manage',
      route: 'POST /reset',
      bulk: true,
      confirm: { en: 'Reset the dynamic weight of {n} account(s)?', zh: '重置 {n} 个账号的动态权重？' }
    }
  ],
  native_entry: 'ui/native/entry.js',
  trust: 'official',
  host_ui_compat: '^3.6'
}

export function dynweightAsset(path: string): { type: string; body: Buffer } | null {
  if (!path.startsWith('ui/native/')) return null
  const rel = normalize(path.slice('ui/native/'.length))
  if (rel.startsWith('..')) return null
  const file = join(DW_DIST, rel)
  if (!existsSync(file)) return null
  return { type: rel.endsWith('.css') ? 'text/css' : 'text/javascript', body: readFileSync(file) }
}

// The factory layer of dynweight 0.4.0 (rules.DefaultLayer).
const DEFAULT_LAYER = {
  enabled: true,
  window_seconds: 60,
  min_samples: 10,
  min_samples_strict: true,
  clear_sticky_on_zero: true,
  exempt_status_codes: '400',
  min_weight_mode: 'base_percent',
  min_weight: 0,
  rules: [
    { name: '错误率过高', conditions: [{ metric: 'error_rate', op: 'gte', value: 30 }], periods: 3, hit_periods: 2 },
    { name: '首字 p95 过慢', conditions: [{ metric: 'ttfb', p: 95, op: 'gte', value: 10000 }], periods: 3, hit_periods: 2 },
    {
      name: '首字挂死',
      conditions: [
        { metric: 'ttfb', p: 100, op: 'gte', value: 60000 },
        { join: 'or', metric: 'latency', p: 100, op: 'gte', value: 120000, scope: 'non_stream' }
      ],
      periods: 1,
      hit_periods: 1
    },
    {
      name: '健康恢复',
      conditions: [
        { metric: 'error_rate', op: 'lt', value: 15 },
        { join: 'and', metric: 'ttfb', p: 95, op: 'lt', value: 10000, when_empty: 'pass' },
        { join: 'and', metric: 'ttfb', p: 100, op: 'lt', value: 60000, when_empty: 'pass' },
        { join: 'and', metric: 'latency', p: 100, op: 'lt', value: 120000, scope: 'non_stream', when_empty: 'pass' }
      ],
      periods: 1,
      hit_periods: 1
    },
    { name: '复活探测', conditions: [{ metric: 'weight', op: 'lte', value: 0 }], periods: 2, hit_periods: 2 }
  ],
  strategies: [
    { name: '错误率过高', when: '错误率过高', actions: [{ type: 'weight_degrade', mode: 'current_percent', value: 30 }, { type: 'affinity_clear', percent: 30 }] },
    { name: '首字 p95 过慢', when: '首字 p95 过慢', actions: [{ type: 'weight_degrade', mode: 'current_percent', value: 30 }, { type: 'affinity_clear', percent: 30 }] },
    { name: '首字挂死', when: '首字挂死', actions: [{ type: 'weight_degrade', mode: 'current_percent', value: 50 }, { type: 'affinity_clear', percent: 30 }] },
    { name: '自动回升', when: '健康恢复', actions: [{ type: 'weight_climb', mode: 'base_percent', value: 10 }] },
    { name: '复活探测', when: '复活探测', actions: [{ type: 'weight_climb', mode: 'base_percent', value: 10 }] }
  ]
}

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T

let version = 1
let config: any = {
  ...clone(DEFAULT_LAYER),
  // Group 2 (vip) degrades without a climb strategy: the no-climb warning shows for its accounts.
  groups: {
    '1': { ...clone(DEFAULT_LAYER), window_seconds: 120 },
    '2': { ...clone(DEFAULT_LAYER), strategies: DEFAULT_LAYER.strategies.filter((s) => s.actions.every((a) => a.type !== 'weight_climb')) }
  },
  // 13 only counted; 21 overrides the exempt codes to none and the floor (the config tab's bold cells).
  accounts: { '13': { enabled: false }, '21': { exempt_status_codes: '', min_weight_mode: 'fixed', min_weight: 1 } },
  notify: { enabled: false, channel: '', recipients: [], language: 'zh' },
  log_events: true
}

// ---- effective weights of a few keys (GET /status, /weights, /degraded, POST /reset)

const unix = (offsetSec = 0) => Math.floor(Date.now() / 1000) + offsetSec

interface MockKey {
  account_id: number
  group_id: number
  model: string
  base: number
  effective: number
  state: 'normal' | 'degrading' | 'climbing'
  hits?: string[]
  fired?: string
  /** Seconds since the last weight change. */
  ago?: number
}

const keys: MockKey[] = [
  { account_id: 12, group_id: 1, model: 'claude-sonnet-4-5', base: 3, effective: 1.47, state: 'degrading', hits: ['错误率过高'], fired: '错误率过高', ago: 300 },
  { account_id: 12, group_id: 2, model: 'claude-opus-4-1', base: 3, effective: 0, state: 'degrading', hits: ['首字挂死'], ago: 540 },
  { account_id: 12, group_id: 1, model: 'claude-haiku-4-5', base: 3, effective: 3, state: 'normal' },
  { account_id: 21, group_id: 1, model: 'claude-sonnet-4-5', base: 2, effective: 1.2, state: 'climbing', ago: 900 },
  { account_id: 13, group_id: 1, model: 'claude-sonnet-4-5', base: 1, effective: 1, state: 'normal' }
]

const degraded = (k: MockKey) => k.effective < k.base
const accountName = (id: number) => accounts.find((a) => a.id === id)?.name ?? ''
const layerOf = (groupId: number) => config.groups?.[String(groupId)] ?? config

/** A full GET /status item of a mock key, with the window and rule detail the console shows. */
function statusOf(k: MockKey) {
  const layer = layerOf(k.group_id)
  const total = 40
  const fail = k.state === 'degrading' ? 14 : 1
  const rules = (layer.rules as any[]).map((r) => {
    const hit = k.hits?.includes(r.name) ?? false
    return {
      name: r.name,
      scope: r.scope || 'all',
      periods: r.periods,
      hit_periods: r.hit_periods,
      hit,
      fired: k.fired === r.name,
      bitmap: hit ? '1'.repeat(Math.min(r.hit_periods, r.periods)) : '0'.repeat(r.periods),
      samples: total,
      lookback: 1,
      no_samples: r.conditions.some((c: any) => c.metric === 'weight'),
      unjudged: false
    }
  })
  return {
    account_id: k.account_id,
    account_name: accountName(k.account_id),
    group_id: k.group_id,
    model: k.model,
    base: k.base,
    effective: k.effective,
    weight_min: 0,
    state: k.state,
    adjusting: config.accounts?.[String(k.account_id)]?.enabled !== false && layer.enabled !== false,
    group_configured: String(k.group_id) in (config.groups ?? {}),
    no_climb_strategy: !(layer.strategies as any[]).some((s) => s.enabled !== false && s.actions.some((a: any) => a.type === 'weight_climb')),
    reset: false,
    verdict: k.state === 'degrading' ? 'bad' : k.state === 'climbing' ? 'good' : 'neutral',
    last_change: k.state === 'degrading' ? 'weight_down' : k.state === 'climbing' ? 'weight_up_start' : '',
    changed_at: k.state === 'normal' ? 0 : unix(-(k.ago ?? 420)),
    next_eval_at: unix(50),
    eval_window: unix(-180),
    total,
    fail,
    lookback: 1,
    error_evaluable: true,
    ttfb_samples: 30,
    latency_samples: 10,
    thresholds: [{ ms: 10000, count: k.state === 'degrading' ? 4 : 0 }, { ms: 60000, count: 0 }],
    latency_thresholds: [{ ms: 120000, count: 0 }],
    recent: { windows: 3, total: total * 3, fail: fail * 3 },
    rules,
    current: { window_start: unix(-45), elapsed_seconds: 45, total: 7, fail: 1, stream_total: 5, stream_fail: 1, ttfb_samples: 5, latency_samples: 2, thresholds: [{ ms: 10000, count: 0 }, { ms: 60000, count: 0 }], latency_thresholds: [{ ms: 120000, count: 0 }] }
  }
}

const events: any[] = keys
  .filter(degraded)
  .map((k, i) => ({
    id: 100 + i,
    created_at: new Date(Date.now() - 420_000 - i * 60_000).toISOString(),
    account_id: k.account_id,
    account_name: accountName(k.account_id),
    group_id: k.group_id,
    model: k.model,
    kind: k.state === 'climbing' ? 'weight_up_start' : 'weight_down',
    reason: '',
    from: k.base,
    to: k.effective,
    rules: k.hits ?? null,
    detail: { window: { total: 40, fail: 14 } }
  }))

/** Puts the keys of an account (or of the given accounts), narrowed to a group / model, back to their base. */
function resetKeys(body: any): number {
  const ids = new Set<number>([...(body?.account_ids ?? []), ...(body?.account_id ? [body.account_id] : [])].map(Number))
  const group = Number(body?.group_id) || 0
  const model = String(body?.model ?? '')
  let n = 0
  for (const k of keys) {
    if (!degraded(k) || (ids.size && !ids.has(k.account_id)) || (group && k.group_id !== group) || (model && k.model !== model)) continue
    events.unshift({ id: 200 + n, created_at: new Date().toISOString(), account_id: k.account_id, account_name: accountName(k.account_id), group_id: k.group_id, model: k.model, kind: 'reset', reason: 'manual', from: k.effective, to: k.base, rules: null, detail: null })
    k.effective = k.base
    k.state = 'normal'
    k.hits = undefined
    k.fired = undefined
    n++
  }
  return n
}

on('GET', '/p/dynweight/config', () => ({ config, version }))
on('GET', '/p/dynweight/defaults', () => DEFAULT_LAYER)
on('GET', '/p/dynweight/channels', () => [])
on('POST', '/p/dynweight/config/validate', (req) => ({ config: req.body?.config, version: req.body?.version ?? version, warnings: [] }))
on('PUT', '/p/dynweight/config', (req) => {
  if (Number(req.body?.version) !== version) return fail(409, 'conflict', 'the configuration was changed meanwhile, reload it / 配置已被修改，请刷新后重试')
  config = req.body.config
  version++
  return { config, version, warnings: [] }
})
on('GET', '/p/dynweight/status', (req) => {
  const q = req.query
  const list = keys
    .filter((k) => (!q.account_id || k.account_id === Number(q.account_id)) && (!q.group_id || k.group_id === Number(q.group_id)) && (!q.model || k.model === q.model) && (!q.degraded || degraded(k)))
    .sort((a, b) => a.effective / a.base - b.effective / b.base)
  return paginate(list.map(statusOf), q)
})
on('GET', '/p/dynweight/events', (req) => {
  const q = req.query
  const list = events.filter((e) => (!q.account_id || e.account_id === Number(q.account_id)) && (!q.group_id || e.group_id === Number(q.group_id)) && (!q.model || e.model === q.model) && (!q.kind || e.kind === q.kind))
  return paginate(list, q)
})
on('GET', '/p/dynweight/stats', () => ({ enabled: true, config_version: version, node_id: 'mock-1', started_at: Math.floor(Date.now() / 1000), degraded_keys: keys.filter(degraded).length }))
// Per account: the degraded keys' count, the lowest effective weight and its base, and the lowest keys (0.5.0)
// with their direction and last change (0.5.1); an account whose override turns adjusting off is listed with
// adjust_off even without a degraded key (0.5.1).
on('GET', '/p/dynweight/weights', (req) => {
  const ids = String(req.query.account_ids ?? '').split(',').map(Number)
  const out: Record<string, unknown> = {}
  for (const id of ids) {
    const mine = keys.filter((k) => k.account_id === id && degraded(k)).sort((a, b) => a.effective - b.effective)
    const adjustOff = config.accounts?.[String(id)]?.enabled === false
    if (!mine.length && !adjustOff) continue
    out[id] = {
      degraded: mine.length,
      lowest: mine.length ? Math.round(mine[0].effective) : 0,
      base: mine[0]?.base ?? 0,
      keys: mine.slice(0, 10).map((k) => ({
        group_id: k.group_id,
        model: k.model,
        eff: k.effective,
        base: k.base,
        state: k.state === 'normal' ? '' : k.state,
        changed_at: new Date(unix(-(k.ago ?? 420)) * 1000).toISOString().replace(/\.\d+Z$/, 'Z')
      })),
      ...(adjustOff ? { adjust_off: true } : {})
    }
  }
  return out
})
on('GET', '/p/dynweight/degraded', () => {
  const ids = [...new Set(keys.filter(degraded).map((k) => k.account_id))].sort((a, b) => a - b)
  return { count: ids.length, account_ids: ids.slice(0, 500) }
})
on('POST', '/p/dynweight/reset', (req) => {
  const b = req.body ?? {}
  if (!b.account_id && !b.account_ids?.length && !b.group_id) return fail(400, 'invalid_argument', 'invalid request / 请求参数不正确', { fields: [{ field: 'account_id', code: 'required', message: 'an account or a group is required / 请指定账号或分组' }] })
  return { reset: resetKeys(b) }
})
