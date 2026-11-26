import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { provideTopupHost } from '@/testing/topupHost'
import type { PaymentEvent } from '../topup'
import OrderEvents from './OrderEvents.vue'
import { eventField, eventKindLabel } from './events'

const api = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const event = (id: number, kind: string, verified: boolean, detail: Record<string, string>): PaymentEvent => ({ id, gateway: 'epay', kind, verified, detail, created_at: '2026-11-26T08:00:00Z' })
const EVENTS = [
  event(3, 'PAYMENT_AMOUNT_MISMATCH', true, { out_trade_no: 's2a_x', trade_no: '2026112622001', status: 'paid', amount: '72999', currency: 'CNY', type: 'alipay' }),
  event(2, 'SIGNATURE_INVALID', false, { out_trade_no: 's2a_x', error: 'bad sign' }),
  event(1, 'SOMETHING_NEW', true, {})
]

describe('order gateway events', () => {
  beforeEach(() => {
    provideTopupHost()
    api.list.mockResolvedValue({ items: EVENTS, page: { page: 1, page_size: 50, total: 60 }, paged: true })
  })

  it('lists the events of the order with their kind, verification and key fields', async () => {
    const w = mount(OrderEvents, { props: { orderId: 42 } })
    await flushPromises()
    expect(api.list).toHaveBeenCalledWith('/topup/orders/42/events', { page: '1', page_size: '50' })
    const items = w.findAll('[data-testid="payment-order-event"]')
    expect(items).toHaveLength(3)
    expect(items[0].text()).toContain('topup.admin.orders.events.kind.PAYMENT_AMOUNT_MISMATCH')
    expect(items[0].text()).toContain('topup.admin.orders.events.verified')
    expect(items[0].text()).toContain('2026112622001')
    expect(items[0].text()).toContain('729.99')
    expect(items[1].text()).toContain('topup.admin.orders.events.unverified')
    expect(items[1].text()).toContain('bad sign')
    // A kind without a label shows as the core wrote it.
    expect(items[2].text()).toContain('SOMETHING_NEW')
    expect(w.text()).toContain('topup.admin.orders.events.more')
  })

  it('says when there is none', async () => {
    api.list.mockResolvedValue({ items: [], page: { page: 1, page_size: 50, total: 0 }, paged: true })
    const w = mount(OrderEvents, { props: { orderId: 7 } })
    await flushPromises()
    expect(w.text()).toContain('topup.admin.orders.events.empty')
  })

  it('reads the amount from the minor unit of its currency', () => {
    provideTopupHost()
    const amount = (value: string, currency: string) => eventField(event(1, 'NOTIFY_OK', true, { amount: value, currency }), 'amount')
    expect(amount('72999', 'CNY')).toContain('729.99')
    expect(amount('1200', 'JPY')).toContain('1,200')
    expect(amount('abc', 'CNY')).toBe('abc')
    expect(eventKindLabel((k) => `t:${k}`, 'NOTIFY_OK')).toBe('t:topup.admin.orders.events.kind.NOTIFY_OK')
    expect(eventKindLabel((k) => `t:${k}`, 'X_Y')).toBe('X_Y')
  })
})
