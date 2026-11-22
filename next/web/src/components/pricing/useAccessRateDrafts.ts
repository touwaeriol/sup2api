import { computed } from 'vue'
import type { SegmentGroupPatch, SegmentGroupSetting } from '@/api/pricing'
import { i18n } from '@/i18n'
import { draftedSetting, mergeSettingDraft, settingPatches, validOverride, type DraftedSetting, type SettingDraft, type SettingSide, type SettingTerms } from './accessRateDrafts'
import { useDrafts } from './useDrafts'

export interface AccessRateDraftsOptions {
  /** The end the rows differ in. */
  side: SettingSide
  /** Asks the server for the saved settings. */
  fetch: () => Promise<SegmentGroupSetting[]>
  /** What the form around the rows says before it is saved. */
  terms?: () => SettingTerms
}

/**
 * The state of an editor of grants and rates: the saved settings, the drafts
 * on top of them (useDrafts) and the rows both give. Nothing is sent until
 * save(), which the editor's parent calls after saving its own form.
 */
export function useAccessRateDrafts(options: AccessRateDraftsOptions) {
  const state = useDrafts<SegmentGroupSetting[], number, SettingDraft>({ empty: () => [], fetch: options.fetch, problem: invalidRate })
  const { loading, error, dirty, refresh, load, check } = state

  const rows = computed<DraftedSetting[]>(() => {
    const terms = options.terms?.()
    return state.saved.value.map((s) => draftedSetting(options.side, s, state.drafts.value.get(s[options.side].id), terms))
  })

  /** A rate that cannot be saved yet, by the name of its row. */
  function invalidRate(): string | null {
    const invalid = rows.value.find((r) => !validOverride(r.rate_override))
    return invalid ? i18n.global.t('pricingAccess.invalidRate', { name: invalid[options.side].name }) : null
  }

  function change(row: DraftedSetting, draft: SettingDraft) {
    state.drafts.value = mergeSettingDraft(state.drafts.value, row, draft)
  }

  /**
   * Sends the drafts through `put` and shows what the server answers. False
   * when a rate cannot be saved yet (a toast names its row). A failed request
   * rejects and keeps the drafts.
   */
  function save(put: (items: SegmentGroupPatch[]) => Promise<SegmentGroupSetting[]>): Promise<boolean> {
    return state.save((sent) => put(settingPatches(options.side, sent)))
  }

  return { rows, loading, error, dirty, refresh, load, check, change, save }
}
