import { describe, expect, it } from 'vitest'
import type { Segment } from '@/api/pricing'
import { hasDiscount } from './pricingFormat'
import { formChanged, formOf, segmentBody, validDiscount } from './segmentForm'

function segment(over: Partial<Segment> = {}): Segment {
  return { id: 2, kind: 'named', name: 'VIP', description: 'paying', discount: '0.9', is_default: false, member_count: 3, granted_groups: [], rate_overrides: [], ...over }
}

describe('the form of a segment', () => {
  it('starts without a discount for a new one', () => {
    expect(formOf(null)).toEqual({ name: '', description: '', discount: '1' })
    expect(formOf(segment())).toEqual({ name: 'VIP', description: 'paying', discount: '0.9' })
  })
  it('takes a rate above zero as the discount', () => {
    expect(['1', '0.9', '1.25'].every(validDiscount)).toBe(true)
    expect(['', '0', '-1', '0.', 'x', '1000000', '0.12345'].some(validDiscount)).toBe(false)
    expect(hasDiscount(segment())).toBe(true)
    expect(hasDiscount(segment({ discount: '1.0' }))).toBe(false)
  })
  it('sends a personal segment its discount only', () => {
    const form = { name: ' VIP ', description: ' paying ', discount: ' 0.8 ' }
    expect(segmentBody(form, false)).toEqual({ name: 'VIP', description: 'paying', discount: '0.8' })
    expect(segmentBody(form, true)).toEqual({ discount: '0.8' })
  })
  it('is changed once a field it saves differs', () => {
    const personal = segment({ kind: 'personal', name: 'a@x.io' })
    expect(formChanged(formOf(segment()), segment())).toBe(false)
    expect(formChanged({ ...formOf(segment()), description: 'gold' }, segment())).toBe(true)
    expect(formChanged({ ...formOf(personal), name: 'ignored' }, personal)).toBe(false)
    expect(formChanged({ ...formOf(personal), discount: '0.5' }, personal)).toBe(true)
  })
})
