// The grant and the rate of segments (user groups) in groups (account groups)
// while they are edited: what a row shows under its unsaved changes and what
// the request sends. One group lists its segments, one segment lists its
// groups; both edit the same settings, so the rules are here once. Pure: an
// editor only holds the state.
import type { Group } from '@/api/types'
import type { GroupRatePatch, SegmentGroupSetting, SegmentRatePatch } from '@/api/pricing'
import { effectiveRate, sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** The end of a setting the rows of an editor differ in: the segments of one group, or the groups of one segment. */
export type SettingSide = 'segment' | 'group'

/** What a row changes; a field left out is as saved. `removed` takes away everything the segment has in the group: its grant, its rate and its model rates there. */
export interface SettingDraft {
  granted?: boolean
  rate_override?: string | null
  removed?: boolean
}

/** The unsaved changes by row id. */
export type SettingDrafts = ReadonlyMap<number, SettingDraft>

/** What the form around the rows says right now, before it is saved; a field left out is as saved. */
export interface SettingTerms {
  visibility?: Group['visibility']
  groupDefault?: string
}

/** A row of an editor: the saved setting under its draft and the terms. */
export interface DraftedSetting extends SegmentGroupSetting {
  /** The row key: the id of the segment or of the group, by the side listed. */
  id: number
  /** The setting on the server, which a draft is compared with. */
  saved: SegmentGroupSetting
  dirty: boolean
  /** Removed by the draft: it goes once saved. */
  removed: boolean
}

/** An override that may be saved: none, or a rate. */
export const validOverride = (v: string | null): boolean => v === null || validRate(v)

/** A rate of the form once it is one, the saved one until then. */
const rated = (typed: string | undefined, saved: string) => (typed !== undefined && validRate(typed.trim()) ? typed.trim() : saved)

/** What a removed row reads as: nothing set. */
const NOTHING = { granted: false, rate_override: null, model_rates: [] }

/** A row as it reads under `draft` and `terms`: the group's visibility, access and the rate follow both. */
export function draftedSetting(side: SettingSide, saved: SegmentGroupSetting, draft?: SettingDraft, terms: SettingTerms = {}): DraftedSetting {
  const removed = !!draft?.removed
  const base = removed ? { ...saved, ...NOTHING } : saved
  const granted = draft?.granted ?? base.granted
  const rate_override = draft?.rate_override === undefined ? base.rate_override : draft.rate_override
  const visibility = terms.visibility ?? saved.group.visibility
  const group = rated(terms.groupDefault, saved.rate.group_default)
  return {
    ...base,
    id: saved[side].id,
    group: { ...saved.group, visibility },
    granted,
    rate_override,
    accessible: visibility === 'public' || granted,
    rate: effectiveRate({ segment: rate_override, group }),
    saved,
    dirty: !!draft,
    removed
  }
}

/** Whether a segment has anything in the group: a grant, a rate or a model rate. */
export const setsAnything = (s: Pick<SegmentGroupSetting, 'granted' | 'rate_override' | 'model_rates'>) => s.granted || s.rate_override !== null || s.model_rates.length > 0

/**
 * The drafts with `change` added to what `row` changes already; a field that
 * reads like the saved setting again is none, and a row without any is no
 * draft. Removing a row that sets nothing is none either; a change after a
 * removal starts from nothing set.
 */
export function mergeSettingDraft(drafts: SettingDrafts, row: Pick<DraftedSetting, 'id' | 'saved'>, change: SettingDraft): SettingDrafts {
  if (change.removed) return withDraft(drafts, row.id, setsAnything(row.saved) ? { removed: true } : undefined)
  const prev = drafts.get(row.id)
  const merged = prev?.removed ? { ...NOTHING, ...change } : { ...prev, ...change }
  const draft: SettingDraft = {}
  if (merged.granted !== undefined && merged.granted !== row.saved.granted) draft.granted = merged.granted
  if (merged.rate_override !== undefined && !sameRate(merged.rate_override, row.saved.rate_override)) draft.rate_override = merged.rate_override
  return withDraft(drafts, row.id, Object.keys(draft).length ? draft : undefined)
}

/** An item of the request for a changed row: the grant only when it changed, the rate as it now is. */
function settingPatch(row: DraftedSetting, draft: SettingDraft) {
  return { ...(draft.granted === undefined ? {} : { granted: draft.granted }), rate: row.rate_override }
}

/** What the request sends of the drafts: the changed rows of a group's segments (`segment_rates`; a removed one by `remove`) or of a segment's groups (`group_rates`). */
export function segmentRatePatches(rows: readonly DraftedSetting[], drafts: SettingDrafts): SegmentRatePatch[] {
  return rows.filter((r) => drafts.has(r.id)).map((r) => (r.removed ? { segment_id: r.id, remove: true } : { segment_id: r.id, ...settingPatch(r, drafts.get(r.id)!) }))
}
export function groupRatePatches(rows: readonly DraftedSetting[], drafts: SettingDrafts): GroupRatePatch[] {
  return rows.filter((r) => drafts.has(r.id)).map((r) => ({ group_id: r.id, ...settingPatch(r, drafts.get(r.id)!) }))
}

/** The setting of a segment not created yet in group `g`: every row of a new segment. */
export function blankGroupSetting(g: Group): SegmentGroupSetting {
  const { id, name, description, status, visibility, platforms } = g
  const rate_multiplier = String(g.rate_multiplier)
  return {
    segment: { id: 0, kind: 'named', name: '' },
    group: { id, name, description, status, visibility, rate_multiplier, platforms },
    accessible: visibility === 'public',
    granted: false,
    rate_override: null,
    rate: effectiveRate({ group: rate_multiplier }),
    model_rates: []
  }
}

/** The setting of segment `s` in a group not created yet: a row of a new group, whose terms come from its form. */
export function blankSegmentSetting(s: Pick<SegmentGroupSetting['segment'], 'id' | 'kind' | 'name' | 'user_id'> & { member_count?: number }): SegmentGroupSetting {
  return {
    segment: { id: s.id, kind: s.kind, name: s.name, user_id: s.user_id },
    group: { id: 0, name: '', description: '', status: 'active', visibility: 'public', rate_multiplier: '1' },
    accessible: true,
    granted: false,
    rate_override: null,
    rate: effectiveRate({ group: '1' }),
    model_rates: [],
    member_count: s.member_count
  }
}
