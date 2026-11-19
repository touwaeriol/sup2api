import type { PluginHost } from '@sub2api/host'

// Plugin-scoped host handed to register(); the forms read their messages
// through it (plugin.vertex.*).
let host: PluginHost | null = null

export function setHost(h: PluginHost | null) {
  host = h
}

export function useVertexHost(): PluginHost {
  if (!host) throw new Error('vertex UI used before register(host)')
  return host
}
