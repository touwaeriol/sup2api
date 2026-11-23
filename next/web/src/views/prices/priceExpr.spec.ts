import { describe, expect, it } from 'vitest'
import golden from '../../../../server/internal/billing/expr/testdata/generate_golden.json'
import {
  compactVisualConfig,
  condExpr,
  requestTier,
  whenExpr,
  fmt,
  goQuote,
  perRequestOnly,
  priceExpression,
  singleTier,
  summarize,
  visualExpr,
  type CondOp,
  type CondValue,
  type FieldCond,
  type MarkupRule,
  type PriceMode,
  type RuleCond,
  type TierCond,
  type VisualConfig,
  type VisualTier
} from './priceExpr'

const upTo = (value: number): TierCond => ({ var: 'len', op: '<=', value })
/** A token tier up to a length, or the rest (null). */
const tier = (name: string, max: number | null, prices: Partial<VisualTier> = {}): VisualTier => ({
  name,
  ...(max === null ? {} : { when: upTo(max) }),
  unit: 'token',
  ...prices
})
const cfg = (tiers: VisualTier[], rules: MarkupRule[] = []): VisualConfig => ({ tiers, rules })
const rule = (multiplier: number, ...conds: RuleCond[]): MarkupRule => ({ name: '', multiplier, conds })

const STANDARD = tier('standard', 200000, { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 })
const LONG = tier('long_context', null, { p: 6, c: 22.5, cr: 0.6, cc: 7.5, cc1h: 12 })
const TWO =
  'len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)'

describe('the generated expression, as the server generates it', () => {
  // The server's TestGenerateGolden reads the same file.
  it.each(golden.map(c => [c.name, c] as const))('is the server’s, byte for byte: %s', (_, c) => {
    expect(priceExpression(c.mode as PriceMode, c.config)).toBe(c.expression)
  })
  it('is the expression of an old per-request and single-tier price, byte for byte', () => {
    expect(priceExpression('visual', requestTier(0.01))).toBe('tier("base", flat(0.01))')
    expect(priceExpression('visual', singleTier({ p: 3, c: 15, cr: 0.3 }))).toBe('tier("base", p*3 + c*15 + cr*0.3)')
    expect(priceExpression('visual', singleTier({ p: 1, c: 5, cr: 0.1, cc: 1.25, cc1h: 2 }))).toBe(
      'tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)'
    )
    expect(priceExpression('expression', {})).toBe('')
  })
  it('always prices p and c, and a cache price only when it is set (0 included)', () => {
    expect(visualExpr(cfg([tier('base', null, { cr: 0, cc: null })]))).toBe('tier("base", p*0 + c*0 + cr*0)')
  })
  it('chains tiers in the order listed as the length tiers did, nesting from the third', () => {
    expect(visualExpr(cfg([STANDARD, LONG]))).toBe(TWO)
    const three = cfg([tier('short', 32000, { p: 1, c: 6 }), tier('mid', 128000, { p: 2, c: 12 }), tier('long', null, { p: 3, c: 15 })])
    expect(visualExpr(three)).toBe(
      'len <= 32000 ? tier("short", p*1 + c*6) : (len <= 128000 ? tier("mid", p*2 + c*12) : tier("long", p*3 + c*15))'
    )
    const four = cfg([tier('a', 1000), tier('b', 2000), tier('c', 3000), tier('d', null)])
    expect(visualExpr(four)).toBe(
      'len <= 1000 ? tier("a", p*0 + c*0) : (len <= 2000 ? tier("b", p*0 + c*0) : (len <= 3000 ? tier("c", p*0 + c*0) : tier("d", p*0 + c*0)))'
    )
  })
  it('prices a request tier flat and image tokens apart', () => {
    expect(visualExpr(cfg([tier('base', 1000, { p: 1, c: 2 }), { name: 'bulk', unit: 'request', price: 0.05 }]))).toBe(
      'len <= 1000 ? tier("base", p*1 + c*2) : tier("bulk", flat(0.05))'
    )
    expect(visualExpr(cfg([tier('base', null, { p: 5, c: 40, cr: 1.25, img: 10, img_o: 40 })]))).toBe(
      'tier("base", p*5 + c*40 + cr*1.25 + img*10 + img_o*40)'
    )
  })
  it('multiplies the base by every rule, its conditions joined by &&', () => {
    const rules = [
      rule(2, { source: 'header', key: 'Anthropic-Beta ', op: 'contains', value: 'fast-mode' }),
      rule(
        2,
        { source: 'param', key: 'service_tier', op: 'eq', value: 'priority' },
        { source: 'usage', key: 'service_tier', op: 'eq', value: 'priority' }
      ),
      rule(0.8, { source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 })
    ]
    expect(visualExpr(cfg([STANDARD, LONG], rules))).toBe(
      `(${TWO}) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)` +
        ' * (param("service_tier") == "priority" && u("service_tier") == "priority" ? 2 : 1)' +
        ' * (hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.8 : 1)'
    )
    expect(visualExpr(cfg([tier('base', null, { p: 1, c: 2 })], [rule(1.5, { source: 'header', key: 'x', op: 'exists' })]))).toBe(
      '(tier("base", p*1 + c*2)) * (header("x") != "" ? 1.5 : 1)'
    )
  })
})

