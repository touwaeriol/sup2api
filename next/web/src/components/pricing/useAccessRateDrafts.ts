import { computed, shallowRef } from 'vue'
import type { SegmentGroupSetting } from '@/api/pricing'
import { i18n } from '@/i18n'
import { draftedSetting, groupRatePatches, mergeSettingDraft, segmentRatePatches, validOverride, type DraftedSetting, type SettingDraft, type SettingSide, type SettingTerms } from './accessRateDrafts'
import { listRequest, withDraft } from './drafts'
import { useDrafts } from './useDrafts'

export interface AccessRateDraftsOptions {
  /** The end the rows differ in. */
  side: SettingSide
  /** Asks the server for the saved settings. */
  fetch: () => Promise<SegmentGroupSetting[]>
  /** What the form around the rows says before it is saved. */
  terms?: () => SettingTerms
}

/** The list of the request the rows go in: a group's segments, or a segment's groups. */
const LIST: Record<SettingSide, 'segment_rates' | 'group_rates'> = { segment: 'segment_rates', group: 'group_rates' }

/**
 * The state of an editor of grants and rates: the saved settings, the drafts
 * on top of them (useDrafts) and the rows both give. body() is what the
 * parent's request sends of them; applyErrors() takes the field errors of its
 * answer back to the rows.
 */
export function useAccessRateDrafts(options: AccessRateDraftsOptions) {
  const state = useDrafts<SegmentGroupSetting[], number, SettingDraft>({ empty: () => [], fetch: options.fetch, problem: invalidRate })
  const rowErrors = shallowRef<ReadonlyMap<number, string>>(new Map())
  const rows = computed<DraftedSetting[]>(() => {
    const terms = options.terms?.()
    return state.saved.value.map((s) => draftedSetting(options.side, s, state.drafts.value.get(s[options.side].id), terms))
  })

  /** A rate that cannot be sent yet, by the name of its row. */
  function invalidRate(): string | null {
    const invalid = rows.value.find((r) => !validOverride(r.rate_override))
    return invalid ? i18n.global.t('pricingAccess.invalidRate', { name: invalid[options.side].name }) : null
  }

  function change(row: DraftedSetting, draft: SettingDraft) {
    state.drafts.value = mergeSettingDraft(state.drafts.value, row, draft)
    rowErrors.value = withDraft(rowErrors.value, row.id, undefined)
  }

  const request = listRequest<{ segment_id?: number; group_id?: number }, number>(
    LIST[options.side],
    () => (options.side === 'segment' ? segmentRatePatches : groupRatePatches)(rows.value, state.drafts.value),
    (item) => item.segment_id ?? item.group_id ?? 0
  )
  const applyErrors = (errors: Record<string, string>) => (rowErrors.value = request.errors(errors))
  const undo = (row: DraftedSetting) => (state.drafts.value = withDraft(state.drafts.value, row.id, undefined))

  return { ...state, rows, rowErrors, change, undo, body: request.body, applyErrors, fields: [LIST[options.side]] }
}
