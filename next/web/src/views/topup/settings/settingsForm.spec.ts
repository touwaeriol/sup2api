import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import { mount } from '@vue/test-utils'
import { useForm } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { PaymentSettingsValue } from '../topup'
import NotifyAddress from './NotifyAddress.vue'
import { DEFAULT_MODEL, METHOD_TYPE_PATTERN, settingsRules, tabOfPath, toModel, toValues, type SettingsModel } from './settingsForm'

const t = (key: string) => key

/** The keys whose rules refuse `patch` over valid settings. */
async function refused(patch: Partial<SettingsModel>): Promise<string[]> {
  const valid = {
    ...DEFAULT_MODEL(),
    price: 7.3,
    min_topup: 1,
    max_topup: 10000,
    amount_options: [10, 100],
    discounts: [{ amount: 100, rate: 0.95 }]
  }
  const model = ref({ ...valid, ...patch } as SettingsModel)
  const form = useForm<SettingsModel>({ model: () => model.value, update: v => (model.value = v), rules: settingsRules(t) })
  await form.validate()
  return Object.keys(form.errors).sort()
}

describe('top-up settings form', () => {
  it('accepts valid settings with presets and a tier', async () => {
    expect(await refused({})).toEqual([])
  })

  it('checks the general values', async () => {
    expect(
      await refused({ price: 0, min_topup: 10, max_topup: 5, amount_options: [], order_timeout_minutes: 0, max_pending_orders: 1.5 })
    ).toEqual(['max_pending_orders', 'max_topup', 'order_timeout_minutes', 'price'])
  })

  it('checks the presets, the discount tiers and the logos', async () => {
    const got = await refused({
      amount_options: [0.5],
      discounts: [
        { amount: 50, rate: 1 },
        { amount: 50, rate: 0.9 }
      ],
      methods: [{ type: 'x', name: 'X', gateway: 'epay', icon: '', enabled: true, logo: 'http://img.example.com/x.png' }]
    })
    expect(got).toEqual(['amount_options', 'discounts.0.amount', 'discounts.0.rate', 'discounts.1.amount', 'methods.0.logo'])
  })

  it('requires the credentials of a gateway that is on, in their formats', async () => {
    expect(await refused({ epay: { enabled: true, endpoint: '', merchant_id: ' ', key: '' } })).toEqual([
      'epay.endpoint',
      'epay.key',
      'epay.merchant_id'
    ])
    expect(await refused({ stripe: { enabled: true, secret_key: 'pk_live_x', webhook_secret: 'secret' } })).toEqual([
      'stripe.secret_key',
      'stripe.webhook_secret'
    ])
    expect(await refused({ stripe: { enabled: false, secret_key: '', webhook_secret: '' } })).toEqual([])
  })

  it('edits amounts as numbers and sends them back as decimal strings', () => {
    const values: PaymentSettingsValue = {
      ...toValues(DEFAULT_MODEL()),
      price: '7.30',
      min_topup: '1',
      amount_options: ['10', '100.5'],
      discounts: [{ amount: '100', rate: '0.95' }],
      methods: [{ type: 'alipay', name: 'Alipay', gateway: 'epay', icon: 'alipay', min_topup: '5', enabled: true }]
    }
    const model = toModel(values)
    expect([model.price, model.amount_options, model.discounts[0].rate, model.methods[0].min_topup]).toEqual([7.3, [10, 100.5], 0.95, 5])
    const back = toValues({ ...model, price: 7.5 })
    expect([back.price, back.min_topup, back.amount_options, back.discounts, back.methods[0].min_topup]).toEqual([
      '7.5',
      '1',
      ['10', '100.5'],
      [{ amount: '100', rate: '0.95' }],
      '5'
    ])
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

  it("shows the core's callback address, or that the site must be https", () => {
    const global = { plugins: [i18n] }
    const url = 'https://site.example/api/v1/payment/notify/stripe'
    const at = mount(NotifyAddress, { props: { url, label: 'Webhook' }, global })
    expect((at.get('input').element as HTMLInputElement).value).toBe(url)
    const none = mount(NotifyAddress, { props: { url: '', label: 'Webhook' }, global })
    expect(none.find('input').exists()).toBe(false)
    expect(none.get('[data-testid="payment-notify-https"]').text()).toBe(i18n.global.t('topup.settings.gateways.needHttps'))
  })
})
