// Group pricing, the state and how it resolves: user groups ("segments"),
// the private groups they are granted, and the prices and rates of account
// groups ("groups").
//
//   paid = list price × rate
//   list price: the group's model price -> the global price
//   rate (one applies, never a product; the most specific first):
//     segment × group × model -> segment × group -> group × model -> group base rate
//
// Users and groups are known here by id only; resources.ts and pricing.ts put
// the API rows together.
import { nextId, now } from './router'
import { configPrice, type PriceDef } from './billing'
import { requestTier, singleTier, type VisualConfig } from '../src/views/prices/priceExpr'

export type RateSource = 'segment_model' | 'segment' | 'group_model' | 'group'
export type PriceSource = 'group' | 'global'

/** The model price of a group: a price of its own, visual or an expression. */
export type PriceRule =
  | { kind: 'custom'; mode: 'visual'; config: VisualConfig; expression?: string }
  | { kind: 'custom'; mode: 'expression'; config: Record<string, never>; expression: string }

export interface MockSegment {
  id: number
  kind: 'named' | 'personal'
  /** A personal segment carries the email of its user. */
  name: string
  description: string
  is_default: boolean
  /** Personal only: its one member, and the segment that user came from. */
  user_id?: number
  origin_id?: number | null
  created_at: string
}

/** What resolving needs of a group. */
export interface PricedGroup {
  id: number
  visibility: 'public' | 'restricted'
  rate_multiplier: string
}

/** One segment in one group: the grant (it matters for a private group) and the rate override. */
interface Pair {
  segment_id: number
  group_id: number
  granted: boolean
  rate_override: string | null
}

/** A model price of a group: at most one on a model. */
export interface RuleRow {
  group_id: number
  model: string
  rule: PriceRule
}

/** A model rate: of a group (`segment_id` null) or of a segment in a group; at most one on a model each. */
export interface ModelRateRow {
  segment_id: number | null
  group_id: number
  model: string
  rate: string
}

// ------------------------------------------------------------------ state

const DEFAULT = 1
const VIP = 2
const ENTERPRISE = 3
const INTERNAL = 4
const PERSONAL_104 = 11
const PERSONAL_107 = 12

export const segments: MockSegment[] = [
  { id: DEFAULT, kind: 'named', name: '默认', description: '注册后自动加入', is_default: true, created_at: now(-86400 * 90) },
  { id: VIP, kind: 'named', name: 'VIP', description: '充值满 $200 的老客户', is_default: false, created_at: now(-86400 * 45) },
  { id: ENTERPRISE, kind: 'named', name: '企业客户', description: '签了合同的公司', is_default: false, created_at: now(-86400 * 30) },
  { id: INTERNAL, kind: 'named', name: '内部', description: '自己人和测试号', is_default: false, created_at: now(-86400 * 20) },
  { id: PERSONAL_104, kind: 'personal', name: 'user5@example.org', description: '', is_default: false, user_id: 104, origin_id: VIP, created_at: now(-86400 * 6) },
  { id: PERSONAL_107, kind: 'personal', name: 'user8@example.org', description: '', is_default: false, user_id: 107, origin_id: DEFAULT, created_at: now(-86400 * 2) }
]

/** User id -> segment id. A user without an entry is in the default segment, as a new user is. */
const membership = new Map<number, number>([
  [1, VIP],
  [2, INTERNAL],
  [3, INTERNAL],
  [4, ENTERPRISE],
  [102, VIP],
  [103, VIP],
  [104, PERSONAL_104],
  [107, PERSONAL_107]
])

// Groups 1 default (public), 2 vip (private), 3 haiku-only (private).
const pairs: Pair[] = [
  { segment_id: VIP, group_id: 2, granted: true, rate_override: '0.7' },
  { segment_id: ENTERPRISE, group_id: 2, granted: true, rate_override: null },
  { segment_id: PERSONAL_104, group_id: 2, granted: true, rate_override: '0.65' },
  { segment_id: PERSONAL_107, group_id: 2, granted: true, rate_override: null },
  { segment_id: INTERNAL, group_id: 3, granted: true, rate_override: null },
  { segment_id: INTERNAL, group_id: 1, granted: false, rate_override: '0.5' }
]

const rules: RuleRow[] = [
  { group_id: 1, model: 'web-search', rule: { kind: 'custom', mode: 'visual', config: requestTier(0.008) } },
  { group_id: 2, model: 'claude-haiku-4-5', rule: { kind: 'custom', mode: 'visual', config: singleTier({ p: 0.8, c: 4, cr: 0.08, cc: 1 }) } },
  // A custom price by the request over a global price by tokens.
  { group_id: 1, model: 'gemini-2.5-flash', rule: { kind: 'custom', mode: 'visual', config: requestTier(0.002) } }
]

