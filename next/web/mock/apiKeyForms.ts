import { readFileSync, existsSync } from 'node:fs'
import { join, normalize } from 'node:path'
import { nextPath } from './paths'

// Native account forms (form.mode "native") of the API key types in
// accounts.ts: the real plugins' forms from plugins/<key>/ui/native/dist
// (build them first), and a hand-written one (no build step) for videogen,
// which has no plugin source in this repository.

const PLUGINS = [
  { key: 'anthropic', version: '0.1.0', name: { en: 'Anthropic', zh: 'Anthropic' } },
  { key: 'openai', version: '0.1.0', name: { en: 'OpenAI', zh: 'OpenAI' } },
  { key: 'gemini', version: '0.1.0', name: { en: 'Gemini', zh: 'Gemini' } },
  { key: 'relay', version: '0.3.0', name: { en: 'Relay', zh: '中转' } },
  { key: 'videogen', version: '0.2.0', name: { en: 'Video generation', zh: '视频生成' } }
]

/** A GET /ui/plugins entry with no menu or page, only the native entry holding the account forms. */
export const nativeFormUIPlugin = (p: { key: string; version: string; name: unknown }) => ({
  ...p,
  asset_base: `/plugin-ui/${p.key}/${p.version}-dev`,
  menus: [],
  pages: {},
  slots: [],
  native_entry: 'ui/native/entry.js',
  trust: 'official',
  host_ui_compat: '^4.0'
})

export const apiKeyFormUIPlugins = PLUGINS.map(nativeFormUIPlugin)

const VIDEOGEN_ENTRY = `
import { createApiKeyForm } from '@sub2api/ui'
export function register(host) {
  host.registerComponent('VideoKeyForm', createApiKeyForm({
    baseUrl: false,
    extra: [{ key: 'region', kind: 'select', label: () => 'Region', options: ['us', 'eu', 'ap'].map((v) => ({ value: v, label: () => v })) }],
    defaults: { region: 'us' }
  }))
}
`

export function apiKeyFormAsset(key: string, path: string): { type: string; body: string | Buffer } | null {
  if (key === 'videogen') return path === 'ui/native/entry.js' ? { type: 'text/javascript', body: VIDEOGEN_ENTRY } : null
  return nativeDistAsset(key, path)
}

/** ui/native/<path> of a plugin from plugins/<dir>/ui/native/dist; null when absent. */
export function nativeDistAsset(dir: string, path: string): { type: string; body: Buffer } | null {
  if (!path.startsWith('ui/native/')) return null
  const rel = normalize(path.slice('ui/native/'.length))
  if (rel.startsWith('..')) return null
  const file = join(nextPath('plugins', dir, 'ui/native/dist'), rel)
  if (!existsSync(file)) return null
  return { type: rel.endsWith('.css') ? 'text/css' : 'text/javascript', body: readFileSync(file) }
}
