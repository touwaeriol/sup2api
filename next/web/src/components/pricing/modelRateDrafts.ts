// The model rates of one end while they are edited: a user group's under
// each of its account groups, or an account group's under each of its user
// groups (the far end of a row). A model there is paid at its list price × a
// rate of its own instead of the pair's rate. What the rows show under their
// unsaved rates and what the request sends. A row added here is a draft
// without a saved row. Pure: an editor only holds the state.
import type { ModelRatePatchOf } from '@/api/pricing'
import type { Money } from '@/api/types'
import { sameRate, validRate } from './pricingFormat'
import { withDraft } from './drafts'

/** The key of the far end, as the request names it: the account group in a user group's request, the user group in an account group's. */
export type ModelRateFar = 'group_id' | 'segment_id'

/** A saved model rate under the far end `far`. */
export interface PairModelRate {
  far: number
  model: string
  rate: string
}

/** An unsaved rate; null removes the saved one. */
export interface ModelRateDraft {
  far: number
  model: string
  rate: Money | null
}

/** The unsaved rates by row key. */
export type ModelRateDrafts = ReadonlyMap<string, ModelRateDraft>

/** The key of a row: one model under one far end. */
export const modelRateKey = (r: Pick<PairModelRate, 'far' | 'model'>) => `${r.far}:${r.model}`

/** A row: a saved model rate, or one added here, under its draft. */
export interface DraftedModelRate extends PairModelRate {
  key: string
  /** The rate on the server; null for a row added here and not saved. */
  saved: PairModelRate | null
  /** Removed by the draft: it goes once saved. */
  removed: boolean
  dirty: boolean
  /** A rate that cannot be saved yet. */
  invalid: boolean
}

/** A row as it reads under `draft`: the rate typed, or removed. */
function draftedModelRate(row: PairModelRate, saved: PairModelRate | null, draft?: ModelRateDraft): DraftedModelRate {
  const removed = draft?.rate === null
  const rate = draft?.rate ?? row.rate
  return { ...row, rate, key: modelRateKey(row), saved, removed, dirty: !!draft, invalid: !removed && !validRate(rate) }
}

/** The rows: the saved rates under their drafts, then those added here, in the order added. */
export function modelRateRows(saved: readonly PairModelRate[], drafts: ModelRateDrafts): DraftedModelRate[] {
  const savedKeys = new Set(saved.map(modelRateKey))
  const added = [...drafts.values()].filter((d) => d.rate !== null && !savedKeys.has(modelRateKey(d)))
  return [...saved.map((r) => draftedModelRate(r, r, drafts.get(modelRateKey(r)))), ...added.map((d) => draftedModelRate({ ...d, rate: d.rate ?? '' }, null, d))]
}

/** The drafts with `rate` on `row`; a rate that reads like the saved one is no draft, and null drops a row added here. */
export function setModelRateDraft(drafts: ModelRateDrafts, row: Pick<DraftedModelRate, 'key' | 'saved' | 'far' | 'model'>, rate: string | null): ModelRateDrafts {
  const same = row.saved ? rate !== null && sameRate(rate, row.saved.rate) : rate === null
  return withDraft(drafts, row.key, same ? undefined : { far: row.far, model: row.model, rate })
}

/** The drafts without those under the far end `far`. */
export const withoutFar = (drafts: ModelRateDrafts, far: number): ModelRateDrafts => new Map([...drafts].filter(([, d]) => d.far !== far))

/** A row added for `model` under the far end `far`, keyed as the rows are. */
export const newModelRate = (far: number, model: string) => ({ key: modelRateKey({ far, model }), saved: null, far, model })

/** What the request sends of the drafts, the far end under `key`. */
export const modelRatePatches = <F extends ModelRateFar>(drafts: ModelRateDrafts, key: F): ModelRatePatchOf<F>[] =>
  [...drafts.values()].map(({ far, model, rate }) => ({ [key]: far, model, rate }) as ModelRatePatchOf<F>)
