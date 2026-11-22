import { describe, expect, it } from 'vitest'
import type { CustomPrice, ModelPriceRow, PriceDef } from '@/api/pricing'
import { requestTier, singleTier, type VisualConfig } from '@/views/prices/priceExpr'
import { newPriceDraft } from '@/views/prices/priceDraft'
import { catalogRow, customRuleOf, draftedRow, mergeRowDraft, priceDraftOfRule, pricingPatches, ruleIssues, sameRule, startRule, validRule, type RowDrafts } from './ruleDrafts'

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.3 }), expression: 'tier("base", p*3 + c*15 + cr*0.3)' }
const tieredConfig: VisualConfig = { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }
const tiered: PriceDef = { mode: 'visual', config: tieredConfig, expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'tier("base", p*3 + c*15)' }
const visual = (config: VisualConfig): CustomPrice => ({ kind: 'custom', mode: 'visual', config })
const written = (src: string): CustomPrice => ({ kind: 'custom', mode: 'expression', expression: src })

function row(over: Partial<ModelPriceRow> = {}): ModelPriceRow {
  return { model: 'gpt-4o', offered: true, base: perToken, price: null, rate: null, ...over }
}

describe('the model price a row starts from', () => {
  it('is the original price as a price of its own, whatever its shape', () => {
    expect(startRule(perToken)).toMatchObject({ kind: 'custom', mode: 'visual', config: { tiers: [{ name: 'base', unit: 'token', p: 3, c: 15, cr: 0.3 }], rules: [] } })
    expect(startRule(tiered)).toMatchObject({ kind: 'custom', mode: 'visual', config: { tiers: [{ name: 'a' }, { name: 'b' }] } })
    expect(startRule(expression)).toEqual(written('tier("base", p*3 + c*15)'))
  })
  it('goes back and forth to the price editor', () => {
    const rule = startRule(tiered)
    expect(sameRule(customRuleOf(priceDraftOfRule(rule)), rule)).toBe(true)
    expect(customRuleOf({ ...newPriceDraft(), mode: 'expression', expression: 'p*2' })).toEqual(written('p*2'))
  })
})

describe('a model price that may be saved', () => {
  it('is none, or a custom price the price editor would save: any number of tiers, or an expression', () => {
    expect(validRule(null)).toBe(true)
    expect(validRule(visual(tieredConfig))).toBe(true)
    expect(validRule(visual(requestTier(0.02)))).toBe(true)
    expect(validRule(visual(requestTier(null)))).toBe(false)
    expect(validRule(visual(singleTier({ p: 1, c: -1 })))).toBe(false)
    expect(validRule(written('p * 2'))).toBe(true)
    expect(ruleIssues(written('  '))).toEqual([{ key: 'prices.local.emptyExpr' }])
  })
})

describe('model prices that price alike', () => {
  it('are compared by what they bill: every tier and markup rule, or the expression', () => {
    expect(sameRule(null, null)).toBe(true)
    expect(sameRule(visual(singleTier({ p: 3, c: 15 })), null)).toBe(false)
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
  const own = visual(requestTier(0.1))

  it('shows its draft and remembers the saved row', () => {
    const saved = row({ price: own, rate: '1.2' })
    expect(draftedRow(saved)).toMatchObject({ dirty: false, invalid: false, saved, price: own, rate: '1.2', list_price: { mode: 'visual', config: requestTier(0.1) } })
    expect(draftedRow(saved, { price: null })).toMatchObject({ dirty: true, price: null, rate: '1.2', list_price: perToken })
    expect(draftedRow(row(), { rate: '0.8' })).toMatchObject({ dirty: true, price: null, rate: '0.8', list_price: perToken })
  })
  it('cannot be saved while its price or its rate is not filled in right', () => {
    expect(draftedRow(row(), { rate: '0.' }).invalid).toBe(true)
    expect(draftedRow(row(), { price: visual(requestTier(null)) }).invalid).toBe(true)
    expect(draftedRow(row(), { rate: null }).invalid).toBe(false)
  })
  it('keeps the price without an original price, which prices nothing', () => {
    expect(draftedRow(row({ base: null }), { price: own })).toMatchObject({ price: own, list_price: null })
    expect(draftedRow(row({ base: null })).list_price).toBeNull()
  })
  it('of a model the server does not list yet is its original price, with nothing set', () => {
    expect(catalogRow('o3', perToken)).toEqual({ model: 'o3', offered: false, base: perToken, price: null, rate: null })
  })
})

describe('the drafts of a table', () => {
  const saved = draftedRow(row({ rate: '2' }))
  const fresh = draftedRow(row({ model: 'o3' }))

  it('keep only the fields that differ from the saved row, and no row without one', () => {
    let drafts: RowDrafts = new Map()
    drafts = mergeRowDraft(drafts, saved, { rate: '3' })
    drafts = mergeRowDraft(drafts, fresh, { price: written('p*2') })
    drafts = mergeRowDraft(drafts, fresh, { rate: '0.5' })
    expect([...drafts]).toEqual([['gpt-4o', { rate: '3' }], ['o3', { price: written('p*2'), rate: '0.5' }]])
    drafts = mergeRowDraft(drafts, saved, { rate: '2.0' })
    drafts = mergeRowDraft(drafts, fresh, { price: null, rate: null })
    expect(drafts.size).toBe(0)
  })
  it('become the changed model prices and model rates, null removing one, a custom price by its config or its expression', () => {
    let drafts = mergeRowDraft(new Map(), saved, { rate: null })
    drafts = mergeRowDraft(drafts, fresh, { price: { ...visual(requestTier(0.1)), expression: 'tier("base", flat(0.1))' }, rate: '1.2' })
    expect(pricingPatches(drafts)).toEqual({
      model_prices: [{ model: 'o3', rule: visual(requestTier(0.1)) }],
      model_rates: [{ model: 'gpt-4o', rate: null }, { model: 'o3', rate: '1.2' }]
    })
    expect(pricingPatches(new Map())).toEqual({})
  })
})
