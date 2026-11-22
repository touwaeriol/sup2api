// The checks of a price before it is saved, as POST /prices/validate reports
// them: the config the server's generator takes, the expression's syntax by
// pattern, the usage facts it reads, trial runs and the big-cost warning.
import { checkVisualConfig } from '../../src/views/prices/priceCheck'
import { PRICE_KEYS, PRICE_MODES, asVisualConfig, priceExpression, type PriceMode } from '../../src/views/prices/priceExpr'
import { ALLOWED_IDENTS, evaluate, fmtN, surcharges } from './engine'
import { billingSettings } from './settings'
import { hashOf, money, msg, type LText } from './shared'

/** Why the server's generator refuses a price: the field and what is wrong with it. */
export type ConfigIssue = { field: 'mode' | 'config'; detail: string }
type Issue = { code: string; message: LText; detail?: unknown; line?: number; column?: number }

/**
 * Why the server's generator refuses a price (expr.Generate): an unknown
 * mode, a config on an expression price, a visual config the console's own
 * checks refuse; null when it takes it.
 */
function configIssue(mode: string, config: Record<string, any>): ConfigIssue | null {
  if (!PRICE_MODES.includes(mode as PriceMode)) return { field: 'mode', detail: `unknown mode "${mode}"` }
  if (mode === 'expression') return Object.keys(config).length ? { field: 'config', detail: 'must be {}: an expression price is its expression' } : null
  const issues = checkVisualConfig(asVisualConfig(config))
  return issues.length ? { field: 'config', detail: issues.map((x) => x.key.replace('prices.local.', '')).join(', ') } : null
}

/** An issue as text: `field: detail`. */
export const issueText = (issue: ConfigIssue) => `${issue.field}: ${issue.detail}`

/**
 * A price as POST /prices/validate reads a body: a visual price's expression
 * is generated from its config, whatever the client sent; a body without a
 * mode is an expression price there (a save wants the mode).
 */
export function exprOf(body: any): { mode: PriceMode; config: Record<string, any>; expression: string; issue: ConfigIssue | null } {
  const mode: string = body?.mode || 'expression'
  const config = body?.config && typeof body.config === 'object' ? body.config : {}
  const issue = configIssue(mode, config)
  const expression = mode === 'expression' ? String(body?.expression || '') : issue ? '' : priceExpression(mode as PriceMode, config)
  // An unknown mode comes with its issue, and nothing saves it.
  return { mode: mode as PriceMode, config, expression, issue }
}

/** 1-based line and column of a 0-based offset. */
function lineCol(src: string, at: number) {
  const before = src.slice(0, at).split('\n')
  return { line: before.length, column: before[before.length - 1].length + 1 }
}

const STR = /"(?:[^"\\]|\\.)*"/g

function editDistance(a: string, b: string): number {
  const d = Array.from({ length: b.length + 1 }, (_, j) => j)
  for (let i = 1; i <= a.length; i++) {
    let prev = d[0]
    d[0] = i
    for (let j = 1; j <= b.length; j++) {
      const t = d[j]
      d[j] = Math.min(d[j] + 1, d[j - 1] + 1, prev + (a[i - 1] === b[j - 1] ? 0 : 1))
      prev = t
    }
  }
  return d[b.length]
}

/**
 * First syntax problem the mock can place: an unbalanced bracket or an
 * unknown name. `src` is the body of `submitted` (no version prefix or
 * surrounding space); the position is in `submitted`, as the server places it.
 */
function syntaxIssue(src: string, submitted: string) {
  const bare = src.replace(STR, (m) => '"' + ' '.repeat(m.length - 2) + '"')
  const start = submitted.trimEnd().length - src.length
  const syntax = (at: number, detail: string) => ({ ...lineCol(submitted, start + at), code: 'syntax_error', message: msg('syntax error', '语法错误'), detail })
  const open: number[] = []
  for (let i = 0; i < bare.length; i++) {
    if (bare[i] === '(') open.push(i)
    else if (bare[i] === ')' && open.pop() === undefined) return syntax(i, '多了一个右括号')
  }
  if (open.length) return syntax(open[open.length - 1], '这个左括号没有配对的右括号')
  for (const m of bare.matchAll(/[A-Za-z_][A-Za-z0-9_]*/g)) {
    if (!ALLOWED_IDENTS.has(m[0])) {
      const near = [...ALLOWED_IDENTS].filter((x) => !x.startsWith('_') && editDistance(x, m[0]) <= 2).sort((a, b) => editDistance(a, m[0]) - editDistance(b, m[0]))[0]
      return syntax(m.index!, `不认识的名字 ${m[0]}${near ? `，是不是 ${near}？` : ''}`)
    }
  }
  return null
}

