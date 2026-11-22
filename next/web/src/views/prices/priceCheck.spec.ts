import { describe, expect, it } from 'vitest'
import { checkPrice, checkVisualConfig } from './priceCheck'
import { requestTier, type MarkupRule, type RuleCond, type TierCond, type VisualConfig, type VisualTier } from './priceExpr'

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
const keys = (c: VisualConfig) => checkVisualConfig(c).map(x => x.key.replace('prices.local.', ''))

const STANDARD = tier('standard', 200000, { p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 })
const LONG = tier('long_context', null, { p: 6, c: 22.5, cr: 0.6, cc: 7.5, cc1h: 12 })

describe('the local checks of tiers', () => {
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
})

describe('the local checks of a tier condition', () => {
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
})

describe('the local checks of rules', () => {
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
})

describe('the local checks, as strict as the server', () => {
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
})

describe('the local checks of a price as it is sent', () => {
  it('check a price as it is sent, by its mode', () => {
    const keys = (p: Parameters<typeof checkPrice>[0]) => checkPrice(p).map(x => x.key.replace('prices.local.', ''))
    expect(keys({ mode: 'expression', config: {}, expression: ' \n' })).toEqual(['emptyExpr'])
    expect(keys({ mode: 'expression', config: {}, expression: 'p*3' })).toEqual([])
    expect(
      keys({
        mode: 'visual',
        config: { ...requestTier(null), rules: [rule(-1, { source: 'header', key: 'x', op: 'exists' })] },
        expression: ''
      })
    ).toEqual(['tierPrice', 'ruleField'])
    expect(keys({ mode: 'visual', config: cfg([]), expression: '' })).toEqual(['noTier'])
  })
})
