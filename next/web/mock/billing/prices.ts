// Mock handlers of the global prices: GET/POST/PATCH/DELETE /prices, their
// validation, the cost preview and the expression history.
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from '../router'
import { breakdownOf, tryEvaluate, type Evaluation } from './engine'
import { MODEL_ID, history, prices, remember, type MockPrice } from './priceStore'
import { hashOf, money } from './shared'
import { sources } from './sources'
import { exprOf, issueText, validate } from './validate'

function priceFilter(q: Record<string, string>) {
  return prices
    .filter((p) => {
      if (q.mode && p.mode !== q.mode) return false
      if (q.source && p.source !== q.source) return false
      if (q.sync_source_id && String(p.sync_source_id ?? '') !== q.sync_source_id) return false
      if (q.enabled && (q.enabled === 'true' || q.enabled === '1') !== p.enabled) return false
      if (q.q && !`${p.model} ${p.note}`.toLowerCase().includes(q.q.toLowerCase())) return false
      return true
    })
    .sort((a, b) => a.model.localeCompare(b.model) || a.id - b.id)
}

/** API shape of a price: adds the name of its sync source. */
function priceView(p: MockPrice) {
  const src = p.sync_source_id ? sources.find((s) => s.id === p.sync_source_id) : undefined
  return { ...p, sync_source_name: src ? src.name : null }
}

const findPrice = (req: MockRequest) => prices.find((p) => p.id === Number(req.params.id))

on('GET', '/prices', (req) => {
  const r = paginate(priceFilter(req.query), req.query)
  return { ...r, data: r.data.map(priceView) }
})
on('POST', '/prices/validate', (req) => {
  const v = validate(req.body)
  return { ok: v.ok, expression: v.expression, expr_hash: v.expr_hash, errors: v.errors, warnings: v.warnings, samples: v.samples, cost_per_million_input: v.cost_per_million_input, analysis: v.analysis }
})

/** The price a preview evaluates: a saved one by price_id, or the body's; a failure when there is none. */
function previewSource(b: any) {
  if (b.price_id) {
    const p = prices.find((x) => x.id === Number(b.price_id))
    return p ? { mode: p.mode, config: p.config, expression: p.expression } : fail(404, 'not_found', 'price not found')
  }
  const e = exprOf(b)
  return e.issue ? fail(400, 'invalid_argument', issueText(e.issue)) : e
}

/** What a preview answers: the call's cost at the price, at the rate of the group (vip, id 2, at 1.5). */
function previewOf(expression: string, r: Evaluation, groupId: unknown) {
  const rate = Number(groupId) === 2 ? 1.5 : 1
  return {
    cost: money(r.cost * rate),
    base_cost: money(r.cost),
    rate_multiplier: rate.toFixed(2),
    tier: r.tier,
    rules: r.rules,
    expression,
    expr_hash: hashOf(expression),
    breakdown: breakdownOf(r)
  }
}

on('POST', '/prices/preview', (req) => {
  const b = req.body || {}
  const src = previewSource(b)
  if ('__status' in src) return src
  const r = tryEvaluate(src.expression, { usage: b.usage || {}, headers: b.headers, params: b.params, metrics: b.metrics, at: b.at ? new Date(b.at) : undefined })
  return r ? previewOf(src.expression, r, b.group_id) : fail(400, 'invalid_argument', 'expression cannot be evaluated')
})

on('GET', '/prices/history/:hash', (req) => history.get(req.params.hash) || fail(404, 'not_found', 'no such expression'))

on('GET', '/prices/:id', (req) => {
  const p = findPrice(req)
  return p ? { ...priceView(p), analysis: { params: [], headers: [], metrics: [] } } : fail(404, 'not_found', 'price not found')
})

