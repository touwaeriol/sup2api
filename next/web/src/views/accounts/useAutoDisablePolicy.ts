// The global auto-disable switch (GET /accounts/auto-disable) the account
// editor's auto-disable row follows: loaded whenever `source` changes (the
// account, its type), again when the window regains focus, and before a save
// sends auto_disable. Only the latest answer counts.
import { onBeforeUnmount, onMounted, ref, watch, type WatchSource } from 'vue'
import { api } from '@sub2api/host'

export function useAutoDisablePolicy(source: WatchSource) {
  /** Whether the switch is on; null while it loads. */
  const enabled = ref<boolean | null>(null)
  const failed = ref(false)
  let request = 0

  async function load() {
    const mine = ++request
    enabled.value = null
    failed.value = false
    try {
      const policy = await api.get<{ enabled: boolean }>('/accounts/auto-disable')
      if (mine === request) enabled.value = policy.enabled
    } catch {
      if (mine === request) failed.value = true
    }
  }

  watch(source, load, { immediate: true })
  onMounted(() => window.addEventListener('focus', load))
  onBeforeUnmount(() => {
    request++
    window.removeEventListener('focus', load)
  })
  return { enabled, failed, load }
}

export type AutoDisablePolicy = ReturnType<typeof useAutoDisablePolicy>
