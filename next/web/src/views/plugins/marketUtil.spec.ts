import { describe, expect, it } from 'vitest'
import type { MarketPlugin } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'
import {
  deprecatedVersion,
  marketStateOf,
  matches,
  newerIncompatible,
  noCompatible,
  offersVersion,
  pluginSource,
  publisherBlocked,
  publisherOptions,
  releaseTag,
  repoURL,
  shortSha,
  stateMatches,
  upgradesByPlugin
} from './marketUtil'

const reason = { reason: { en: 'old', zh: '旧' } }

function plugin(over: Partial<MarketPlugin> = {}): MarketPlugin {
  return {
    source_id: 1,
    source_name: 'official',
    id: '_acme__tool',
    key: 'tool',
    name: { en: 'Tool' },
    publisher: 'acme',
    publisher_name: { en: 'Acme' },
    trust: 'verified',
    publisher_status: 'active',
    repo: 'acme/tool',
    builtin: false,
    latest_version: '1.0.0',
    update_available: false,
    versions: [
      { version: '1.0.0', compatible: true },
      { version: '1.1.0', compatible: true, deprecated: reason },
      { version: '2.0.0', compatible: false }
    ],
    ...over
  }
}

describe('market versions', () => {
  it('reports a newer version this core cannot run, beside the latest one the core reports', () => {
    expect(newerIncompatible(plugin())).toBe('2.0.0')
    expect(newerIncompatible(plugin({ latest_version: '' }))).toBe('')
    expect(newerIncompatible(plugin({ versions: [{ version: '1.0.0', compatible: true }] }))).toBe('')
  })
  it('reports a listing no version of which runs on this core', () => {
    expect(noCompatible(plugin())).toBe(false)
    expect(noCompatible(plugin({ versions: [{ version: '2.0.0', compatible: false }] }))).toBe(true)
    expect(noCompatible(plugin({ versions: [] }))).toBe(false)
  })
})

describe('market publishers', () => {
  it('blocks the plugins of a revoked or unlisted publisher', () => {
    expect(publisherBlocked(plugin())).toBe(false)
    expect(publisherBlocked(plugin({ publisher_status: 'revoked' }))).toBe(true)
    expect(publisherBlocked(plugin({ publisher_status: 'missing' }))).toBe(true)
  })
  it('offers each publisher once, by name, and searches by it', () => {
    const items = [plugin(), plugin({ key: 'b' }), plugin({ key: 'c', publisher: 'zed', publisher_name: undefined }), plugin({ publisher: 'sub2api', publisher_name: { en: 'Sub2API' } })]
    expect(publisherOptions(items)).toEqual([
      { value: 'sub2api', label: 'Sub2API' },
      { value: 'acme', label: 'Acme' },
      { value: 'zed', label: 'zed' }
    ])
    expect(matches(plugin(), 'acme')).toBe(true)
    expect(matches(plugin(), 'acme/tool')).toBe(true)
    expect(matches(plugin(), 'nothing')).toBe(false)
  })
  it('links the GitHub repository', () => {
    expect(repoURL(plugin())).toBe('https://github.com/acme/tool')
    expect(repoURL(plugin({ repo: undefined }))).toBe('')
  })
})

const installed = (key: string, version: string | null, over: Partial<PluginSummary> = {}): PluginSummary =>
  ({ key, name: { en: key }, status: 'enabled', active_version: version, ...over }) as PluginSummary

describe('market state', () => {
  it('offers an upgrade only to the latest version, by the plugin row first', () => {
    expect(marketStateOf(plugin(), undefined)).toBe('install')
    expect(marketStateOf(plugin(), installed('_acme__tool', '0.9.0'))).toBe('upgrade')
    expect(marketStateOf(plugin(), installed('_acme__tool', '1.0.0'))).toBe('installed')
    expect(marketStateOf(plugin({ installed_version: '0.1.0' }), undefined)).toBe('upgrade')
    expect(marketStateOf(plugin(), installed('_acme__tool', null))).toBe('installed')
  })
  it('filters by state: updates, installed, not installed', () => {
    expect(['install', 'upgrade', 'installed'].filter(s => stateMatches(s as never, 'upgrade'))).toEqual(['upgrade'])
    expect(['install', 'upgrade', 'installed'].filter(s => stateMatches(s as never, 'installed'))).toEqual(['upgrade', 'installed'])
    expect(['install', 'upgrade', 'installed'].filter(s => stateMatches(s as never, 'notInstalled'))).toEqual(['install'])
    expect(['install', 'upgrade', 'installed'].filter(s => stateMatches(s as never, ''))).toHaveLength(3)
  })
  it('never offers a built-in plugin a version below the installed one', () => {
    const v = (version: string) => ({ version, compatible: true })
    expect(offersVersion(plugin({ builtin: true }), v('0.9.0'), '1.0.0')).toBe(false)
    expect(offersVersion(plugin(), v('0.9.0'), '1.0.0')).toBe(true)
    expect(offersVersion(plugin({ builtin: true }), v('1.1.0'), '1.0.0')).toBe(true)
    expect(offersVersion(plugin({ builtin: true }), v('1.0.0'), '1.0.0')).toBe(false)
    expect(offersVersion(plugin(), { version: '2.0.0', compatible: false }, '')).toBe(false)
  })
  it('notes the newest deprecated version, unless the whole plugin is deprecated', () => {
    expect(deprecatedVersion(plugin())?.version).toBe('1.1.0')
    expect(deprecatedVersion(plugin({ deprecated: reason }))).toBeUndefined()
  })
  it('counts the upgrades of installed plugins by plugin id, skipping blocked publishers', () => {
    const items = [plugin(), plugin({ id: 'codex', key: 'codex', publisher: 'sub2api' }), plugin({ id: '_bad__x', publisher_status: 'revoked' })]
    const rows = [installed('_acme__tool', '0.9.0'), installed('codex', '1.0.0'), installed('_bad__x', '0.1.0'), installed('_other__tool', '0.1.0')]
    expect(Object.keys(upgradesByPlugin(items, rows))).toEqual(['_acme__tool'])
  })
})

describe('market text', () => {
  it('shortens a sha256 and names a release by its tag', () => {
    expect(shortSha('9f3a6d0be41c7a28f5e0d93b1c66a4e2b8d17f0c5a93e4d2b16c78a0e5f4c21e')).toBe('9f3a…c21e')
    expect(releaseTag('https://github.com/a/b/releases/tag/plugin-codex-v0.2.3')).toBe('plugin-codex-v0.2.3')
    expect(releaseTag(undefined)).toBe('')
  })
})

describe('installed source', () => {
  it('is the store for an indexed version, else built-in or a manual upload', () => {
    expect(pluginSource({ signature_status: 'indexed', builtin: true })).toBe('market')
    expect(pluginSource({ signature_status: 'valid', builtin: true })).toBe('builtin')
    expect(pluginSource({ signature_status: 'valid', builtin: false })).toBe('upload')
  })
})
