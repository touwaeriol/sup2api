import { describe, expect, it } from 'vitest'
import type { MarketPlugin } from '@/api/types'
import { matches, newerIncompatible, noCompatible, publisherBlocked, publisherOptions, repoURL } from './marketUtil'

const reason = { reason: { en: 'old', zh: '旧' } }

function plugin(over: Partial<MarketPlugin> = {}): MarketPlugin {
  return {
    source_id: 1,
    source_name: 'official',
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
    const items = [plugin(), plugin({ key: 'b' }), plugin({ key: 'c', publisher: 'zed', publisher_name: undefined })]
    expect(publisherOptions(items)).toEqual([
      { value: 'acme', label: 'Acme' },
      { value: 'zed', label: 'zed' }
    ])
    expect(matches(plugin(), 'acme')).toBe(true)
    expect(matches(plugin(), 'nothing')).toBe(false)
  })
  it('links the GitHub repository', () => {
    expect(repoURL(plugin())).toBe('https://github.com/acme/tool')
    expect(repoURL(plugin({ repo: undefined }))).toBe('')
  })
})
