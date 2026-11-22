// An editor that keeps its edits until its parent saves: the saved state as
// the server has it, the drafts on top of it by row key, and the one way to
// load and refresh them. The parent sends the drafts with the rest of its
// form in one request (useTabbedForm). The pricing table and the tables of
// grants and rates all edit this way; what a draft is and how a row reads
// under one is theirs (ruleDrafts.ts, accessRateDrafts.ts, modelRateDrafts.ts).
import { computed, ref, shallowRef } from 'vue'
import { toast } from '@sub2api/ui'
import { latestOnly } from '@/composables/useLatestRequest'

export interface DraftsOptions<S> {
  /** The saved state while there is none: before the server answers, and when a load starts over. */
  empty: () => S
  /** Asks the server for the saved state. */
  fetch: () => Promise<S>
  /** Why the drafts cannot be saved yet, as a toast says it; null when they can. */
  problem?: () => string | null
}

/** The saved state as the server has it: fetched again on refresh(), only the latest request counting. */
function useSavedState<S>(options: DraftsOptions<S>) {
  const saved = shallowRef<S>(options.empty())
  const loading = ref(false)
  const error = ref<unknown>(null)
  const begin = latestOnly()

  /** Fetches the saved state again. Only the latest request counts. */
  async function refresh() {
    const latest = begin()
    loading.value = true
    error.value = null
    try {
      const state = await options.fetch()
      if (latest()) saved.value = state
    } catch (e) {
      if (latest()) error.value = e
    } finally {
      if (latest()) loading.value = false
    }
  }

  return { saved, loading, error, refresh }
}

/** The drafts by row key; `problem` says why they cannot be saved yet. */
function useDraftMap<K, D>(problem?: () => string | null) {
  const drafts = shallowRef<ReadonlyMap<K, D>>(new Map())
  const dirty = computed(() => drafts.value.size > 0)

  /** Whether the drafts can be sent as they are; a toast says why not. */
  function check(): boolean {
    const why = dirty.value ? problem?.() : null
    if (why) toast(why, 'error')
    return !why
  }

  return { drafts, dirty, check }
}

/**
 * The state of such an editor: `S` is the saved state, `K` the key of a row
 * and `D` its draft. refresh() keeps the drafts on the saved state fetched
 * again; load() starts over without them.
 */
export function useDrafts<S, K, D>(options: DraftsOptions<S>) {
  const { saved, loading, error, refresh } = useSavedState(options)
  const { drafts, dirty, check } = useDraftMap<K, D>(options.problem)

  /** Starts over: no drafts, the saved state fetched anew. */
  function load() {
    drafts.value = new Map()
    saved.value = options.empty()
    return refresh()
  }

  return { saved, drafts, loading, error, dirty, refresh, load, check }
}
