// Writing the prices and rates of an account group ("group") or a user group
// ("segment") together with the group or the segment itself, as one
// transaction would: every item is checked first, every problem of every
// item is named (`model_prices.0.rule`, by its position in the list it came
// in), and any of them changes nothing. The codes are the server's:
// required, not_found, invalid. The handlers of POST/PATCH /groups
// (groups.ts) and POST/PATCH /segments (pricing.ts) call these.
import { fail, type MockRequest, type MockResult } from './router'
import { caller } from './core'
import { configPrice, globalPrice, validate } from './billing'
import { compactVisualConfig } from '../src/views/prices/priceExpr'
import { findSegment, removePair, segmentOfUser, setModelRate, setPair, setModelPrice, type ModelPrice } from './pricingStore'

export interface FieldError {
  field: string
  code: string
  message: string
}

/** A rate as the server takes one: a JSON string of a non-negative decimal of at most four decimals, below 1000000. */
export const validRate = (v: unknown) => typeof v === 'string' && /^\d+(\.\d{1,4})?$/.test(v) && Number(v) < 1000000
/** What the message of a value that is no rate says. */
export const RATE_RULE = 'must be 0 or more and below 1000000, with at most 4 decimals'

/** A problem of an item: its subfield, the code and why. */
type Problem = [field: string, code: string, message: string]
/** What is wrong with one item; nothing when it is right. */
type ItemCheck = (item: any) => Problem[]

/** The field errors of the list `name` of a body; a list left out has none. */
function listErrors(body: any, name: string, check: ItemCheck): FieldError[] {
  const list = body?.[name]
  if (list === undefined) return []
  if (!Array.isArray(list)) return [{ field: name, code: 'invalid', message: 'must be a list' }]
  return list.flatMap((item, i) => check(item ?? {}).map(([field, code, message]) => ({ field: `${name}.${i}.${field}`, code, message })))
}

/** The 400 of field errors; undefined when there are none. */
export const fieldsFailure = (fields: FieldError[]): MockResult | undefined => (fields.length ? fail(400, 'invalid_argument', 'invalid', { fields }) : undefined)

/** The rate of an item: required, a rate or null. */
function rateProblems(item: any): Problem[] {
  if (!('rate' in item)) return [['rate', 'required', 'a rate or null is required']]
  return item.rate === null || validRate(item.rate) ? [] : [['rate', 'invalid', RATE_RULE]]
}

/** Setting a price or a rate on a model takes an enabled global price for it. */
const pricedProblems = (model: unknown): Problem[] => (globalPrice(String(model).trim()) ? [] : [['model', 'invalid', 'the model has no global price']])

/** Why a model price cannot be saved; removing one always can. It takes a model with a global price, and is checked as a global price is. */
function priceProblems(item: any): Problem[] {
  if (item.rule == null) return []
  if (item.rule.mode !== 'visual' && item.rule.mode !== 'expression') return [...pricedProblems(item.model), ['rule', 'invalid', 'a model price is visual or an expression']]
  const v = validate(item.rule)
  return [...pricedProblems(item.model), ...(v.errors.length ? [['rule', 'invalid', `invalid price: ${v.errors[0].message.en}`] as Problem] : [])]
}

/** Why a model rate cannot be saved: a rate on a model with a global price; removing one always can. */
const modelRateProblems = (item: any): Problem[] => [...(item.rate == null ? [] : pricedProblems(item.model)), ...rateProblems(item)]

/** Why a pair (a segment in a group) cannot be saved; `exists` says whether the other end does. An item of segment_rates may `remove` the pair instead. */
const pairProblems = (key: string, exists: (id: number) => boolean): ItemCheck => (item) => [
  ...(exists(Number(item[key])) ? [] : [[key, 'not_found', 'not found'] as Problem]),
  ...(item.remove === true ? [] : rateProblems(item))
]

