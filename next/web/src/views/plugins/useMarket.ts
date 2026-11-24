// The plugin store as the store pages read it: the stores and the channels
// of the store being viewed (CONTRACTS §137.6), the listing of the channel
// being viewed with the running core's version, and the plugins installed
// here by plugin id (CONTRACTS §137).
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { requestRaw } from '@sub2api/host'
import type { MarketListing, MarketPlugin, MarketSource, MarketStore } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'
import { fetchFirstPage } from '@/composables/lookups'
import { notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import { marketInstallLocation } from './consent/paths'
import { installedVersionOf, marketStateOf, noCompatible, publisherBlocked } from './marketUtil'
import { NO_PREFS, pickChannel, pickStore, rememberChannel, rememberStore, type MarketPrefs } from './marketChannels'

/** The listing as the store pages keep it (no plugins until it loads). */
const emptyListing = (): MarketListing => ({ data: [], host_version: '' })

/**
 * GET /market/plugins of a channel, the official store's default one without
 * sourceId; refresh fetches the index afresh instead of the core's cached
 * copy. A raw envelope: host_version sits next to data.
 */
export async function fetchListing(sourceId?: number, refresh = false): Promise<MarketListing> {
  const query = { ...(sourceId ? { source_id: sourceId } : {}), ...(refresh ? { refresh: true } : {}) }
  const json = await requestRaw('GET', '/market/plugins', { query })
  return { ...emptyListing(), ...json, data: Array.isArray(json?.data) ? json.data : [] }
}

export const fetchStores = () => fetchFirstPage<MarketStore>('/market/stores')
/** The channels of store storeId, of every store without it. */
export const fetchChannels = (storeId?: number) => fetchFirstPage<MarketSource>('/market/sources', storeId ? { store_id: storeId } : {})

/** The plugins installed here by plugin id; none when they cannot be read. */
export async function fetchInstalled(): Promise<Record<string, PluginSummary>> {
  try {
    return Object.fromEntries((await fetchFirstPage<PluginSummary>('/plugins')).map(p => [p.key, p]))
  } catch {
    return {}
  }
}

function createState() {
  return {
    stores: ref<MarketStore[]>([]),
    /** The store being viewed (null when no store is configured). */
    store: ref<MarketStore | null>(null),
    /** The channels of the store being viewed. */
    channels: ref<MarketSource[]>([]),
    /** The channel being viewed (null when the store has no enabled channel). */
    channel: ref<MarketSource | null>(null),
    listing: ref<MarketListing>(emptyListing()),
    /** Counts the listing requests: only the newest one's answer is kept. */
    listingSeq: { n: 0 },
    /** The plugins installed here, by plugin id. */
    installed: ref<Record<string, PluginSummary>>({}),
    loadingStore: ref(true),
    loading: ref(false)
  }
}
type State = ReturnType<typeof createState>

/** Lists the channels of the store being viewed; the old store's are gone before the request, so none of them is listed meanwhile. */
async function loadChannels(s: State, prefs: MarketPrefs) {
  const store = s.store.value
  s.channels.value = []
  s.channel.value = null
  const list = store ? await fetchChannels(store.id) : []
  if (store !== s.store.value) return
  s.channels.value = list
  s.channel.value = store ? pickChannel(list, prefs.channel(store.id)) : null
}

async function loadStores(s: State, prefs: MarketPrefs) {
  s.loadingStore.value = true
  try {
    s.stores.value = await fetchStores()
    s.store.value = pickStore(s.stores.value, prefs.store())
    await loadChannels(s, prefs)
  } catch (e) {
    notifyError(e)
  } finally {
    s.loadingStore.value = false
  }
}

/** Lists the channel being viewed (afresh with refresh); an answer that a newer request overtook is dropped. */
async function loadListing(s: State, refresh = false) {
  const seq = ++s.listingSeq.n
  if (!s.channel.value) return void (s.listing.value = emptyListing())
  s.loading.value = true
  try {
    const listing = await fetchListing(s.channel.value.id, refresh)
    if (seq === s.listingSeq.n) s.listing.value = listing
  } catch (e) {
    if (seq !== s.listingSeq.n) return
    s.listing.value = emptyListing()
    notifyError(e)
  } finally {
    if (seq === s.listingSeq.n) s.loading.value = false
  }
}

/** Switches to another store, remembered for this viewer, and lists its channel (the viewer's last one there, else the default). */
async function selectStore(s: State, prefs: MarketPrefs, id: number) {
  const store = s.stores.value.find(x => x.id === id)
  if (!store || store.id === s.store.value?.id) return
  s.store.value = store
  rememberStore(id)
  try {
    await loadChannels(s, prefs)
  } catch (e) {
    notifyError(e)
  }
  return loadListing(s)
}

/** Switches to another channel of the store, remembered for this viewer, and lists it. */
function selectChannel(s: State, id: number) {
  const c = s.channels.value.find(x => x.id === id && x.enabled)
  if (!c || c.id === s.channel.value?.id) return
  s.channel.value = c
  rememberChannel(c.store_id, id)
  return loadListing(s)
}

/** Why m cannot be installed or upgraded at all ('' when it can), against the core version of the listing. */
function blockedOf(t: ReturnType<typeof useI18n>['t'], m: MarketPlugin, hostVersion: string): string {
  if (m.publisher_status === 'revoked') return t('market.revokedExplain')
  if (publisherBlocked(m)) return t('market.missingExplain')
  return noCompatible(m) ? t('market.noCompatible', { version: hostVersion || '?' }) : ''
}

/** The store pages' state; `prefs` names the store and channel to open (the official store's default channel when it names none). */
export function useMarket(prefs: MarketPrefs = NO_PREFS) {
  const { t } = useI18n()
  const auth = useAuthStore()
  const s = createState()
  const installedLoad = async () => {
    if (auth.has('plugin:read')) s.installed.value = await fetchInstalled()
  }
  return {
    ...s,
    items: computed(() => s.listing.value.data),
    hostVersion: computed(() => s.listing.value.host_version),
    enabledChannels: computed(() => s.channels.value.filter(c => c.enabled)),
    /** Lists the channel being viewed; refresh fetches its index afresh (the refresh button). */
    loadListing: (refresh = false) => loadListing(s, refresh),
    loadInstalled: installedLoad,
    /** Loads the stores, the chosen store's channels and the chosen channel's listing. */
    loadChannelListing: () => loadStores(s, prefs).then(() => loadListing(s)),
    /** Loads the chosen channel's listing (as loadChannelListing) and what is installed here. */
    load: () => Promise.all([loadStores(s, prefs).then(() => loadListing(s)), installedLoad()]),
    selectStore: (id: number) => selectStore(s, prefs, id),
    selectChannel: (id: number) => selectChannel(s, id),
    installedOf: (m: MarketPlugin) => installedVersionOf(m, s.installed.value[m.id]),
    stateOf: (m: MarketPlugin) => marketStateOf(m, s.installed.value[m.id]),
    blockedOf: (m: MarketPlugin) => blockedOf(t, m, s.listing.value.host_version),
    /** The consent page of a version of the channel being viewed: nothing is downloaded or registered before it. */
    installLocation: (m: MarketPlugin, version: string) => marketInstallLocation(s.channel.value?.id ?? 0, m, version)
  }
}

export type Market = ReturnType<typeof useMarket>