// Group vip (2) charges ×0.9 for claude-opus-4-1 instead of its ×0.8, and default (1) ×1.2 for gpt-4o.
// VIP pays ×0.85 for gpt-4o and ×0.9 for claude-sonnet-4-5 in default, whatever default charges.
const modelRates: ModelRateRow[] = [
  { segment_id: null, group_id: 1, model: 'gpt-4o', rate: '1.2' },
  { segment_id: null, group_id: 2, model: 'claude-opus-4-1', rate: '0.9' },
  { segment_id: VIP, group_id: 1, model: 'gpt-4o', rate: '0.85' },
  { segment_id: VIP, group_id: 1, model: 'claude-sonnet-4-5', rate: '0.9' }
]

// ------------------------------------------------------------------ reads

/** Rows whose fields equal those of `by`. */
function where<T extends object>(rows: T[], by: Partial<T>): T[] {
  const want = Object.entries(by) as Array<[keyof T, unknown]>
  return rows.filter((row) => want.every(([k, v]) => row[k] === v))
}

export const pairsWhere = (by: Partial<Pair>) => where(pairs, by)
export const rulesWhere = (by: Partial<RuleRow>) => where(rules, by)
export const modelRatesWhere = (by: Partial<ModelRateRow>) => where(modelRates, by)

export function findSegment(id: number | null | undefined): MockSegment | undefined {
  return segments.find((s) => s.id === id)
}

export function defaultSegment(): MockSegment {
  return segments.find((s) => s.is_default)!
}

/** The one segment a user belongs to. */
export function segmentOfUser(userId: number): MockSegment {
  return findSegment(membership.get(userId)) ?? defaultSegment()
}

/**
 * A segment as other rows refer to it. A personal one goes by its user's
 * email only for a caller who may read users (`showUsers`); to others it is
 * "#<user_id>" (segment.Masked).
 */
export function segmentRef(s: MockSegment, showUsers = true) {
  if (s.kind !== 'personal') return { id: s.id, kind: s.kind, name: s.name }
  return { id: s.id, kind: s.kind, name: showUsers ? s.name : `#${s.user_id}`, user_id: s.user_id }
}

/** The groups a segment is granted, public ones included. */
export function grantedGroupIds(segmentId: number): number[] {
  return pairsWhere({ segment_id: segmentId, granted: true }).map((p) => p.group_id)
}

export function rateOverrideOf(segmentId: number, groupId: number): string | null {
  return pairsWhere({ segment_id: segmentId, group_id: groupId })[0]?.rate_override ?? null
}

/** Who a private group is granted to: the named segments, and how many personal ones. A public group is open to all: nobody is listed. */
export function groupAccess(g: PricedGroup) {
  if (g.visibility !== 'restricted') return { segments: [], personal_count: 0 }
  const ids = new Set(pairsWhere({ group_id: g.id, granted: true }).map((p) => p.segment_id))
  const granted = segments.filter((s) => ids.has(s.id))
  return { segments: granted.filter((s) => s.kind === 'named').map((s) => segmentRef(s)), personal_count: granted.filter((s) => s.kind === 'personal').length }
}

// ------------------------------------------------------------------ resolving

/** Whether a segment may use a group: public, or granted. */
export function accessOf(s: MockSegment, g: PricedGroup): { accessible: boolean; granted: boolean; source: 'public' | 'grant' | 'none' } {
  const granted = pairsWhere({ segment_id: s.id, group_id: g.id, granted: true }).length > 0
  if (g.visibility === 'public') return { accessible: true, granted, source: 'public' }
  return { accessible: granted, granted, source: granted ? 'grant' : 'none' }
}

/** The model rate of a group (`segmentId` null) or of a segment in it; null when none is set. */
export function modelRateOf(segmentId: number | null, groupId: number, model: string): string | null {
  return modelRatesWhere({ segment_id: segmentId, group_id: groupId, model })[0]?.rate ?? null
}

/** Every level of the rate of a segment in a group for a model, most specific first; null = not set. Without a segment or a model those levels are not set. */
export function rateLevelsOf(s: MockSegment | null, g: PricedGroup, model?: string) {
  return {
    segment_model: s && model ? modelRateOf(s.id, g.id, model) : null,
    segment: s ? rateOverrideOf(s.id, g.id) : null,
    group_model: model ? modelRateOf(null, g.id, model) : null,
    group: g.rate_multiplier
  }
}

/** The rate that applies: the most specific level set (one, never a product), with where it comes from and the group's base rate. */
export function rateOf(s: MockSegment | null, g: PricedGroup, model?: string): { value: string; source: RateSource; group_default: string } {
  const levels = rateLevelsOf(s, g, model)
  const source = (['segment_model', 'segment', 'group_model'] as const).find((k) => levels[k] !== null) ?? 'group'
  return { value: levels[source] ?? g.rate_multiplier, source, group_default: g.rate_multiplier }
}

