// A native settings form (usePluginSettings + SSettingsPage) in a spec,
// against a fake core for GET / PUT /plugins/<key>/settings.
import { expect } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Component } from 'vue'
import type { ApiClient, PluginHost } from '@sub2api/host'
import SSettingsPage from '../form/SSettingsPage.vue'
import type { PluginSettingsForm } from '../form/pluginSettings'
import { SECRET_MASK } from '../form/rules'
import { fakeHost, track } from './host'

type M = Record<string, any>
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v))

export interface FakeCore {
  /** host.api answering GET / PUT /plugins/<plugin>/settings. */
  api: ApiClient
  /** The values of each PUT, in order. */
  puts: M[]
  /** The stored values (secrets unmasked). */
  stored(): M
  /** Every PUT from now on fails with this error (after recording its values). */
  refuse(e: unknown): void
}

/**
 * The core's settings endpoints for `plugin`, holding `values`. It reads
 * the `secrets` (top-level keys, manifest ui.settings.secrets) back as
 * SECRET_MASK when set, and a PUT of SECRET_MASK keeps the stored value, as
 * the core does; a PUT stores the values otherwise as they are.
 */
export function fakeCore(plugin: string, values: M, secrets: string[] = []): FakeCore {
  const path = `/plugins/${plugin}/settings`
  let stored: M = clone(values)
  const puts: M[] = []
  let refusal: unknown = null
  const api = {
    get: async (p: string) => {
      expect(p).toBe(path)
      return { values: Object.fromEntries(Object.entries(stored).map(([k, v]) => [k, secrets.includes(k) && v ? SECRET_MASK : v])) }
    },
    put: async (p: string, body: { values: M }) => {
      expect(p).toBe(path)
      puts.push(clone(body.values))
      if (refusal) throw refusal
      const next = { ...body.values }
      for (const k of secrets) if (next[k] === SECRET_MASK) next[k] = stored[k]
      stored = clone(next)
      return null
    }
  } as unknown as ApiClient
  return { api, puts, stored: () => stored, refuse: (e) => (refusal = e) }
}

export interface MountedSettings {
  w: VueWrapper
  core: FakeCore
  /** The form the page renders (SSettingsPage's `settings`). */
  settings: PluginSettingsForm<M>
}

/**
 * Mounts a plugin's settings component (it renders SSettingsPage) after
 * loading: the console grants `granted` (default plugin:manage), the
 * plugin's host (handed to `setHost`, undone by cleanup()) answers keys
 * with themselves and reaches fakeCore(plugin, values, secrets).
 */
export async function mountSettings(
  component: object,
  opts: { plugin: string; setHost: (h: PluginHost | null) => void; values?: M; granted?: string[]; secrets?: string[] }
): Promise<MountedSettings> {
  const core = fakeCore(opts.plugin, opts.values ?? {}, opts.secrets)
  opts.setHost(fakeHost({ granted: opts.granted ?? ['plugin:manage'], plugin: opts.plugin, api: core.api }))
  const w = track(mount(component as Component, { attachTo: document.body }), () => opts.setHost(null))
  await flushPromises()
  return { w, core, settings: w.findComponent(SSettingsPage).props('settings') as PluginSettingsForm<M> }
}
