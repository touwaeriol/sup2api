import { describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { mount } from '@vue/test-utils'
import type { ApiClient } from '@sub2api/host'
import { useForm } from '@sub2api/ui'
import { provideTopupHost } from '@/testing/topupHost'
import NotifyAddress from './NotifyAddress.vue'
import { METHOD_TYPE_PATTERN, SETTINGS_DEFAULTS, keepingNotifyBase, notifyUrl, settingsRules, tabOfPath, type SettingsModel } from './settingsForm'

const t = (key: string) => key

/** The keys whose rules refuse `patch` over valid settings. */
async function refused(patch: Partial<SettingsModel>): Promise<string[]> {
  const valid = { ...SETTINGS_DEFAULTS, amount_options: [10, 100], discounts: [{ amount: 100, rate: 0.95 }] }
  const model = ref({ ...valid, ...patch } as SettingsModel)
  const form = useForm<SettingsModel>({ model: () => model.value, update: (v) => (model.value = v), rules: settingsRules(t) })
  await form.validate()
  return Object.keys(form.errors).sort()
}

describe('top-up settings form', () => {
  it('accepts the defaults with presets and a tier', async () => {
    expect(await refused({})).toEqual([])
  })

  it('checks the general values', async () => {
    expect(await refused({ price: 0, min_topup: 10, max_topup: 5, amount_options: [], order_timeout_minutes: 0, max_pending_orders: 1.5 })).toEqual(['max_pending_orders', 'max_topup', 'order_timeout_minutes', 'price'])
  })

  it('checks the presets, the discount tiers and the logos', async () => {
    const got = await refused({
      amount_options: [0.5],
      discounts: [{ amount: 50, rate: 1 }, { amount: 50, rate: 0.9 }],
      methods: [{ type: 'x', name: 'X', gateway: 'epay', icon: '', enabled: true, logo: 'http://img.example.com/x.png' }]
    })
    expect(got).toEqual(['amount_options', 'discounts.0.amount', 'discounts.0.rate', 'discounts.1.amount', 'methods.0.logo'])
  })

  it('requires the credentials of a gateway that is on, in their formats', async () => {
    expect(await refused({ epay: { enabled: true, endpoint: '', merchant_id: ' ', key: '' } })).toEqual(['epay.endpoint', 'epay.key', 'epay.merchant_id'])
    expect(await refused({ stripe: { enabled: true, secret_key: 'pk_live_x', webhook_secret: 'secret' } })).toEqual(['stripe.secret_key', 'stripe.webhook_secret'])
    expect(await refused({ stripe: { enabled: false, secret_key: '', webhook_secret: '' } })).toEqual([])
  })

  it('files a field path under its tab', () => {
    expect(tabOfPath('epay.key')).toBe('gateways')
    expect(tabOfPath('methods.2.logo')).toBe('methods')
    expect(tabOfPath('discounts.1.rate')).toBe('general')
  })

  it('takes method type keys as the core does', () => {
    for (const ok of ['alipay', 'alipay_qr', 'a', 'x'.repeat(50)]) expect(METHOD_TYPE_PATTERN.test(ok), ok).toBe(true)
    for (const bad of ['', 'Alipay', 'ali-pay', 'ali pay', 'x'.repeat(51)]) expect(METHOD_TYPE_PATTERN.test(bad), bad).toBe(false)
  })

  it("builds the notify address from the core's base only", () => {
    expect(notifyUrl('https://site.example/api/v1/payment/notify', 'epay')).toBe('https://site.example/api/v1/payment/notify/epay')
    expect(notifyUrl('https://site.example/api/v1/payment/notify/', 'p.gw')).toBe('https://site.example/api/v1/payment/notify/p.gw')
    expect(notifyUrl(undefined, 'stripe')).toBe('')
  })

  it('keeps the notify base of every settings read, "" when the core gives none', async () => {
    const base = ref('')
    const get = vi.fn().mockResolvedValueOnce({ values: {}, notify_base: 'https://site.example/api/v1/payment/notify/' }).mockResolvedValueOnce({ values: {}, notify_base: '' })
    const api = keepingNotifyBase({ get, put: vi.fn() } as unknown as ApiClient, base)
    expect(await api.get('/topup/settings')).toEqual({ values: {}, notify_base: 'https://site.example/api/v1/payment/notify/' })
    expect(base.value).toBe('https://site.example/api/v1/payment/notify/')
    await api.get('/topup/settings')
    expect(base.value).toBe('')
  })

  it('shows the callback address, or that the site must be https', () => {
    provideTopupHost()
    const at = mount(NotifyAddress, { props: { base: 'https://site.example/api/v1/payment/notify/', gateway: 'stripe', label: 'Webhook' } })
    expect((at.get('input').element as HTMLInputElement).value).toBe('https://site.example/api/v1/payment/notify/stripe')
    const none = mount(NotifyAddress, { props: { base: '', gateway: 'stripe', label: 'Webhook' } })
    expect(none.find('input').exists()).toBe(false)
    expect(none.get('[data-testid="payment-notify-https"]').text()).toBe('topup.settings.gateways.needHttps')
  })
})
