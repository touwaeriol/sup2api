// The model rates of a segment (user group) while they are edited: in one
// group (account group), a model paid at its list price × a rate of its own
// instead of the segment's rate there. What the rows show under their unsaved
// rates and what the request sends. A row added here is a draft without a
// saved row. Pure: an editor only holds the state.
import type { SegmentModelRatePatch } from '@/api/pricing'
import { sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** A saved model rate of the segment in a group. */
export interface SegmentModelRate {
  group_id: number
  model: string
  rate: string
}

/** The unsaved rates by row key; a null rate removes the saved one. */
export type ModelRateDrafts = ReadonlyMap<string, SegmentModelRatePatch>

/** The key of a row: one model in one group. */
export const modelRateKey = (r: Pick<SegmentModelRate, 'group_id' | 'model'>) => `${r.group_id}:${r.model}`

/** A row: a saved model rate, or one added here, under its draft. */
export interface DraftedModelRate extends SegmentModelRate {
  key: string
  /** The rate on the server; null for a row added here and not saved. */
  saved: SegmentModelRate | null
  /** Removed by the draft: it goes once saved. */
  removed: boolean
  dirty: boolean
  /** A rate that cannot be saved yet. */
  invalid: boolean
}

/** A row as it reads under `draft`: the rate typed, or removed. */
function draftedModelRate(row: SegmentModelRate, saved: SegmentModelRate | null, draft?: SegmentModelRatePatch): DraftedModelRate {
  const removed = draft?.rate === null
  const rate = draft?.rate ?? row.rate
  return { ...row, rate, key: modelRateKey(row), saved, removed, dirty: !!draft, invalid: !removed && !validRate(rate) }
}

/** The rows: the saved rates under their drafts, then those added here, in the order added. */
export function modelRateRows(saved: readonly SegmentModelRate[], drafts: ModelRateDrafts): DraftedModelRate[] {
  const savedKeys = new Set(saved.map(modelRateKey))
  const added = [...drafts.values()].filter((d) => d.rate !== null && !savedKeys.has(modelRateKey(d)))
  return [...saved.map((r) => draftedModelRate(r, r, drafts.get(modelRateKey(r)))), ...added.map((d) => draftedModelRate({ ...d, rate: d.rate ?? '' }, null, d))]
}

/** The drafts with `rate` on `row`; a rate that reads like the saved one is no draft, and null drops a row added here. */
export function setModelRateDraft(drafts: ModelRateDrafts, row: Pick<DraftedModelRate, 'key' | 'saved' | 'group_id' | 'model'>, rate: string | null): ModelRateDrafts {
  const same = row.saved ? rate !== null && sameRate(rate, row.saved.rate) : rate === null
  return withDraft(drafts, row.key, same ? undefined : { group_id: row.group_id, model: row.model, rate })
}

/** A row added for `model` in group `groupId`, keyed as the rows are. */
export const newModelRate = (groupId: number, model: string) => ({ key: modelRateKey({ group_id: groupId, model }), saved: null, group_id: groupId, model })

/** What the request sends of the drafts (`model_rates` of POST/PATCH /segments). */
export const modelRatePatches = (drafts: ModelRateDrafts): SegmentModelRatePatch[] => [...drafts.values()]
