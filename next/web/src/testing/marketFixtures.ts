// Store fixtures of the plugin store specs: stores, channels, listings and
// installed plugins in the shapes of GET /market/stores, /market/sources,
// /market/plugins and /plugins.
import type { MarketPlugin, MarketSource, MarketStore, MarketVersion } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'

/** A store: store 1 is the official one, the only store the core has. */
export const store = (id: number, over: Partial<MarketStore> = {}): MarketStore => ({
  id,
  name: id === 1 ? '官方商店' : `store${id}`,
  repo: id === 1 ? 'touwaeriol/sup2api-plugin' : `team${id}/s2-plugins`,
  official: id === 1,
  created_at: '2026-11-24T00:00:00Z',
  ...over
})

/** A channel of store 1 unless over names another. */
export const channel = (id: number, over: Partial<MarketSource> = {}): MarketSource => ({
  id,
  store_id: 1,
  store_name: '官方商店',
  name: id === 1 ? '正式' : `c${id}`,
  branch: id === 1 ? 'main' : `b${id}`,
  url: `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-${id === 1 ? 'main' : `b${id}`}/index.json`,
  enabled: true,
  is_default: id === 1,
  created_at: '2026-11-24T00:00:00Z',
  ...over
})

const id = (publisher: string, key: string) => (publisher === 'sub2api' ? key : `_${publisher}__${key}`)
const displayId = (publisher: string, key: string) => (publisher === 'sub2api' ? key : `${publisher}/${key}`)

/** A compatible version of a store plugin. */
export const marketVersion = (version: string, over: Partial<MarketVersion> = {}): MarketVersion => ({
  version,
  sha256: 'ab'.repeat(32),
  size: 1024,
  host_compat: '>=0.1.0',
  compatible: true,
  release_url: `https://github.com/x/releases/tag/v${version}`,
  ...over
})

/** A store plugin; its id follows its publisher and key. */
export function storePlugin(publisher: string, key: string, over: Partial<MarketPlugin> = {}): MarketPlugin {
  return {
    store_id: 1,
    store_name: '官方商店',
    source_id: 1,
    source_name: '正式',
    id: id(publisher, key),
    display_id: displayId(publisher, key),
    key,
    name: { en: key, zh: key },
    publisher,
    publisher_name: { en: publisher },
    trust: publisher === 'sub2api' ? 'official' : 'community',
    publisher_status: 'active',
    builtin: false,
    latest_version: '1.0.0',
    update_available: false,
    versions: [marketVersion('1.0.0', { release_url: `https://github.com/x/releases/tag/${key}-v1.0.0` })],
    ...over
  }
}

export const installedPlugin = (key: string, version: string | null, over: Partial<PluginSummary> = {}): PluginSummary => ({
  key,
  display_id: key.startsWith('_') ? key.slice(1).replace('__', '/') : key,
  name: { en: key, zh: key },
  status: version ? 'enabled' : 'awaiting_consent',
  active_version: version,
  desired_version: version,
  publisher: key.startsWith('_') ? key.slice(1, key.indexOf('__', 1)) : 'sub2api',
  trust: 'official',
  signature_status: 'valid',
  ...over
})

/**
 * The store as the specs serve it: codex installed at 0.2.1 with 0.2.3 the
 * latest (an upgrade), gemini up to date, erio/codex not installed, a plugin
 * deprecated as a whole and one of a revoked publisher.
 */
export const STORE: MarketPlugin[] = [
  storePlugin('sub2api', 'codex', {
    builtin: true,
    latest_version: '0.2.3',
    versions: ['0.2.3', '0.2.2', '0.2.1', '0.2.0'].map(version =>
      marketVersion(version, version === '0.2.0' ? { deprecated: { reason: { en: 'leaks slots', zh: '并发位泄漏' } } } : {})
    )
  }),
  storePlugin('sub2api', 'gemini'),
  storePlugin('erio', 'codex', { latest_version: '1.2.0', versions: [marketVersion('1.2.0', { host_compat: '>=0.1.4' })] }),
  storePlugin('acme', 'log_export', { deprecated: { reason: { en: 'replaced', zh: '已取代' } } }),
  storePlugin('shady', 'tool', { publisher_status: 'revoked' })
]

export const INSTALLED: PluginSummary[] = [installedPlugin('codex', '0.2.1', { builtin: true }), installedPlugin('gemini', '1.0.0')]

interface StoreData {
  stores?: MarketStore[]
  /** Channels of every store: a request gets those of its store_id, every one without. */
  channels?: MarketSource[]
  installed?: PluginSummary[]
}

/** fetchFirstPage as the store specs serve it: the stores, the channels (of store_id when given) and the installed plugins. */
export function serveStore(data: StoreData = {}) {
  return async (path: string, query: Record<string, unknown> = {}): Promise<unknown[]> => {
    if (path === '/market/stores') return data.stores ?? [store(1)]
    if (path === '/market/sources') return (data.channels ?? [channel(1)]).filter(c => query.store_id == null || c.store_id === Number(query.store_id))
    return data.installed ?? INSTALLED
  }
}

/** api.list as the store specs serve it (fetchFirstPage left real). */
export function serveStoreList(data: StoreData = {}) {
  const serve = serveStore(data)
  return async (path: string, query: Record<string, unknown> = {}) => ({ items: await serve(path, query) })
}
