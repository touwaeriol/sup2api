// The templates the price editors start from, in one place: tier templates
// (the visual editor, and the expression editor as generated source),
// request rule groups (the rule editor's "common rules") and expression
// examples (the guide, and the expression editor's "advanced" templates).
import type { MarkupRule, PriceKey, TierCond, TokenTier } from './priceExpr'

function tokenTier(name: string, when: TierCond | null, prices: Partial<Record<PriceKey, number | null>>): TokenTier {
  return when ? { name, when, unit: 'token', ...prices } : { name, unit: 'token', ...prices }
}

/** The condition of a tier up to a context length. */
const upTo = (n: number): TierCond => ({ var: 'len', op: '<=', value: n })

/** Tier templates by key (named by prices.presets.item.<key>); each makes fresh tiers. */
export const PRESETS: Record<string, () => TokenTier[]> = {
  single: () => [tokenTier('base', null, { p: 3, c: 15 })],
  claude: () => [tokenTier('base', null, { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 })],
  claudeLong: () => [
    tokenTier('standard', upTo(200000), { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 }),
    tokenTier('long_context', null, { p: 6, c: 22.5, cr: 0.6, cc: 7.5, cc1h: 12 })
  ],
  gptLong: () => [tokenTier('standard', upTo(272000), { p: 2.5, c: 15, cr: 0.25 }), tokenTier('long_context', null, { p: 5, c: 22.5, cr: 0.5 })],
  three: () => [
    tokenTier('short', upTo(32000), { p: 1.2, c: 6, cr: 0.24 }),
    tokenTier('mid', upTo(128000), { p: 2.4, c: 12, cr: 0.48 }),
    tokenTier('long', null, { p: 3, c: 15, cr: 0.6 })
  ],
  image: () => [tokenTier('base', null, { p: 5, c: 40, cr: 1.25, img: 10, img_o: 40 })],
  request: () => [{ name: 'base', unit: 'request', price: 0.01 }]
}

/** Templates in labelled groups (prices.presets.group.<key>), as PricePresets shows them. */
export type PresetGroup = { key: string; items: string[] }
export const TIER_PRESET_GROUPS: PresetGroup[] = [
  { key: 'fixed', items: ['single', 'claude', 'image', 'request'] },
  { key: 'tiered', items: ['claudeLong', 'gptLong', 'three'] }
]

/** Rule groups to start from (named by prices.rules.preset.<key>); each makes a fresh rule. */
export const RULE_PRESETS: Record<string, () => MarkupRule> = {
  priority: () => ({ name: 'priority', multiplier: 2, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'priority' }] }),
  flex: () => ({ name: 'flex', multiplier: 0.5, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'flex' }] }),
  fast: () => ({ name: 'fast mode', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] }),
  night: () => ({ name: 'night', multiplier: 0.8, conds: [{ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 }] })
}

/** Expression examples by key (titled by prices.guide.example.<key>), in the order the guide lists them. */
export const EXPR_EXAMPLES: Record<string, string> = {
  single: 'tier("base", p*3 + c*15 + cr*0.3)',
  longContext: 'len <= 200000\n  ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6)\n  : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)',
  priority: 'tier("base", p*2.5 + c*15 + cr*0.25)\n* (param("service_tier") == "priority" ? 2 : 1)',
  serviceTier: 'tier("base", p*2.5 + c*15 + cr*0.25)\n* (u("service_tier") == "priority" ? 2 : 1)\n* (u("service_tier") == "flex" ? 0.5 : 1)',
  night: 'tier("base", p*3 + c*15)\n* (hour("Asia/Shanghai") < 8 ? 0.5 : 1)',
  weekend: 'tier("base", p*3 + c*15)\n* (weekday("Asia/Shanghai") == 0 || weekday("Asia/Shanghai") == 6 ? 0.8 : 1)',
  minimum: 'tier("base", max(p*3 + c*15, flat(0.001)))',
  flatFee: 'tier("base", p*3 + c*15 + flat(0.002))',
  effort: 'param("reasoning.effort") == "high"\n  ? tier("high", p*3 + c*20)\n  : tier("normal", p*3 + c*15)'
}

/** The expression editor's templates beyond the tier ones: examples by key (prices.presets.item.<key>). */
export const EXPR_PRESET_GROUP: PresetGroup = { key: 'advanced', items: ['priority', 'night', 'minimum'] }
