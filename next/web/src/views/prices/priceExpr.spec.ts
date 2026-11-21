import { describe, expect, it } from 'vitest'
import golden from '../../../../server/internal/billing/expr/testdata/generate_golden.json'
import {
  checkTokenConfig,
  compactTokenConfig,
  condExpr,
  checkPrice,
  requestTier,
  takesCustomPrice,
  whenExpr,
  fmt,
  goQuote,
  priceExpression,
  simpleTier,
  singleTier,
  summarize,
  tokenExpr,
  type CondOp,
  type CondValue,
  type FieldCond,
  type MarkupRule,
  type PriceMode,
  type RuleCond,
  type TierCond,
  type VisualConfig,
  type TokenTier
} from './priceExpr'

const upTo = (value: number): TierCond => ({ var: 'len', op: '<=', value })
/** A token tier up to a length, or the rest (null). */
const tier = (name: string, max: number | null, prices: Partial<TokenTier> = {}): TokenTier => ({
  name,
  ...(max === null ? {} : { when: upTo(max) }),
  unit: 'token',
  ...prices
})
const cfg = (tiers: TokenTier[], rules: MarkupRule[] = []): VisualConfig => ({ tiers, rules })
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
    expect(tokenExpr(cfg([tier('base', null, { cr: 0, cc: null })]))).toBe('tier("base", p*0 + c*0 + cr*0)')
  })
  it('chains tiers in the order listed as the length tiers did, nesting from the third', () => {
    expect(tokenExpr(cfg([STANDARD, LONG]))).toBe(TWO)
    const three = cfg([tier('short', 32000, { p: 1, c: 6 }), tier('mid', 128000, { p: 2, c: 12 }), tier('long', null, { p: 3, c: 15 })])
    expect(tokenExpr(three)).toBe(
      'len <= 32000 ? tier("short", p*1 + c*6) : (len <= 128000 ? tier("mid", p*2 + c*12) : tier("long", p*3 + c*15))'
    )
    const four = cfg([tier('a', 1000), tier('b', 2000), tier('c', 3000), tier('d', null)])
    expect(tokenExpr(four)).toBe(
      'len <= 1000 ? tier("a", p*0 + c*0) : (len <= 2000 ? tier("b", p*0 + c*0) : (len <= 3000 ? tier("c", p*0 + c*0) : tier("d", p*0 + c*0)))'
    )
  })
  it('prices a request tier flat and image tokens apart', () => {
    expect(tokenExpr(cfg([tier('base', 1000, { p: 1, c: 2 }), { name: 'bulk', unit: 'request', price: 0.05 }]))).toBe(
      'len <= 1000 ? tier("base", p*1 + c*2) : tier("bulk", flat(0.05))'
    )
    expect(tokenExpr(cfg([tier('base', null, { p: 5, c: 40, cr: 1.25, img: 10, img_o: 40 })]))).toBe(
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
    expect(tokenExpr(cfg([STANDARD, LONG], rules))).toBe(
      `(${TWO}) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1)` +
        ' * (param("service_tier") == "priority" && u("service_tier") == "priority" ? 2 : 1)' +
        ' * (hour("Asia/Shanghai") >= 0 && hour("Asia/Shanghai") < 8 ? 0.8 : 1)'
    )
    expect(tokenExpr(cfg([tier('base', null, { p: 1, c: 2 })], [rule(1.5, { source: 'header', key: 'x', op: 'exists' })]))).toBe(
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
    expect(tokenExpr(c)).toBe(
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
  it('leaves out unset prices, a request tier’s token prices and the value of exists', () => {
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
    expect(compactTokenConfig(edited)).toEqual({
      tiers: [
        { name: 'a', when: { and: [upTo(1000), { var: 'p', op: '>', value: 5 }, { not: { var: 'hour', op: '<', value: 6, tz: 'UTC' } }] }, unit: 'token', p: 1, c: 0 },
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
  it('is one tier for a set of prices or a price per request, and only that shape is simple', () => {
    expect(singleTier({ p: 1, c: 2, cr: null, cc: 0 })).toEqual({
      tiers: [{ name: 'base', unit: 'token', p: 1, c: 2, cc: 0 }],
      rules: []
    })
    expect(requestTier(0.5)).toEqual({ tiers: [{ name: 'base', unit: 'request', price: 0.5 }], rules: [] })
    expect(simpleTier(singleTier({ p: 1, c: 2 }))).toMatchObject({ p: 1 })
    expect(simpleTier(requestTier(1))).toMatchObject({ unit: 'request', price: 1 })
    expect(simpleTier(cfg([STANDARD, LONG]))).toBeNull()
    expect(simpleTier(cfg([tier('base', null)], [rule(2, { source: 'header', key: 'x', op: 'exists' })]))).toBeNull()
    expect(takesCustomPrice('visual', requestTier(1))).toBe(true)
    expect(takesCustomPrice('visual', cfg([STANDARD, LONG]))).toBe(false)
    expect(takesCustomPrice('expression', {})).toBe(false)
  })
})

describe('local checks', () => {
  const keys = (c: VisualConfig) => checkTokenConfig(c).map(x => x.key.replace('prices.local.', ''))
  it('pass a whole config', () => {
    expect(keys(cfg([STANDARD, LONG], [rule(2, { source: 'param', key: 'n', op: 'gt', value: 3 })]))).toEqual([])
  })
  it('want tiers with valid, distinct names, a condition on each but the last, and at most 20', () => {
    expect(keys(cfg([]))).toEqual(['noTier'])
    expect(keys(cfg([tier('a b', 10), tier('x', 5), tier('x', null)]))).toEqual(['tierName', 'tierDup'])
    expect(keys(cfg([tier('a', null), tier('b', null)]))).toEqual(['tierWhen'])
    expect(keys(cfg([tier('a', 100), tier('b', 200)]))).toEqual(['tierRest'])
    expect(keys(cfg(Array.from({ length: 21 }, (_, i) => tier(`t${i}`, i === 20 ? null : i + 1))))).toEqual(['tooManyTiers'])
    expect(keys(cfg(Array.from({ length: 20 }, (_, i) => tier(`t${i}`, i === 19 ? null : i + 1))))).toEqual([])
  })
  it('want every tier condition whole, as the server checks it', () => {
    const one = (when: unknown) => keys(cfg([{ name: 'a', when: when as TierCond, unit: 'token' }, tier('b', null)]))
    expect(one({ var: 'len', op: '>', value: -5 })).toEqual([])
    expect(one({ var: 'len', op: '>', value: '200000' })).toEqual([])
    expect(one({ var: 'len', op: '>', value: null })).toEqual(['condValue'])
    expect(one({ var: 'len', op: '>', value: 'x' })).toEqual(['condValue'])
    expect(one({ var: 'len', op: '=>', value: 1 })).toEqual(['condShape'])
    expect(one({ var: 'cr', op: '>', value: 1 })).toEqual(['condShape'])
    expect(one({ var: 'len', op: '>' })).toEqual(['condShape'])
    expect(one({ var: 'len', op: '>', value: 1, tz: 'UTC' })).toEqual(['condShape'])
    expect(one({ var: 'len', op: '>', value: 1, and: [] })).toEqual(['condShape'])
    expect(one({ and: [], or: [] })).toEqual(['condShape'])
    expect(one({ and: [] })).toEqual(['condEmpty'])
    expect(one({ or: [{ var: 'p', op: '<', value: 1 }, { and: [] }] })).toEqual(['condEmpty'])
    expect(one({ not: { var: 'hour', op: '<', value: 24 } })).toEqual(['condTime'])
    expect(one({ var: 'hour', op: '<', value: 1.5 })).toEqual(['condTime'])
    expect(one({ var: 'month', op: '==', value: 0 })).toEqual(['condTime'])
    expect(one({ var: 'day', op: '==', value: 31, tz: '' })).toEqual([])
    expect(one({ var: 'weekday', op: '==', value: 6, tz: 'Mars/Base' })).toEqual(['condTz'])
    expect(one({ var: 'minute', op: '>=', value: 59, tz: 'Asia/Shanghai' })).toEqual([])
  })
  it('want a tier condition at most 5 deep and of at most 20 comparisons', () => {
    const one = (when: TierCond) => keys(cfg([{ name: 'a', when, unit: 'token' }, tier('b', null)]))
    const nest = (depth: number): TierCond => (depth === 1 ? upTo(1) : { not: nest(depth - 1) })
    expect(one(nest(5))).toEqual([])
    expect(one(nest(6))).toEqual(['condDepth'])
    expect(one({ and: Array.from({ length: 20 }, (_, i) => upTo(i)) })).toEqual([])
    expect(one({ and: Array.from({ length: 21 }, (_, i) => upTo(i)) })).toEqual(['condLeaves'])
  })
  it('want every rule complete', () => {
    const bad: RuleCond[] = [
      { source: 'param', key: ' ', op: 'eq', value: 'x' },
      { source: 'header', key: 'x', op: 'contains', value: '' },
      { source: 'usage', key: 'n', op: 'gt', value: 'many' },
      { source: 'header', key: 'n', op: 'gt', value: 1 },
      { source: 'time', tz: 'UTC', from_hour: 3, to_hour: 3 }
    ]
    expect(bad.map(c => keys(cfg([tier('a', null)], [rule(2, c)])))).toEqual(bad.map(() => ['ruleField']))
    expect(keys(cfg([tier('a', null)], [{ name: '', multiplier: null, conds: [{ source: 'header', key: 'x', op: 'exists' }] }]))).toEqual([
      'ruleField'
    ])
  })
  it('refuse what the server refuses', () => {
    const one = (c: RuleCond) => keys(cfg([tier('a', null)], [rule(2, c)]))
    expect(one({ source: 'header', key: 'k', op: 'ne', value: 3 })).toEqual(['ruleField'])
    expect(one({ source: 'header', key: 'bad header', op: 'exists' })).toEqual(['ruleHeader'])
    expect(one({ source: 'param', key: 'k', op: 'eq', value: '' })).toEqual(['ruleField'])
    expect(one({ source: 'param', key: 'k', op: 'contains', value: 5 })).toEqual(['ruleField'])
    expect(one({ source: 'param', key: 'k'.repeat(201), op: 'exists' })).toEqual(['ruleField'])
    expect(one({ source: 'time', tz: 'Mars/Base', from_hour: 1, to_hour: 2 })).toEqual(['ruleTz'])
    // Go's time.LoadLocation takes neither an offset nor a name in another case, though Intl does.
    for (const tz of ['+08:00', '-05:00', 'asia/shanghai', 'utc', 'Local'])
      expect([tz, one({ source: 'time', tz, from_hour: 1, to_hour: 2 })]).toEqual([tz, ['ruleTz']])
    for (const tz of ['Asia/Shanghai', 'UTC', 'Etc/UTC', 'America/New_York'])
      expect([tz, one({ source: 'time', tz, from_hour: 1, to_hour: 2 })]).toEqual([tz, []])
    expect(one({ source: 'time', tz: '', from_hour: 23, to_hour: 24 })).toEqual([])
    expect(keys(cfg([tier('a', null)], [rule(-1, { source: 'header', key: 'x', op: 'exists' })]))).toEqual(['ruleField'])
    expect(keys(cfg([tier('a', 100, { p: -1 }), tier('b', null)]))).toEqual(['negative'])
    expect(keys(cfg([{ name: 'q', unit: 'request', price: null }]))).toEqual(['tierPrice'])
    expect(keys(requestTier(-1))).toEqual(['negative'])
    expect(keys(requestTier(0))).toEqual([])
  })
  it('check a price as it is sent, by its mode', () => {
    const keys = (p: Parameters<typeof checkPrice>[0]) => checkPrice(p).map(x => x.key.replace('prices.local.', ''))
    expect(keys({ mode: 'expression', config: {}, expression: ' \n' })).toEqual(['emptyExpr'])
    expect(keys({ mode: 'expression', config: {}, expression: 'p*3' })).toEqual([])
    expect(keys({ mode: 'visual', config: { ...requestTier(null), rules: [rule(-1, { source: 'header', key: 'x', op: 'exists' })] }, expression: '' })).toEqual([
      'tierPrice',
      'ruleField'
    ])
    expect(keys({ mode: 'visual', config: cfg([]), expression: '' })).toEqual(['noTier'])
  })
})

describe('the summary of a price', () => {
  it('is its first tier (the first to match), with the number of tiers and rules', () => {
    expect(summarize('visual', requestTier(0.02))).toEqual({ kind: 'visual', first: { name: 'base', unit: 'request', price: 0.02 }, tiers: 1, rules: 0 })
    expect(summarize('visual', cfg([STANDARD, LONG], [rule(2, { source: 'header', key: 'x', op: 'exists' })]))).toEqual({
      kind: 'visual',
      first: STANDARD,
      tiers: 2,
      rules: 1
    })
    expect(summarize('expression', {})).toEqual({ kind: 'expression' })
  })

  it('a price per request takes rules as a price by tokens does, and then no custom price', () => {
    const rules = [rule(2, { source: 'param', key: 'quality', op: 'eq', value: 'hd' })]
    const c = { ...requestTier(0.04), rules }
    expect(priceExpression('visual', c)).toBe('(tier("base", flat(0.04))) * (param("quality") == "hd" ? 2 : 1)')
    expect(takesCustomPrice('visual', c)).toBe(false)
  })
})
