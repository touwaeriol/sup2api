// Where a market install's package comes from, for the consent page's source
// check: the store and channel it is installed from and the version as that
// channel's index lists it (size, release, sha256). Nothing when the page is
// not a market install, or the store cannot be read (CONTRACTS §136).
import { computed, ref } from 'vue'
import type { MarketPlugin, MarketSource, MarketStore } from '@/api/marketTypes'
import { fetchChannels, fetchListing, fetchStores } from '../useMarket'
import { versionOf } from '../marketUtil'
import type { MarketTarget } from './consentSources'

/** The channel of id among the stores' channels, with its store. */
async function channelOf(id: number): Promise<{ store: MarketStore; channel: MarketSource } | null> {
  const stores = await fetchStores()
  const lists = await Promise.all(stores.map(s => fetchChannels(s.id)))
  const channel = lists.flat().find(c => c.id === id)
  const store = channel && stores.find(s => s.id === channel.store_id)
  return store && channel ? { store, channel } : null
}

export function useMarketOrigin(target: MarketTarget | undefined) {
  const origin = ref<{ store: MarketStore; channel: MarketSource } | null>(null)
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