describe('a tier condition', () => {
  const leaf = (v: 'len' | 'p' | 'c', op: '<' | '<=' | '>' | '>=' | '==' | '!=', value: number): TierCond => ({ var: v, op, value })
  const hour = (op: '>=' | '<', value: number, tz?: string): TierCond => ({ var: 'hour', op, value, ...(tz === undefined ? {} : { tz }) })
  it('compares a count, and a time in its zone (UTC when blank or left out)', () => {
    expect(whenExpr(leaf('p', '>', 100))).toBe('p > 100')
    expect(whenExpr(leaf('c', '!=', 0.5))).toBe('c != 0.5')
    expect(whenExpr(hour('>=', 22, 'Asia/Shanghai'))).toBe('hour("Asia/Shanghai") >= 22')
    expect(whenExpr(hour('<', 6, ''))).toBe('hour("UTC") < 6')
    expect(whenExpr(hour('<', 6))).toBe('hour("UTC") < 6')
    expect(whenExpr({ var: 'weekday', tz: 'UTC', op: '==', value: 0 })).toBe('weekday("UTC") == 0')
    expect(whenExpr({ var: 'minute', op: '<', value: '30' })).toBe('minute("UTC") < 30')
  })
  it('joins a group by && or ||, writes a group of one as its child, and puts a group of several inside another in parentheses', () => {
    const a = leaf('len', '>', 200000)
    const night: TierCond = { or: [hour('>=', 22, 'Asia/Shanghai'), hour('<', 6, 'Asia/Shanghai')] }
    expect(whenExpr({ and: [a, night] })).toBe('len > 200000 && (hour("Asia/Shanghai") >= 22 || hour("Asia/Shanghai") < 6)')
    expect(whenExpr({ and: [a] })).toBe('len > 200000')
    expect(whenExpr({ or: [{ and: [a, leaf('p', '<', 5)] }] })).toBe('len > 200000 && p < 5')
    // A group of one holding a group of several is that group: in parentheses where it is one of several.
    expect(whenExpr({ and: [{ or: [{ or: [a, leaf('p', '<', 5)] }] }, leaf('c', '>', 1)] })).toBe('(len > 200000 || p < 5) && c > 1')
    expect(whenExpr({ and: [{ and: [a, leaf('p', '<', 5)] }, leaf('c', '>', 1)] })).toBe('(len > 200000 && p < 5) && c > 1')
  })
  it('negates in parentheses, nested as deep as it goes', () => {
    expect(whenExpr({ not: leaf('len', '<=', 1000) })).toBe('!(len <= 1000)')
    expect(whenExpr({ not: { or: [leaf('p', '>', 1), { not: { and: [leaf('c', '>', 2), hour('>=', 9, 'UTC')] } }] } })).toBe(
      '!(p > 1 || !(c > 2 && hour("UTC") >= 9))'
    )
  })
  it('chains three and more tiers by any condition, the last the rest', () => {
    const night: TierCond = { and: [leaf('len', '>', 200000), { or: [hour('>=', 22, 'Asia/Shanghai'), hour('<', 6, 'Asia/Shanghai')] }] }
    const c = cfg([
      { name: 'long_night', when: night, unit: 'token', p: 4, c: 18 },
      { name: 'long', when: leaf('len', '>', 200000), unit: 'token', p: 6, c: 22.5 },
      { name: 'base', unit: 'token', p: 3, c: 15 }
    ])
    expect(visualExpr(c)).toBe(
      'len > 200000 && (hour("Asia/Shanghai") >= 22 || hour("Asia/Shanghai") < 6) ? tier("long_night", p*4 + c*18) : (len > 200000 ? tier("long", p*6 + c*22.5) : tier("base", p*3 + c*15))'
    )
  })
})

