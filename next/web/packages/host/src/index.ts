// Host API exposed to native plugin UIs and used by the console itself.
// The console calls provideHost() once at startup; plugins receive a
// plugin-scoped PluginHost in their register(host) function.

import type { Component, Ref } from 'vue'
import type { Router } from 'vue-router'
import { createClient, type ApiClient } from './http'

export * from './http'
export * from './routes'
export * from './bridge-protocol'
export { useList, type UseListOptions, type UseListResult } from './useList'

/** Version of the host UI contract; plugins declare hostUICompat against it. */
export const HOST_UI_VERSION = '3.14.0'

/**
 * Accounts per plugin account action request and per `GET /accounts?ids=`
 * (CONTRACTS §23.3 UI 3.6); the Go side is `manifest.MaxAccountBatch`.
 */
export const MAX_ACCOUNT_BATCH = 500

/** {"en": "...", "zh": "..."} or a bare string. */
export type LocalizedText = string | Record<string, string> | null | undefined

export type ToastKind = 'success' | 'error' | 'info' | 'warning'

export interface ConfirmOptions {
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

export interface HostI18n {
  /** Current locale ("zh" | "en"). */
  locale: Ref<string>
  t(key: string, params?: Record<string, unknown>): string
  /** Picks the current locale from a localized text, falling back to en. */
  text(value: LocalizedText): string
  /** Adds messages under a namespace (plugins: "plugin.<key>"). */
  addMessages(namespace: string, messages: Record<string, Record<string, unknown>>): void
  formatNumber(n: number | string | null | undefined, digits?: number): string
  formatMoney(v: number | string | null | undefined, digits?: number): string
  formatDateTime(v: string | number | Date | null | undefined): string
}

export interface HostPermissions {
  superuser(): boolean
  has(key: string): boolean
  any(...keys: string[]): boolean
}

/**
 * Core components handed to plugins (docs/COMMON-CONTROLS.md §3): the account
 * editors (UI 3.7) and the entity pickers (UI 3.11; `exclude` and the multiple
 * one UI 3.12). An editor's v-model is the
 * account API field as the core executes it; all take `disabled`:
 * - ParamOverrideEditor: `param_override` object | null; `models?: string[]` (preview suggestions)
 * - RateLimitEditor: `{ rpm_limit, tpm_limit, spm_limit, spm_window_seconds? }` (the window only once given or changed); `errors?: Record<string, string>`
 * - ModelLimitsEditor: `model_limits` array; `options?: string[]` (model suggestions), `errors?`
 * - HeaderPassthroughEditor: `header_passthrough` string[]
 * - ProxyPicker: `proxy_id` number | null
 * - ModelMappingEditor: `model_mapping` object; `models?: string[]` (the account's model list)
 * - UpstreamEditor: the account layer of `upstream`; `accountType` (its resolved declaration), `errors?`
 * - EntityPicker: picks a user / group / api_key / account / model / proxy (`kind`) by name or id from
 *   GET /lookup/{kind}. `v-model:id` string ('' = none; a model's id is its name); `v-model:text?` string
 *   (bound: typed text can be kept as a "name contains" search; id and text exclude each other); `change`
 *   ({ id, text }) after a pick or clear. `placeholder?`, `size?: 'sm' | 'md'`, `mine?` (the caller's own),
 *   `scope?` ({ user_id } for api_key, { prefer_group_id } for account), `extra?: { id, name, hint? }[]`
 *   (more candidates), `exclude?: (string | number)[]` (ids kept out of the list). A plain text box for a
 *   caller who may not look the kind up.
 * - EntityMultiPicker: picks several of a `kind`: chips of the picked ones, then an EntityPicker that adds
 *   one and lists only what is not picked yet. `v-model:ids` string[] in the order added; `change` (ids)
 *   after an add or remove. `max?` (no adding once reached); `placeholder?`, `size?`, `mine?`, `scope?`,
 *   `extra?` and `disabled?` as the EntityPicker's.
 */
export const HOST_COMPONENT_NAMES = [
  'ParamOverrideEditor',
  'RateLimitEditor',
  'ModelLimitsEditor',
  'HeaderPassthroughEditor',
  'ProxyPicker',
  'ModelMappingEditor',
  'UpstreamEditor',
  'EntityPicker',
  'EntityMultiPicker'
] as const
export type HostComponentName = (typeof HOST_COMPONENT_NAMES)[number]
export type HostComponents = Readonly<Record<HostComponentName, Component>>

export interface HostContext {
  version: string
  /** Core editors (UI 3.7) and the entity pickers (UI 3.11, 3.12): see HOST_COMPONENT_NAMES. */
  components: HostComponents
  api: ApiClient
  router: Router
  i18n: HostI18n
  permissions: HostPermissions
  theme: Ref<'light' | 'dark'>
  toast(message: string, kind?: ToastKind): void
  confirm(opts: ConfirmOptions): Promise<boolean>
}

/** Scoped host handed to a plugin's register(host). */
export interface PluginHost extends HostContext {
  plugin: { key: string; version: string; assetBase: string; trust: string }
  /** Client rooted at /api/v1/p/<key>. */
  pluginApi: ApiClient
  /** Makes a component available under the name used in manifest pages/slots. */
  registerComponent(name: string, component: Component): void
  /** Absolute URL of a file inside the plugin package (e.g. "ui/native/logo.svg"). */
  asset(path: string): string
  /** Plugin-local translation: t("x") looks up "plugin.<key>.x". */
  t(key: string, params?: Record<string, unknown>): string
  /** Registers plugin messages: {en: {...}, zh: {...}}. */
  addMessages(messages: Record<string, Record<string, unknown>>): void
  /** Plugin-local permission check: can("stats:read") -> plugin.<key>:stats:read. */
  can(permission: string): boolean
}

/** Shape of ui/native/entry.js. */
export interface NativePluginModule {
  register(host: PluginHost): void | Promise<void>
  unregister?(): void | Promise<void>
}

let ctx: HostContext | null = null

export function provideHost(c: HostContext) {
  ctx = c
}

/** Returns the host context (console or plugin code). */
export function useHost(): HostContext {
  if (!ctx) throw new Error('@sub2api/host: host context not initialised')
  return ctx
}

export function createPluginHost(
  base: HostContext,
  plugin: PluginHost['plugin'],
  registerComponent: (name: string, c: Component) => void
): PluginHost {
  const ns = `plugin.${plugin.key}`
  return {
    ...base,
    plugin,
    pluginApi: createClient(`/p/${plugin.key}`),
    registerComponent,
    asset: (path: string) => plugin.assetBase.replace(/\/$/, '') + '/' + path.replace(/^\//, ''),
    t: (key, params) => base.i18n.t(`${ns}.${key}`, params),
    addMessages: messages => base.i18n.addMessages(ns, messages),
    can: perm => base.permissions.has(`plugin.${plugin.key}:${perm}`)
  }
}

/** Minimal semver range check used for hostUICompat ("^1.0", ">=1.0.0 <2.0.0", "1.x", "*"). */
export function satisfiesRange(version: string, range: string | undefined | null): boolean {
  if (!range || range.trim() === '' || range.trim() === '*') return true
  const v = parse(version)
  if (!v) return false
  return range.split('||').some(alt =>
    alt
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .every(cmp => check(v, cmp))
  )
}

function parse(s: string): [number, number, number] | null {
  const m = /^v?(\d+)(?:\.(\d+|x|\*))?(?:\.(\d+|x|\*))?/.exec(s.trim())
  if (!m) return null
  const n = (x: string | undefined) => (x === undefined || x === 'x' || x === '*' ? 0 : Number(x))
  return [n(m[1]), n(m[2]), n(m[3])]
}

function cmpV(a: number[], b: number[]): number {
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] - b[i]
  return 0
}

function check(v: [number, number, number], c: string): boolean {
  const m = /^(\^|~|>=|<=|>|<|=)?(.+)$/.exec(c)
  if (!m) return false
  const op = m[1] || ''
  const raw = m[2]
  const b = parse(raw)
  if (!b) return false
  const parts = raw.replace(/^v/, '').split('.')
  switch (op) {
    case '^':
      return (
        cmpV(v, b) >= 0 &&
        (b[0] > 0 ? v[0] === b[0] : b[1] > 0 ? v[0] === 0 && v[1] === b[1] : cmpV(v, b) === 0 || (v[0] === 0 && v[1] === 0))
      )
    case '~':
      return cmpV(v, b) >= 0 && v[0] === b[0] && (parts.length < 2 || v[1] === b[1])
    case '>=':
      return cmpV(v, b) >= 0
    case '<=':
      return cmpV(v, b) <= 0
    case '>':
      return cmpV(v, b) > 0
    case '<':
      return cmpV(v, b) < 0
    default: {
      // "1", "1.x", "1.2.x" match prefixes; full versions match exactly
      const fixed = parts.filter(p => p !== 'x' && p !== '*').length
      return v.slice(0, fixed).every((x, i) => x === b[i])
    }
  }
}
