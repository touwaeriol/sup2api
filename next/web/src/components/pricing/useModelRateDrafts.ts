import { computed, shallowRef } from 'vue'
import { toast } from '@sub2api/ui'
import { i18n } from '@/i18n'
import { listRequest, withDraft } from './drafts'
import { modelRatePatches, modelRateRows, newModelRate, setModelRateDraft, type DraftedModelRate, type ModelRateDrafts, type SegmentModelRate } from './modelRateDrafts'

export interface ModelRateDraftsOptions {
  /** The segment's saved model rates, of every group. */
  saved: () => readonly SegmentModelRate[]
  /** A group's name, for the toast of a rate that cannot be sent. */
  groupName: (id: number) => string
}

/** Whether the rates can be sent; a toast names the first that cannot. */
function checkRates(rows: readonly DraftedModelRate[], groupName: (id: number) => string): boolean {
  const row = rows.find((r) => r.invalid)
  if (row) toast(i18n.global.t('segments.modelRates.invalid', { group: groupName(row.group_id), model: row.model }), 'error')
  return !row
}


/**
 * The model rates of a segment in all its groups while they are edited: the
 * drafts over the saved rates and the rows both give. body() is what the
 * segment's request sends of them (`model_rates`); applyErrors() takes the
 * field errors of its answer back to the rows.
 */
export function useModelRateDrafts(options: ModelRateDraftsOptions) {
  const drafts = shallowRef<ModelRateDrafts>(new Map())
  const rowErrors = shallowRef<ReadonlyMap<string, string>>(new Map())
  const rows = computed(() => modelRateRows(options.saved(), drafts.value))
  const request = listRequest('model_rates', () => modelRatePatches(drafts.value), (item) => newModelRate(item.group_id, item.model).key)

  function change(row: Pick<DraftedModelRate, 'key' | 'saved' | 'group_id' | 'model'>, rate: string | null) {
    drafts.value = setModelRateDraft(drafts.value, row, rate)
    rowErrors.value = withDraft(rowErrors.value, row.key, undefined)
  }

  return {
    rows,
    rowErrors,
    dirty: computed(() => drafts.value.size > 0),
    /** The rows of one group. */
    rowsOf: (groupId: number) => rows.value.filter((r) => r.group_id === groupId),
    reset: () => (drafts.value = new Map()),
    check: () => checkRates(rows.value, options.groupName),
    change,
    /** Adds `model` in group `groupId` at `rate`, the rate it pays there now. */
    add: (groupId: number, model: string, rate: string) => change(newModelRate(groupId, model), rate),
    undo: (row: DraftedModelRate) => (drafts.value = withDraft(drafts.value, row.key, undefined)),
    body: request.body,
    applyErrors: (errors: Record<string, string>) => (rowErrors.value = request.errors(errors))
  }
}