describe('a rule condition', () => {
  const field = (source: FieldCond['source'], op: CondOp, value?: CondValue) => condExpr({ source, key: 'k', op, value })
  it('reads each source', () => {
    expect(field('param', 'eq', 'a')).toBe('param("k") == "a"')
    expect(field('header', 'eq', 'a')).toBe('header("k") == "a"')
    expect(field('usage', 'eq', 'a')).toBe('u("k") == "a"')
  })
  it('compares by each operator, literals by their type', () => {
    expect(field('param', 'eq', 3)).toBe('param("k") == 3')
    expect(field('param', 'eq', true)).toBe('param("k") == true')
    expect(field('param', 'ne', 'x')).toBe('param("k") != "x"')
    expect(field('usage', 'contains', 'fast')).toBe('has(u("k"), "fast")')
    expect(field('usage', 'gt', 1.5)).toBe('u("k") > 1.5')
    expect(field('usage', 'gte', 2)).toBe('u("k") >= 2')
    expect(field('param', 'lt', 0.25)).toBe('(param("k") ?? 0) < 0.25')
    expect(field('param', 'lte', 100)).toBe('(param("k") ?? 0) <= 100')
    expect(field('param', 'eq', 'say "hi"')).toBe('param("k") == "say \\"hi\\""')
  })
  it('tests presence: a parameter against nil, a header against "", a usage field against 0', () => {
    expect(field('header', 'exists')).toBe('header("k") != ""')
    expect(field('param', 'exists')).toBe('param("k") != nil')
    expect(field('usage', 'exists')).toBe('u("k") != 0')
  })
  it('takes a time range, across midnight in parentheses, UTC without a zone', () => {
    expect(condExpr({ source: 'time', tz: 'UTC', from_hour: 9, to_hour: 18 })).toBe('hour("UTC") >= 9 && hour("UTC") < 18')
    expect(condExpr({ source: 'time', tz: ' ', from_hour: 22, to_hour: 6 })).toBe('(hour("UTC") >= 22 || hour("UTC") < 6)')
  })
})

describe('strings', () => {
  it('are quoted as Go quotes them', () => {
    expect(goQuote('say "hi" \\ ok')).toBe('"say \\"hi\\" \\\\ ok"')
    expect(goQuote('\x07\b\f\n\r\t\v')).toBe('"\\a\\b\\f\\n\\r\\t\\v"')
    expect(goQuote('a\u0001b\u007f')).toBe('"a\\x01b\\x7f"')
    expect(goQuote('\u00a0\u200b\u2028\u{e0001}')).toBe('"\\u00a0\\u200b\\u2028\\U000e0001"')
    expect(goQuote('中文 é 😀')).toBe('"中文 é 😀"')
    expect(goQuote('\ud800')).toBe('"\ufffd"')
  })
})

describe('numbers', () => {
  it('print as Go prints them: shortest, without an exponent', () => {
    expect(fmt(3.75)).toBe('3.75')
    expect(fmt(200000)).toBe('200000')
    expect(fmt(1e-7)).toBe('0.0000001')
    expect(fmt(1.25e-8)).toBe('0.0000000125')
    expect(fmt(1e21)).toBe('1000000000000000000000')
    expect(fmt(0.1 + 0.2)).toBe('0.30000000000000004')
  })
})

