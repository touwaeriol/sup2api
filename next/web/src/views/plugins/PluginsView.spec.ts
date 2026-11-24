import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { STORE, installedPlugin, serveStore } from '@/testing/marketFixtures'

const mocks = vi.hoisted(() => ({ firstPage: vi.fn(), raw: vi.fn(), get: vi.fn(async () => []), perms: null as string[] | null }))
vi.mock('@/composables/lookups', async original => ({ ...(await original<object>()), fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), requestRaw: mocks.raw, api: { get: mocks.get, list: vi.fn(async () => ({ items: [] })) } }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }), RouterLink: { props: ['to'], template: '<a :data-to="JSON.stringify(to)"><slot /></a>' } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => !mocks.perms || mocks.perms.includes(p) }) }))
enableAutoUnmount(afterEach)

import PluginsView from './PluginsView.vue'

const INSTALLED = [
  installedPlugin('codex', '0.2.1', { builtin: true }),
  installedPlugin('gemini', '1.0.0', { signature_status: 'indexed' }),
  installedPlugin('_erio__codex', '1.0.0', { signature_status: 'indexed', trust: 'community' }),
  installedPlugin('_ourteam__billing', '0.1.0', { trust: 'community' })
]
const tt = (key: string, named: Record<string, unknown> = {}) => i18n.global.t(key, named)

async function render() {
  const w = mount(PluginsView, { global: { plugins: [i18n], stubs: { teleport: true, RetiredKeys: true } } })
  await flushPromises()
  return w
}
const row = (w: Awaited<ReturnType<typeof render>>, key: string) => w.findAll('tr').find(r => r.find(`[data-plugin-id="${key}"]`).exists())!

describe('PluginsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.perms = null
    i18n.global.locale.value = 'zh'
    mocks.firstPage.mockImplementation(serveStore({ installed: INSTALLED }))
    localStorage.clear()
    mocks.raw.mockResolvedValue({ data: STORE, host_version: '0.1.5' })
  })

  it('counts the upgrades against the official store default channel, whatever the viewer last browsed', async () => {
    localStorage.setItem('s2a.market', JSON.stringify({ store: 1, channels: { 1: 2 } }))
    const w = await render()
    expect(mocks.firstPage).not.toHaveBeenCalledWith('/market/sources', expect.anything())
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: {} })
    expect(w.get('[data-testid="plugins-updatable"]').text()).toBe(tt('market.updatable', { n: 2 }))
  })

  it('refreshes the list and the upgrades, the index fetched afresh', async () => {
    const w = await render()
    await w.get('[data-testid="plugins-refresh"]').trigger('click')
    await flushPromises()
    expect(mocks.raw).toHaveBeenLastCalledWith('GET', '/market/plugins', { query: { refresh: true } })
  })

  it('filters the plugins the store has a newer version of', async () => {
    const w = await render()
    await w.findComponent({ name: 'SSelect' }).vm.$emit('update:modelValue', 'upgrade')
    await flushPromises()
    expect(w.findAll('[data-plugin-id]').map(e => e.attributes('data-plugin-id'))).toEqual(['codex', '_erio__codex'])
  })

  it('shows the upgrade without a link to the consent to a viewer who cannot install', async () => {
    mocks.perms = ['plugin:read', 'plugin:market:read']
    const w = await render()
    const badge = row(w, 'codex').get('[data-testid="plugin-upgrade-badge"]')
    expect(badge.text()).toBe(tt('market.upgradeBadge', { version: '0.2.3' }))
    expect(badge.element.tagName).not.toBe('A')
    expect(row(w, 'codex').find('a[data-to]').exists()).toBe(false)
  })

  it('offers the upgrade of a plugin the store has newer, linking to its consent', async () => {
    const w = await render()
    const codex = row(w, 'codex')
    expect(codex.get('[data-testid="plugin-upgrade-badge"]').text()).toBe(tt('market.upgradeBadge', { version: '0.2.3' }))
    expect(codex.find('[data-testid="plugin-upgrade"]').exists()).toBe(true)
    expect(row(w, 'gemini').find('[data-testid="plugin-upgrade-badge"]').exists()).toBe(false)
    expect(row(w, '_erio__codex').get('[data-testid="plugin-upgrade-badge"]').text()).toBe(tt('market.upgradeBadge', { version: '1.2.0' }))
  })

  it('shows where each version came from and the id as publisher/key', async () => {
    const w = await render()
    const source = (key: string) => row(w, key).get('[data-source]').attributes('data-source')
    expect(source('codex')).toBe('builtin')
    expect(source('gemini')).toBe('market')
    expect(source('_ourteam__billing')).toBe('upload')
    expect(row(w, '_erio__codex').get('[data-plugin-id]').text()).toBe('erio/codex')
    expect(w.get('[data-testid="plugins-footnote"]').text()).toContain(tt('market.identityNote'))
  })

  it('does not read the store without the market permission', async () => {
    mocks.perms = ['plugin:read', 'plugin:install']
    const w = await render()
    expect(mocks.raw).not.toHaveBeenCalled()
    expect(w.find('[data-testid="plugins-market"]').exists()).toBe(false)
    expect(w.find('[data-testid="plugin-upgrade-badge"]').exists()).toBe(false)
  })
})
