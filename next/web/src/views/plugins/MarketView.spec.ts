import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { STORE, channel, serveStore, store } from '@/testing/marketFixtures'

const mocks = vi.hoisted(() => ({ firstPage: vi.fn(), raw: vi.fn(), push: vi.fn() }))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), requestRaw: mocks.raw }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }), RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
enableAutoUnmount(afterEach)

import MarketView from './MarketView.vue'
import StoreCard from './parts/StoreCard.vue'

const STORES = [store(1), store(2, { name: '团队商店' })]
const CHANNELS = [
  channel(1),
  channel(2, { name: '测试', branch: 'develop' }),
  channel(3, { enabled: false }),
  channel(4, { store_id: 2, name: '稳定', is_default: true }),
  channel(5, { store_id: 2, name: '尝鲜', is_default: false })
]
const tt = (key: string, named: Record<string, unknown> = {}) => i18n.global.t(key, named)

async function render() {
  const w = mount(MarketView, { global: { plugins: [i18n], stubs: { teleport: true } } })
  await flushPromises()
  return w
}
const row = (w: Awaited<ReturnType<typeof render>>, id: string) => w.get(`[data-plugin="${id}"]`)

describe('MarketView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    i18n.global.locale.value = 'zh'
    mocks.firstPage.mockImplementation(serveStore({ stores: STORES, channels: CHANNELS }))
    mocks.raw.mockResolvedValue({ data: STORE, host_version: '0.1.5' })
  })

  it('opens the official store on its default channel, showing names only, no repository or index address', async () => {
    const w = await render()
    expect(mocks.firstPage).toHaveBeenCalledWith('/market/sources', { store_id: 1 })
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 1 } })
    const card = w.get('[data-testid="market-store"]')
    expect(card.get('[data-testid="market-viewing"]').text()).toContain(tt('market.viewing', { store: '官方商店', channel: '正式' }))
    expect(card.text()).not.toContain('touwaeriol/sup2api-plugin')
    expect(card.text()).not.toContain('raw.githubusercontent.com')
    expect(card.find('[data-testid="market-branch"]').exists()).toBe(false)
    // The disabled channel is not offered.
    expect(w.findAll('[data-testid="market-channels"] input').map(i => (i.element as HTMLInputElement).value)).toEqual(['1', '2'])
    expect(w.get('[data-testid="market-store-settings"]').attributes('data-to')).toBe('/settings/market')
  })

  it('switches channels, lists the new one and remembers it; the next visit opens it', async () => {
    const w = await render()
    await w.get('[data-testid="market-channels"] input[value="2"]').setValue(true)
    await flushPromises()
    expect(mocks.raw).toHaveBeenLastCalledWith('GET', '/market/plugins', { query: { source_id: 2 } })
    w.unmount()
    mocks.raw.mockClear()
    await render()
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 2 } })
  })

  it('switches stores: lists the default channel of the store, and remembers the store and the channel of each', async () => {
    const w = await render()
    w.findComponent(StoreCard).vm.$emit('selectStore', 2)
    await flushPromises()
    expect(mocks.firstPage).toHaveBeenCalledWith('/market/sources', { store_id: 2 })
    expect(mocks.raw).toHaveBeenLastCalledWith('GET', '/market/plugins', { query: { source_id: 4 } })
    expect(w.findAll('[data-testid="market-channels"] input').map(i => (i.element as HTMLInputElement).value)).toEqual(['4', '5'])
    await w.get('[data-testid="market-channels"] input[value="5"]').setValue(true)
    await flushPromises()
    w.findComponent(StoreCard).vm.$emit('selectStore', 1)
    await flushPromises()
    expect(mocks.raw).toHaveBeenLastCalledWith('GET', '/market/plugins', { query: { source_id: 1 } })
    w.unmount()
    mocks.raw.mockClear()
    const again = await render()
    // The last store is the official one; back to the team store, its last channel opens.
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 1 } })
    again.findComponent(StoreCard).vm.$emit('selectStore', 2)
    await flushPromises()
    expect(mocks.raw).toHaveBeenLastCalledWith('GET', '/market/plugins', { query: { source_id: 5 } })
  })

  it('shows each state with its action and counts the updates', async () => {
    const w = await render()
    expect(row(w, 'codex').get('[data-testid="market-upgrade"]').text()).toBe(tt('market.upgradeTo', { version: '0.2.3' }))
    expect(row(w, 'codex').find('[data-testid="market-has-update"]').exists()).toBe(true)
    expect(row(w, 'gemini').find('[data-testid="market-up-to-date"]').exists()).toBe(true)
    expect(row(w, '_erio__codex').get('[data-plugin-id]').text()).toBe('erio/codex')
    expect(row(w, '_erio__codex').get('[data-testid="market-install"]').text()).toBe(tt('market.install'))
    expect(row(w, '_acme__log_export').get('[data-testid="market-install"]').text()).toBe(tt('market.installAnyway'))
    const revoked = row(w, '_shady__tool')
    expect(revoked.get('[data-testid="market-not-installable"]').attributes('disabled')).toBeDefined()
    expect(revoked.get('[data-testid="market-blocked"]').text()).toContain(tt('market.revokedExplain'))
    expect(w.get('[data-testid="market-summary"]').text()).toContain(tt('market.updatable', { n: 1 }))
  })

  it('filters by state', async () => {
    const w = await render()
    const filter = w.get('[data-testid="market-status-filter"] select, select[data-testid="market-status-filter"]')
    const pick = async (label: string) => {
      const index = filter.findAll('option').findIndex(o => o.text() === label)
      await filter.setValue(filter.findAll('option')[index].attributes('value'))
    }
    await pick(tt('market.status.upgrade'))
    expect(w.findAll('[data-plugin]').map(r => r.attributes('data-plugin'))).toEqual(['codex'])
    await pick(tt('market.status.notInstalled'))
    expect(w.findAll('[data-plugin]').map(r => r.attributes('data-plugin'))).toEqual(['_erio__codex', '_acme__log_export', '_shady__tool'])
  })

  it('reviews an upgrade first, naming the channel, publisher, key and version', async () => {
    const w = await render()
    await row(w, 'codex').get('[data-testid="market-upgrade"]').trigger('click')
    expect(mocks.push).toHaveBeenCalledWith({
      path: '/plugins/install/market',
      query: { source_id: '1', publisher: 'sub2api', key: 'codex', version: '0.2.3' }
    })
  })

  it('says the store has no enabled channel, keeping the store picker', async () => {
    mocks.firstPage.mockImplementation(serveStore({ stores: STORES, channels: [channel(1, { enabled: false })] }))
    const w = await render()
    expect(w.get('[data-testid="market-no-channel"]').text()).toContain(tt('market.noChannel'))
    expect(w.find('[data-testid="market-store"]').exists()).toBe(true)
    expect(mocks.raw).not.toHaveBeenCalled()
  })

  it('without a configured store, links to the store settings', async () => {
    mocks.firstPage.mockImplementation(serveStore({ stores: [] }))
    const w = await render()
    expect(w.get('[data-testid="market-no-store"]').text()).toContain(tt('market.noStore'))
    expect(w.get('[data-testid="market-configure"]').attributes('data-to')).toBe('/settings/market')
    expect(w.find('[data-testid="market-store"]').exists()).toBe(false)
    expect(mocks.raw).not.toHaveBeenCalled()
  })
})
