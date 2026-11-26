import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import type { SettingsView } from '../topup'
import { useTopupSettings } from './useTopupSettings'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))

const VIEW: SettingsView = {
  values: {
    enabled: false,
    currency: 'CNY',
    price: '7.3',
    min_topup: '1',
    max_topup: '10000',
    order_timeout_minutes: 30,
    max_pending_orders: 3,
    amount_options: ['10'],
    discounts: [],
    methods: [],
    epay: { enabled: true, endpoint: 'https://pay.example.com', merchant_id: '1001', key: '******' },
    stripe: { enabled: false, secret_key: '', webhook_secret: '' }
  },
  secret_fields: ['epay/key'],
  notify_urls: { epay: 'https://site.example/api/v1/payment/notify/epay', stripe: '' },
  currencies: [{ code: 'CNY', exponent: 2 }]
} as SettingsView

async function setup() {
  api.get.mockResolvedValue(VIEW)
  const scope = effectScope()
  const s = scope.run(() => useTopupSettings(() => ({})))!
  await flushPromises()
  return { ...s, scope }
}

describe('top-up settings', () => {
  afterEach(() => vi.clearAllMocks())

  it('reads the view: its values to edit (money as numbers), its callback addresses and currencies beside them', async () => {
    const s = await setup()
    expect(api.get).toHaveBeenCalledWith('/topup/settings')
    expect(s.settings.get('price')).toBe(7.3)
    expect(s.notifyUrls.value.epay).toBe(VIEW.notify_urls.epay)
    expect(s.currencies.value).toEqual(VIEW.currencies)
    expect(s.settings.dirty.value).toBe(false)
    s.settings.set('price', 7.5)
    expect(s.settings.dirty.value).toBe(true)
  })

  it('saves decimal strings and takes the stored view as the new baseline', async () => {
    const s = await setup()
    s.settings.set('price', 7.5)
    api.put.mockResolvedValue({ ...VIEW, values: { ...VIEW.values, price: '7.5' } })
    expect(await s.settings.save()).toBe(true)
    expect(api.put.mock.calls[0][1]).toMatchObject({ values: { price: '7.5', min_topup: '1' } })
    expect(s.settings.dirty.value).toBe(false)
  })

  it('shows the server field errors at their fields', async () => {
    const s = await setup()
    api.put.mockRejectedValue(
      new ApiError(400, {
        code: 'invalid_argument',
        message: 'bad',
        details: { fields: [{ field: 'values.epay.key', message: 'wrong key' }] }
      })
    )
    expect(await s.settings.save()).toBe(false)
    expect(s.settings.error('epay.key')).toBe('wrong key')
  })
})
