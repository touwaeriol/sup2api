import { describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import type { PriceDef } from '@/api/pricing'
import { paidLabel, priceLabel } from './priceLabels'
import { requestTier, singleTier } from '@/views/prices/priceExpr'

const t = i18n.global.t
i18n.global.locale.value = 'en'

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: 'tier("base", p*3 + c*15)' }
const perRequest: PriceDef = { mode: 'visual', config: requestTier(0.02), expression: 'flat(0.02)' }
const opaque: PriceDef = { mode: 'expression', config: null, expression: 'p * 2 + unknown(1)' }

describe('a price in a table cell', () => {
  it('reads a rate into its numbers', () => {
    expect(priceLabel(t, perToken, 1.5)).toBe('$4.5 / $22.5')
    expect(priceLabel(t, perRequest, 1.5)).toBe('$0.03 per request')
    expect(priceLabel(t, perToken, 0)).toBe('$0 / $0')
    expect(priceLabel(t, perToken, 1)).toBe(priceLabel(t, perToken))
  })
  it('names the rate after an expression it cannot break down', () => {
    expect(priceLabel(t, opaque)).toBe('Billed by expression')
    expect(priceLabel(t, opaque, 1.5)).toBe('Billed by expression ×1.5')
    expect(priceLabel(t, opaque, 0)).toBe('Billed by expression ×0')
  })
  it('leaves a blank price of a draft blank, per request as per token', () => {
    expect(priceLabel(t, { mode: 'visual', config: requestTier(null), expression: '' })).toBe('— per request')
    expect(priceLabel(t, { mode: 'visual', config: singleTier({ p: null, c: 15 }), expression: '' })).toBe('— / $15')
  })
  it('says that there is none', () => {
    expect(priceLabel(t, null)).toBe('No price')
  })
})

describe('what is paid', () => {
  it('is the list price times the rate, none without a price, and unknown while the rate is typed', () => {
    expect(paidLabel(t, perToken, '2')).toBe('$6 / $30')
    expect(paidLabel(t, null, '2')).toBe('No price')
    expect(paidLabel(t, perToken, '1.')).toBe('—')
  })
})
