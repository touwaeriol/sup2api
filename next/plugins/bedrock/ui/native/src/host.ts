import type { PluginHost } from '@sub2api/host'

// Plugin-scoped host handed to register(); the form reads its messages
// through it (plugin.bedrock.*).
let host: PluginHost | null = null

export function setHost(h: PluginHost | null) {
  host = h
}

export function useBedrockHost(): PluginHost {
  if (!host) throw new Error('bedrock UI used before register(host)')
  return host
}
