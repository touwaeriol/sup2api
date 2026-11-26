import { computed, onMounted, ref } from 'vue'
import { api } from '@sub2api/host'
import { useAuthStore } from '@/stores/auth'

/**
 * The caller's balance (GET /me/balance), loaded on mount; reload() fetches
 * it again and refreshes the header's balance too. Shared by the wallet and
 * the balance changes log.
 */
export function useMyBalance() {
  const auth = useAuthStore()
  const balance = ref<string | null>(null)
  const loading = ref(false)

  async function load() {
    loading.value = true
    try {
      const r = await api.get<{ balance: string }>('/me/balance')
      balance.value = r?.balance ?? null
    } catch {
      balance.value = null
    } finally {
      loading.value = false
    }
  }

  function reload() {
    load()
    auth.refreshBalance()
  }

  onMounted(reload)
  return { balance, loading, negative: computed(() => Number(balance.value) < 0), reload }
}
