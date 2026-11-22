// Mock handlers of the price sync sources: the sources, and their previews
// and applies against the mock catalogs (catalog.ts).
import { fail, nextId, noContent, now, on, type MockRequest } from '../router'
import { priceExpression } from '../../src/views/prices/priceExpr'
import { CATALOG, SKIPPED, UPSTREAM_GROUP_RATE, scaleConfig, vendorOf, type CatalogEntry } from './catalog'
import { MODEL_ID, prices, remember, type MockPrice } from './priceStore'
import { fieldFail, type FieldError } from './shared'

type SourceKind = 'litellm' | 'models_dev' | 'sup2api'
const SOURCE_KINDS: SourceKind[] = ['litellm', 'models_dev', 'sup2api']
const DEFAULT_URL: Record<SourceKind, string> = {
  litellm: 'https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json',
  models_dev: 'https://models.dev/api.json',
  sup2api: ''
}
const DEFAULT_PROVIDERS: Record<'litellm' | 'models_dev', string[]> = {
  litellm: ['anthropic', 'openai', 'gemini'],
  models_dev: ['anthropic', 'openai', 'google']
}

interface MockSource {
  id: number
  name: string
  kind: SourceKind
  url: string
  api_key: string
  options: Record<string, any>
  enabled: boolean
  last_synced_at: string | null
  last_error: string
  created_at: string
  updated_at: string
}

export const sources: MockSource[] = [
  {
    id: 1,
    name: 'LiteLLM',
    kind: 'litellm',
    url: DEFAULT_URL.litellm,
    api_key: '',
    options: { providers: [...DEFAULT_PROVIDERS.litellm] },
    enabled: true,
    last_synced_at: now(-2 * 86400),
    last_error: '',
    created_at: now(-30 * 86400),
    updated_at: now(-2 * 86400)
  },
  {
    id: 2,
    name: 'models.dev',
    kind: 'models_dev',
    url: DEFAULT_URL.models_dev,
    api_key: '',
    options: { providers: [...DEFAULT_PROVIDERS.models_dev] },
    enabled: true,
    last_synced_at: now(-5 * 86400),
    last_error: '',
    created_at: now(-30 * 86400),
    updated_at: now(-5 * 86400)
  },
  {
    id: 3,
    name: '上游 A',
    kind: 'sup2api',
    url: 'https://up.example.com',
    api_key: 'sk-s2a-mock-upstream',
    options: { apply_multiplier: true },
    enabled: true,
    last_synced_at: null,
    last_error: '',
    created_at: now(-3 * 86400),
    updated_at: now(-3 * 86400)
  },
  {
    id: 4,
    name: '上游 B（离线）',
    kind: 'sup2api',
    url: 'https://down.example.com',
    api_key: 'sk-s2a-mock-down',
    options: { apply_multiplier: false },
    enabled: false,
    last_synced_at: null,
    last_error: 'GET https://down.example.com/api/v1/me/prices: dial tcp: connection refused',
    created_at: now(-3 * 86400),
    updated_at: now(-3 * 86400)
  }
]

function sourceView(s: MockSource) {
  const { api_key, ...rest } = s
  return { ...rest, has_api_key: !!api_key, price_count: prices.filter((p) => p.sync_source_id === s.id).length }
}

const findSource = (req: MockRequest) => sources.find((s) => s.id === Number(req.params.id))

function isHttpUrl(v: unknown): boolean {
  try {
    const u = new URL(String(v))
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.host
  } catch {
    return false
  }
}

/** Validates and normalizes options for a kind; returns the options or a field error list. */
function sourceOptions(kind: SourceKind, raw: any): Record<string, any> | FieldError[] {
  if (kind === 'sup2api') {
    if (raw?.apply_multiplier !== undefined && typeof raw.apply_multiplier !== 'boolean') {
      return [{ field: 'options.apply_multiplier', code: 'invalid', message: 'must be a boolean' }]
    }
    return { apply_multiplier: raw?.apply_multiplier !== false }
  }
  if (raw?.providers === undefined) return { providers: [...DEFAULT_PROVIDERS[kind]] }
  if (!Array.isArray(raw.providers) || raw.providers.some((x: unknown) => typeof x !== 'string' || !/^[a-z0-9_.-]{1,64}$/i.test(x))) {
    return [{ field: 'options.providers', code: 'invalid', message: 'providers must be a list of vendor ids' }]
  }
  return { providers: [...new Set(raw.providers as string[])] }
}

