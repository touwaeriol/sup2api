import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { fileURLToPath, URL } from 'node:url'
import { watch } from 'vue'
import type { UIPlugin } from '@/api/types'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@sub2api/host', async importOriginal => {
  const real = await importOriginal<typeof import('@sub2api/host')>()
  const ctx = { i18n: { t: (k: string) => k, addMessages: () => undefined }, permissions: { has: () => true } }
  return { ...real, api: { ...real.api, get: mocks.get }, useHost: () => ctx }
})

import { usePluginStore } from './plugins'

// Native entries are real modules under __fixtures__ (asset_base points there).
const fixtures = fileURLToPath(new URL('./__fixtures__', import.meta.url))
const FAST = 'fast'
const SLOW = 'slow'
const CHANGED = 'changed'
const OLD = 'old'
const NEW = 'new'
const plugin = (key: string, entry: string): UIPlugin =>
  ({
    key,
    version: '1.0.0',
    name: key,
    asset_base: fixtures,
    native_entry: entry,
    trust: 'official',
    host_ui_compat: '*',
    menus: [],
    pages: {},
    slots: []
  }) as unknown as UIPlugin

const release = () => (globalThis as { __releaseSlowPlugin?: () => void }).__releaseSlowPlugin
const until = async (cond: () => unknown) => {
  for (let i = 0; i < 300 && !cond(); i++) await new Promise(r => setTimeout(r, 10))
}

describe('plugin store loading', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.get.mockReset()
    delete (globalThis as { __releaseSlowPlugin?: () => void }).__releaseSlowPlugin
  })

  it('publishes the list before the native modules finish and tracks each plugin on its own', async () => {
    mocks.get.mockResolvedValue([plugin(FAST, 'fast-plugin.ts'), plugin(SLOW, 'slow-plugin.ts')])
    const store = usePluginStore()
    const done = store.refresh()

    await until(() => store.listed)
    expect(store.listed).toBe(true)
    expect(store.uiPlugins.map(p => p.key)).toEqual([FAST, SLOW])

    await until(() => store.component(FAST, 'FastPage') && release())
    expect(store.component(FAST, 'FastPage')).toBeTruthy()
    expect(store.pending[FAST]).toBeUndefined()
    expect(store.pending[SLOW]).toBe(true)

    // An overlapping refresh leaves the still-loading plugin to the first call.
    const releaseFirst = release()
    await store.refresh()
    expect(release()).toBe(releaseFirst)
    expect(store.pending[SLOW]).toBe(true)

    release()!()
    await done
    expect(store.component(SLOW, 'SlowPage')).toBeTruthy()
    expect(store.pending[SLOW]).toBeUndefined()
  })

  it('drops a plugin removed by a refresh that ran while it was loading', async () => {
    mocks.get.mockResolvedValueOnce([plugin(SLOW, 'slow-plugin.ts')])
    const store = usePluginStore()
    const done = store.refresh()
    await until(() => release())

    mocks.get.mockResolvedValueOnce([])
    await store.refresh()
    release()!()
    await done
    expect(store.component(SLOW, 'SlowPage')).toBeUndefined()
    expect(store.pending[SLOW]).toBeUndefined()
  })

  it('a plugin that fails to import or register only records its own error and leaves nothing behind', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const g = globalThis as { __throwPluginUnregistered?: number }
    delete g.__throwPluginUnregistered
    mocks.get.mockResolvedValue([
      plugin('missing', 'missing-plugin.ts'),
      plugin('throws', 'throw-plugin.ts'),
      plugin(FAST, 'fast-plugin.ts')
    ])
    const store = usePluginStore()
    await store.refresh()

    expect(store.errors.missing).toBeTruthy()
    expect(store.errors.throws).toBe('register blew up')
    expect(store.pending).toEqual({})
    expect(store.components.throws).toBeUndefined()
    expect(g.__throwPluginUnregistered).toBe(1)
    expect(store.component(FAST, 'FastPage')).toBeTruthy()
    expect(store.errors[FAST]).toBeUndefined()

    // The next refresh tries again from a clean slate.
    await store.refresh()
    expect(store.components.throws).toBeUndefined()
    expect(g.__throwPluginUnregistered).toBe(2)
  })

  it('marks a changed plugin pending before its old module is unloaded', async () => {
    mocks.get.mockResolvedValueOnce([plugin(CHANGED, 'fast-plugin.ts')])
    const store = usePluginStore()
    await store.refresh()
    expect(store.component(CHANGED, 'FastPage')).toBeTruthy()

    const pendingWhenGone: unknown[] = []
    watch(
      () => store.components[CHANGED],
      c => !c && pendingWhenGone.push(store.pending[CHANGED]),
      { flush: 'sync' }
    )
    mocks.get.mockResolvedValueOnce([plugin(CHANGED, 'slow-plugin.ts')])
    const done = store.refresh()
    await until(() => release())
    expect(pendingWhenGone).toEqual([true])
    release()!()
    await done
    expect(store.component(CHANGED, 'SlowPage')).toBeTruthy()
  })

  it('keeps the newer list when an older GET /ui/plugins answers last', async () => {
    let answerOld: (v: UIPlugin[]) => void = () => {}
    mocks.get.mockReturnValueOnce(new Promise(r => (answerOld = r))).mockResolvedValueOnce([plugin(FAST, 'fast-plugin.ts')])
    const store = usePluginStore()
    const older = store.refresh()
    await store.refresh()
    answerOld([plugin(OLD, 'fast-plugin.ts')])
    await older
    expect(store.uiPlugins.map(p => p.key)).toEqual([FAST])
    expect(store.component(OLD, 'FastPage')).toBeUndefined()
  })

  it('stays empty when reset (logout) lands while the list is loading', async () => {
    let answer: (v: UIPlugin[]) => void = () => {}
    mocks.get.mockReturnValueOnce(new Promise(r => (answer = r)))
    const store = usePluginStore()
    const done = store.refresh()
    await store.reset()
    answer([plugin(FAST, 'fast-plugin.ts')])
    await done
    expect(store.uiPlugins).toEqual([])
    expect(store.listed).toBe(false)
    expect(store.components).toEqual({})
    expect(store.pending).toEqual({})
  })

  it('lists when GET /ui/plugins fails', async () => {
    mocks.get.mockRejectedValue(new Error('down'))
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const store = usePluginStore()
    await store.refresh()
    expect(store.listed).toBe(true)
    expect(store.uiPlugins).toEqual([])
  })
})

