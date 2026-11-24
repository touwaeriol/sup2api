// Where a market install's package comes from, for the consent page's source
// check: the channel it is installed from and the version as that channel's
// index lists it (size, release, sha256). Nothing when the page is not a
// market install, or the store cannot be read (CONTRACTS §136).
import { computed, ref } from 'vue'
import type { MarketPlugin, MarketSource } from '@/api/marketTypes'
import { fetchChannels, fetchListing } from '../useMarket'
import { versionOf } from '../marketUtil'
import type { MarketTarget } from './consentSources'

export function useMarketOrigin(target: MarketTarget | undefined) {
  const channel = ref<MarketSource | null>(null)
  const plugin = ref<MarketPlugin | null>(null)

  async function load() {
    if (!target) return
    try {
      const [channels, listing] = await Promise.all([fetchChannels(), fetchListing(target.source_id)])
      channel.value = channels.find(c => c.id === target.source_id) ?? null
      plugin.value = listing.data.find(m => m.publisher === target.publisher && m.key === target.key) ?? null
    } catch {
      // The source check shows what the review says without the store's side.
      channel.value = null
      plugin.value = null
    }
  }

  return { channel, plugin, version: computed(() => (plugin.value && target ? versionOf(plugin.value, target.version) : undefined)), load }
}
