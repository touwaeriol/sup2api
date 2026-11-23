// Editing a visual config in place (VisualPriceEditor): the edits that keep
// the config whole. A price beyond p and c is charged when its key is in the
// tier (held as null while it is blank): unticking cache writes 1 h or image
// tokens drops the key, and they fall back to another price. A condition
// value is edited as text and typed when the config is compacted
// (typedValue), so "0.05" is not 0 while it is typed. The templates are in
// presets.ts.
import {
  asVisualConfig,
  condKind,
  num,
  valueText,
  type CondLeaf,
  type CondOp,
  type CondSource,
  type FieldCond,
  type MarkupRule,
  type PriceKey,
  type RuleCond,
  type VisualConfig,
  type VisualTier
} from './priceExpr'
import { PRESETS } from './presets'

export function defaultVisualConfig(): VisualConfig {
  return { tiers: PRESETS.claude(), rules: [] }
}

/** A condition as the editor holds it: a time window in full (the server reads no tz as UTC), any other value as text. */
function editableCond(c: RuleCond): RuleCond {
  if (c.source === 'time') return { source: 'time', tz: c.tz?.trim() || 'UTC', from_hour: c.from_hour ?? 0, to_hour: c.to_hour ?? 24 }
  return c.op === 'exists' ? c : { ...c, value: valueText(c, c.value) }
}

/** Editable copies of saved rules. */
function editableRules(raw: readonly MarkupRule[]): MarkupRule[] {
  return (JSON.parse(JSON.stringify(raw)) as MarkupRule[]).map(r => ({ ...r, conds: r.conds.map(editableCond) }))
}

/** A saved tier with its unit; a token tier without p or c charges 0 for it, as the server bills it (blank, null, is only what the editor leaves unfilled). */
function editableTier(t: VisualTier): VisualTier {
  const unit = t.unit ?? 'token'
  return unit === 'token' ? { p: 0, c: 0, ...t, unit } : { ...t, unit }
}

/** An editable copy of a saved config: tiers in the order they match, the last one the rest, each with its unit. */
export function editableVisualConfig(raw: unknown): VisualConfig {
  const c = asVisualConfig(raw)
  const tiers = (JSON.parse(JSON.stringify(c.tiers)) as VisualTier[]).map(editableTier)
  return { tiers, rules: editableRules(c.rules) }
}

/** The bound of a tier that applies up to a length, `len <= N`; 0 for any other condition. */
const lenBound = (t: VisualTier) => (condKind(t.when) === 'leaf' && (t.when as CondLeaf).var === 'len' && (t.when as CondLeaf).op === '<=' ? num((t.when as CondLeaf).value) : 0)

/** Adds a tier before the rest, priced as the rest is, up to twice the longest `len <=` bound (200000 without one), under the first tier_N name not taken. */
export function addTier(tiers: VisualTier[]) {
  const last = tiers[tiers.length - 1]
  const bound = Math.max(0, ...tiers.map(lenBound))
  const names = new Set(tiers.map(t => t.name))
  let n = tiers.length + 1
  while (names.has(`tier_${n}`)) n++
  tiers.splice(tiers.length - 1, 0, { ...last, name: `tier_${n}`, when: { var: 'len', op: '<=', value: bound ? bound * 2 : 200000 } })
}

/** Removes a tier; the last one left is the rest, without a condition. */
export function removeTier(tiers: VisualTier[], i: number) {
  tiers.splice(i, 1)
  delete tiers[tiers.length - 1].when
}

/** Whether tier i moves by `by` (±1) and stays before the rest: the rest stays last. */
export const canMoveTier = (tiers: readonly VisualTier[], i: number, by: number) => i < tiers.length - 1 && i + by >= 0 && i + by < tiers.length - 1

/** Swaps tier i with its neighbour `by` (±1) away (canMoveTier). */
export function moveTier(tiers: VisualTier[], i: number, by: number) {
  tiers.splice(i + by, 0, ...tiers.splice(i, 1))
}

/** Whether a tier charges a price: p and c always, any other while its key is in the tier. */
export const priceOn = (t: VisualTier, k: PriceKey) => k === 'p' || k === 'c' || k in t

/** Charges a price (its key held as null until it is typed) or stops charging it; p and c are always charged. */
export function setPriceOn(t: VisualTier, k: PriceKey, on: boolean) {
  if (k === 'p' || k === 'c') return
  if (on) t[k] ??= null
  else delete t[k]
}

export function newCond(source: CondSource): RuleCond {
  return source === 'time' ? { source, tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 } : { source, key: '', op: 'eq', value: '' }
}

/** Changes the operator; the text typed stays (exists takes none). */
export function setOp(c: FieldCond, op: CondOp) {
  c.op = op
  if (op === 'exists') delete c.value
  else c.value ??= ''
}
