import { describe, expect, it } from 'vitest'
import type { Segment } from '@/api/pricing'
import { formChanged, formOf, segmentBody } from './segmentForm'

function segment(over: Partial<Segment> = {}): Segment {
  return { id: 2, kind: 'named', name: 'VIP', description: 'paying', is_default: false, member_count: 3, granted_groups: [], rate_overrides: [], ...over }
}

describe('the form of a segment', () => {
  it('starts empty for a new one', () => {
    expect(formOf(null)).toEqual({ name: '', description: '' })
    expect(formOf(segment())).toEqual({ name: 'VIP', description: 'paying' })
  })
  it('sends all of a new one, trimmed', () => {
    expect(segmentBody({ name: ' VIP ', description: ' paying ' }, null)).toEqual({ name: 'VIP', description: 'paying' })
  })
  it('sends of a saved one only what changed', () => {
    expect(segmentBody({ name: 'VIP ', description: 'paying' }, segment())).toEqual({})
    expect(segmentBody({ name: 'Gold', description: 'paying' }, segment())).toEqual({ name: 'Gold' })
  })
  it('is changed once a field it saves differs, or anything is typed for a new one', () => {
    expect(formChanged(formOf(segment()), segment())).toBe(false)
    expect(formChanged({ ...formOf(segment()), description: 'gold' }, segment())).toBe(true)
    expect(formChanged(formOf(null), null)).toBe(false)
    expect(formChanged({ name: 'x', description: '' }, null)).toBe(true)
  })
})
