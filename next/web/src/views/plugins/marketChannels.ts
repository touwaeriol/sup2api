// The plugin stores and channels as the console picks them (CONTRACTS §136):
// the market page opens the viewer's last store (kept in this browser), else
// the official one, and in it the viewer's last channel of that store, else
// its default one.
import type { MarketSource, MarketStore } from '@/api/marketTypes'

/** Managing the stores and their channels. */
export const MARKET_MANAGE_PERM = 'plugin:manage'

/** The official store's repository (market.OfficialRepo): the one store, whose publishers change by a pull request to it. */
export const OFFICIAL_STORE_REPO = 'touwaeriol/sup2api-plugin'

const STORAGE_KEY = 's2a.market'

/** What this viewer last looked at: the store, and the channel of each store. */
interface Remembered {
  store?: number
  channels?: Record<string, number>
}

/** The store and channel a store page prefers (null: none, the official store and the default channel). */
export interface MarketPrefs {
  store: () => number | null
  channel: (store: number) => number | null
}

export const NO_PREFS: MarketPrefs = { store: () => null, channel: () => null }

function read(): Remembered {
  try {
    const v = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
    return v && typeof v === 'object' ? v : {}
  } catch {
    return {}
  }
}

function write(r: Remembered) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(r))
  } catch {
    /* a browser that keeps nothing opens the official store's default channel next time */
  }
}

const positive = (v: unknown): number | null => (Number.isInteger(v) && (v as number) > 0 ? (v as number) : null)

/** The viewer's remembered store and channels. */
export const REMEMBERED: MarketPrefs = {
  store: () => positive(read().store),
  channel: store => positive(read().channels?.[store])
}

export function rememberStore(id: number) {
  write({ ...read(), store: id })
}

export function rememberChannel(store: number, id: number) {
  const r = read()
  write({ ...r, store, channels: { ...r.channels, [store]: id } })
}

/** The store to show: the preferred one, else the official one, else the first. */
export function pickStore(stores: MarketStore[], preferred: number | null): MarketStore | null {
  return stores.find(s => s.id === preferred) ?? stores.find(s => s.official) ?? stores[0] ?? null
}

/** The channel to show: the preferred one when it is enabled, else the default, else the first enabled one. */
export function pickChannel(channels: MarketSource[], preferred: number | null): MarketSource | null {
  const enabled = channels.filter(c => c.enabled)
  return enabled.find(c => c.id === preferred) ?? enabled.find(c => c.is_default) ?? enabled[0] ?? null
}
