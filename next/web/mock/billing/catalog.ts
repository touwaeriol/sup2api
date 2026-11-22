// The price catalogs the sync sources "fetch" (mock upstream data): known
// models, as their vendors price them, and deterministic synthetic ones.
import { singleTier, type PriceMode } from '../../src/views/prices/priceExpr'
import { PRESETS, upTo } from '../../src/views/prices/presets'
import { rnd } from './shared'

type Family = 'anthropic' | 'openai' | 'google' | 'mistral' | 'deepseek' | 'xai'
export interface CatalogEntry {
  model: string
  family: Family
  mode: PriceMode
  config: Record<string, any>
}

const round = (n: number) => Number(n.toPrecision(4))

/**
 * Two tiers at 200K, as LiteLLM prices Claude Sonnet. It follows the
 * editor's preset claudeLong: changing the preset changes the sync samples of
 * the mock with it.
 */
export const SONNET = { tiers: PRESETS.claudeLong(), rules: [] }

/** Sonnet's two tiers with a cheaper long context at night (22:00–06:00 in Shanghai), checked first. */
export const SONNET_NIGHT = {
  tiers: [
    {
      name: 'long_night',
      when: { and: [{ var: 'len', op: '>', value: 200000 }, { or: [{ var: 'hour', tz: 'Asia/Shanghai', op: '>=', value: 22 }, { var: 'hour', tz: 'Asia/Shanghai', op: '<', value: 6 }] }] },
      unit: 'token',
      p: 4,
      c: 18
    },
    ...SONNET.tiers
  ],
  rules: []
}

const KNOWN: CatalogEntry[] = [
  {
    model: 'claude-sonnet-4-5',
    family: 'anthropic',
    mode: 'visual',
    config: SONNET
  },
  { model: 'claude-haiku-4-5', family: 'anthropic', mode: 'visual', config: singleTier({ p: 1, c: 5, cr: 0.1, cc: 1.25, cc1h: 2 }) },
  { model: 'claude-opus-4-1', family: 'anthropic', mode: 'visual', config: singleTier({ p: 15, c: 75, cr: 1.5, cc: 18.75, cc1h: 30 }) },
  { model: 'gpt-4o', family: 'openai', mode: 'visual', config: singleTier({ p: 2.5, c: 10, cr: 1.25 }) },
  { model: 'gpt-4o-mini', family: 'openai', mode: 'visual', config: singleTier({ p: 0.15, c: 0.6, cr: 0.075 }) },
  { model: 'gpt-5', family: 'openai', mode: 'visual', config: singleTier({ p: 1.25, c: 10, cr: 0.125 }) },
  { model: 'gemini-2.5-flash', family: 'google', mode: 'visual', config: singleTier({ p: 0.3, c: 2.5, cr: 0.075 }) },
  {
    model: 'gemini-2.5-pro',
    family: 'google',
    mode: 'visual',
    config: {
      tiers: [
        { name: 'standard', when: upTo(200000), unit: 'token', p: 1.25, c: 10, cr: 0.31 },
        { name: 'long_context', unit: 'token', p: 2.5, c: 15, cr: 0.625 }
      ],
      rules: []
    }
  },
  { model: 'mistral-large-latest', family: 'mistral', mode: 'visual', config: singleTier({ p: 2, c: 6 }) },
  { model: 'deepseek-chat', family: 'deepseek', mode: 'visual', config: singleTier({ p: 0.27, c: 1.1, cr: 0.07 }) },
  { model: 'grok-4', family: 'xai', mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.75 }) }
]

const PREFIX: Record<Family, string> = { anthropic: 'claude-legacy', openai: 'gpt-variant', google: 'gemini-exp', mistral: 'mistral', deepseek: 'deepseek', xai: 'grok' }
const SUFFIX = ['preview', 'latest', '2024-08-06', 'mini', 'turbo', 'instruct']
const FAMILIES = Object.keys(PREFIX) as Family[]

/** Deterministic synthetic models of a family (to exercise large previews). */
function synthetic(family: Family, count: number): CatalogEntry[] {
  const out: CatalogEntry[] = []
  for (let i = 1; i <= count; i++) {
    const r = (k: number) => rnd(FAMILIES.indexOf(family) * 10007 + i * 31 + k)
    const p = round(0.05 + r(1) * 10)
    const cfg: Record<string, number> = { p, c: round(p * (3 + Math.floor(r(2) * 3))) }
    if (r(3) > 0.4) cfg.cr = round(p / 10)
    out.push({ model: `${PREFIX[family]}-${String(i).padStart(4, '0')}-${SUFFIX[i % SUFFIX.length]}`, family, mode: 'visual', config: singleTier(cfg) })
  }
  return out
}

/** Vendor id of a family in each catalog (LiteLLM calls Google "gemini"). */
export const vendorOf = (kind: 'litellm' | 'models_dev', f: Family) => (kind === 'litellm' && f === 'google' ? 'gemini' : f)

export const CATALOG = {
  litellm: [...KNOWN, ...FAMILIES.flatMap((f) => synthetic(f, 800))],
  models_dev: [
    // models.dev lists the newer GPT-4o price than the one we synced from LiteLLM.
    ...KNOWN.map((e) => (e.model === 'gpt-4o' ? { ...e, config: singleTier({ p: 2.5, c: 10, cr: 1.25, cc: 0 }) } : e)),
    ...FAMILIES.flatMap((f) => synthetic(f, 12))
  ],
  // What the upstream sup2api instance exposes to our key (base prices; its group rate is 1.5).
  sup2api: KNOWN.filter((e) => e.family === 'anthropic' || e.family === 'openai')
}
export const UPSTREAM_GROUP_RATE = 1.5
export const SKIPPED = { litellm: 37, models_dev: 4, sup2api: 0 }

/** A visual config with every price times `m`. */
export function scaleConfig(cfg: Record<string, any>, m: number): Record<string, any> {
  const scale = (t: Record<string, any>) => Object.fromEntries(Object.entries(t).map(([k, v]) => [k, typeof v === 'number' ? round(v * m) : v]))
  return { ...cfg, tiers: cfg.tiers.map(scale) }
}
