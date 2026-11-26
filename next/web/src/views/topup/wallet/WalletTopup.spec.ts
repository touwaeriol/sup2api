import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { provideTopupHost } from '@/testing/topupHost'
import ConfirmDialog from './ConfirmDialog.vue'
import WalletTopup from './WalletTopup.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const NO = 's2a_0123456789abcdef'
const METHOD = { type: 'alipay', name: 'Alipay', icon: 'alipay', min_topup: 0 }
const CHECKOUT = { enabled: true, currency: 'CNY', price: 7.3, min_topup: 1, max_topup: 1000, amount_options: [100], discounts: [], methods: [METHOD] }
const PREVIEW = { amount: 100, bonus: 0, discount_rate: 1, pay_amount: 730, original_pay_amount: 730 }
// The order as the server keeps it: its price differs from the preview (the settings changed in between).
const order = (status: string) => ({ out_trade_no: NO, amount: 100, bonus: 5, pay_amount: 693.5, currency: 'CNY', method: 'alipay', method_name: 'Alipay', status, expires_at: new Date(Date.now() + 600_000).toISOString() })

function serve(payUrl: string, statuses: string[]) {
  api.get.mockImplementation(async (path: string) => (path === '/me/topup' ? CHECKOUT : order(statuses.shift() ?? 'completed')))
  api.post.mockImplementation(async (path: string) =>
    path === '/me/topup/preview' ? PREVIEW : { out_trade_no: NO, pay_url: payUrl, expires_at: new Date(Date.now() + 600_000).toISOString() }
  )
}

/** Mounts the wallet's top-up, picks 100 with Alipay ("Pay now") and confirms with a promo code. */
async function payNow() {
  const w = mount(WalletTopup, { attachTo: document.body, global: { stubs: { TopupCard: true } } })
  await flushPromises()
  w.findComponent({ name: 'TopupCard' }).vm.$emit('pay', 100, METHOD)
  await flushPromises()
  expect(api.post).toHaveBeenCalledWith('/me/topup/preview', { amount: 100, method: 'alipay', promo_code: undefined })
  w.findComponent(ConfirmDialog).vm.$emit('confirm', 'SAVE5')
  await flushPromises()
  return w
}

const fakeWindow = () => ({ closed: false, opener: {} as unknown, location: { href: 'about:blank' }, close: vi.fn() })
const body = () => document.body

describe('wallet top-up', () => {
  let host: ReturnType<typeof provideTopupHost>
  beforeEach(() => {
    host = provideTopupHost()
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  })
  afterEach(() => vi.useRealTimers())

  it('orders, opens the payment page, shows the order own price and reports the credit', async () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    serve('https://pay.example.com/submit.php?x=1', ['pending', 'completed'])
    const w = await payNow()

    expect(api.post).toHaveBeenCalledWith('/me/topup/orders', { amount: 100, method: 'alipay', promo_code: 'SAVE5' })
    expect(win.location.href).toBe('https://pay.example.com/submit.php?x=1')
    expect(body().querySelector('[data-testid="pay-amount-due"]')?.textContent).toContain('693.5')
    expect(body().querySelector('[data-testid="pay-amount-due"]')?.textContent).not.toContain('730')

    vi.advanceTimersByTime(3000)
    await flushPromises()
    expect(body().querySelector('[data-testid="pay-success"]')).not.toBeNull()
    expect(w.emitted('credited')).toHaveLength(1)
  })

  it('says so when the browser blocks the payment window', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    serve('https://pay.example.com/submit.php', ['pending'])
    await payNow()
    expect(body().textContent).toContain('topup.wallet.paying.blocked')
    expect(body().querySelector('[data-testid="pay-reopen"]')).not.toBeNull()
  })

  it('never loads an address that is not https', async () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    serve('javascript:alert(document.domain)', ['pending'])
    await payNow()
    expect(win.location.href).toBe('about:blank')
    expect(win.close).toHaveBeenCalled()
    expect(host.toast).toHaveBeenCalledWith('topup.wallet.paying.badUrl', 'error')
    expect(body().querySelector('[data-testid="pay-bad-url"]')).not.toBeNull()
    expect(body().querySelector('[data-testid="pay-reopen"]')).toBeNull()
  })
})
