import { describe, expect, it } from 'vitest'
import type { CheckoutInfo } from '../topup'
import { roundTo } from '../rounding'
import { quoteOf } from './useTopupQuote'

const info = (over: Partial<CheckoutInfo> = {}): CheckoutInfo => ({
  enabled: true,
  currency: 'CNY',
  currency_exponent: 2,
  price: '7.3',
  min_topup: '1',
  max_topup: '1000',
  amount_options: [],
  discounts: [{ amount: '100', rate: '0.95' }],
  methods: [],
  ...over
})

describe('top-up estimate', () => {
  it('rounds what to pay once, to the currency decimals', () => {
    expect(quoteOf(100, info())).toEqual({ amount: 100, rate: 0.95, original: 730, pay: 693.5 })
    // 2.006 × 0.5 = 1.003 → 1.00; rounding the 2.006 to 2.01 first would make it 1.01.
    expect(quoteOf(1, info({ price: '2.006', discounts: [{ amount: '1', rate: '0.5' }] }))).toMatchObject({ original: 2.01, pay: 1 })
  })

  it('takes the decimals the server gives (JPY has none)', () => {
    expect(quoteOf(10, info({ currency: 'JPY', currency_exponent: 0, price: '149.55', discounts: [] }))).toMatchObject({
      original: 1496,
      pay: 1496
    })
  })

  it('rounds half away from zero without float noise', () => {
    expect(roundTo(2.675, 2)).toBe(2.68)
    expect(roundTo(1.005, 2)).toBe(1.01)
    expect(roundTo(-0.125, 2)).toBe(-0.13)
  })
})