on('GET', '/price-sources', () => sources.map(sourceView))

on('POST', '/price-sources', (req) => {
  const b = req.body || {}
  const errs: FieldError[] = []
  const name = String(b.name ?? '').trim()
  if (!name) errs.push({ field: 'name', code: 'required', message: 'Name is required' })
  if (!SOURCE_KINDS.includes(b.kind)) errs.push({ field: 'kind', code: 'enum', message: 'kind must be litellm, models_dev or sup2api' })
  const kind = b.kind as SourceKind
  const url = String(b.url ?? (SOURCE_KINDS.includes(kind) ? DEFAULT_URL[kind] : '')).trim()
  if (!url) errs.push({ field: 'url', code: 'required', message: 'URL is required' })
  else if (!isHttpUrl(url)) errs.push({ field: 'url', code: 'invalid', message: 'must be an http(s) URL' })
  if (kind === 'sup2api' && !String(b.api_key ?? '').trim()) errs.push({ field: 'api_key', code: 'required', message: 'An upstream sup2api source needs an API key' })
  const opts = SOURCE_KINDS.includes(kind) ? sourceOptions(kind, b.options) : {}
  if (Array.isArray(opts)) errs.push(...opts)
  if (errs.length) return fieldFail(errs)
  if (sources.some((s) => s.name === name)) return fail(409, 'conflict', 'a price source with this name already exists')
  const s: MockSource = {
    id: nextId(),
    name,
    kind,
    url,
    api_key: String(b.api_key ?? '').trim(),
    options: opts as Record<string, any>,
    enabled: b.enabled !== false,
    last_synced_at: null,
    last_error: '',
    created_at: now(),
    updated_at: now()
  }
  sources.push(s)
  return { __status: 201, body: { data: sourceView(s) } }
})

on('PATCH', '/price-sources/:id', (req) => {
  const s = findSource(req)
  if (!s) return fail(404, 'not_found', 'price source not found')
  const b = req.body || {}
  const errs: FieldError[] = []
  const kind: SourceKind = b.kind !== undefined ? b.kind : s.kind
  if (!SOURCE_KINDS.includes(kind)) errs.push({ field: 'kind', code: 'enum', message: 'kind must be litellm, models_dev or sup2api' })
  const name = b.name !== undefined ? String(b.name).trim() : s.name
  if (!name) errs.push({ field: 'name', code: 'required', message: 'Name is required' })
  const url = b.url !== undefined ? String(b.url).trim() : s.url
  if (!url) errs.push({ field: 'url', code: 'required', message: 'URL is required' })
  else if (!isHttpUrl(url)) errs.push({ field: 'url', code: 'invalid', message: 'must be an http(s) URL' })
  // api_key: omitted / null = keep, "" = clear, other = replace.
  const apiKey = b.api_key === undefined || b.api_key === null ? s.api_key : String(b.api_key).trim()
  if (kind === 'sup2api' && !apiKey) errs.push({ field: 'api_key', code: 'required', message: 'An upstream sup2api source needs an API key' })
  const opts = SOURCE_KINDS.includes(kind) ? (b.options !== undefined || kind !== s.kind ? sourceOptions(kind, b.options) : s.options) : {}
  if (Array.isArray(opts)) errs.push(...opts)
  if (errs.length) return fieldFail(errs)
  if (sources.some((x) => x.id !== s.id && x.name === name)) return fail(409, 'conflict', 'a price source with this name already exists')
  Object.assign(s, { name, kind, url, api_key: apiKey, options: opts, enabled: b.enabled !== undefined ? !!b.enabled : s.enabled, updated_at: now() })
  return sourceView(s)
})

