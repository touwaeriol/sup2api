// The plugin store as the store pages read it: the configured stores and the
// channels of the store being viewed (CONTRACTS §136), the listing of the
// channel being viewed with the running core's version, and the plugins
// installed here by plugin id (CONTRACTS §137).
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

/** GET /market/plugins of a channel: a raw envelope, host_version sits next to data. */
export async function fetchListing(sourceId: number): Promise<MarketListing> {
  const json = await requestRaw('GET', '/market/plugins', { query: { source_id: sourceId } })
  return { ...emptyListing(), ...json, data: Array.isArray(json?.data) ? json.data : [] }
}

export const fetchStores = () => fetchFirstPage<MarketStore>('/market/stores')
export const fetchChannels = (storeId: number) => fetchFirstPage<MarketSource>('/market/sources', { store_id: storeId })

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
    /** The plugins installed here, by plugin id. */
    installed: ref<Record<string, PluginSummary>>({}),
    loadingStore: ref(true),
    loading: ref(false)
  }
}
type State = ReturnType<typeof createState>

async function loadChannels(s: State, prefs: MarketPrefs) {
  const store = s.store.value
  s.channels.value = store ? await fetchChannels(store.id) : []
  s.channel.value = store ? pickChannel(s.channels.value, prefs.channel(store.id)) : null
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

async function loadListing(s: State) {
  if (!s.channel.value) return void (s.listing.value = emptyListing())
  s.loading.value = true
  try {
    s.listing.value = await fetchListing(s.channel.value.id)
  } catch (e) {
    s.listing.value = emptyListing()
    notifyError(e)
  } finally {
    s.loading.value = false
  }
}

async function loadInstalled(s: State) {
  try {
    s.installed.value = Object.fromEntries((await fetchFirstPage<PluginSummary>('/plugins')).map(p => [p.key, p]))
  } catch {
    s.installed.value = {}
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

/** The store pages' state; `prefs` names the store and channel to open (the official store's default channel when it names none). */
export function useMarket(prefs: MarketPrefs = NO_PREFS) {
  const { t } = useI18n()
  const auth = useAuthStore()
  const s = createState()
  /** Why m cannot be installed or upgraded at all ('' when it can). */
  const blockedOf = (m: MarketPlugin): string => {
    if (m.publisher_status === 'revoked') return t('market.revokedExplain')
    if (publisherBlocked(m)) return t('market.missingExplain')
    return noCompatible(m) ? t('market.noCompatible', { version: s.listing.value.host_version || '?' }) : ''
  }
  const installedLoad = () => (auth.has('plugin:read') ? loadInstalled(s) : Promise.resolve())
  return {
    ...s,
    items: computed(() => s.listing.value.data),
    hostVersion: computed(() => s.listing.value.host_version),
    enabledChannels: computed(() => s.channels.value.filter(c => c.enabled)),
    loadListing: () => loadListing(s),
    loadInstalled: installedLoad,
    /** Loads the stores, the chosen store's channels and the chosen channel's listing. */
    loadChannelListing: () => loadStores(s, prefs).then(() => loadListing(s)),
    /** Loads the chosen channel's listing (as loadChannelListing) and what is installed here. */
    load: () => Promise.all([loadStores(s, prefs).then(() => loadListing(s)), installedLoad()]),
    selectStore: (id: number) => selectStore(s, prefs, id),
    selectChannel: (id: number) => selectChannel(s, id),
    installedOf: (m: MarketPlugin) => installedVersionOf(m, s.installed.value[m.id]),
    stateOf: (m: MarketPlugin) => marketStateOf(m, s.installed.value[m.id]),
    blockedOf,
    /** The consent page of a version of the channel being viewed: nothing is downloaded or registered before it. */
    installLocation: (m: MarketPlugin, version: string) => marketInstallLocation(s.channel.value?.id ?? 0, m, version)
  }
}

export type Market = ReturnType<typeof useMarket>
