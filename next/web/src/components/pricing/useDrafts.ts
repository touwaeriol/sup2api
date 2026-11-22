// An editor that keeps its edits until its parent saves: the saved state as
// the server has it, the drafts on top of it by row key, and the one way to
// load, refresh and save them. The model price table and the tables of
// grants and rates both edit this way; what a draft is and how a row reads
// under one is theirs (ruleDrafts.ts, accessRateDrafts.ts).
import { computed, ref, shallowRef } from 'vue'
import { toast } from '@sub2api/ui'
import { latestOnly } from '@/composables/useLatestRequest'
import { unsent } from './drafts'

export interface DraftsOptions<S> {
  /** The saved state while there is none: before the server answers, and when a load starts over. */
  empty: () => S
  /** Asks the server for the saved state. */
  fetch: () => Promise<S>
  /** Why the drafts cannot be saved yet, as a toast says it; null when they can. */
  problem?: () => string | null
}

/**
 * The state of such an editor: `S` is the saved state, `K` the key of a row
 * and `D` its draft. Nothing is sent until save().
 */
export function useDrafts<S, K, D>(options: DraftsOptions<S>) {
  const saved = shallowRef<S>(options.empty())
  const drafts = shallowRef<ReadonlyMap<K, D>>(new Map())
  const loading = ref(false)
  const error = ref<unknown>(null)
  const begin = latestOnly()
  // Each load() is of another object (another group, user group or user): an answer to a save of the one before is not this one's.
  const nextLoad = latestOnly()
  let sameLoad = nextLoad()
  const dirty = computed(() => drafts.value.size > 0)

  /** Fetches the saved state again; the drafts stay on it. Only the latest request counts. */
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

  /** Starts over: no drafts, the saved state fetched anew. */
  function load() {
    sameLoad = nextLoad()
    drafts.value = new Map()
    saved.value = options.empty()
    return refresh()
  }

  /** Whether the drafts can be saved as they are; a toast says why not. Run before saving anything they depend on. */
  function check(): boolean {
    const problem = dirty.value ? options.problem?.() : null
    if (problem) toast(problem, 'error')
    return !problem
  }

  /**
   * Sends the drafts through `put` and shows what the server answers. False
   * when they cannot be saved yet (a toast says why). A failed request
   * rejects and keeps the drafts. Only what was sent is saved: a draft made
   * or changed while the request was under way stays. An answer that comes
   * after a load() is left out: it is of the object loaded before.
   */
  async function save(put: (sent: ReadonlyMap<K, D>) => Promise<S>): Promise<boolean> {
    if (!dirty.value) return true
    if (!check()) return false
    const sent = drafts.value
    const loaded = sameLoad
    const state = await put(sent)
    if (!loaded()) return true
    // The answer is newer than that of a fetch still under way.
    begin()
    loading.value = false
    saved.value = state
    drafts.value = unsent(drafts.value, sent)
    return true
  }

  return { saved, drafts, loading, error, dirty, refresh, load, check, save }
}
