// The console around a plugin's components in a spec, and the cleanup of
// what the helpers mounted.
import type { VueWrapper } from '@vue/test-utils'
import { provideHost, type ApiClient, type HostContext, type PluginHost } from '@sub2api/host'

type Messages = { en: Record<string, unknown> }

/** host.t over a plugin's English messages: a dotted key, {name} params filled in; an unknown key answers itself. */
export function messagesT(messages: Messages): (key: string, params?: Record<string, unknown>) => string {
  return (key, params) => {
    const v = key.split('.').reduce<unknown>((o, k) => (o && typeof o === 'object' ? (o as Record<string, unknown>)[k] : undefined), messages.en)
    const s = typeof v === 'string' ? v : key
    return params ? s.replace(/\{(\w+)\}/g, (m, k) => (params[k] === undefined ? m : String(params[k]))) : s
  }
}

/**
 * Provides the console (provideHost) to the components mounted next: it
 * grants the permissions in `granted`, and its own translation answers the
 * key. Returns the plugin's host, for the plugin's setHost: `t` over
 * `messages` (the key itself without them), `plugin.key`, `api`.
 */
export function fakeHost(opts: { granted?: string[]; messages?: Messages; plugin?: string; api?: ApiClient } = {}): PluginHost {
  const granted = opts.granted ?? []
  const consoleHost = {
    permissions: { superuser: () => false, has: (k: string) => granted.includes(k), any: (...k: string[]) => k.some((x) => granted.includes(x)) },
    i18n: { t: (k: string) => k }
  } as unknown as HostContext
  provideHost(consoleHost)
  return {
    ...consoleHost,
    t: opts.messages ? messagesT(opts.messages) : (k: string) => k,
    plugin: { key: opts.plugin ?? '' },
    api: opts.api,
    addMessages: () => {},
    registerComponent: () => {}
  } as unknown as PluginHost
}

const mounted: { unmount(): void }[] = []
const resets: (() => void)[] = []

/** @internal Remembers a wrapper (and what to undo) for cleanup(). */
export function track(w: VueWrapper, reset?: () => void): VueWrapper {
  mounted.push(w)
  if (reset) resets.push(reset)
  return w
}

/**
 * Unmounts what mountAccountForm / mountSettings mounted, undoes their
 * setHost, withdraws the console fakeHost provided (useHost() throws until
 * the next fakeHost(), so no permission carries over to the next case) and
 * empties <body>: `afterEach(cleanup)`.
 */
export function cleanup() {
  mounted.splice(0).forEach((w) => w.unmount())
  resets.splice(0).forEach((r) => r())
  provideHost(null as unknown as HostContext)
  document.body.innerHTML = ''
}
