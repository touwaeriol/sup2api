// The global prices of the mock: their rows (a few manual and synced ones,
// as the sync previews need them), the expressions they ever had, and the
// price math group pricing uses (pricingStore.ts, pricing.ts).
import { now } from '../router'
import { priceExpression, requestTier, singleTier, type PriceMode } from '../../src/views/prices/priceExpr'
import { CATALOG, SONNET, SONNET_NIGHT, scaleConfig } from './catalog'
import { tryEvaluate, type Usage } from './engine'
import { hashOf } from './shared'

// Prices are keyed by complete model ids; wildcards are rejected (same rule as the server).
export const MODEL_ID = /^[A-Za-z0-9._:/@+-]{1,200}$/

export interface MockPrice {
  id: number
  model: string
  mode: PriceMode
  config: Record<string, any>
  expression: string
  expr_version: number
  expr_hash: string
  /** manual: entered by an admin; sync: imported from a price source. */
  source: 'manual' | 'sync'
  sync_source_id: number | null
  synced_at: string | null
  enabled: boolean
  note: string
  updated_by: number | null
  updated_at: string
}

export const history = new Map<string, { expr_hash: string; expression: string; expr_version: number; created_at: string }>()

export function remember(expression: string) {
  const h = hashOf(expression)
  if (!history.has(h)) history.set(h, { expr_hash: h, expression, expr_version: 1, created_at: now() })
  return h
}

type PriceSeed = Omit<MockPrice, 'expr_hash' | 'expr_version' | 'updated_at' | 'expression' | 'sync_source_id' | 'synced_at' | 'updated_by'> & {
  expression?: string
  sync_source_id?: number | null
}

function mkPrice(p: PriceSeed): MockPrice {
  const expression = p.mode === 'expression' ? p.expression ?? '' : priceExpression(p.mode, p.config)
  const synced = p.source === 'sync'
  return {
    ...p,
    expression,
    expr_version: 1,
    expr_hash: remember(expression),
    sync_source_id: synced ? p.sync_source_id ?? null : null,
    synced_at: synced ? now(-2 * 86400) : null,
    updated_by: 1,
    updated_at: now(-86400)
  }
}

const litellmSynth = CATALOG.litellm.filter((e) => e.model.startsWith('claude-legacy-'))

export const prices: MockPrice[] = [
  // Manual, identical to the synced definition -> "unchanged" in previews.
  mkPrice({ id: 1, model: 'claude-sonnet-4-5', mode: 'visual', config: SONNET, source: 'manual', enabled: true, note: '' }),
  // Synced from LiteLLM and still current -> "unchanged".
  mkPrice({ id: 2, model: 'claude-haiku-4-5', mode: 'visual', config: singleTier({ p: 1, c: 5, cr: 0.1, cc: 1.25, cc1h: 2 }), source: 'sync', sync_source_id: 1, enabled: true, note: '' }),
  mkPrice({
    id: 3,
    model: 'claude-sonnet-x',
    mode: 'visual',
    config: {
      tiers: SONNET.tiers,
      rules: [
        { name: 'fast mode', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] },
        {
          name: 'priority',
          multiplier: 2,
          conds: [
            { source: 'param', key: 'service_tier', op: 'eq', value: 'priority' },
            { source: 'usage', key: 'service_tier', op: 'eq', value: 'priority' }
          ]
        },
        { name: 'night', multiplier: 0.8, conds: [{ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 }] }
      ]
    },
    source: 'manual',
    enabled: true,
    note: 'fast mode costs double'
  }),
  mkPrice({ id: 4, model: 'web-search', mode: 'visual', config: requestTier(0.01), source: 'manual', enabled: true, note: '' }),
  // Tiers by length and by the time of day.
  mkPrice({ id: 9, model: 'claude-sonnet-night', mode: 'visual', config: SONNET_NIGHT, source: 'manual', enabled: true, note: 'cheaper long context at night' }),
  // Manual and different from every source -> "manual" (unticked by default).
  mkPrice({
    id: 5,
    model: 'claude-opus-4-1',
    mode: 'expression',
    config: {},
    expression: 'tier("base", flat(0.01) + p*15 + c*75) * (hour("Asia/Shanghai") < 8 ? 0.8 : 1)',
    source: 'manual',
    enabled: false,
    note: 'night discount'
  }),
  // Synced from LiteLLM, source price changed since -> "update".
  mkPrice({ id: 6, model: 'gpt-4o', mode: 'visual', config: singleTier({ p: 5, c: 15, cr: 2.5 }), source: 'sync', sync_source_id: 1, enabled: true, note: '' }),
  // Synced from models.dev and current.
  mkPrice({ id: 7, model: 'gemini-2.5-flash', mode: 'visual', config: singleTier({ p: 0.3, c: 2.5, cr: 0.075 }), source: 'sync', sync_source_id: 2, enabled: true, note: '' }),
  mkPrice({ id: 8, model: 'gpt-4o-mini', mode: 'visual', config: singleTier({ p: 0.3, c: 1.2 }), source: 'sync', sync_source_id: 1, enabled: true, note: '' }),
  // A few synthetic LiteLLM models: current, outdated, and manually edited.
  ...litellmSynth.slice(0, 6).map((e, i) => mkPrice({ id: 20 + i, model: e.model, mode: e.mode, config: e.config, source: 'sync', sync_source_id: 1, enabled: true, note: '' })),
  ...litellmSynth
    .slice(6, 10)
    .map((e, i) => mkPrice({ id: 30 + i, model: e.model, mode: e.mode, config: scaleConfig(e.config, 0.8), source: 'sync', sync_source_id: 1, enabled: true, note: '' })),
  ...litellmSynth
    .slice(10, 13)
    .map((e, i) => mkPrice({ id: 40 + i, model: e.model, mode: e.mode, config: scaleConfig(e.config, 1.2), source: 'manual', enabled: true, note: 'negotiated' }))
]

// ------------------------------------------------------------------ price math of group pricing

/** A price as the pricing endpoints return it. `multiplier` is only on the list price of a multiplier rule: that price (the global one, as it is) times it. */
export interface PriceDef {
  mode: PriceMode
  config: Record<string, any> | null
  expression: string
  multiplier?: string
}

/** The enabled price of a model: one per complete model id (no wildcards); null when it has none. */
export function priceFor(model: string) {
  return prices.find((p) => p.enabled && p.model === model) || null
}

/** The enabled global price of a model; null when it has none. */
export function globalPrice(model: string): PriceDef | null {
  const p = priceFor(model)
  return p && { mode: p.mode, config: p.config, expression: p.expression }
}

/** The models with an enabled global price, sorted. */
export function pricedModels(): string[] {
  return prices.filter((p) => p.enabled).map((p) => p.model).sort()
}

/** A price given as a config: what a custom price rule holds. */
export function configPrice(mode: PriceMode, config: Record<string, any>): PriceDef {
  return { mode, config, expression: priceExpression(mode, config) }
}

/** What a call with `usage` costs at a price, in USD, its multiplier included; null when the expression cannot be evaluated with that usage (an error, or a result that is not a finite non-negative number, as expr.Eval refuses). */
export function priceCost(def: PriceDef, usage: Partial<Usage>): number | null {
  const cost = tryEvaluate(def.expression, { usage })?.cost
  return cost !== undefined && Number.isFinite(cost) && cost >= 0 ? cost * Number(def.multiplier ?? 1) : null
}