describe('the config the editor saves', () => {
  it('keeps a blank price blank (never 0), and leaves out a request tier’s token prices and the value of exists', () => {
    const edited = cfg(
      [
        { ...tier('a', 0, { p: 1, c: null, cr: null, cc1h: null, img: null, price: 9 }), when: { and: [{ var: 'len', op: '<=', value: '1000' }, { var: 'p', op: '>', value: 5, tz: 'UTC' }, { not: { var: 'hour', op: '<', value: 6, tz: 'UTC' } }] } },
        { ...tier('b', null, { p: 2, c: 3 }), unit: 'request', price: 0.1 }
      ],
      [
        {
          name: 'r',
          multiplier: 2,
          conds: [
            { source: 'header', key: 'x', op: 'exists', value: 'stale' },
            { source: 'time', tz: 'UTC', from_hour: 1, to_hour: 2 }
          ]
        }
      ]
    )
    expect(compactVisualConfig(edited)).toEqual({
      tiers: [
        { name: 'a', when: { and: [upTo(1000), { var: 'p', op: '>', value: 5 }, { not: { var: 'hour', op: '<', value: 6, tz: 'UTC' } }] }, unit: 'token', p: 1, c: null, cr: null, cc1h: null, img: null },
        { name: 'b', unit: 'request', price: 0.1 }
      ],
      rules: [
        {
          name: 'r',
          multiplier: 2,
          conds: [
            { source: 'header', key: 'x', op: 'exists' },
            { source: 'time', tz: 'UTC', from_hour: 1, to_hour: 2 }
          ]
        }
      ]
    })
  })
  it('is one tier for a set of prices or a price per request', () => {
    expect(singleTier({ p: 1, c: 2, cr: null, cc: 0 })).toEqual({
      tiers: [{ name: 'base', unit: 'token', p: 1, c: 2, cc: 0 }],
      rules: []
    })
    expect(requestTier(0.5)).toEqual({ tiers: [{ name: 'base', unit: 'request', price: 0.5 }], rules: [] })
  })
})

describe('a price due per request', () => {
  const night: TierCond = { var: 'hour', tz: 'UTC', op: '<', value: 6 }
  it('has request tiers only, whatever time they are matched by', () => {
    expect(perRequestOnly(requestTier(0.02))).toBe(true)
    expect(perRequestOnly(cfg([{ name: 'night', when: night, unit: 'request', price: 1 }, { name: 'day', unit: 'request', price: 2 }]))).toBe(true)
    expect(perRequestOnly({ ...requestTier(1), rules: [rule(2, { source: 'usage', key: 'n', op: 'gt', value: 3 })] })).toBe(true)
  })
  it('reads no token count: no token tier, no condition on len, p or c, and some tier', () => {
    expect(perRequestOnly(cfg([]))).toBe(false)
    expect(perRequestOnly(cfg([{ name: 'night', when: night, unit: 'request', price: 1 }, tier('day', null, { p: 1, c: 2 })]))).toBe(false)
    const long: TierCond = { and: [night, { not: { var: 'p', op: '>', value: 100 } }] }
    expect(perRequestOnly(cfg([{ name: 'long', when: long, unit: 'request', price: 1 }, { name: 'short', unit: 'request', price: 2 }]))).toBe(false)
    expect(perRequestOnly(cfg([{ name: 'long', when: upTo(1000), unit: 'request', price: 1 }, { name: 'short', unit: 'request', price: 2 }]))).toBe(false)
  })
})

describe('the summary of a price', () => {
  it('is its rest (the last tier, which applies when none before it matches), with the number of tiers and rules', () => {
    expect(summarize('visual', requestTier(0.02))).toEqual({ kind: 'visual', rest: { name: 'base', unit: 'request', price: 0.02 }, tiers: 1, rules: 0 })
    expect(summarize('visual', cfg([STANDARD, LONG], [rule(2, { source: 'header', key: 'x', op: 'exists' })]))).toEqual({
      kind: 'visual',
      rest: LONG,
      tiers: 2,
      rules: 1
    })
    expect(summarize('expression', {})).toEqual({ kind: 'expression' })
  })

  it('a price per request takes rules as a price by tokens does', () => {
    const rules = [rule(2, { source: 'param', key: 'quality', op: 'eq', value: 'hd' })]
    const c = { ...requestTier(0.04), rules }
    expect(priceExpression('visual', c)).toBe('(tier("base", flat(0.04))) * (param("quality") == "hd" ? 2 : 1)')
  })
})
