// Store fixtures of the plugin store specs: channels, listings and installed
// plugins in the shapes of GET /market/sources, /market/plugins and /plugins.
import type { MarketPlugin, MarketSource } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'

export const channel = (id: number, over: Partial<MarketSource> = {}): MarketSource => ({
  id,
  name: id === 1 ? '正式' : `c${id}`,
  branch: id === 1 ? 'main' : `b${id}`,
  url: `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-${id === 1 ? 'main' : `b${id}`}/index.json`,
  enabled: true,
  is_default: id === 1,
  ...over
})

const id = (publisher: string, key: string) => (publisher === 'sub2api' ? key : `_${publisher}__${key}`)

/** A store plugin; its id follows its publisher and key. */
export function storePlugin(publisher: string, key: string, over: Partial<MarketPlugin> = {}): MarketPlugin {
  return {
    source_id: 1,
    source_name: '正式',
    id: id(publisher, key),
    key,
    name: { en: key, zh: key },
    publisher,
    publisher_name: { en: publisher },
    trust: publisher === 'sub2api' ? 'official' : 'community',
    publisher_status: 'active',
    builtin: false,
    latest_version: '1.0.0',
    update_available: false,
    versions: [{ version: '1.0.0', compatible: true, size: 1024, host_compat: '>=0.1.0', release_url: `https://github.com/x/releases/tag/${key}-v1.0.0` }],
    ...over
  }
}

export const installedPlugin = (key: string, version: string | null, over: Partial<PluginSummary> = {}): PluginSummary => ({
  key,
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
    versions: ['0.2.3', '0.2.2', '0.2.1', '0.2.0'].map(version => ({
      version,
      compatible: true,
      host_compat: '>=0.1.0',
      ...(version === '0.2.0' ? { deprecated: { reason: { en: 'leaks slots', zh: '并发位泄漏' } } } : {})
    }))
  }),
  storePlugin('sub2api', 'gemini'),
  storePlugin('erio', 'codex', { latest_version: '1.2.0', versions: [{ version: '1.2.0', compatible: true, host_compat: '>=0.1.4' }] }),
  storePlugin('acme', 'log_export', { deprecated: { reason: { en: 'replaced', zh: '已取代' } } }),
  storePlugin('shady', 'tool', { publisher_status: 'revoked' })
]

export const INSTALLED: PluginSummary[] = [installedPlugin('codex', '0.2.1', { builtin: true }), installedPlugin('gemini', '1.0.0')]
