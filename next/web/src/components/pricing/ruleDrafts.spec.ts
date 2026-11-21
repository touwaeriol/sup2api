import { describe, expect, it } from 'vitest'
import type { ModelPriceRow, PriceDef, PriceRule } from '@/api/pricing'
import { requestTier, singleTier } from '@/views/prices/priceExpr'
import { customPrices, customRule, draftedRow, shownFields, rulePatches, sameRule, setRuleDraft, startRule, takesCustom, validRule, withRule, type RuleDrafts } from './ruleDrafts'

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6, img: 10 }), expression: '' }
const tiered: PriceDef = { mode: 'visual', config: { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }, expression: '' }
const ruled: PriceDef = { mode: 'visual', config: { ...singleTier({ p: 3, c: 15 }), rules: [{ name: '', multiplier: 2, conds: [{ source: 'header', key: 'x', op: 'exists' }] }] }, expression: '' }
const perCall: PriceDef = { mode: 'visual', config: { tiers: [{ name: 'a', unit: 'request', price: 0.1 }], rules: [] }, expression: '' }
const perRequest: PriceDef = { mode: 'visual', config: requestTier(0.02), expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'tier("base", p*3 + c*15)' }
const times = (multiplier: string): PriceRule => ({ kind: 'multiplier', multiplier })
const custom = (prices: Record<string, number | null>) => customRule('token', prices)

function row(over: Partial<ModelPriceRow> = {}): ModelPriceRow {
  return { model: 'gpt-4o', base: perToken, group_rule: null, list_price: perToken, source: 'global', ...over }
}

describe('the rule a mode starts from', () => {
  it('is ×1, or the global price as a price of its own, priced as its one tier is', () => {
    expect(startRule('multiplier', perToken)).toEqual(times('1'))
    expect(startRule('custom', perToken)).toMatchObject(custom({ p: 3, c: 15, cr: 0.3, cc: 3.75 }))
    expect(startRule('custom', perRequest)).toEqual({ kind: 'custom', mode: 'visual', config: requestTier(0.02) })
  })
  it('is a multiplier for a price that takes no custom price, whatever was asked', () => {
    for (const base of [expression, tiered, ruled]) {
      expect(takesCustom(base)).toBe(false)
      expect(startRule('custom', base)).toEqual(times('1'))
    }
    expect([perToken, perRequest, perCall].every(takesCustom)).toBe(true)
    expect(startRule('custom', perCall)).toEqual(customRule('request', { price: 0.1 }))
  })
  it('is one token tier for a price by tokens, its unset cache prices left out', () => {
    expect(startRule('custom', { ...perToken, config: singleTier({ p: 3, c: 15 }) })).toEqual({
      kind: 'custom',
      mode: 'visual',
      config: { tiers: [{ name: 'base', unit: 'token', p: 3, c: 15 }], rules: [] }
    })
  })
})

describe('a rule that may be saved', () => {
  it('has a rate as its multiplier', () => {
    expect(validRule(null)).toBe(true)
    expect(validRule(times('1.5'))).toBe(true)
    expect(validRule(times(''))).toBe(false)
    expect(validRule(times('-1'))).toBe(false)
    expect(validRule(times('1000000'))).toBe(false)
  })
  it('has its input and output prices, cache prices only when given, none negative', () => {
    expect(validRule(custom({ p: 1, c: 8, cr: null, cc: null }))).toBe(true)
    expect(validRule(custom({ p: 1, c: null, cr: null, cc: null }))).toBe(false)
    expect(validRule(custom({ p: 1, c: 8, cr: -0.1, cc: null }))).toBe(false)
    expect(validRule({ kind: 'custom', mode: 'visual', config: requestTier(null) })).toBe(false)
  })
})

describe('the prices of a custom rule', () => {
  it('read as one row, whatever the rule leaves out', () => {
    expect(customPrices(custom({ p: 1, c: 8 }))).toEqual({ p: 1, c: 8, cr: null, cc: null, cc1h: null, img: null, img_o: null })
  })
  it('keep image prices, shown where the global price prices images apart or the rule does', () => {
    expect(customPrices(startRule('custom', perToken) as ReturnType<typeof custom>)).toMatchObject({ img: 10, img_o: null })
    expect(sameRule(custom({ p: 3, c: 15, img: 10 }), custom({ p: 3, c: 15 }))).toBe(false)
    expect(validRule(custom({ p: 3, c: 15, img_o: -1 }))).toBe(false)
    const plain: PriceDef = { ...perToken, config: singleTier({ p: 3, c: 15 }) }
    expect(shownFields(custom({ p: 1, c: 2 }), perToken)).toEqual(['p', 'c', 'cr', 'cc', 'cc1h', 'img', 'img_o'])
    expect(shownFields(custom({ p: 1, c: 2 }), plain)).toEqual(['p', 'c', 'cr', 'cc', 'cc1h'])
    expect(shownFields(custom({ p: 1, c: 2, img_o: 4 }), plain)).toEqual(['p', 'c', 'cr', 'cc', 'cc1h', 'img', 'img_o'])
    expect(customPrices({ kind: 'custom', mode: 'visual', config: requestTier(0.5) })).toEqual({ price: 0.5 })
  })
})

