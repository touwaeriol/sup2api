import { ref } from 'vue'
import type { Segment } from '@/api/pricing'
import { fetchFirstPage } from '@/composables/lookups'

const segments = ref<Segment[]>([])
let segmentsP: Promise<Segment[]> | null = null

/** The request of the named segments: the cached one, a new one when forced or after one failed. */
function request(force: boolean): Promise<Segment[]> {
  if (force || !segmentsP) {
    const p = fetchFirstPage<Segment>('/segments')
    segmentsP = p
    p.then(
      items => {
        if (segmentsP === p) segments.value = items
      },
      () => {
        if (segmentsP === p) segmentsP = null
      }
    )
  }
  return segmentsP
}

/** The named segments, cached for selects (as useGroupsLookup). Call with `force` after one was created, renamed or deleted. */
export function useSegmentsLookup(force = false) {
  return { segments, ready: request(force).then(noop, noop) }
}

function noop() {}
