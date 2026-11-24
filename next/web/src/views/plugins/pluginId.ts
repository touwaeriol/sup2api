// Plugin ids (CONTRACTS §137): copies of sdk/manifest's id rules for the
// router guard and route params; a payload's display_id is what pages show.

/** Copies of sdk/manifest: the official publisher, the longest plugin id, the key and publisher rules of check. */
export const OFFICIAL_PUBLISHER = 'sub2api'
const PLUGIN_ID_MAX = 30
const PLUGIN_KEY = /^[a-z](_?[a-z0-9]){1,29}$/
const PUBLISHER_ID = /^[a-z0-9](_?[a-z0-9])*$/
/** check.PublisherIDMaxLen: a publisher id that still makes a plugin id with the shortest key. */
export const PUBLISHER_ID_MAX = 25

/** manifest.ParsePluginID: an official id is the bare key, any other "_<publisher>__<key>"; null for neither. */
export function parsePluginId(id: string): { publisher: string; key: string } | null {
  if (!id.startsWith('_')) return id ? { publisher: OFFICIAL_PUBLISHER, key: id } : null
  const sep = id.indexOf('__', 1)
  const publisher = id.slice(1, Math.max(sep, 1))
  const key = sep < 0 ? '' : id.slice(sep + 2)
  return publisher && key ? { publisher, key } : null
}

/** manifest.DisplayID, how people see a plugin id: "codex", or "erio/codex" for another publisher's. */
export function displayPluginId(id: string): string {
  const p = parsePluginId(id)
  return !p || p.publisher === OFFICIAL_PUBLISHER ? id : `${p.publisher}/${p.key}`
}

/** A plugin id the core can hold (manifest.PluginID of a valid publisher and key, at most 30 bytes). */
export function isPluginKey(id: string): boolean {
  const p = parsePluginId(id)
  if (!p || id.length > PLUGIN_ID_MAX || !PLUGIN_KEY.test(p.key)) return false
  if (p.publisher === OFFICIAL_PUBLISHER) return !id.startsWith('_')
  return PUBLISHER_ID.test(p.publisher) && p.publisher.length <= PUBLISHER_ID_MAX
}

/** The prefix of every table, index and sequence of the plugin (by its id) in the core's schema (manifest.TablePrefix). */
export const tablePrefix = (id: string) => `plg_${id}__`
