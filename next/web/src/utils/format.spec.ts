import { describe, expect, it } from 'vitest'
import { formatBalance } from './format'

// A balance is the upstream's number and unit as they are (CONTRACTS §52):
// thousands separators, no trailing zeros, nothing rounded, the unit as text.
describe('formatBalance', () => {
  it.each([
    ['7.500000', 'USD', '7.5 USD'],
    ['3.500000', 'CNY', '3.5 CNY'],
    ['1250000.000000', undefined, '1,250,000'],
    ['1250000.000000', '', '1,250,000'],
    ['0.123457', undefined, '0.123457'],
    ['0.000001', 'USD', '0.000001 USD'],
    ['12.340000', 'usd credits / 额度', '12.34 usd credits / 额度'],
    ['-1.250000', 'CNY', '-1.25 CNY'],
    ['0.000000', undefined, '0'],
    ['999.000000', undefined, '999'],
    ['1000.100000', undefined, '1,000.1'],
    ['9223372036854.775807', undefined, '9,223,372,036,854.775807'],
    ['123456789012.500000', 'CNY', '123,456,789,012.5 CNY']
  ])('%s %s → %s', (amount, unit, want) => {
    expect(formatBalance(amount, unit)).toBe(want)
  })
})
