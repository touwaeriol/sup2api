import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { STORE, channel, serveStore, store } from '@/testing/marketFixtures'

const mocks = vi.hoisted(() => ({
  firstPage: vi.fn(),
  raw: vi.fn(),
  push: vi.fn(),
  route: { params: { publisher: 'sub2api', key: 'codex' } as Record<string, string>, query: { store: '2', channel: '4' } as Record<string, string> }
}))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), requestRaw: mocks.raw }))
vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.push }),
  RouterLink: { props: ['to'], template: '<a><slot /></a>' }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
enableAutoUnmount(afterEach)

import MarketPluginView from './MarketPluginView.vue'

const tt = (key: string, named: Record<string, unknown> = {}) => i18n.global.t(key, named)

async function render() {
  const w = mount(MarketPluginView, { global: { plugins: [i18n], stubs: { teleport: true } } })
  await flushPromises()
  return w
}

describe('MarketPluginView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.route.params = { publisher: 'sub2api', key: 'codex' }
    const channels = [channel(1), channel(3, { store_id: 2, name: '稳定', is_default: true }), channel(4, { store_id: 2, name: '测试', is_default: false })]
    mocks.firstPage.mockImplementation(serveStore({ stores: [store(1), store(2, { name: '团队商店' })], channels }))
    mocks.raw.mockResolvedValue({ data: STORE, host_version: '0.1.5' })
  })

  it('lists the versions of the store and channel the market was viewing', async () => {
    const w = await render()
    expect(mocks.firstPage).toHaveBeenCalledWith('/market/sources', { store_id: 2 })
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 4 } })
    expect(w.get('[data-testid="market-detail-channel"]').text()).toBe(i18n.global.t('market.detail.channel', { store: '团队商店', name: '测试' }))
    expect(w.findAll('[data-testid="market-versions-table"] [data-version]').map(c => c.attributes('data-version'))).toEqual(['0.2.3', '0.2.2', '0.2.1', '0.2.0'])
  })

  it('marks the latest, installed and deprecated versions and never offers a built-in plugin a lower one', async () => {
    const w = await render()
    const status = (v: string) => w.get(`[data-version="${v}"]`).text()
    expect(status('0.2.3')).toContain(tt('market.detail.latest'))
    expect(status('0.2.1')).toContain(tt('market.detail.installed'))
    expect(status('0.2.0')).toContain(tt('market.deprecated'))
    expect(w.get('[data-testid="version-note-0.2.1"]').text()).toBe(tt('market.detail.current'))
    expect(w.get('[data-testid="version-note-0.2.0"]').text()).toBe(tt('market.detail.belowInstalled'))
    expect(w.find('[data-testid="version-install-0.2.0"]').exists()).toBe(false)
    expect(w.get('[data-testid="version-install-0.2.2"]').text()).toBe(tt('market.detail.upgradeToThis'))
    expect(w.find('[data-testid="market-builtin-floor"]').exists()).toBe(true)
  })

  it('reviews a picked version first, from that channel', async () => {
    const w = await render()
    await w.get('[data-testid="version-install-0.2.2"]').trigger('click')
    expect(mocks.push).toHaveBeenCalledWith({ path: '/plugins/install/market', query: { source_id: '4', publisher: 'sub2api', key: 'codex', version: '0.2.2' } })
    await w.get('[data-testid="market-upgrade"]').trigger('click')
    expect(mocks.push).toHaveBeenLastCalledWith({ path: '/plugins/install/market', query: { source_id: '4', publisher: 'sub2api', key: 'codex', version: '0.2.3' } })
  })

  it('shows the publisher card, and tells a third-party plugin apart by its publisher', async () => {
    mocks.route.params = { publisher: 'erio', key: 'codex' }
    const w = await render()
    expect(w.get('[data-testid="market-publisher-card"]').text()).toContain('erio')
    expect(w.get('[data-plugin-id]').text()).toBe('erio/codex')
    expect(w.get('[data-testid="market-install"]').text()).toBe(tt('market.install'))
  })

  it('says so for a plugin the store does not list', async () => {
    mocks.route.params = { publisher: 'nobody', key: 'codex' }
    const w = await render()
    expect(w.text()).toContain(tt('market.detail.notFound'))
  })
})
