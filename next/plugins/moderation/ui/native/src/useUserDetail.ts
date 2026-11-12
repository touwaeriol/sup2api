import { computed, watch, type Ref } from 'vue'
import { fetchUserDetail, markWatchSeen, useModHost } from './host'
import type { UserDetail, Watch } from './types'
import { useAsync } from './useAsync'

/**
 * GET /users/:user_id for an open drawer: loads on open and user change.
 * Opening also marks the user's new records as seen (manage only).
 */
export function useUserDetail(open: Ref<boolean>, userId: Ref<number | null>, opts: { manage: Ref<boolean>; changed: (w: Partial<Watch> | null) => void }) {
  const t = useModHost().t
  const detail = useAsync<UserDetail>(() => fetchUserDetail(userId.value!), () => t('watch.loadFailed'))
  const data = detail.data
  const entry = computed(() => data.value?.watch ?? null)

  /** Loads the user; the first load of an opening also marks new records as seen. */
  async function load(markSeen = false) {
    await detail.reload()
    const d = data.value
    if (!d || d.user_id !== userId.value) return
    opts.changed(d.watch)
    if (!markSeen || !opts.manage.value || !d.watch?.hits_since_seen) return
    await markWatchSeen(d.user_id).catch(() => undefined)
    d.watch.hits_since_seen = 0
    opts.changed({ hits_since_seen: 0 })
  }

  watch(
    () => [open.value, userId.value] as const,
    ([isOpen, id]) => {
      if (!isOpen || !id) return
      if (data.value?.user_id !== id) data.value = null
      void load(true)
    },
    { immediate: true }
  )

  return { detail: data, loading: detail.loading, error: detail.error, entry, reload: () => load() }
}
