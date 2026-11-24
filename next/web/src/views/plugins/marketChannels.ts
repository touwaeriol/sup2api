// The plugin store's channels as the console picks them (CONTRACTS §136): a
// channel is a branch of the store repository; the market page opens the
// viewer's last channel (kept in this browser), else the default one.
import type { MarketSource } from '@/api/marketTypes'

/** The official store: one GitHub repository, fixed. */
export const OFFICIAL_STORE_REPO = 'touwaeriol/sup2api-plugin'

/** Managing the store's channels. */
export const MARKET_MANAGE_PERM = 'plugin:manage'

const STORAGE_KEY = 's2a.market.channel'

/** The channel this viewer last looked at (null when none, or the browser keeps nothing). */
export function rememberedChannel(): number | null {
  try {
    const v = Number(localStorage.getItem(STORAGE_KEY))
    return Number.isInteger(v) && v > 0 ? v : null
  } catch {
    return null
  }
}

export function rememberChannel(id: number) {
  try {
    localStorage.setItem(STORAGE_KEY, String(id))
  } catch {
    /* a browser that keeps nothing opens the default channel next time */
  }
}

/** The channel to show: the preferred one when it is enabled, else the default, else the first enabled one. */
export function pickChannel(channels: MarketSource[], preferred: number | null): MarketSource | null {
  const enabled = channels.filter(c => c.enabled)
  return enabled.find(c => c.id === preferred) ?? enabled.find(c => c.is_default) ?? enabled[0] ?? null
}
