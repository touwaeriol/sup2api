import type { PluginHost } from '@sub2api/host'

// Plugin-scoped host handed to register(); the settings form reads it.
let host: PluginHost | null = null

export function setHost(h: PluginHost | null) {
  host = h
}

export function useMailerHost(): PluginHost {
  if (!host) throw new Error('mailer UI used before register(host)')
  return host
}
