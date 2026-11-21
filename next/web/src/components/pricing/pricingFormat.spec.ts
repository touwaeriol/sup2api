import { describe, expect, it } from 'vitest'
import type { PriceDef } from '@/api/pricing'
import { applyRule, effectiveRate, hasDiscount, inheritedRate, priceCells, priceExtras, priceText, sameRate, usd, validRate } from './pricingFormat'
import { requestTier, singleTier } from '@/views/prices/priceExpr'

describe('a rate typed in a form', () => {
  it('is a decimal of 0 or more with at most four decimals', () => {
    expect(['0', '1', ' 0.8 ', '1.2345'].every(validRate)).toBe(true)
    expect(['', null, undefined, '-1', '0.', '.5', 'x', '1.23456'].some(validRate)).toBe(false)
  })
  it('stays below 1000000, as the server keeps it', () => {
    expect(validRate('999999.9999')).toBe(true)
    expect(validRate('1000000')).toBe(false)
  })
})

describe('the rate a segment inherits', () => {
  const rate = (group_default: string, discount: string) => inheritedRate({ group_default, discount })

  it('is the default of the group times the discount', () => {
    expect(rate('1.2', '0.9')).toBe(1.08)
    expect(rate('1.2', '1')).toBe(1.2)
  })
  it('keeps four decimals, rounded half up as the server rounds', () => {
    expect(rate('0.3333', '0.3333')).toBe(0.1111)
    expect(rate('0.5125', '0.5')).toBe(0.2563)
    expect(rate('0.0001', '0.4')).toBe(0)
  })
  it('is capped at the largest rate', () => {
    expect(rate('999999', '2')).toBe(999999.9999)
  })
})

describe('the rate helpers every pricing view shares', () => {
  it('compare rates as numbers, and one still being typed only with itself', () => {
    expect(sameRate('0.80', '0.8')).toBe(true)
    expect(sameRate(null, null)).toBe(true)
    expect(sameRate('0.', '0')).toBe(false)
    expect(sameRate('0.8', null)).toBe(false)
  })
  it('tell a discount from none', () => {
    expect(hasDiscount({ discount: '0.9' })).toBe(true)
    expect(hasDiscount({ discount: '1.0' })).toBe(false)
  })
  it('say where the rate comes from', () => {
    expect(effectiveRate({ group_default: '1.2', discount: '0.9' }, null)).toMatchObject({ value: '1.08', source: 'discount' })
    expect(effectiveRate({ group_default: '1.2', discount: '1' }, null)).toMatchObject({ value: '1.2', source: 'group' })
    expect(effectiveRate({ group_default: '1.2', discount: '0.9' }, '0.5')).toMatchObject({ value: '0.5', source: 'segment' })
  })
})

