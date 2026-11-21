// Editing a per_token config in place (TokenPriceEditor): the edits that keep
// the config whole. A price beyond p and c is charged when its key is in the
// tier (held as null while it is blank): unticking cache writes 1 h or image
// tokens drops the key, and they fall back to another price. A condition
// value is edited as text and typed when the config is compacted
// (typedValue), so "0.05" is not 0 while it is typed. The templates are in
// presets.ts.
import {
  asTokenConfig,
  num,
  sortedTiers,
  valueText,
  type CondOp,
  type CondSource,
  type FieldCond,
  type MarkupRule,
  type PriceKey,
  type RuleCond,
  type TokenConfig,
  type TokenTier
} from './priceExpr'
import { PRESETS } from './presets'

export function defaultTokenConfig(): TokenConfig {
  return { tiers: PRESETS.claude(), rules: [] }
}

/** A condition as the editor holds it: a time window in full (the server reads no tz as UTC), any other value as text. */
function editableCond(c: RuleCond): RuleCond {
  if (c.source === 'time') return { source: 'time', tz: c.tz?.trim() || 'UTC', from_hour: c.from_hour ?? 0, to_hour: c.to_hour ?? 24 }
  return c.op === 'exists' ? c : { ...c, value: valueText(c, c.value) }
}

/** Editable copies of saved rules (a per_request config's, or a per_token one's). */
export function editableRules(raw: readonly MarkupRule[]): MarkupRule[] {
  return (JSON.parse(JSON.stringify(raw)) as MarkupRule[]).map(r => ({ ...r, conds: r.conds.map(editableCond) }))
}

/** An editable copy of a saved config: tiers in the order they match, the last one the rest, each with its unit. */
export function editableTokenConfig(raw: unknown): TokenConfig {
  const c = asTokenConfig(raw)
  const tiers = sortedTiers(JSON.parse(JSON.stringify(c.tiers)) as TokenTier[]).map(t => ({ ...t, unit: t.unit ?? 'token' }))
  if (tiers.length) tiers[tiers.length - 1].max_len = null
  return { tiers, rules: editableRules(c.rules) }
}

/** Adds a bounded tier before the rest, priced as the rest is, under the first tier_N name not taken. */
export function addTier(tiers: TokenTier[]) {
  const last = tiers[tiers.length - 1]
  const prevMax = tiers.slice(0, -1).reduce((m, t) => Math.max(m, num(t.max_len)), 0)
  const names = new Set(tiers.map(t => t.name))
  let n = tiers.length + 1
  while (names.has(`tier_${n}`)) n++
  tiers.splice(tiers.length - 1, 0, { ...last, name: `tier_${n}`, max_len: prevMax ? prevMax * 2 : 200000 })
}

/** Removes a tier; the last one left is the rest. */
export function removeTier(tiers: TokenTier[], i: number) {
  tiers.splice(i, 1)
  tiers[tiers.length - 1].max_len = null
}

/** Whether a tier charges a price: p and c always, any other while its key is in the tier. */
export const priceOn = (t: TokenTier, k: PriceKey) => k === 'p' || k === 'c' || k in t

/** Charges a price (its key held as null until it is typed) or stops charging it; p and c are always charged. */
export function setPriceOn(t: TokenTier, k: PriceKey, on: boolean) {
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
