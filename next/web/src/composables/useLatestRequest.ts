import { onScopeDispose, ref, shallowRef, watch, type Ref, type ShallowRef, type WatchSource } from 'vue'
import { errorMessage } from '@/utils/errors'

export interface LatestRequest<R> {
  /** The answer to the latest input; kept while the next one loads, null once the input is null. */
  result: ShallowRef<R | null>
  loading: Ref<boolean>
  /** Why the latest request failed, '' otherwise. */
  error: Ref<string>
  /** Asks again for the latest input at once (after a failure, say). */
  reload: () => void
}

/**
 * Tells the latest of a row of requests from the earlier ones. Each call of
 * the function it gives starts a request and returns the check to make once
 * its answer is there: true until the next request starts.
 */
export function latestOnly(): () => () => boolean {
  let seq = 0
  return () => {
    const my = ++seq
    return () => my === seq
  }
}

/**
 * Asks `load` for the answer to `input` each time it changes, `delay` ms after
 * the last change (typing, dragging a number). Only the latest input counts:
 * the answers of earlier ones are dropped. A null input asks nothing.
 */
export function useLatestRequest<I, R>(input: WatchSource<I | null>, load: (input: I) => Promise<R>, delay = 300): LatestRequest<R> {
  const state = { result: shallowRef<R | null>(null), loading: ref(false), error: ref('') }
  const begin = latestOnly()
  let timer: ReturnType<typeof setTimeout> | undefined
  let last: I | null = null

  function ask(value: I | null, wait: number) {
    clearTimeout(timer)
    last = value
    const latest = begin()
    state.error.value = ''
    state.loading.value = value !== null
    if (value === null) state.result.value = null
    else timer = setTimeout(() => answer(state, () => load(value), latest), wait)
  }

  watch(input, (value) => ask(value, delay), { immediate: true })
  onScopeDispose(() => clearTimeout(timer))
  return { ...state, reload: () => ask(last, 0) }
}

/** Takes one request's answer or failure, if it is still the latest. */
async function answer<R>(s: Omit<LatestRequest<R>, 'reload'>, request: () => Promise<R>, latest: () => boolean) {
  try {
    const r = await request()
    if (latest()) s.result.value = r
  } catch (e) {
    if (!latest()) return
    s.result.value = null
    s.error.value = errorMessage(e)
  } finally {
    if (latest()) s.loading.value = false
  }
}
