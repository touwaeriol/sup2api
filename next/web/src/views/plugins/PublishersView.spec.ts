import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { SFormModal } from '@sub2api/ui'
import { i18n } from '@/i18n'
import { INSTALLED, STORE, installedPlugin, serveStore, storePlugin } from '@/testing/marketFixtures'
import { storePublishers } from './publishers/storePublishers'

const LOCAL = [{ id: 1, name: 'ourteam', trust_level: 'community', status: 'active', keys: [] }]
const mocks = vi.hoisted(() => ({
  firstPage: vi.fn(),
  raw: vi.fn(),
  list: vi.fn(async () => ({ items: [] as object[], page: { total: 0 } })),
  perms: null as string[] | null
}))
vi.mock('@/composables/lookups', async original => ({ ...(await original<object>()), fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), requestRaw: mocks.raw, api: { list: mocks.list, get: vi.fn(async () => ({})) } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => !mocks.perms || mocks.perms.includes(p) }) }))
enableAutoUnmount(afterEach)

import PublishersView from './PublishersView.vue'

describe('storePublishers', () => {
  it('derives one row per store publisher, the official one first, counting store and installed plugins', () => {
    const erio = { publisher_github: ['erio-dev'] }
    const items = [...STORE.map(m => (m.publisher === 'erio' ? { ...m, ...erio } : m)), storePlugin('erio', 'gemini', erio)]
    // A local plugin of the same key is not a store plugin installed here.
    const rows = storePublishers(items, [...INSTALLED, installedPlugin('_erio__codex', '1.2.0'), installedPlugin('_erio__other', '1.0.0')])
    expect(rows.map(r => r.id)).toEqual(['sub2api', 'acme', 'erio', 'shady'])
    expect(rows[0]).toMatchObject({ plugins: 2, installed: 2, trust: 'official', status: 'active' })
    expect(rows[2]).toMatchObject({ plugins: 2, installed: 1, github: ['erio-dev'] })
    expect(rows[3].status).toBe('revoked')
  })
})

describe('PublishersView', () => {
  async function render() {
    const w = mount(PublishersView, { global: { plugins: [i18n], stubs: { teleport: true } } })
    await flushPromises()
    return w
  }

  beforeEach(() => {
    vi.clearAllMocks()
    mocks.perms = null
    i18n.global.locale.value = 'zh'
    mocks.firstPage.mockImplementation(serveStore())
    mocks.raw.mockResolvedValue({ data: STORE, host_version: '0.1.5' })
    mocks.list.mockResolvedValue({ items: LOCAL, page: { total: 1 } })
  })

  it('opens the store tab, read-only, with both tabs counted', async () => {
    const w = await render()
    const tabs = w.findAll('[role="tab"]').map(t => t.text().replace(/\s+/g, ''))
    expect(tabs).toEqual([`${i18n.global.t('publishers.tabs.store')}4`, `${i18n.global.t('publishers.tabs.local')}1`])
    const store = w.get('[data-testid="store-publishers"]')
    expect(store.text()).toContain('sub2api')
    expect(store.findAll('button').length).toBe(0)
    expect(w.get('[data-testid="store-publishers-hint"]').text()).toContain('touwaeriol/sup2api-plugin')
    expect(w.get('[data-testid="local-publisher-id-rule"]').text()).toBe(i18n.global.t('publishers.local.idRule'))
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: {} })
    expect(w.find('[data-testid="publisher-create"]').exists()).toBe(false)
  })

  it('offers a new local publisher at the end of the tab bar, on the local tab only', async () => {
    const w = await render()
    await w.findAll('[role="tab"]')[1].trigger('click')
    await w.get('[data-testid="publisher-create"]').trigger('click')
    await flushPromises()
    expect(w.findAllComponents(SFormModal).filter(m => m.props('open')).map(m => m.props('title'))).toEqual([i18n.global.t('publishers.create')])
  })

  it('shows only the local publishers without the market permission', async () => {
    mocks.perms = ['plugin:read', 'plugin:manage']
    const w = await render()
    expect(mocks.raw).not.toHaveBeenCalled()
    expect(w.find('[data-testid="store-publishers"]').exists()).toBe(false)
    // The local tab alone: no tab bar.
    expect(w.findAll('[role="tab"]')).toHaveLength(0)
    expect(w.get('[data-testid="local-publisher-id-rule"]').isVisible()).toBe(true)
  })
})
