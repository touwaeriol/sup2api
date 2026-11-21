import { describe, expect, it } from 'vitest'
import type { Group } from '@/api/types'
import type { Segment, SegmentGroupSetting } from '@/api/pricing'
import { blankGroupSetting, blankSetting, draftedSetting, mergeSettingDraft, settingPatches, validOverride, type SettingDraft, type SettingDrafts } from './accessRateDrafts'
import { effectiveRate } from './pricingFormat'

function setting(group: number, over: Partial<SegmentGroupSetting> = {}, visibility: 'public' | 'restricted' = 'public'): SegmentGroupSetting {
  return {
    segment: { id: 2, kind: 'named', name: 'VIP' },
    group: { id: group, name: `g${group}`, description: '', status: 'active', visibility, rate_multiplier: '1.2' },
    accessible: visibility === 'public',
    granted: false,
    rate_override: null,
    rate: { value: '1.08', source: 'discount', group_default: '1.2', discount: '0.9' },
    ...over
  }
}
const none: SettingDrafts = new Map()
/** The drafts after `change` on the row of `saved` among the groups of a segment. */
const drafted = (saved: SegmentGroupSetting, change: SettingDraft, drafts = none) => mergeSettingDraft(drafts, draftedSetting('group', saved, drafts.get(saved.group.id)), change)

describe('the rate that applies', () => {
  it('is the override, else the default times the discount, else the default', () => {
    expect(effectiveRate({ group_default: '1.2', discount: '0.9' }, '0.8')).toMatchObject({ value: '0.8', source: 'segment' })
    expect(effectiveRate({ group_default: '1.2', discount: '0.9' }, null)).toMatchObject({ value: '1.08', source: 'discount' })
    expect(effectiveRate({ group_default: '1.2', discount: '1' }, null)).toMatchObject({ value: '1.2', source: 'group' })
  })
  it('has no value while the override is not a rate', () => {
    expect(effectiveRate({ group_default: '1.2', discount: '0.9' }, '0.')).toMatchObject({ value: '', source: 'segment' })
    expect(validOverride('0.')).toBe(false)
    expect(validOverride(null)).toBe(true)
  })
})

describe('a row under its draft', () => {
  it('reads as saved without one, keyed by the end its editor lists', () => {
    const saved = setting(7)
    expect(draftedSetting('group', saved)).toMatchObject({ id: 7, granted: false, rate_override: null, accessible: true, dirty: false, saved })
    expect(draftedSetting('segment', saved).id).toBe(2)
  })
  it('follows the grant and the rate of the draft', () => {
    const row = draftedSetting('group', setting(2, {}, 'restricted'), { granted: true, rate_override: '0.5' })
    expect(row).toMatchObject({ granted: true, accessible: true, rate_override: '0.5', dirty: true })
    expect(row.rate).toMatchObject({ value: '0.5', source: 'segment' })
  })
  it('goes back to the inherited rate when the draft clears the override', () => {
    const row = draftedSetting('group', setting(1, { rate_override: '0.5' }), { rate_override: null })
    expect(row.rate).toMatchObject({ value: '1.08', source: 'discount' })
  })
  it('lets everyone use a public group, and only the granted ones a private group', () => {
    expect(draftedSetting('segment', setting(1)).accessible).toBe(true)
    expect(draftedSetting('segment', setting(1, {}, 'restricted')).accessible).toBe(false)
    expect(draftedSetting('segment', setting(1, { granted: true }, 'restricted')).accessible).toBe(true)
  })
})

