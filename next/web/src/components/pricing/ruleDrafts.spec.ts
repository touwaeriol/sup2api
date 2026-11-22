import { describe, expect, it } from 'vitest'
import type { ModelPriceRow, PriceDef, PriceRule } from '@/api/pricing'
import { requestTier, singleTier, type VisualConfig } from '@/views/prices/priceExpr'
import { newPriceDraft } from '@/views/prices/priceDraft'
import { catalogRow, customRuleOf, draftedRow, priceDraftOfRule, ruleIssues, rulePatches, sameRule, setRuleDraft, startRule, validRule, type CustomRule, type RuleDrafts } from './ruleDrafts'

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.3 }), expression: 'tier("base", p*3 + c*15 + cr*0.3)' }
const tieredConfig: VisualConfig = { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }
const tiered: PriceDef = { mode: 'visual', config: tieredConfig, expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'tier("base", p*3 + c*15)' }
const times = (multiplier: string): PriceRule => ({ kind: 'multiplier', multiplier })
const visual = (config: VisualConfig): CustomRule => ({ kind: 'custom', mode: 'visual', config })
const written = (src: string): CustomRule => ({ kind: 'custom', mode: 'expression', expression: src })

function row(over: Partial<ModelPriceRow> = {}): ModelPriceRow {
  return { model: 'gpt-4o', offered: true, base: perToken, group_rule: null, list_price: perToken, source: 'global', ...over }
}

describe('the rule a mode starts from', () => {
  it('is ×1, or the original price as a price of its own, whatever the original price is', () => {
    expect(startRule('multiplier', perToken)).toEqual(times('1'))
    expect(startRule('custom', perToken)).toMatchObject({ kind: 'custom', mode: 'visual', config: { tiers: [{ name: 'base', unit: 'token', p: 3, c: 15, cr: 0.3 }], rules: [] } })
    expect(startRule('custom', tiered)).toMatchObject({ kind: 'custom', mode: 'visual', config: { tiers: [{ name: 'a' }, { name: 'b' }] } })
    expect(startRule('custom', expression)).toEqual(written('tier("base", p*3 + c*15)'))
  })
  it('goes back and forth to the price editor', () => {
    const rule = startRule('custom', tiered) as CustomRule
    expect(sameRule(customRuleOf(priceDraftOfRule(rule)), rule)).toBe(true)
    expect(customRuleOf({ ...newPriceDraft(), mode: 'expression', expression: 'p*2' })).toEqual(written('p*2'))
  })
})

describe('a rule that may be saved', () => {
  it('has a rate as its multiplier', () => {
    expect(validRule(null)).toBe(true)
    expect(validRule(times('1.5'))).toBe(true)
    for (const bad of ['', '-1', '1000000']) expect(validRule(times(bad))).toBe(false)
    expect(ruleIssues(times('x'))).toEqual([{ key: 'pricing.rate.invalid' }])
  })
  it('is a custom price the price editor would save: any number of tiers, or an expression', () => {
    expect(validRule(visual(tieredConfig))).toBe(true)
    expect(validRule(visual(requestTier(0.02)))).toBe(true)
    expect(validRule(visual(requestTier(null)))).toBe(false)
    expect(validRule(visual(singleTier({ p: 1, c: -1 })))).toBe(false)
    expect(validRule(written('p * 2'))).toBe(true)
    expect(ruleIssues(written('  '))).toEqual([{ key: 'prices.local.emptyExpr' }])
  })
})

