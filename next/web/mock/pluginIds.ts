// Plugin ids as the core makes them (sdk/manifest PluginID, DisplayID): the
// bare key of a sub2api plugin, "_<publisher>__<key>" for any other.
export const OFFICIAL_PUBLISHER = 'sub2api'

export const pluginId = (publisher: string, key: string) => (publisher === OFFICIAL_PUBLISHER ? key : `_${publisher}__${key}`)

export function displayId(id: string): string {
  const m = /^_([^_](?:_?[^_])*)__(.+)$/.exec(id)
  return m ? `${m[1]}/${m[2]}` : id
}
