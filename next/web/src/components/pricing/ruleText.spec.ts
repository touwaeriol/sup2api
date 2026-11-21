import { describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import type { PriceDef } from '@/api/pricing'
import { priceLabel, ruleText } from './ruleText'
import { requestTier, singleTier } from '@/views/prices/priceExpr'

const t = i18n.global.t
i18n.global.locale.value = 'en'

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: 'tier("base", p*3 + c*15)' }
const perRequest: PriceDef = { mode: 'visual', config: requestTier(0.02), expression: 'flat(0.02)' }
const opaque: PriceDef = { mode: 'expression', config: null, expression: 'p * 2 + unknown(1)' }

describe('a price in a table cell', () => {
  it('reads the multiplier of a rule in its numbers', () => {
    expect(priceLabel(t, { ...perToken, multiplier: '1.5' })).toBe('$4.5 / $22.5')
    expect(priceLabel(t, { ...perRequest, multiplier: '1.5' })).toBe('$0.03 per request')
    expect(priceLabel(t, { ...perToken, multiplier: '0' })).toBe('$0 / $0')
    expect(priceLabel(t, { ...perToken, multiplier: '1' })).toBe(priceLabel(t, perToken))
  })
  it('names the multiplier after an expression it cannot break down', () => {
    expect(priceLabel(t, opaque)).toBe('Billed by expression')
    expect(priceLabel(t, { ...opaque, multiplier: '1.5' })).toBe('Billed by expression ×1.5')
    expect(priceLabel(t, { ...opaque, multiplier: '0' })).toBe('Billed by expression ×0')
  })
  it('leaves a blank price of a custom rule blank, per request as per token', () => {
    expect(priceLabel(t, { mode: 'visual', config: requestTier(null), expression: '' })).toBe('— per request')
    expect(priceLabel(t, { mode: 'visual', config: singleTier({ p: null, c: 15 }), expression: '' })).toBe('— / $15')
  })
  it('says that there is none', () => {
    expect(priceLabel(t, null)).toBe('No price')
  })
})

describe('what kind of group price a rule is', () => {
  it('is the multiplier of the original price, or custom', () => {
    expect(ruleText(t, { kind: 'multiplier', multiplier: '0.80' })).toBe('Original × 0.8')
    expect(ruleText(t, { kind: 'multiplier', multiplier: '1.' })).toBe('Original × —')
    expect(ruleText(t, { kind: 'custom', mode: 'expression', expression: 'p*2' })).toBe('Custom')
    expect(ruleText(t, null)).toBe('')
  })
})