describe('the terms of the form around the rows', () => {
  it('take the discount of a segment form once it is a rate', () => {
    expect(draftedSetting('group', setting(1), undefined, { discount: '0.5' }).rate).toMatchObject({ value: '0.6', discount: '0.5' })
    expect(draftedSetting('group', setting(1), undefined, { discount: '1' }).rate).toMatchObject({ value: '1.2', source: 'group' })
    // A discount of 0 is none the form may save: the saved one stays.
    expect(draftedSetting('group', setting(1), undefined, { discount: '0' }).rate).toMatchObject({ value: '1.08', discount: '0.9' })
    expect(draftedSetting('group', setting(1), undefined, { discount: '0.' }).rate).toMatchObject({ value: '1.08', discount: '0.9' })
  })
  it('take the visibility and the default rate of a group form', () => {
    const row = draftedSetting('segment', setting(1), undefined, { visibility: 'restricted', groupDefault: '2' })
    expect(row).toMatchObject({ accessible: false, group: { visibility: 'restricted' } })
    expect(row.rate).toMatchObject({ value: '1.8', group_default: '2' })
    // An override replaces the default whatever the form says.
    expect(draftedSetting('segment', setting(1, { rate_override: '0.5' }), undefined, { groupDefault: '2' }).rate.value).toBe('0.5')
  })
})

describe('the drafts', () => {
  it('keep what differs from the saved setting, field by field', () => {
    const saved = setting(2, { granted: true, rate_override: '0.8' }, 'restricted')
    let drafts = drafted(saved, { granted: false })
    drafts = drafted(saved, { rate_override: null }, drafts)
    expect(drafts.get(2)).toEqual({ granted: false, rate_override: null })
    drafts = drafted(saved, { granted: true }, drafts)
    expect(drafts.get(2)).toEqual({ rate_override: null })
  })
  it('drop a row that reads like the saved one again', () => {
    const saved = setting(1, { rate_override: '0.80' })
    // The same rate written differently is no change.
    expect(drafted(saved, { rate_override: '0.8' }, drafted(saved, { rate_override: '0.9' })).size).toBe(0)
  })
  it('keep an override still being typed, to be refused on saving', () => {
    expect(drafted(setting(1), { rate_override: '0.' }).get(1)).toEqual({ rate_override: '0.' })
  })
  it('are sent as one patch per changed row, under the id of the side listed', () => {
    const drafts = drafted(setting(2, {}, 'restricted'), { granted: true }, drafted(setting(1), { rate_override: '0.5' }))
    expect(settingPatches('group', drafts)).toEqual([
      { group_id: 1, rate_override: '0.5' },
      { group_id: 2, granted: true }
    ])
    expect(settingPatches('segment', drafts)[0]).toEqual({ segment_id: 1, rate_override: '0.5' })
    expect(settingPatches('segment', none)).toEqual([])
  })
})

describe('a group not created yet', () => {
  it('starts every segment with nothing set, at its own discount', () => {
    const personal = { id: 104, kind: 'personal', name: 'a@b.c', user_id: 9, discount: '0.9', member_count: 1 } as Segment
    const blank = blankSetting(personal)
    expect(blank).toMatchObject({ segment: { id: 104, kind: 'personal', name: 'a@b.c', user_id: 9 }, granted: false, rate_override: null, member_count: 1 })
    // The group's side comes from its form.
    expect(draftedSetting('segment', blank, undefined, { visibility: 'restricted', groupDefault: '0.8' })).toMatchObject({ id: 104, accessible: false, rate: { value: '0.72', source: 'discount' } })
  })
})

describe('a segment not created yet', () => {
  it('starts every group with nothing set, at the discount of its form', () => {
    const group = { id: 5, name: 'Claude', description: '', status: 'active', visibility: 'restricted', rate_multiplier: 1.2, model_allowlist: [] } as Group
    const blank = blankGroupSetting(group)
    expect(blank).toMatchObject({ group: { id: 5, name: 'Claude', visibility: 'restricted', rate_multiplier: '1.2' }, accessible: false, granted: false, rate_override: null })
    expect(draftedSetting('group', blank, undefined, { discount: '0.9' })).toMatchObject({ id: 5, accessible: false, rate: { value: '1.08', source: 'discount' } })
    expect(draftedSetting('group', blank, { granted: true }).accessible).toBe(true)
    expect(blankGroupSetting({ ...group, visibility: 'public' }).accessible).toBe(true)
  })
})
