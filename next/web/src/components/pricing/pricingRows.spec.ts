import { describe, expect, it } from 'vitest'
import type { CustomPrice, ModelPriceRow, PriceDef } from '@/api/pricing'
import { requestTier, singleTier, type VisualConfig } from '@/views/prices/priceExpr'
import { newPriceDraft } from '@/views/prices/priceDraft'
import { catalogRow, customPriceOf, draftedRow, mergeRowDraft, groupModelRatePatches, modelPricePatches, priceDraftOfCustom, priceIssues, samePrice, savedPrice, startDraft, validPrice, type RowDrafts } from './pricingRows'

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.3 }), expression: 'tier("base", p*3 + c*15 + cr*0.3)' }
const tieredConfig: VisualConfig = { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }
const tiered: PriceDef = { mode: 'visual', config: tieredConfig, expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'tier("base", p*3 + c*15)' }
const visual = (config: VisualConfig): CustomPrice => ({ mode: 'visual', config })
const written = (src: string): CustomPrice => ({ mode: 'expression', expression: src })

function row(over: Partial<ModelPriceRow> = {}): ModelPriceRow {
  return { model: 'gpt-4o', offered: true, base: perToken, price: null, rate: null, ...over }
}

const startPrice = (base: PriceDef | null) => customPriceOf(startDraft(base))

describe('the model price a row starts from', () => {
  it('is the original price as a price of its own, whatever its shape', () => {
    expect(startPrice(perToken)).toMatchObject({ mode: 'visual', config: { tiers: [{ name: 'base', unit: 'token', p: 3, c: 15, cr: 0.3 }], rules: [] } })
    expect(startPrice(tiered)).toMatchObject({ mode: 'visual', config: { tiers: [{ name: 'a' }, { name: 'b' }] } })
    expect(startPrice(expression)).toEqual(written('tier("base", p*3 + c*15)'))
  })
  it('is blank without an original price: one token tier, its prices not typed yet', () => {
    expect(startDraft(null)).toEqual({ mode: 'visual', config: { tiers: [{ name: 'base', unit: 'token', p: null, c: null }], rules: [] }, expression: '' })
  })
  it('goes back and forth to the price editor', () => {
    const rule = startPrice(tiered)
    expect(samePrice(customPriceOf(priceDraftOfCustom(rule)), rule)).toBe(true)
    expect(customPriceOf({ ...newPriceDraft(), mode: 'expression', expression: 'p*2' })).toEqual(written('p*2'))
  })
})

describe('a model price that may be saved', () => {
  it('is none, or a custom price the price editor would save: any number of tiers, or an expression', () => {
    expect(validPrice(null)).toBe(true)
    expect(validPrice(visual(tieredConfig))).toBe(true)
    expect(validPrice(visual(requestTier(0.02)))).toBe(true)
    expect(validPrice(visual(requestTier(null)))).toBe(false)
    expect(validPrice(visual(singleTier({ p: 1, c: -1 })))).toBe(false)
    expect(validPrice(written('p * 2'))).toBe(true)
    expect(priceIssues(written('  '))).toEqual([{ key: 'prices.local.emptyExpr' }])
  })
})

describe('a saved model price', () => {
  // As the server and older consoles store them: p and c absent read 0, other prices absent are not charged.
  const stored = visual({ tiers: [{ name: 'short', when: { var: 'len', op: '<=', value: 1000 }, p: 3 }, { name: 'rest', unit: 'token', c: 0, img: 2 }], rules: [] })

  it('reads as the server bills it: valid, and the same price once its editor sends it back', () => {
    const read = savedPrice(stored)!
    expect(read.config?.tiers).toMatchObject([{ name: 'short', unit: 'token', p: 3, c: 0 }, { name: 'rest', unit: 'token', p: 0, c: 0, img: 2 }])
    expect(validPrice(read)).toBe(true)
    expect(samePrice(customPriceOf(priceDraftOfCustom(read)), read)).toBe(true)
    expect(savedPrice(null)).toBeNull()
    expect(savedPrice(written('p*2'))).toEqual(written('p*2'))
  })
  it('keeps a blank of a draft blank when its editor opens it again', () => {
    const draft = customPriceOf(startDraft(null))
    expect(priceDraftOfCustom(draft).config.tiers[0]).toMatchObject({ p: null, c: null })
    expect(validPrice(draft)).toBe(false)
  })
  it('starts a price of its own from an original price stored without c, valid', () => {
    expect(validPrice(startPrice({ mode: 'visual', config: { tiers: [{ name: 'base', p: 1 }], rules: [] }, expression: '' }))).toBe(true)
  })
})

describe('model prices that price alike', () => {
  it('are compared by what they bill: every tier and markup rule, or the expression', () => {
    expect(samePrice(null, null)).toBe(true)
    expect(samePrice(visual(singleTier({ p: 3, c: 15 })), null)).toBe(false)
    expect(samePrice(visual(singleTier({ p: 3, c: 15 })), visual(singleTier({ p: 3, c: 15, cr: null })))).toBe(true)
    // A price left blank is not 0.
    expect(samePrice(visual(singleTier({ p: 3, c: null })), visual(singleTier({ p: 3, c: 0 })))).toBe(false)
    expect(samePrice(visual(requestTier(null)), visual(requestTier(0)))).toBe(false)
    expect(samePrice(visual(tieredConfig), visual({ ...tieredConfig, tiers: [tieredConfig.tiers[0], { name: 'b', p: 6, c: 30 }] }))).toBe(false)
    const ruled: VisualConfig = { ...tieredConfig, rules: [{ name: 'hd', multiplier: 2, conds: [{ source: 'header', key: 'x', op: 'exists' }] }] }
    expect(samePrice(visual(tieredConfig), visual(ruled))).toBe(false)
    // The server returns the expression a visual price generates; it changes nothing.
    expect(samePrice(visual(tieredConfig), { ...visual(tieredConfig), expression: 'whatever' })).toBe(true)
    expect(samePrice(written('p*2 '), written('p*2'))).toBe(true)
    expect(samePrice(written('p*2'), written('p*3'))).toBe(false)
    expect(samePrice(written('tier("base", p*3 + c*15)'), visual(singleTier({ p: 3, c: 15 })))).toBe(false)
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
  it('prices a model without an original price by its model price alone, and not at all without one', () => {
    expect(draftedRow(row({ base: null }), { price: own })).toMatchObject({ price: own, list_price: { mode: 'visual', config: requestTier(0.1) } })
    expect(draftedRow(row({ base: null, rate: '2' })).list_price).toBeNull()
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
    expect(modelPricePatches(drafts)).toEqual([{ model: 'o3', rule: visual(requestTier(0.1)) }])
    expect(groupModelRatePatches(drafts)).toEqual([{ model: 'gpt-4o', rate: null }, { model: 'o3', rate: '1.2' }])
    expect(modelPricePatches(new Map())).toEqual([])
  })
})
