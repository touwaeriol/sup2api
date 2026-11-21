// Editing a per_token config in place (TokenPriceEditor): the templates it
// starts from and the edits that keep the config whole. "Cache writes split
// 5 min / 1 h" and "image tokens priced apart" are not fields: they are the
// presence of the cc1h and img/img_o keys (a key held as null while unset).
// A condition value is edited as text and typed when the config is
// compacted (typedValue), so "0.05" is not 0 while it is typed.
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

type Prices = Partial<Record<PriceKey, number | null>>

export function tokenTier(name: string, maxLen: number | null, prices: Prices): TokenTier {
  return { name, max_len: maxLen, unit: 'token', ...prices }
}

/** Templates by key (named by prices.presets.item.<key>); each makes fresh tiers. */
export const PRESETS: Record<string, () => TokenTier[]> = {
  single: () => [tokenTier('base', null, { p: 3, c: 15 })],
  claude: () => [tokenTier('base', null, { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 })],
  claudeLong: () => [
    tokenTier('standard', 200000, { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 }),
    tokenTier('long_context', null, { p: 6, c: 22.5, cr: 0.6, cc: 7.5, cc1h: 12 })
  ],
  gptLong: () => [tokenTier('standard', 272000, { p: 2.5, c: 15, cr: 0.25 }), tokenTier('long_context', null, { p: 5, c: 22.5, cr: 0.5 })],
  three: () => [
    tokenTier('short', 32000, { p: 1.2, c: 6, cr: 0.24 }),
    tokenTier('mid', 128000, { p: 2.4, c: 12, cr: 0.48 }),
    tokenTier('long', null, { p: 3, c: 15, cr: 0.6 })
  ],
  image: () => [tokenTier('base', null, { p: 5, c: 40, cr: 1.25, img: 10, img_o: 40 })]
}

/** Templates in labelled groups (prices.presets.group.<key>), as PricePresets shows them. */
export type PresetGroup = { key: string; items: string[] }
export const TIER_PRESET_GROUPS: PresetGroup[] = [
  { key: 'fixed', items: ['single', 'claude', 'image'] },
  { key: 'tiered', items: ['claudeLong', 'gptLong', 'three'] }
]

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
  const c = JSON.parse(JSON.stringify(asTokenConfig(raw))) as TokenConfig
  const tiers = sortedTiers(c.tiers).map(t => ({ ...t, unit: t.unit ?? 'token' }))
  if (tiers.length) tiers[tiers.length - 1].max_len = null
  return { tiers, rules: c.rules.map(r => ({ ...r, conds: r.conds.map(editableCond) })) }
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

export const hasSplitCache = (t: TokenTier) => 'cc1h' in t
export const hasImages = (t: TokenTier) => 'img' in t || 'img_o' in t

/** One cache write price, or 5 min and 1 h apart: one price drops cc1h. */
export function setSplitCache(t: TokenTier, on: boolean) {
  if (on) t.cc1h ??= null
  else delete t.cc1h
}

/** Image tokens at prices of their own, or at the text prices: off drops img and img_o. */
export function setImages(t: TokenTier, on: boolean) {
  if (on) Object.assign(t, { img: t.img ?? null, img_o: t.img_o ?? null })
  else {
    delete t.img
    delete t.img_o
  }
}

export function newCond(source: CondSource): RuleCond {
  return source === 'time' ? { source, tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 } : { source, key: '', op: 'eq', value: '' }
}

export function newRule(name: string): MarkupRule {
  return { name, multiplier: 2, conds: [newCond('param')] }
}

/** Changes the operator; the text typed stays (exists takes none). */
export function setOp(c: FieldCond, op: CondOp) {
  c.op = op
  if (op === 'exists') delete c.value
  else c.value ??= ''
}