describe('rules that price alike', () => {
  it('compares multipliers as numbers and custom prices field by field', () => {
    expect(sameRule(null, null)).toBe(true)
    expect(sameRule(times('1.50'), times('1.5'))).toBe(true)
    expect(sameRule(times('1.5'), null)).toBe(false)
    expect(sameRule(times('1'), custom({ p: 3, c: 15 }))).toBe(false)
    expect(sameRule(custom({ p: 3, c: 15 }), custom({ p: 3, c: 15, cr: null, cc: null }))).toBe(true)
    expect(sameRule(custom({ p: 3, c: 15 }), custom({ p: 3, c: 16 }))).toBe(false)
  })
  it('does not take a multiplier still being typed for zero', () => {
    expect(sameRule(times(''), times('0'))).toBe(false)
  })
})

describe('a row under a rule', () => {
  it('takes its list price from the first rule that applies', () => {
    const group = withRule(row(), 'group', times('2'))
    expect(group).toMatchObject({ source: 'group', list_price: { ...perToken, multiplier: '2' } })
    const own = withRule({ ...group, segment_rule: null }, 'segment', custom({ p: 1, c: 8 }))
    expect(own).toMatchObject({ source: 'segment', list_price: { mode: 'visual', config: singleTier({ p: 1, c: 8 }) } })
    expect(withRule(own, 'segment', null)).toMatchObject({ source: 'group', list_price: { ...perToken, multiplier: '2' } })
    expect(withRule(group, 'group', null)).toMatchObject({ source: 'global', list_price: perToken })
  })
  it('skips a rule that is not in effect, and takes no rule without a global price', () => {
    const stale = { ...custom({ p: 1, c: 8 }), inactive: true }
    const skipped = withRule(row({ segment_rule: stale }), 'group', times('2'))
    expect(skipped).toMatchObject({ source: 'group', segment_rule: stale, list_price: { ...perToken, multiplier: '2' } })
    expect(withRule(row({ group_rule: stale }), 'segment', null)).toMatchObject({ source: 'global', list_price: perToken })
    expect(withRule(row({ base: null, group_rule: times('2'), list_price: null }), 'segment', null)).toMatchObject({ source: 'global', list_price: null })
  })
  it('shows its draft and remembers the saved rule', () => {
    const saved = row({ group_rule: times('2'), source: 'group' })
    expect(draftedRow(saved, 'group', new Map())).toMatchObject({ dirty: false, saved: times('2'), group_rule: times('2') })
    expect(draftedRow(saved, 'group', new Map([['gpt-4o', null]]))).toMatchObject({ dirty: true, saved: times('2'), group_rule: null, source: 'global' })
  })
})

describe('the drafts of a table', () => {
  const saved = { model: 'gpt-4o', saved: times('2') }
  const fresh = { model: 'o3', saved: null }

  it('keep only the rules that differ from the saved ones', () => {
    let drafts: RuleDrafts = new Map()
    drafts = setRuleDraft(drafts, saved, times('3'))
    drafts = setRuleDraft(drafts, fresh, custom({ p: 1, c: 8 }))
    expect([...drafts.keys()]).toEqual(['gpt-4o', 'o3'])
    drafts = setRuleDraft(drafts, saved, times('2.0'))
    drafts = setRuleDraft(drafts, fresh, null)
    expect(drafts.size).toBe(0)
  })
  it('become one patch per model, null removing the rule', () => {
    const drafts = setRuleDraft(setRuleDraft(new Map(), saved, null), fresh, times('1.2'))
    expect(rulePatches(drafts, null)).toEqual([
      { segment_id: null, model: 'gpt-4o', rule: null },
      { segment_id: null, model: 'o3', rule: times('1.2') }
    ])
    expect(rulePatches(drafts, 7).map((p) => p.segment_id)).toEqual([7, 7])
    expect(rulePatches(new Map(), 7)).toEqual([])
  })
})
