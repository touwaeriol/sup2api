// Writing the prices and rates of an account group ("group") or a user group
// ("segment") together with the group or the segment itself, as one
// transaction would: every item is checked first, and any item that is wrong
// changes nothing. A field error names the item by its position in the list
// it came in (`model_prices.0.rule`). The handlers of POST/PATCH /groups
// (groups.ts) and POST/PATCH /segments (pricing.ts) call these.
import { fail, type MockResult } from './router'
import { configPrice, globalPrice, validate } from './billing'
import { compactVisualConfig } from '../src/views/prices/priceExpr'
import { findSegment, setModelRate, setPair, setModelPrice, type ModelPrice } from './pricingStore'

interface FieldError {
  field: string
  code: string
  message: string
}

/** A rate: a non-negative decimal of at most four decimals, below 1000000. */
export const validRate = (v: unknown) => /^\d+(\.\d{1,4})?$/.test(String(v)) && Number(v) < 1000000
/** What the message of a value that is no rate says. */
const RATE_RULE = 'must be 0 or more, below 1000000, with at most 4 decimals'

/** What is wrong with one item: the subfield and why; null when nothing. */
type ItemCheck = (item: any) => [string, string] | null

/** The field errors of the list `name` of a body; a list left out has none. */
function listErrors(body: any, name: string, check: ItemCheck): FieldError[] {
  const list = body?.[name]
  if (list === undefined) return []
  if (!Array.isArray(list)) return [{ field: name, code: 'invalid', message: 'must be a list' }]
  return list.flatMap((item, i) => {
    const wrong = check(item ?? {})
    return wrong ? [{ field: `${name}.${i}.${wrong[0]}`, code: 'invalid', message: wrong[1] }] : []
  })
}

/** The 400 of field errors; undefined when there are none. */
export const fieldsFailure = (fields: FieldError[]): MockResult | undefined => (fields.length ? fail(400, 'invalid_argument', 'invalid', { fields }) : undefined)

const rateError = (rate: unknown): string | null => (rate === null || validRate(rate) ? null : RATE_RULE)

/** Why a model price cannot be saved; removing one always can. It takes a model with a global price, and is checked as a global price is. */
function priceError(item: any): [string, string] | null {
  if (item.rule == null) return null
  if (!globalPrice(item.model)) return ['model', 'the model has no global price']
  if (item.rule.kind !== 'custom' || (item.rule.mode !== 'visual' && item.rule.mode !== 'expression')) return ['rule', 'a model price is visual or an expression']
  const v = validate(item.rule)
  return v.errors.length ? ['rule', `invalid price: ${v.errors[0].message.en}`] : null
}

/** Why a model rate cannot be saved: a rate on a model with a global price; removing one always can. */
function modelRateError(item: any): [string, string] | null {
  if (item.rate !== null && !globalPrice(item.model)) return ['model', 'the model has no global price']
  const why = rateError(item.rate)
  return why ? ['rate', why] : null
}

/** Why a pair (a segment in a group) cannot be saved; `exists` says whether the other end does. */
const pairError = (key: string, exists: (id: number) => boolean): ItemCheck => (item) => {
  if (!exists(Number(item[key]))) return [key, 'not found']
  if (item.granted !== undefined && typeof item.granted !== 'boolean') return ['granted', 'must be true or false']
  const why = rateError(item.rate)
  return why ? ['rate', why] : null
}

/** The field errors of what POST/PATCH /groups take besides the group's own fields. */
export function groupPricingErrors(body: any): FieldError[] {
  return [
    ...listErrors(body, 'model_prices', priceError),
    ...listErrors(body, 'model_rates', modelRateError),
    ...listErrors(body, 'segment_rates', pairError('segment_id', (id) => !!findSegment(id)))
  ]
}

/** The field errors of what POST/PATCH /segments take besides the segment's own fields; `hasGroup` says whether a group exists. */
export function segmentRatesErrors(body: any, hasGroup: (id: number) => boolean): FieldError[] {
  return [...listErrors(body, 'group_rates', pairError('group_id', hasGroup)), ...listErrors(body, 'model_rates', (item) => (hasGroup(Number(item.group_id)) ? modelRateError(item) : ['group_id', 'not found']))]
}

/** A model price as it is stored: with its expression (generated from the config of a visual one). */
function storedPrice(price: any): ModelPrice | null {
  if (price == null) return null
  if (price.mode === 'expression') return { kind: 'custom', mode: 'expression', config: {}, expression: String(price.expression).trim() }
  const config = compactVisualConfig(price.config)
  return { kind: 'custom', mode: 'visual', config, expression: configPrice('visual', config).expression }
}

const rateOf = (rate: unknown) => (rate === null ? null : String(rate))
const listOf = (body: any, name: string): any[] => (Array.isArray(body?.[name]) ? body[name] : [])

/** Applies the checked prices and rates of group `gid`, in the order given (a later item on the same thing wins). */
export function applyGroupPricing(gid: number, body: any) {
  for (const item of listOf(body, 'model_prices')) setModelPrice(gid, String(item.model), storedPrice(item.rule))
  for (const item of listOf(body, 'model_rates')) setModelRate(null, gid, String(item.model), rateOf(item.rate))
  for (const item of listOf(body, 'segment_rates')) setPair(Number(item.segment_id), gid, { granted: item.granted, rate_override: rateOf(item.rate) })
}

/** Applies the checked rates of segment `sid`, in the order given. */
export function applySegmentRates(sid: number, body: any) {
  for (const item of listOf(body, 'group_rates')) setPair(sid, Number(item.group_id), { granted: item.granted, rate_override: rateOf(item.rate) })
  for (const item of listOf(body, 'model_rates')) setModelRate(sid, Number(item.group_id), String(item.model), rateOf(item.rate))
}
