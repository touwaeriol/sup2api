// The installed plugins list's upgrades: the store's newer version of each
// installed plugin, counted against the store's default channel (CONTRACTS
// §136), and the consent page that installs one.
import { computed, type Ref } from 'vue'
import type { PluginSummary } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { upgradesByPlugin } from './marketUtil'
import { useMarket } from './useMarket'

export function usePluginUpgrades(installed: Ref<PluginSummary[]>) {
  const auth = useAuthStore()
  const market = useMarket()
  const upgrades = computed(() => upgradesByPlugin(market.items.value, installed.value))
  return {
    upgrades,
    count: computed(() => Object.keys(upgrades.value).length),
    load: () => (auth.has('plugin:market:read') ? market.loadChannelListing() : Promise.resolve()),
    /** The consent page of p's upgrade, before anything is downloaded. */
    location: (p: PluginSummary) => {
      const m = upgrades.value[p.key]
      return m ? market.installLocation(m, m.latest_version) : undefined
    }
  }
}
