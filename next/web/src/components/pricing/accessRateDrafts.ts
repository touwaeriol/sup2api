// The grant and the rate of segments in groups while they are edited: what a
// row shows under its unsaved changes and what saving sends. One group lists
// its segments, one segment lists its groups; both edit the same settings, so
// the rules are here once. Pure: an editor only holds the state.
import type { Group } from '@/api/types'
import type { Segment, SegmentGroupPatch, SegmentGroupSetting } from '@/api/pricing'
import { effectiveRate, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'
import { validDiscount } from './segmentForm'

/** The end of a setting the rows of an editor differ in: the segments of one group, or the groups of one segment. */
export type SettingSide = 'segment' | 'group'

/** What a row changes; a field left out is as saved. */
export type SettingDraft = Pick<SegmentGroupPatch, 'granted' | 'rate_override'>

/** The unsaved changes by row id. */
export type SettingDrafts = ReadonlyMap<number, SettingDraft>

/** What the form around the rows says right now, before it is saved; a field left out is as saved. */
export interface SettingTerms {
  visibility?: Group['visibility']
  groupDefault?: string
  discount?: string
}

/** A row of an editor: the saved setting under its draft and the terms. */
export interface DraftedSetting extends SegmentGroupSetting {
  /** The row key: the id of the segment or of the group, by the side listed. */
  id: number
  /** The setting on the server, which a draft is compared with. */
  saved: SegmentGroupSetting
  dirty: boolean
}

/** An override that may be saved: none, or a rate. */
export const validOverride = (v: string | null): boolean => v === null || validRate(v)

/** A value of the form once `valid` takes it (a rate, a discount), the saved one until then. */
const rated = (typed: string | undefined, saved: string, valid: (v: string) => boolean) => (typed !== undefined && valid(typed.trim()) ? typed.trim() : saved)

/** A row as it reads under `draft` and `terms`: the group's visibility, access and the rate follow both. */
export function draftedSetting(side: SettingSide, saved: SegmentGroupSetting, draft?: SettingDraft, terms: SettingTerms = {}): DraftedSetting {
  const granted = draft?.granted ?? saved.granted
  const rate_override = draft?.rate_override === undefined ? saved.rate_override : draft.rate_override
  const visibility = terms.visibility ?? saved.group.visibility
  const base = { group_default: rated(terms.groupDefault, saved.rate.group_default, validRate), discount: rated(terms.discount, saved.rate.discount, validDiscount) }
  return {
    ...saved,
    id: saved[side].id,
    group: { ...saved.group, visibility },
    granted,
    rate_override,
    accessible: visibility === 'public' || granted,
    rate: effectiveRate(base, rate_override),
    saved,
    dirty: !!draft
  }
}

/** The drafts with `change` added to what `row` changes already; a field that reads like the saved setting again is none, and a row without any is no draft. */
export function mergeSettingDraft(drafts: SettingDrafts, row: Pick<DraftedSetting, 'id' | 'saved'>, change: SettingDraft): SettingDrafts {
  const merged = { ...drafts.get(row.id), ...change }
  const draft: SettingDraft = {}
  if (merged.granted !== undefined && merged.granted !== row.saved.granted) draft.granted = merged.granted
  if (merged.rate_override !== undefined && !sameRate(merged.rate_override, row.saved.rate_override)) draft.rate_override = merged.rate_override
  return withDraft(drafts, row.id, Object.keys(draft).length ? draft : undefined)
}

/** What saving the drafts sends: per changed row, only what changed (PUT /groups/:id/segments, PUT /segments/:id/groups). */
export function settingPatches(side: SettingSide, drafts: SettingDrafts): SegmentGroupPatch[] {
  return [...drafts].map(([id, draft]) => ({ [`${side}_id`]: id, ...draft }))
}

/** The setting of segment `s` in a group not created yet: every row of a new group, whose terms come from its form. */
export function blankSetting(s: Segment): SegmentGroupSetting {
  return {
    segment: { id: s.id, kind: s.kind, name: s.name, user_id: s.user_id },
    group: { id: 0, name: '', description: '', status: 'active', visibility: 'public', rate_multiplier: '1' },
    accessible: true,
    granted: false,
    rate_override: null,
    rate: effectiveRate({ group_default: '1', discount: s.discount }, null),
    member_count: s.member_count
  }
}

/** The setting of a segment not created yet in group `g`: every row of a new segment, whose discount comes from its form. */
export function blankGroupSetting(g: Group): SegmentGroupSetting {
  const { id, name, description, status, visibility, platforms } = g
  const rate_multiplier = String(g.rate_multiplier)
  return {
    segment: { id: 0, kind: 'named', name: '' },
    group: { id, name, description, status, visibility, rate_multiplier, platforms },
    accessible: visibility === 'public',
    granted: false,
    rate_override: null,
    rate: effectiveRate({ group_default: rate_multiplier, discount: '1' }, null)
  }
}