/** What an expression reads (expr.Analysis), by pattern. */
function analyze(src: string) {
  const all = (re: RegExp) => [...new Set([...src.matchAll(re)].map((m) => m[1]))]
  const bare = src.replace(STR, '""')
  const idents = new Set(bare.match(/[A-Za-z_][A-Za-z0-9_]*/g) || [])
  return {
    version: 1,
    vars: [...PRICE_KEYS, 'len'].filter((k) => idents.has(k)),
    params: all(/param\("([^"]+)"\)/g),
    headers: all(/header\("([^"]+)"\)/g).map((h) => h.toLowerCase()),
    facts: all(/\bu\("([^"]+)"\)/g),
    tiers: all(/tier\("([^"]+)"/g),
    rules: surcharges(src).map((r) => r.cond),
    len_boundaries: [...new Set([...bare.matchAll(/len\s*(?:<=|<|>=|>)\s*(\d+)/g)].map((m) => Number(m[1])))].sort((a, b) => a - b),
    zones: all(/(?:hour|minute|weekday|day|month)\("([^"]+)"\)/g)
  }
}

/** Usage fields the mock plugins declare. */
const DECLARED_FACTS = new Set(['service_tier', 'speed', 'video_seconds'])

const SMOKE: Array<[string, Record<string, number>]> = [
  ['全部为 0', {}],
  ['只有输入 1,000,000 token', { p: 1e6, len: 1e6 }],
  ['只有输出 1,000 token', { c: 1000 }],
  ['缓存读写各 1,000 token', { cr: 1000, cc: 1000, cc1h: 1000 }],
  ['len = 1,000,000,000', { p: 1e6, len: 1e9 }]
]

/** The first error of a price's definition: its config, a blank expression, its syntax; null when there is none. */
function definitionError(issue: ConfigIssue | null, src: string, submitted: string): Issue | null {
  if (issue) return { code: 'invalid_config', message: msg(issueText(issue), `价格配置无效：${issueText(issue)}`) }
  if (!src) return { code: 'empty_expression', message: msg('expression is empty', '表达式为空') }
  return syntaxIssue(src, submitted)
}

/** The usage fields an expression reads that no plugin declares. */
const factErrors = (facts: string[]): Issue[] =>
  facts
    .filter((f) => !DECLARED_FACTS.has(f))
    .map((f) => ({ code: 'unknown_usage_fact', message: msg('usage field not declared by any plugin', '没有插件声明这个用量字段'), detail: f }))

/** A trial run of each SMOKE sample: the first that fails, and what 1M input tokens cost (0 when no run got that far). */
function smokeTest(expression: string): { error: Issue | null; perMillion: number } {
  let perMillion = 0
  for (const [name, usage] of SMOKE) {
    try {
      const r = evaluate(expression, { usage })
      if (!Number.isFinite(r.cost) || r.cost < 0) throw new Error(`result ${r.cost} is not a finite non-negative number`)
      if (usage.p === 1e6 && usage.len === 1e6) perMillion = r.cost
    } catch (e) {
      const text = e instanceof Error ? e.message : String(e)
      return { error: { code: 'smoke_test_failed', message: msg('trial run failed', '试跑失败'), detail: `样例「${name}」：${text}` }, perMillion }
    }
  }
  return { error: null, perMillion }
}

/** The big-cost warning, when 1M input tokens cost more than the billing setting. */
function bigCostWarnings(perMillion: number): Issue[] {
  const threshold = Number(billingSettings.big_cost_warning_usd) || 10
  if (perMillion <= threshold) return []
  const message = msg(`1M input tokens cost $${fmtN(perMillion)} (threshold $${threshold})`, `100 万输入 token 费用 $${fmtN(perMillion)}，超过阈值 $${threshold}`)
  return [{ code: 'big_cost', message, detail: { cost: perMillion } }]
}

/** A price checked as the server checks it before a save (expr.Validate); `issue` is what the generator refuses of its mode or config. */
export function validate(body: any) {
  const { mode, config, expression, issue } = exprOf(body)
  const src = expression.trim().replace(/^v1\s*:/, '')
  const first = definitionError(issue, src, expression)
  const analysis = first ? undefined : analyze(src)
  const errors = first ? [first] : factErrors(analysis!.facts)
  const smoke = errors.length ? { error: null, perMillion: 0 } : smokeTest(expression)
  if (smoke.error) errors.push(smoke.error)
  const warnings = errors.length ? [] : bigCostWarnings(smoke.perMillion)
  const perMillion = money(smoke.perMillion)
  return { ok: !errors.length, expression, expr_hash: hashOf(expression), errors, warnings, samples: 38, cost_per_million_input: perMillion, analysis, mode, config, issue }
}
