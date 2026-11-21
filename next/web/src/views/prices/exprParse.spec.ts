import { describe, expect, it } from 'vitest'
import golden from '../../../../server/internal/billing/expr/testdata/generate_golden.json'
import { visualOf } from './exprParse'
import { EXPR_EXAMPLES, PRESETS, RULE_PRESETS } from './presets'
import { priceExpression, requestTier, tokenExpr, type VisualConfig } from './priceExpr'

const NIGHT: VisualConfig = {
  tiers: [
    {
      name: 'long_night',
      when: {
        and: [
          { var: 'len', op: '>', value: 200000 },
          { or: [{ var: 'hour', tz: 'Asia/Shanghai', op: '>=', value: 22 }, { not: { var: 'weekday', tz: 'UTC', op: '!=', value: 6 } }] }
        ]
      },
      unit: 'token',
      p: 4,
      c: 18,
      cr: 0.4
    },
    { name: 'mid', when: { or: [{ var: 'p', op: '<', value: 0.5 }, { var: 'c', op: '>=', value: -1 }] }, unit: 'request', price: 0.02 },
    { name: 'base', unit: 'token', p: 3, c: 15, img: 10, img_o: 40 }
  ],
  rules: Object.values(RULE_PRESETS).map(make => make())
}

/** A config's expression, generated, read back and generated again. */
const roundTrip = (cfg: VisualConfig) => {
  const back = visualOf(tokenExpr(cfg))
  return back && tokenExpr(back)
}

describe('an expression the visual editor generated', () => {
  it.each(golden.filter(c => c.mode === 'visual').map(c => [c.name, c] as const))('reads back to the same expression: %s', (_, c) => {
    const back = visualOf(c.expression)
    expect(back).not.toBeNull()
    expect(tokenExpr(back!)).toBe(c.expression)
  })
  it('reads back for every template, the rule groups and a tree of every kind of node', () => {
    for (const make of Object.values(PRESETS)) expect(roundTrip({ tiers: make(), rules: [] })).toBe(tokenExpr({ tiers: make(), rules: [] }))
    expect(roundTrip(NIGHT)).toBe(tokenExpr(NIGHT))
    expect(roundTrip({ ...requestTier(0.04), rules: [{ name: '', multiplier: 2, conds: [{ source: 'param', key: 'q', op: 'eq', value: '3' }] }] })).toBe(
      '(tier("base", flat(0.04))) * (param("q") == "3" ? 2 : 1)'
    )
  })
  it('reads back every kind of rule condition', () => {
    const conds: VisualConfig['rules'][number]['conds'] = [
      { source: 'param', key: 'a.b', op: 'gte', value: 1.5 },
      { source: 'param', key: 'flag', op: 'ne', value: true },
      { source: 'param', key: 'p', op: 'exists' },
      { source: 'header', key: 'x-k', op: 'eq', value: 'say "hi"\n' },
      { source: 'header', key: 'x-k', op: 'exists' },
      { source: 'usage', key: 'n', op: 'lt', value: 3 },
      { source: 'usage', key: 'n', op: 'contains', value: '中文' },
      { source: 'time', tz: 'UTC', from_hour: 9, to_hour: 18 },
      { source: 'time', tz: 'Asia/Shanghai', from_hour: 22, to_hour: 6 }
    ]
    const cfg: VisualConfig = { tiers: [{ name: 'base', unit: 'token', p: 1, c: 2 }], rules: [{ name: 'r', multiplier: 0.5, conds }] }
    expect(roundTrip(cfg)).toBe(tokenExpr(cfg))
  })
  it('reads back with its whitespace changed outside string literals', () => {
    const src = tokenExpr(NIGHT)
    const spaced = src.replace(/ \? /g, '\n  ? ').replace(/ : /g, '\n  : ').replace(/ && /g, '\t&&\t').replace(/\(/g, '( ')
    expect(spaced).not.toBe(src)
    expect(tokenExpr(visualOf(`\n ${spaced} \n`)!)).toBe(src)
    expect(visualOf('tier( "base" , p * 3+c*15 )')).toEqual({ tiers: [{ name: 'base', unit: 'token', p: 3, c: 15 }], rules: [] })
  })
})

describe('an expression written or changed by hand', () => {
  const two = priceExpression('visual', { tiers: PRESETS.claudeLong(), rules: [] })
  it.each([
    ['blank', ''],
    ['a guide example', EXPR_EXAMPLES.priority],
    ['another function', 'tier("base", max(p*3 + c*15, flat(0.001)))'],
    ['the prices reordered', 'tier("base", c*15 + p*3)'],
    ['a price written the other way round', 'tier("base", 3*p + c*15)'],
    ['without the price of c', 'tier("base", p*3)'],
    ['a parenthesis added', two.replace('len <= 200000', '(len <= 200000)')],
    ['the tiers chained without parentheses', 'len <= 1 ? tier("a", p*1 + c*1) : len <= 2 ? tier("b", p*1 + c*1) : tier("c", p*1 + c*1)'],
    ['the comparison turned round', two.replace('len <= 200000', '200000 >= len')],
    ['whitespace changed inside a string', two.replace('"standard"', '"standard "')],
    ['a number written otherwise', two.replace('200000', '2e5')],
    ['a version prefix', `v1: ${two}`],
    ['an unknown variable', 'tier("base", p*3 + c*15 + ai*2)'],
    ['a rule of an unknown shape', '(tier("base", p*1 + c*1)) * (param("x") == "y" ? 2 : 0.5)'],
    ['a condition that is not visual', 'param("x") == "y" ? tier("a", p*1 + c*1) : tier("b", p*1 + c*1)'],
    ['a zone Go does not load', 'hour("Mars/Base") >= 1 ? tier("a", p*1 + c*1) : tier("b", p*1 + c*1)'],
    ['a trailing operator', `${two} +`]
  ])('is not visual: %s', (_, src) => {
    expect(visualOf(src)).toBeNull()
  })
})

