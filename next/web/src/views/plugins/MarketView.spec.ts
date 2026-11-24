import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { INSTALLED, STORE, channel } from '@/testing/marketFixtures'

const mocks = vi.hoisted(() => ({ firstPage: vi.fn(), raw: vi.fn(), push: vi.fn() }))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), requestRaw: mocks.raw }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }), RouterLink: { props: ['to'], template: '<a><slot /></a>' } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
enableAutoUnmount(afterEach)

import MarketView from './MarketView.vue'

const CHANNELS = [channel(1), channel(2, { name: '测试', branch: 'develop' }), channel(3, { enabled: false })]
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
    mocks.firstPage.mockImplementation(async (path: string) => (path === '/market/sources' ? CHANNELS : INSTALLED))
    mocks.raw.mockResolvedValue({ data: STORE, host_version: '0.1.5' })
  })

  it('opens the default channel: the store repository, its branch and read-only index URL', async () => {
    const w = await render()
    expect(w.get('[data-testid="market-repo"]').text()).toBe('touwaeriol/sup2api-plugin')
    const url = w.get('[data-testid="market-store-url"] input, input[data-testid="market-store-url"]')
    expect((url.element as HTMLInputElement).value).toBe(CHANNELS[0].url)
    expect(url.attributes('readonly')).toBeDefined()
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 1 } })
    // The disabled channel is not offered.
    expect(w.findAll('[data-testid="market-channels"] input').map(i => (i.element as HTMLInputElement).value)).toEqual(['1', '2'])
  })

  it('switches channels, lists the new one and remembers it; the next visit opens it', async () => {
    const w = await render()
    await w.get('[data-testid="market-channels"] input[value="2"]').setValue(true)
    await flushPromises()
    expect(mocks.raw).toHaveBeenLastCalledWith('GET', '/market/plugins', { query: { source_id: 2 } })
    expect(localStorage.getItem('s2a.market.channel')).toBe('2')
    w.unmount()
    mocks.raw.mockClear()
    await render()
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 2 } })
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

  it('says the store is unavailable without an enabled channel and lists nothing', async () => {
    mocks.firstPage.mockResolvedValue([channel(1, { enabled: false })])
    const w = await render()
    expect(w.text()).toContain(tt('market.storeUnavailable'))
    expect(mocks.raw).not.toHaveBeenCalled()
  })
})
