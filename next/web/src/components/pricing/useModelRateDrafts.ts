import { computed, shallowRef } from 'vue'
import { toast } from '@sub2api/ui'
import { i18n } from '@/i18n'
import { listRequest, withDraft } from './drafts'
import {
  modelRateKey,
  modelRatePatches,
  modelRateRows,
  newModelRate,
  setModelRateDraft,
  withoutFar,
  type DraftedModelRate,
  type ModelRateDrafts,
  type ModelRateFar,
  type PairModelRate
} from './modelRateDrafts'

export interface ModelRateDraftsOptions {
  /** The key of the far end of the rows: `group_id` in a user group's editor, `segment_id` in an account group's. */
  far: ModelRateFar
  /** The saved model rates, under every far end. */
  saved: () => readonly PairModelRate[]
  /** A far end's name, for the toast of a rate that cannot be sent. */
  farName: (id: number) => string
}

/** The list of the request the rates go in: a user group's `model_rates`, an account group's `segment_model_rates`. */
const LIST: Record<ModelRateFar, 'model_rates' | 'segment_model_rates'> = { group_id: 'model_rates', segment_id: 'segment_model_rates' }

/** Whether the rates can be sent; a toast names the first that cannot. */
function checkRates(rows: readonly DraftedModelRate[], farName: (id: number) => string): boolean {
  const row = rows.find((r) => r.invalid)
  if (row) toast(i18n.global.t('segments.modelRates.invalid', { name: farName(row.far), model: row.model }), 'error')
  return !row
}

/**
 * The model rates of one end under all its far ends while they are edited:
 * the drafts over the saved rates and the rows both give. body() is what the
 * request sends of them; applyErrors() takes the field errors of its answer
 * back to the rows.
 */
export function useModelRateDrafts(options: ModelRateDraftsOptions) {
  const drafts = shallowRef<ModelRateDrafts>(new Map())
  const rowErrors = shallowRef<ReadonlyMap<string, string>>(new Map())
  const rows = computed(() => modelRateRows(options.saved(), drafts.value))
  const request = listRequest(LIST[options.far], () => modelRatePatches(drafts.value, options.far), (item) => modelRateKey({ far: item[options.far], model: item.model }))

  function change(row: Pick<DraftedModelRate, 'key' | 'saved' | 'far' | 'model'>, rate: string | null) {
    drafts.value = setModelRateDraft(drafts.value, row, rate)
    rowErrors.value = withDraft(rowErrors.value, row.key, undefined)
  }

  return {
    rows,
    rowErrors,
    dirty: computed(() => drafts.value.size > 0),
    field: LIST[options.far],
    /** The rows under one far end. */
    rowsOf: (far: number) => rows.value.filter((r) => r.far === far),
    reset: () => (drafts.value = new Map()),
    /** Drops the drafts under one far end. */
    discard: (far: number) => (drafts.value = withoutFar(drafts.value, far)),
    check: () => checkRates(rows.value, options.farName),
    change,
    /** Adds `model` under the far end `far` at `rate`, the rate it pays there now. */
    add: (far: number, model: string, rate: string) => change(newModelRate(far, model), rate),
    undo: (row: DraftedModelRate) => (drafts.value = withDraft(drafts.value, row.key, undefined)),
    body: request.body,
    applyErrors: (errors: Record<string, string>) => (rowErrors.value = request.errors(errors))
  }
}
