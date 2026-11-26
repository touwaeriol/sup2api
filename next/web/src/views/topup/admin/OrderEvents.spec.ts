import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { PaymentEvent } from '../topup'
import OrderEvents from './OrderEvents.vue'
import { eventField, eventKindLabel } from './events'

const api = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const { t } = i18n.global
// vue-i18n's te is typed against the whole message tree, deeper than tsc follows.
const te = i18n.global.te as unknown as (key: string) => boolean
const event = (id: number, kind: string, verified: boolean, detail: Record<string, string>): PaymentEvent => ({
  id,
  gateway: 'epay',
  kind,
  verified,
  detail,
  created_at: '2026-11-26T08:00:00Z'
})
const EVENTS = [
  event(3, 'PAYMENT_AMOUNT_MISMATCH', true, {
    out_trade_no: 's2a_x',
    trade_no: '2026112622001',
    status: 'paid',
    amount: '729.99',
    currency: 'CNY',
    type: 'alipay'
  }),
  event(2, 'SIGNATURE_INVALID', false, { out_trade_no: 's2a_x', error: 'bad sign' }),
  event(1, 'SOMETHING_NEW', true, {})
]
const render = (orderId: number) => mount(OrderEvents, { props: { orderId }, global: { plugins: [i18n] } })
/** The request starts on a timer (useLatestRequest), then answers. */
async function settle() {
  await new Promise(resolve => setTimeout(resolve))
  await flushPromises()
}

describe('order gateway events', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    api.list.mockResolvedValue({ items: EVENTS, page: { page: 1, page_size: 50, total: 60 }, paged: true })
  })

  it('lists the events of the order with their kind, verification and key fields', async () => {
    const w = render(42)
    await settle()
    expect(api.list).toHaveBeenCalledWith('/topup/orders/42/events', { page: 1, page_size: 50 })
    const items = w.findAll('[data-testid="payment-order-event"]')
    expect(items).toHaveLength(3)
    expect(items[0].text()).toContain(t('topup.admin.orders.events.kind.PAYMENT_AMOUNT_MISMATCH'))
    expect(items[0].text()).toContain(t('topup.admin.orders.events.verified'))
    expect(items[0].text()).toContain('2026112622001')
    expect(items[0].text()).toContain('729.99')
    expect(items[1].text()).toContain(t('topup.admin.orders.events.unverified'))
    expect(items[1].text()).toContain('bad sign')
    // A kind without a label shows as the core wrote it.
    expect(items[2].text()).toContain('SOMETHING_NEW')
    expect(w.text()).toContain(t('topup.admin.orders.events.more', { shown: 3, total: 60 }))
  })

  it('says when there is none', async () => {
    api.list.mockResolvedValue({ items: [], page: { page: 1, page_size: 50, total: 0 }, paged: true })
    const w = render(7)
    await settle()
    expect(w.text()).toContain(t('topup.admin.orders.events.empty'))
  })

  it('shows the amount the server gives in the major unit with its currency, and labels known kinds only', () => {
    const amount = (value: string, currency: string) => eventField(event(1, 'NOTIFY_OK', true, { amount: value, currency }), 'amount')
    expect(amount('729.99', 'CNY')).toBe('¥729.99')
    expect(amount('1200', 'JPY')).toBe('¥1,200')
    const label = (kind: string) => eventKindLabel(k => t(k), te, kind)
    const accepted: string = t('topup.admin.orders.events.kind.NOTIFY_OK')
    expect(label('NOTIFY_OK')).toBe(accepted)
    expect(label('X_Y')).toBe('X_Y')
  })
})
