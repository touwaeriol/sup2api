// Where a market install's package comes from, for the consent page's source
// check: the store and channel it is installed from and the version as that
// channel's index lists it (size, release, sha256). Nothing when the page is
// not a market install, or the store cannot be read (CONTRACTS §136).
import { computed, ref } from 'vue'
import type { MarketPlugin, MarketSource } from '@/api/marketTypes'
import { fetchChannels, fetchListing } from '../useMarket'
import { versionOf } from '../marketUtil'
import type { MarketTarget } from './consentSources'

/** The channel of id, with its store's name (one GET /market/sources of every store). */
async function channelOf(id: number): Promise<MarketSource | null> {
  return (await fetchChannels()).find(c => c.id === id) ?? null
}

export function useMarketOrigin(target: MarketTarget | undefined) {
  /** The channel installed from (its store_name names the store). */
  const origin = ref<MarketSource | null>(null)
  const plugin = ref<MarketPlugin | null>(null)

  async function load() {
    if (!target) return
    try {
      const [found, listing] = await Promise.all([channelOf(target.source_id), fetchListing(target.source_id)])
      origin.value = found
      plugin.value = listing.data.find(m => m.publisher === target.publisher && m.key === target.key) ?? null
    } catch {
      // The source check shows what the review says without the store's side.
      origin.value = null
      plugin.value = null
    }
  }

  return { origin, plugin, version: computed(() => (plugin.value && target ? versionOf(plugin.value, target.version) : undefined)), load }
}
