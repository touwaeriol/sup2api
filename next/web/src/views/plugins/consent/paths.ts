// Where the consent pages are: a version registered and awaiting consent,
// and an install not registered yet (from the market, or an uploaded file).
import type { RouteLocationRaw } from 'vue-router'

const enc = encodeURIComponent

export const pluginPath = (key: string) => `/plugins/${enc(key)}`
export const rolloutPath = (key: string) => `${pluginPath(key)}/rollout`

/** The consent page of a registered version awaiting consent. */
export function consentPath(key: string, version: string): string {
  return `${pluginPath(key)}/versions/${enc(version)}/consent`
}

/** The consent page of a market version before anything is downloaded or registered. */
export const MARKET_INSTALL_PATH = '/plugins/install/market'
export function marketInstallLocation(sourceId: number, key: string, version: string): RouteLocationRaw {
  return { path: MARKET_INSTALL_PATH, query: { source_id: String(sourceId), key, version } }
}

/** The consent page of the file chosen on the plugins page (held in memory: pendingUpload). */
export const UPLOAD_INSTALL_PATH = '/plugins/install/upload'

/** The plugin's page saying the version was approved, offering to enable or upgrade it by hand. */
export function consentedLocation(key: string, version: string): RouteLocationRaw {
  return { path: pluginPath(key), query: { consented: version } }
}