/** Why a model rate of a segment in a group cannot be saved: the far end `key` that exists, a model named, and what a model rate of a group takes. */
const segmentModelRateProblems = (key: string, exists: (id: number) => boolean): ItemCheck => (item) => [
  ...(exists(Number(item[key])) ? [] : [[key, 'not_found', 'not found'] as Problem]),
  ...(String(item.model ?? '').trim() ? modelRateProblems(item) : [['model', 'required', 'model is required'] as Problem, ...rateProblems(item)])
]

const hasSegment = (id: number) => !!findSegment(id)

/** The field errors of what POST/PATCH /groups take besides the group's own fields. */
export function groupPricingErrors(body: any): FieldError[] {
  return [
    ...listErrors(body, 'model_prices', priceProblems),
    ...listErrors(body, 'model_rates', modelRateProblems),
    ...listErrors(body, 'segment_rates', pairProblems('segment_id', hasSegment)),
    ...listErrors(body, 'segment_model_rates', segmentModelRateProblems('segment_id', hasSegment))
  ]
}

/** The field errors of what POST/PATCH /segments take besides the segment's own fields; `hasGroup` says whether a group exists. */
export function segmentRatesErrors(body: any, hasGroup: (id: number) => boolean): FieldError[] {
  return [...listErrors(body, 'group_rates', pairProblems('group_id', hasGroup)), ...listErrors(body, 'model_rates', segmentModelRateProblems('group_id', hasGroup))]
}

/**
 * The 409 of a write that changes the terms of a user group the caller is in
 * (the server's guardOwnSegments): a caller who may act on himself (here a
 * superuser) may; the default user group is not checked.
 */
export function ownSegmentConflict(req: MockRequest, segmentIds: number[]): MockResult | undefined {
  const who = caller(req)
  const own = segmentOfUser(who.id)
  if (who.superuser || own.is_default || !segmentIds.includes(own.id)) return undefined
  return fail(409, 'conflict', 'you cannot change the terms of a user group you are in')
}

/** The segments segment_rates and segment_model_rates of a group's body name. */
export const segmentsOf = (body: any): number[] => [...listOf(body, 'segment_rates'), ...listOf(body, 'segment_model_rates')].map((item) => Number(item.segment_id))

/** Whether a segment's body changes its rates. */
export const writesRates = (body: any) => body?.group_rates !== undefined || body?.model_rates !== undefined

/** A model price as it is stored: with its expression (generated from the config of a visual one). */
function storedPrice(price: any): ModelPrice | null {
  if (price == null) return null
  if (price.mode === 'expression') return { mode: 'expression', config: {}, expression: String(price.expression).trim() }
  const config = compactVisualConfig(price.config)
  return { mode: 'visual', config, expression: configPrice('visual', config).expression }
}

const listOf = (body: any, name: string): any[] => (Array.isArray(body?.[name]) ? body[name] : [])

/** Applies one checked item of segment_rates of group `gid`. */
function applySegmentRate(gid: number, item: any) {
  if (item.remove === true) return removePair(Number(item.segment_id), gid)
  setPair(Number(item.segment_id), gid, { granted: item.granted, rate_override: item.rate })
}

/** Applies the checked prices and rates of group `gid`, in the order given (a later item on the same thing wins). */
export function applyGroupPricing(gid: number, body: any) {
  for (const item of listOf(body, 'model_prices')) setModelPrice(gid, String(item.model), storedPrice(item.rule))
  for (const item of listOf(body, 'model_rates')) setModelRate(null, gid, String(item.model), item.rate)
  for (const item of listOf(body, 'segment_rates')) applySegmentRate(gid, item)
  for (const item of listOf(body, 'segment_model_rates')) setModelRate(Number(item.segment_id), gid, String(item.model).trim(), item.rate)
}

/** Applies the checked rates of segment `sid`, in the order given. */
export function applySegmentRates(sid: number, body: any) {
  for (const item of listOf(body, 'group_rates')) setPair(sid, Number(item.group_id), { granted: item.granted, rate_override: item.rate })
  for (const item of listOf(body, 'model_rates')) setModelRate(sid, Number(item.group_id), String(item.model).trim(), item.rate)
}
