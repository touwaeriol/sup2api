// The local checks of a price before it is saved (i18n keys under
// prices.local): they mirror the server's generator, as priceExpr.ts copies
// it, so a price the console sends is one the server generates. Pure TS, as
// priceExpr.
import {
  CMP_OPS,
  COND_VARS,
  MAX_COND_DEPTH,
  MAX_COND_LEAVES,
  NUMERIC_OPS,
  PRICE_KEYS,
  TIME_RANGES,
  asVisualConfig,
  condChildren,
  condDepth,
  condKind,
  condLeaves,
  goTrim,
  isSet,
  isTimeVar,
  num,
  timeValueFits,
  type CmpOp,
  type CondLeaf,
  type MarkupRule,
  type PriceMode,
  type RuleCond,
  type TierCond,
  type VisualConfig,
  type VisualTier
} from './priceExpr'

/** The most tiers a price takes. */
const MAX_TIERS = 20

export interface LocalIssue {
  key: string
  params?: Record<string, unknown>
}

const TIER_NAME = /^[A-Za-z0-9_.-]{1,64}$/
/** As the server's headerNameRe, of the trimmed lower-case key. */
const HEADER_NAME = /^[A-Za-z0-9!#$%&'*+.^_|~-]{1,100}$/

/** Whether a price is set and not negative. */
const isPrice = (v: unknown) => isSet(v) && num(v) >= 0
const isText = (v: unknown) => typeof v === 'string' && v !== ''

/**
 * Whether Go's time.LoadLocation takes a zone, as far as Intl can tell: an
 * IANA name, spelt with its case (Intl takes "asia/shanghai" and offsets
 * such as "+08:00", Go takes neither), and not "Local".
 */
function validTz(tz: string): boolean {
  if (tz === 'Local' || /^[+-]/.test(tz)) return false
  try {
    const resolved = new Intl.DateTimeFormat('en-US', { timeZone: tz }).resolvedOptions().timeZone
    return resolved === tz || resolved.toLowerCase() !== tz.toLowerCase()
  } catch {
    return false
  }
}

/** Why a condition of rule n (from 1) fails the server's generator, as the server checks it; null when it passes. */
function condIssue(c: RuleCond, n: number): LocalIssue | null {
  const field = { key: 'prices.local.ruleField', params: { n } }
  if (c.source === 'time') {
    const [a, b] = [c.from_hour, c.to_hour]
    if (!validTz(goTrim(c.tz ?? '') || 'UTC')) return { key: 'prices.local.ruleTz', params: { n } }
    const hours = Number.isInteger(a) && Number.isInteger(b) && a >= 0 && a <= 23 && b >= 0 && b <= 24 && a !== b
    return hours ? null : field
  }
  const key = goTrim(c.key)
  if (c.source === 'header' && !HEADER_NAME.test(key.toLowerCase())) return { key: 'prices.local.ruleHeader', params: { n } }
  if (c.source !== 'header' && (!key || new TextEncoder().encode(key).length > 200)) return field
  if (c.op === 'exists') return null
  if (NUMERIC_OPS.includes(c.op)) return c.source !== 'header' && typeof c.value === 'number' ? null : field
  if (c.op === 'contains' || c.source === 'header') return isText(c.value) ? null : field
  return c.value === '' || c.value === undefined || c.value === null ? field : null
}

function checkRules(rules: readonly MarkupRule[]): LocalIssue[] {
  const out: LocalIssue[] = []
  rules.forEach((r, i) => {
    const n = i + 1
    const issue =
      !isPrice(r.multiplier) || !r.conds.length
        ? { key: 'prices.local.ruleField', params: { n } }
        : r.conds.map(c => condIssue(c, n)).find(Boolean)
    if (issue) out.push(issue)
  })
  return out
}

const isCmpOp = (op: unknown): op is CmpOp => CMP_OPS.includes(op as CmpOp)

/** Why a comparison fails the server's generator; null when it passes. */
function leafIssue(n: CondLeaf, name: string | number): LocalIssue | null {
  if (!COND_VARS.includes(n.var) || !isCmpOp(n.op)) return { key: 'prices.local.condShape', params: { name } }
  if (!isTimeVar(n.var)) {
    if (n.tz !== undefined) return { key: 'prices.local.condShape', params: { name } }
    return isSet(n.value) ? null : { key: 'prices.local.condValue', params: { name } }
  }
  const tz = n.tz ?? ''
  if (typeof tz !== 'string' || !validTz(tz || 'UTC')) return { key: 'prices.local.condTz', params: { name } }
  const [lo, hi] = TIME_RANGES[n.var]
  return timeValueFits(n.var, n.value) ? null : { key: 'prices.local.condTime', params: { name, lo, hi } }
}

/** The first reason a condition fails the server's generator, of every node depth first; null when it passes. */
function whenIssue(n: TierCond, name: string | number): LocalIssue | null {
  const kind = condKind(n)
  if (!kind) return { key: 'prices.local.condShape', params: { name } }
  if (kind === 'leaf') return leafIssue(n as CondLeaf, name)
  const kids = condChildren(n)
  if (!kids.length) return { key: 'prices.local.condEmpty', params: { name } }
  return kids.reduce<LocalIssue | null>((found, k) => found ?? whenIssue(k, name), null)
}

/** Why a tier's condition is refused: every tier but the last has one, the last has none; within the limits. */
function tierWhenIssue(t: VisualTier, last: boolean, name: string | number): LocalIssue | null {
  if (last) return t.when ? { key: 'prices.local.tierRest' } : null
  if (!t.when) return { key: 'prices.local.tierWhen', params: { name } }
  const issue = whenIssue(t.when, name)
  if (issue) return issue
  if (condDepth(t.when) > MAX_COND_DEPTH) return { key: 'prices.local.condDepth', params: { name, n: MAX_COND_DEPTH } }
  return condLeaves(t.when) > MAX_COND_LEAVES ? { key: 'prices.local.condLeaves', params: { name, n: MAX_COND_LEAVES } } : null
}

/**
 * Problems of a visual config as saved (compactVisualConfig) that block
 * saving (i18n keys under prices.local). They mirror the server's generator,
 * and refuse more: a value a rule condition compares is never blank.
 */
export function checkVisualConfig(cfg: VisualConfig): LocalIssue[] {
  const out: LocalIssue[] = []
  if (!cfg.tiers.length) out.push({ key: 'prices.local.noTier' })
  if (cfg.tiers.length > MAX_TIERS) out.push({ key: 'prices.local.tooManyTiers', params: { n: MAX_TIERS } })
  const names = new Set<string>()
  cfg.tiers.forEach((t, i) => {
    const name = t.name || i + 1
    if (!TIER_NAME.test(t.name)) out.push({ key: 'prices.local.tierName', params: { n: i + 1 } })
    else if (names.has(t.name)) out.push({ key: 'prices.local.tierDup', params: { name } })
    names.add(t.name)
    if (t.unit === 'request' && !isSet(t.price)) out.push({ key: 'prices.local.tierPrice', params: { name } })
    else if ([t.price, ...PRICE_KEYS.map(k => t[k])].some(v => isSet(v) && num(v) < 0))
      out.push({ key: 'prices.local.negative', params: { name } })
    const issue = tierWhenIssue(t, i === cfg.tiers.length - 1, name)
    if (issue) out.push(issue)
  })
  return [...out, ...checkRules(cfg.rules)]
}

/** Problems of a price as it is sent (its config as saved) that block saving. */
export function checkPrice(p: { mode: PriceMode; config: unknown; expression: string }): LocalIssue[] {
  if (p.mode === 'expression') return p.expression.trim() ? [] : [{ key: 'prices.local.emptyExpr' }]
  return checkVisualConfig(asVisualConfig(p.config))
}