describe('the price a view shows', () => {
  const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.3 }), expression: 'tier("base", p*3 + c*15 + cr*0.3)' }
  const perRequest: PriceDef = { mode: 'visual', config: requestTier(0.02), expression: 'tier("base", flat(0.02))' }
  const tiered: PriceDef = {
    mode: 'visual',
    config: { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] },
    expression: 'len <= 200000 ? tier("a", p*3 + c*15) : tier("b", p*6 + c*22.5)'
  }
  const perCall: PriceDef = { mode: 'visual', config: { tiers: [{ name: 'a', unit: 'request', price: 0.05 }], rules: [] }, expression: '' }
  const opaque: PriceDef = { mode: 'expression', config: null, expression: 'p * 2 + unknown(1)' }
  const times = (def: PriceDef, multiplier: string): PriceDef => ({ ...def, multiplier })

  it('works the multiplier of a rule into the cells of a price by tokens and of one by the request', () => {
    expect(priceCells(times(perToken, '1.5')).tokens).toMatchObject({ p: 4.5, c: 22.5, cr: 0.45, cc: null })
    expect(priceCells(times(perRequest, '1.5'))).toEqual({ perRequest: 0.03, tiers: 1 })
  })
  it('works it into the rest of a tiered price (its last tier), and the price of a request tier', () => {
    expect(priceCells(times(tiered, '2'))).toMatchObject({ tokens: { p: 12, c: 45 }, tiers: 2 })
    expect(priceCells(times(tiered, '2')).multiplier).toBeUndefined()
    expect(priceCells(times(perCall, '2'))).toEqual({ perRequest: 0.1, tiers: 1 })
  })
  it('keeps it beside an expression the console cannot break down', () => {
    expect(priceCells(times(opaque, '1.5'))).toEqual({ tiers: 1, opaque: true, multiplier: '1.5' })
    expect(priceCells(opaque).multiplier).toBeUndefined()
  })
  it('makes a price free at ×0 and leaves it as it is at ×1', () => {
    expect(priceCells(times(perToken, '0')).tokens).toMatchObject({ p: 0, c: 0, cr: 0, cc: null })
    expect(priceCells(times(perRequest, '0')).perRequest).toBe(0)
    // A draft's blank price per request stays blank, as a blank token price does.
    const blank: PriceDef = { mode: 'visual', config: requestTier(null), expression: '' }
    expect(priceCells(blank)).toEqual({ perRequest: null, tiers: 1 })
    expect(priceCells(times(blank, '1.5')).perRequest).toBeNull()
    expect(priceCells(times(perToken, '1'))).toEqual(priceCells(perToken))
  })
  it('rewrites neither the config nor the expression for it', () => {
    const shown = times(tiered, '2')
    priceCells(shown)
    expect(shown).toEqual({ ...tiered, multiplier: '2' })
  })
})

describe('the list price a rule being edited gives', () => {
  const base: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: 'tier("base", p*3 + c*15)' }

  it('is the global price with the multiplier, as the server returns it once saved', () => {
    expect(applyRule(base, { kind: 'multiplier', multiplier: '1.5' })).toEqual({ ...base, multiplier: '1.5' })
    expect(applyRule(base, { kind: 'multiplier', multiplier: '0' })).toEqual({ ...base, multiplier: '0' })
    expect(applyRule(base, { kind: 'multiplier', multiplier: '1' })).toEqual({ ...base, multiplier: '1' })
  })
  it('leaves the price alone while the multiplier is still being typed, and has none without a global price', () => {
    expect(applyRule(base, { kind: 'multiplier', multiplier: '1.' })).toBe(base)
    expect(applyRule(null, { kind: 'multiplier', multiplier: '2' })).toBeNull()
  })
  it('is a price of its own for a custom rule', () => {
    expect(applyRule(base, { kind: 'custom', mode: 'visual', config: singleTier({ p: 1, c: 8 }) })).toEqual({ mode: 'visual', config: singleTier({ p: 1, c: 8 }), expression: '' })
  })
})

describe('what the price views write of a price', () => {
  it('is an amount at eight significant digits, so a small one never reads $0', () => {
    expect([3, 0.3, 0.00001, 1e-7, 0.1 + 0.2, 1.234567891, 1234567.891].map(v => usd(v))).toEqual(['$3', '$0.3', '$0.00001', '$0.0000001', '$0.3', '$1.2345679', '$1234567.9'])
    expect([null, undefined].map(v => usd(v))).toEqual(['—', '—'])
    expect(priceText(0.00001, 3)).toBe('$0.00003')
  })
  it('says what a price has beyond its rest: its tiers when several, the rules of a visual price', () => {
    const t = (key: string, params?: Record<string, unknown>) => `${key.replace('prices.summary.', '')}:${params?.n}`
    const rule = { name: '', multiplier: 2, conds: [{ source: 'header', key: 'x', op: 'exists' }] }
    expect(priceExtras(t, { mode: 'visual', config: singleTier({ p: 1, c: 2 }) })).toBe('')
    const twoTiers = { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 10 }, p: 1 }, { name: 'b', p: 2 }], rules: [rule] }
    expect(priceExtras(t, { mode: 'visual', config: twoTiers })).toBe('tiers:2 · rules:1')
    expect(priceExtras(t, { mode: 'visual', config: { ...requestTier(1), rules: [rule, rule] } })).toBe('rules:2')
    expect(priceExtras(t, { mode: 'expression', config: {} })).toBe('')
  })
})
