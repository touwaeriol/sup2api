// The installed plugins list's upgrades: the store's newer version of each
// installed plugin, counted against the official store's default channel
// (GET /market/plugins without a channel; CONTRACTS §137.6), and the consent
// page that installs one.
import { computed, ref, type Ref } from 'vue'
import type { MarketPlugin } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { marketInstallLocation } from './consent/paths'
import { upgradesByPlugin } from './marketUtil'
import { fetchListing } from './useMarket'

export function usePluginUpgrades(installed: Ref<PluginSummary[]>) {
  const auth = useAuthStore()
  const items = ref<MarketPlugin[]>([])
  const upgrades = computed(() => upgradesByPlugin(items.value, installed.value))
  /** Reads the default channel's listing; refresh fetches its index afresh (the page's refresh button). */
  async function load(refresh = false) {
    if (!auth.has('plugin:market:read')) return
    try {
      items.value = (await fetchListing(undefined, refresh)).data
    } catch (e) {
      items.value = []
      notifyError(e)
    }
  }
  return {
    upgrades,
    count: computed(() => Object.keys(upgrades.value).length),
    load,
    /** The consent page of p's upgrade, before anything is downloaded. */
    location: (p: PluginSummary) => {
      const m = upgrades.value[p.key]
      return m ? marketInstallLocation(m.source_id, m, m.latest_version) : undefined
    }
  }
}