/** The list price a model price gives: a price of its own, whatever the global price `base` is. A model without a global price has none. */
export function applyRule(base: PriceDef | null, rule: PriceRule | null): PriceDef | null {
  if (!base || !rule) return base
  return rule.mode === 'expression' ? { mode: rule.mode, config: {}, expression: rule.expression } : configPrice(rule.mode, rule.config)
}

/** A model price as a row carries it: with its expression. */
function listedRule(rule: PriceRule | undefined): PriceRule | null {
  if (!rule || rule.mode === 'expression') return rule ?? null
  return { ...rule, expression: configPrice(rule.mode, rule.config).expression }
}

/**
 * The list price of a model in a group, first hit: the rule of the group,
 * the global price; without a global price no rule applies. The rule is
 * returned as configured.
 */
export function listPriceOf(groupId: number, model: string, base: PriceDef | null) {
  const group_rule = listedRule(rulesWhere({ group_id: groupId, model })[0]?.rule)
  const source: PriceSource = base && group_rule ? 'group' : 'global'
  return { group_rule, source, list_price: applyRule(base, group_rule) }
}

// ------------------------------------------------------------------ changes

/** Removes the rows `gone` picks, in place. */
function drop<T>(rows: T[], gone: (row: T) => boolean) {
  for (let i = rows.length - 1; i >= 0; i--) if (gone(rows[i])) rows.splice(i, 1)
}

/** Sets the grant and/or the rate override of a segment in a group; a field left undefined stays. */
export function setPair(segmentId: number, groupId: number, patch: { granted?: boolean; rate_override?: string | null }) {
  const cur = pairsWhere({ segment_id: segmentId, group_id: groupId })[0]
  const pair = cur ?? { segment_id: segmentId, group_id: groupId, granted: false, rate_override: null }
  if (patch.granted !== undefined) pair.granted = patch.granted
  if (patch.rate_override !== undefined) pair.rate_override = patch.rate_override
  if (!cur) pairs.push(pair)
  // A pair that says nothing is not kept.
  drop(pairs, (p) => !p.granted && p.rate_override === null)
}

/** Sets or (`rule` null) removes the model price of a group. */
export function setRule(groupId: number, model: string, rule: PriceRule | null) {
  drop(rules, (r) => r.group_id === groupId && r.model === model)
  if (rule) rules.push({ group_id: groupId, model, rule })
}

/** Sets or (`rate` null) removes the model rate of a group (`segmentId` null) or of a segment in it. */
export function setModelRate(segmentId: number | null, groupId: number, model: string, rate: string | null) {
  drop(modelRates, (r) => r.segment_id === segmentId && r.group_id === groupId && r.model === model)
  if (rate !== null) modelRates.push({ segment_id: segmentId, group_id: groupId, model, rate })
}

export function addSegment(name: string, description: string): MockSegment {
  const s: MockSegment = { id: nextId(), kind: 'named', name, description, is_default: false, created_at: now() }
  segments.push(s)
  return s
}

/** Deletes a segment with its grants and rates. Its members fall back to the default segment. */
export function removeSegment(s: MockSegment) {
  drop(pairs, (p) => p.segment_id === s.id)
  drop(modelRates, (r) => r.segment_id === s.id)
  drop(segments, (x) => x === s)
  for (const [userId, id] of membership) if (id === s.id) membership.delete(userId)
  for (const x of segments) if (x.origin_id === s.id) x.origin_id = null
}

/**
 * Gives a user a segment of their own. With `copy` it starts from what the
 * segment they leave has now (grants, rates, model rates); the two are
 * unrelated afterwards.
 */
export function makePersonal(user: { id: number; email: string }, copy: boolean): MockSegment {
  const from = segmentOfUser(user.id)
  const s: MockSegment = { id: nextId(), kind: 'personal', name: user.email, description: '', is_default: false, user_id: user.id, origin_id: from.id, created_at: now() }
  segments.push(s)
  membership.set(user.id, s.id)
  if (!copy) return s
  pairs.push(...pairsWhere({ segment_id: from.id }).map((p) => ({ ...p, segment_id: s.id })))
  modelRates.push(...modelRatesWhere({ segment_id: from.id }).map((r) => ({ ...r, segment_id: s.id })))
  return s
}

/** Moves a user to a named segment; a personal segment they had is deleted. False when they are in it already. */
export function moveUser(userId: number, to: MockSegment): boolean {
  const from = segmentOfUser(userId)
  if (from.id === to.id) return false
  if (from.kind === 'personal') removeSegment(from)
  membership.set(userId, to.id)
  return true
}

/** A deleted user: out of their segment, and a personal one goes with them. */
export function forgetUser(userId: number) {
  const s = segmentOfUser(userId)
  if (s.kind === 'personal') removeSegment(s)
  membership.delete(userId)
}

/** A deleted group: its grants, rates and prices go with it. */
export function forgetGroup(groupId: number) {
  drop(pairs, (p) => p.group_id === groupId)
  drop(rules, (r) => r.group_id === groupId)
  drop(modelRates, (r) => r.group_id === groupId)
}