on('DELETE', '/price-sources/:id', (req) => {
  const i = sources.findIndex((s) => s.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', 'price source not found')
  const id = sources[i].id
  // Imported prices are kept; they just lose the link to the source.
  for (const p of prices) if (p.sync_source_id === id) p.sync_source_id = null
  sources.splice(i, 1)
  return noContent()
})

/** "Fetches" a source: the mock catalog filtered by vendors, or an error message. */
function fetchCatalog(s: MockSource): { entries: CatalogEntry[] } | { error: string } {
  if (/down|fail/i.test(s.url)) return { error: `GET ${s.url.replace(/\/$/, '')}${s.kind === 'sup2api' ? '/api/v1/me/prices' : ''}: dial tcp: connection refused` }
  if (s.kind === 'sup2api') {
    if (!s.api_key) return { error: 'upstream rejected the API key (401 unauthenticated)' }
    const m = s.options?.apply_multiplier === false ? 1 : UPSTREAM_GROUP_RATE
    return { entries: CATALOG.sup2api.map((e) => ({ ...e, config: m === 1 ? e.config : scaleConfig(e.config, m) })) }
  }
  const kind = s.kind
  const want = new Set<string>(Array.isArray(s.options?.providers) ? s.options.providers : DEFAULT_PROVIDERS[kind])
  return { entries: CATALOG[kind].filter((e) => want.has(vendorOf(kind, e.family))) }
}

function incomingOf(e: CatalogEntry) {
  return { mode: e.mode, config: e.config, expression: priceExpression(e.mode, e.config) }
}

function syncAction(e: CatalogEntry): { action: 'create' | 'update' | 'manual' | 'unchanged'; current: MockPrice | undefined } {
  const current = prices.find((p) => p.model === e.model)
  if (!current) return { action: 'create', current }
  const inc = incomingOf(e)
  if (current.mode === inc.mode && current.expression.trim() === inc.expression.trim()) return { action: 'unchanged', current }
  return { action: current.source === 'sync' ? 'update' : 'manual', current }
}

/** The catalog of a source; a failure that it keeps as its last error when it cannot be fetched. */
function fetchOrFail(s: MockSource) {
  const r = fetchCatalog(s)
  if (!('error' in r)) return r
  s.last_error = r.error
  return fail(503, 'unavailable', `cannot fetch prices from ${s.name}: ${r.error}`)
}

/** A line of a preview: what applying the model would do, the incoming price and the current one. */
function previewItem(e: CatalogEntry) {
  const { action, current } = syncAction(e)
  return {
    model: e.model,
    action,
    incoming: incomingOf(e),
    current: current
      ? { id: current.id, mode: current.mode, config: current.config, expression: current.expression, source: current.source, sync_source_id: current.sync_source_id, enabled: current.enabled }
      : null
  }
}

on('POST', '/price-sources/:id/preview', (req) => {
  const s = findSource(req)
  if (!s) return fail(404, 'not_found', 'price source not found')
  const r = fetchOrFail(s)
  if ('__status' in r) return r
  s.last_error = ''
  const items = r.entries.map(previewItem).sort((a, b) => a.model.localeCompare(b.model))
  return { source_id: s.id, fetched_at: now(), total: items.length, skipped: SKIPPED[s.kind], items }
})

type ApplyResult = { created: number; updated: number; unchanged: number; skipped: Array<{ model: string; reason: string }> }

/** Why an apply skips a model it names; null when it applies it. */
function skipReason(model: string, e: CatalogEntry | undefined): string | null {
  if (!e) return 'not offered by the source'
  return MODEL_ID.test(model) ? null : 'not a complete model id'
}

/** Takes the catalog's price of a model as the price synced from source s: updates the model's price, or creates it. */
function applyEntry(s: MockSource, e: CatalogEntry, out: ApplyResult) {
  const { action, current } = syncAction(e)
  if (action === 'unchanged') {
    out.unchanged++
    return
  }
  const inc = incomingOf(e)
  const fields = { ...inc, expr_hash: remember(inc.expression), source: 'sync' as const, sync_source_id: s.id, synced_at: now(), updated_by: 1, updated_at: now() }
  if (current) {
    Object.assign(current, fields, { expr_version: current.expr_version })
    out.updated++
  } else {
    prices.push({ id: nextId(), model: e.model, expr_version: 1, enabled: true, note: '', ...fields })
    out.created++
  }
}

on('POST', '/price-sources/:id/apply', (req) => {
  const s = findSource(req)
  if (!s) return fail(404, 'not_found', 'price source not found')
  const models = req.body?.models
  if (!Array.isArray(models) || models.some((m: unknown) => typeof m !== 'string')) {
    return fieldFail([{ field: 'models', code: 'invalid', message: 'models must be a list of model ids' }])
  }
  const r = fetchOrFail(s)
  if ('__status' in r) return r
  const byModel = new Map(r.entries.map((e) => [e.model, e]))
  const out: ApplyResult = { created: 0, updated: 0, unchanged: 0, skipped: [] }
  for (const model of new Set<string>(models)) {
    const e = byModel.get(model)
    const reason = skipReason(model, e)
    if (reason) out.skipped.push({ model, reason })
    else applyEntry(s, e!, out)
  }
  s.last_synced_at = now()
  s.last_error = ''
  return out
})
