import type { PluginHost } from '@sub2api/host'

// Plugin-scoped host handed to register(); shared by the volcengine
// components and the texts of the account forms (functions, so they follow
// the current locale).
let host: PluginHost | null = null

export function setHost(h: PluginHost | null) {
  host = h
}

export function useVolcHost(): PluginHost {
  if (!host) throw new Error('volcengine UI used before register(host)')
  return host
}

/** A plugin message as a function of the current locale: text('apikey.keyHelp'). */
export const text = (key: string) => () => useVolcHost().t(key)