describe('plugin store slots', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.get.mockReset()
  })

  it('hands down the navigation entry of an account editor section (UI 3.7)', async () => {
    const section = { slot: 'account.editor.section', component: 'FastPage', label: { en: 'Demo' }, icon: 'chart', accountTypes: ['t'] }
    mocks.get.mockResolvedValue([
      { ...plugin(FAST, 'fast-plugin.ts'), slots: [section, { slot: 'account.editor.section', component: 'Missing' }] }
    ])
    const store = usePluginStore()
    await store.refresh()
    expect(store.slotEntries('account.editor.section')).toEqual([
      {
        pluginKey: FAST,
        name: 'FastPage',
        component: store.component(FAST, 'FastPage'),
        label: { en: 'Demo' },
        icon: 'chart',
        accountTypes: ['t']
      }
    ])
  })
})

describe('plugin store outdated native UIs', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.get.mockReset()
  })

  it('tells a trusted native UI built for an older or a newer host UI contract and does not load it', async () => {
    mocks.get.mockResolvedValue([
      { ...plugin(OLD, 'fast-plugin.ts'), host_ui_compat: '^3.20' },
      { ...plugin(FAST, 'fast-plugin.ts'), host_ui_compat: '^4.0' },
      { ...plugin(NEW, 'fast-plugin.ts'), host_ui_compat: '^4.2' },
      { ...plugin('community', 'fast-plugin.ts'), host_ui_compat: '^3.20', trust: 'community' }
    ])
    const store = usePluginStore()
    await store.refresh()
    expect([OLD, FAST, NEW, 'community', 'gone'].map(k => store.uiMismatch(k))).toEqual(['plugin', null, 'core', null, null])
    expect(store.component(OLD, 'FastPage')).toBeUndefined()
    expect(store.component(NEW, 'FastPage')).toBeUndefined()
    expect(store.errors[OLD]).toBe('hostUICompat ^3.20 does not match host UI 4.1.0')
    expect(store.errors[NEW]).toBe('hostUICompat ^4.2 does not match host UI 4.1.0')
  })
})
