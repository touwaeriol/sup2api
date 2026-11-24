import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'

const official = 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index/index.json'
const mocks = vi.hoisted(() => ({ firstPage: vi.fn(), raw: vi.fn(), push: vi.fn() }))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), requestRaw: mocks.raw }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))

import MarketView from './MarketView.vue'

describe('MarketView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'en'
    mocks.firstPage.mockImplementation(async (path: string) =>
      path === '/market/sources' ? [{ id: 7, name: 'official', url: official, enabled: true }] : []
    )
    mocks.raw.mockResolvedValue({ data: [], host_version: '0.1.0' })
  })

  it('shows the official store address read-only and lists that store', async () => {
    const w = mount(MarketView, { global: { plugins: [i18n], stubs: { RouterLink: true } } })
    await flushPromises()
    const input = w.find('[data-testid="market-store-url"] input, input[data-testid="market-store-url"]')
    expect((input.element as HTMLInputElement).value).toBe(official)
    expect(input.attributes('readonly')).toBeDefined()
    expect(mocks.raw).toHaveBeenCalledOnce()
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 7 } })
  })

  it('says the store is unavailable without an enabled store and lists nothing', async () => {
    mocks.firstPage.mockResolvedValue([])
    const w = mount(MarketView, { global: { plugins: [i18n], stubs: { RouterLink: true } } })
    await flushPromises()
    expect(w.text()).toContain(i18n.global.t('plugins.market.storeUnavailable'))
    expect(mocks.raw).not.toHaveBeenCalled()
  })
})