/** A body checked before a save: a config the generator refuses is a field error (mode or config), as the server says it. */
function checkSave(body: any) {
  const v = validate(body)
  const { issue, expression, errors, warnings } = v
  if (issue) return fail(400, 'invalid_argument', issueText(issue), { fields: [{ field: issue.field, code: 'invalid_config', message: issue.detail }], errors })
  if (errors.length) return fail(400, 'invalid_argument', 'expression is invalid', { expression, errors, warnings })
  if (warnings.some((w) => w.code === 'big_cost') && !body?.confirm) {
    return fail(400, 'invalid_argument', 'high cost requires confirmation', { expression, errors: [], warnings, confirmation_required: true })
  }
  return v
}

const modelInvalid = () => fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'model', code: 'invalid', message: 'a complete model id (no wildcards)' }] })
const modelTaken = () => fail(409, 'conflict', 'a price for this model already exists')

/** A new manual price of a checked body. */
function newPrice(b: any, v: ReturnType<typeof validate>): MockPrice {
  return {
    id: nextId(),
    model: b.model,
    mode: v.mode,
    config: v.config,
    expression: v.expression,
    expr_version: 1,
    expr_hash: remember(v.expression),
    source: 'manual',
    sync_source_id: null,
    synced_at: null,
    enabled: b.enabled !== false,
    note: b.note || '',
    updated_by: 1,
    updated_at: now()
  }
}

on('POST', '/prices', (req) => {
  const b = req.body || {}
  if (!b.model) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'model', code: 'required', message: 'Model is required' }] })
  if (!MODEL_ID.test(b.model)) return modelInvalid()
  if ('platform' in b) return fail(400, 'invalid_argument', 'unknown field "platform"')
  if (!b.mode) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'mode', code: 'required', message: 'visual or expression' }] })
  const v = checkSave(b)
  if ('__status' in v) return v
  // One price per model (prices are global and keyed by complete model ids).
  if (prices.some((p) => p.model === b.model)) return modelTaken()
  const p = newPrice(b, v)
  prices.push(p)
  return { __status: 201, body: { data: priceView(p) } }
})

/** A patch of the enabled flag or the note only, which checks nothing. */
function patchMeta(p: MockPrice, b: any) {
  if (b.enabled !== undefined) p.enabled = !!b.enabled
  if (b.note !== undefined) p.note = String(b.note)
  p.updated_at = now()
  return priceView(p)
}

/** Whether a patch changes the definition of a price: its model, mode, expression or config. */
const changesDef = (p: MockPrice, b: any, v: { mode: string; expression: string }) =>
  (b.model !== undefined && b.model !== p.model) ||
  v.mode !== p.mode ||
  v.expression !== p.expression ||
  (b.config !== undefined && JSON.stringify(b.config) !== JSON.stringify(p.config))

on('PATCH', '/prices/:id', (req) => {
  const p = findPrice(req)
  if (!p) return fail(404, 'not_found', 'price not found')
  const b = req.body || {}
  if (b.model !== undefined && !MODEL_ID.test(b.model)) return modelInvalid()
  if (b.model !== undefined && b.model !== p.model && prices.some((x) => x.model === b.model)) return modelTaken()
  if (Object.keys(b).every((k) => ['enabled', 'note', 'confirm'].includes(k))) return patchMeta(p, b)
  const v = checkSave({ ...p, ...b })
  if ('__status' in v) return v
  // Editing the definition of a synced price turns it into a manual price.
  const manual = p.source === 'sync' && changesDef(p, b, v)
  Object.assign(p, {
    model: b.model ?? p.model,
    mode: v.mode,
    config: v.config,
    expression: v.expression,
    expr_hash: remember(v.expression),
    enabled: b.enabled ?? p.enabled,
    note: b.note ?? p.note,
    updated_by: 1,
    updated_at: now()
  })
  if (manual) Object.assign(p, { source: 'manual', sync_source_id: null })
  return priceView(p)
})

on('DELETE', '/prices/:id', (req) => {
  const i = prices.findIndex((p) => p.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', 'price not found')
  prices.splice(i, 1)
  return noContent()
})