describe('rules that price alike', () => {
  it('compares multipliers as numbers', () => {
    expect(sameRule(null, null)).toBe(true)
    expect(sameRule(times('1.50'), times('1.5'))).toBe(true)
    expect(sameRule(times('1.5'), null)).toBe(false)
    expect(sameRule(times(''), times('0'))).toBe(false)
  })
  it('compares custom prices by what they bill: every tier and markup rule, or the expression', () => {
    expect(sameRule(times('1'), visual(singleTier({ p: 3, c: 15 })))).toBe(false)
    expect(sameRule(visual(singleTier({ p: 3, c: 15 })), visual(singleTier({ p: 3, c: 15, cr: null })))).toBe(true)
    expect(sameRule(visual(tieredConfig), visual({ ...tieredConfig, tiers: [tieredConfig.tiers[0], { name: 'b', p: 6, c: 30 }] }))).toBe(false)
    const ruled: VisualConfig = { ...tieredConfig, rules: [{ name: 'hd', multiplier: 2, conds: [{ source: 'header', key: 'x', op: 'exists' }] }] }
    expect(sameRule(visual(tieredConfig), visual(ruled))).toBe(false)
    // The server returns the expression a visual price generates; it changes nothing.
    expect(sameRule(visual(tieredConfig), { ...visual(tieredConfig), expression: 'whatever' })).toBe(true)
    expect(sameRule(written('p*2 '), written('p*2'))).toBe(true)
    expect(sameRule(written('p*2'), written('p*3'))).toBe(false)
    expect(sameRule(written('tier("base", p*3 + c*15)'), visual(singleTier({ p: 3, c: 15 })))).toBe(false)
  })
})

describe('a row under its draft', () => {
  it('shows its draft and remembers the saved rule', () => {
    const saved = row({ group_rule: times('2'), list_price: { ...perToken, multiplier: '2' }, source: 'group' })
    expect(draftedRow(saved, new Map())).toMatchObject({ dirty: false, saved: times('2'), group_rule: times('2') })
    expect(draftedRow(saved, new Map([['gpt-4o', null]]))).toMatchObject({ dirty: true, saved: times('2'), group_rule: null, source: 'global', list_price: perToken })
    expect(draftedRow(row(), new Map([['gpt-4o', times('0.8')]]))).toMatchObject({ source: 'group', list_price: { ...perToken, multiplier: '0.8' } })
    expect(draftedRow(row(), new Map([['gpt-4o', visual(requestTier(0.1))]]))).toMatchObject({ source: 'group', list_price: { mode: 'visual', config: requestTier(0.1) } })
  })
  it('keeps the rule without an original price, which prices nothing', () => {
    expect(draftedRow(row({ base: null, list_price: null }), new Map([['gpt-4o', times('2')]]))).toMatchObject({ group_rule: times('2'), source: 'global', list_price: null })
  })
  it('of a model the server does not list yet is its original price', () => {
    expect(catalogRow('o3', perToken)).toEqual({ model: 'o3', offered: false, base: perToken, group_rule: null, list_price: perToken, source: 'global' })
  })
})

describe('the drafts of a table', () => {
  const saved = { model: 'gpt-4o', saved: times('2') }
  const fresh = { model: 'o3', saved: null }

  it('keep only the rules that differ from the saved ones', () => {
    let drafts: RuleDrafts = new Map()
    drafts = setRuleDraft(drafts, saved, times('3'))
    drafts = setRuleDraft(drafts, fresh, written('p*2'))
    expect([...drafts.keys()]).toEqual(['gpt-4o', 'o3'])
    drafts = setRuleDraft(drafts, saved, times('2.0'))
    drafts = setRuleDraft(drafts, fresh, null)
    expect(drafts.size).toBe(0)
  })
  it('become one patch per model, null removing the rule, a custom price by its config or its expression', () => {
    let drafts = setRuleDraft(setRuleDraft(new Map(), saved, null), fresh, times('1.2'))
    drafts = setRuleDraft(drafts, { model: 'a', saved: null }, { ...visual(requestTier(0.1)), expression: 'tier("base", flat(0.1))' })
    drafts = setRuleDraft(drafts, { model: 'b', saved: null }, written('p*2'))
    expect(rulePatches(drafts)).toEqual([
      { model: 'gpt-4o', rule: null },
      { model: 'o3', rule: times('1.2') },
      { model: 'a', rule: visual(requestTier(0.1)) },
      { model: 'b', rule: written('p*2') }
    ])
    expect(rulePatches(new Map())).toEqual([])
  })
})
