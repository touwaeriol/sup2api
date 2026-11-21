import { describe, expect, it } from 'vitest'
import {
  checkTokenConfig,
  compactTokenConfig,
  condExpr,
  checkPerRequest,
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
  type RuleCond,
  type TokenConfig,
  type TokenTier
} from './priceExpr'

const tier = (name: string, max_len: number | null, prices: Partial<TokenTier> = {}): TokenTier => ({
  name,
  max_len,
  unit: 'token',
  ...prices
})
const cfg = (tiers: TokenTier[], rules: MarkupRule[] = []): TokenConfig => ({ tiers, rules })
const rule = (multiplier: number, ...conds: RuleCond[]): MarkupRule => ({ name: '', multiplier, conds })

const STANDARD = tier('standard', 200000, { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 })
const LONG = tier('long_context', null, { p: 6, c: 22.5, cr: 0.6, cc: 7.5, cc1h: 12 })
const TWO =
  'len <= 200000 ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6) : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)'

describe('the generated expression, as the server generates it', () => {
  it('is the legacy per-request and single-tier per-token expression, byte for byte', () => {
    expect(priceExpression('per_request', { price: 0.01 })).toBe('tier("base", flat(0.01))')
    expect(priceExpression('per_token', singleTier({ p: 3, c: 15, cr: 0.3 }))).toBe('tier("base", p*3 + c*15 + cr*0.3)')
    expect(priceExpression('per_token', singleTier({ p: 1, c: 5, cr: 0.1, cc: 1.25, cc1h: 2 }))).toBe(
      'tier("base", p*1 + c*5 + cr*0.1 + cc*1.25 + cc1h*2)'
    )
    expect(priceExpression('expression', {})).toBe('')
  })
  it('always prices p and c, and a cache price only when it is set (0 included)', () => {
    expect(tokenExpr(cfg([tier('base', null, { cr: 0, cc: null })]))).toBe('tier("base", p*0 + c*0 + cr*0)')
  })
  it('chains tiers by max_len as the legacy tiers did, nesting from the third', () => {
    expect(tokenExpr(cfg([STANDARD, LONG]))).toBe(TWO)
    const three = cfg([tier('long', null, { p: 3, c: 15 }), tier('mid', 128000, { p: 2, c: 12 }), tier('short', 32000, { p: 1, c: 6 })])
    expect(tokenExpr(three)).toBe(
      'len <= 32000 ? tier("short", p*1 + c*6) : (len <= 128000 ? tier("mid", p*2 + c*12) : tier("long", p*3 + c*15))'
    )
    const four = cfg([tier('a', 1000), tier('b', 2000), tier('c', 3000), tier('d', null)])
    expect(tokenExpr(four)).toBe(
      'len <= 1000 ? tier("a", p*0 + c*0) : (len <= 2000 ? tier("b", p*0 + c*0) : (len <= 3000 ? tier("c", p*0 + c*0) : tier("d", p*0 + c*0)))'
    )
  })
  it('prices a request tier flat and image tokens apart', () => {
    expect(tokenExpr(cfg([tier('base', 1000, { p: 1, c: 2 }), { name: 'bulk', max_len: null, unit: 'request', price: 0.05 }]))).toBe(
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

describe('a condition', () => {
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
        tier('a', 1000, { p: 1, c: null, cr: null, cc1h: null, img: null, price: 9 }),
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
        { name: 'a', max_len: 1000, unit: 'token', p: 1, c: 0 },
        { name: 'b', max_len: null, unit: 'request', price: 0.1 }
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
  it('is one token tier for a set of prices, and only that shape is simple', () => {
    expect(singleTier({ p: 1, c: 2, cr: null, cc: 0 })).toEqual({
      tiers: [{ name: 'base', max_len: null, unit: 'token', p: 1, c: 2, cc: 0 }],
      rules: []
    })
    expect(simpleTier(singleTier({ p: 1, c: 2 }))).toMatchObject({ p: 1 })
    expect(simpleTier(cfg([STANDARD, LONG]))).toBeNull()
    expect(simpleTier(cfg([tier('base', null)], [rule(2, { source: 'header', key: 'x', op: 'exists' })]))).toBeNull()
    expect(simpleTier(cfg([{ name: 'base', max_len: null, unit: 'request', price: 1 }]))).toBeNull()
  })
})

describe('local checks', () => {
  const keys = (c: TokenConfig) => checkTokenConfig(c).map(x => x.key.replace('prices.local.', ''))
  it('pass a whole config', () => {
    expect(keys(cfg([STANDARD, LONG], [rule(2, { source: 'param', key: 'n', op: 'gt', value: 3 })]))).toEqual([])
  })
  it('want tiers with valid, distinct names and rising bounds', () => {
    expect(keys(cfg([]))).toEqual(['noTier'])
    expect(keys(cfg([tier('a b', 10), tier('x', 5), tier('x', null)]))).toEqual(['tierName', 'tierOrder', 'tierDup'])
    expect(keys(cfg([tier('a', null), tier('b', null)]))).toEqual(['tierMax'])
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
    expect(one({ source: 'time', tz: '', from_hour: 23, to_hour: 24 })).toEqual([])
    expect(keys(cfg([tier('a', null)], [rule(-1, { source: 'header', key: 'x', op: 'exists' })]))).toEqual(['ruleField'])
    expect(keys(cfg([tier('a', 100, { p: -1 }), tier('b', null)]))).toEqual(['negative'])
    expect(keys(cfg([tier('a', 100), tier('b', 200)]))).toEqual(['tierRest'])
    expect(keys(cfg([{ name: 'q', max_len: null, unit: 'request', price: null }]))).toEqual(['tierPrice'])
    expect(checkPerRequest(null).map(x => x.key)).toEqual(['prices.local.price'])
    expect(checkPerRequest(-1)).toHaveLength(1)
    expect(checkPerRequest(0)).toEqual([])
  })
})

describe('the summary of a price', () => {
  it('is its first tier, with the number of tiers and rules', () => {
    expect(summarize('per_request', { price: 0.02 })).toEqual({ kind: 'per_request', price: 0.02 })
    expect(summarize('per_token', cfg([LONG, STANDARD], [rule(2, { source: 'header', key: 'x', op: 'exists' })]))).toEqual({
      kind: 'per_token',
      first: STANDARD,
      tiers: 2,
      rules: 1
    })
    expect(summarize('expression', {})).toEqual({ kind: 'expression' })
  })
})
