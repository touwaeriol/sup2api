import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import ConfirmDialog from './ConfirmDialog.vue'
import WalletTopup from './WalletTopup.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const NO = 's2a_0123456789abcdef'
const METHOD = { type: 'alipay', name: 'Alipay', icon: 'alipay', min_topup: '0' }
const CHECKOUT = {
  enabled: true,
  currency: 'CNY',
  currency_exponent: 2,
  price: '7.3',
  min_topup: '1',
  max_topup: '1000',
  amount_options: ['100'],
  discounts: [],
  methods: [METHOD]
}
const PREVIEW = { amount: '100', bonus: '0', discount_rate: '1', pay_amount: '730.00', original_pay_amount: '730.00' }
const later = () => new Date(Date.now() + 600_000).toISOString()
// The order as the server keeps it: its price differs from the preview (the settings changed in between).
const order = (status: string) => ({
  out_trade_no: NO,
  amount: '100',
  bonus: '5',
  pay_amount: '693.50',
  currency: 'CNY',
  method: 'alipay',
  method_name: 'Alipay',
  status,
  payable: status === 'pending',
  expires_at: later()
})

function serve(created: object, statuses: string[]) {
  api.get.mockImplementation(async (path: string) => (path === '/me/topup' ? CHECKOUT : order(statuses.shift() ?? 'completed')))
  api.post.mockImplementation(async (path: string) =>
    path === '/me/topup/preview' ? PREVIEW : { out_trade_no: NO, expires_at: later(), ...created }
  )
}

/** Lets the timers due now run (the preview's), then the requests they started. */
async function tick(ms = 0) {
  await flushPromises()
  vi.advanceTimersByTime(ms)
  await flushPromises()
}

/** Mounts the wallet's top-up, picks 100 with Alipay ("Pay now") and confirms with a promo code. */
async function payNow() {
  const w = mount(WalletTopup, { attachTo: document.body, global: { plugins: [i18n], stubs: { TopupCard: true } } })
  await flushPromises()
  w.findComponent({ name: 'TopupCard' }).vm.$emit('pay', 100, METHOD)
  await tick()
  expect(api.post).toHaveBeenCalledWith('/me/topup/preview', { amount: '100', method: 'alipay', promo_code: undefined })
  w.findComponent(ConfirmDialog).vm.$emit('confirm', 'SAVE5')
  await flushPromises()
  return w
}

const fakeWindow = () => ({ closed: false, opener: {} as unknown, location: { href: 'about:blank' }, close: vi.fn() })
const find = (testid: string) => document.body.querySelector(`[data-testid="${testid}"]`)

describe('wallet top-up', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  })
  afterEach(() => vi.useRealTimers())

  it('orders, opens the payment page, shows the order own price and reports the credit', async () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    serve({ pay_url: 'https://pay.example.com/submit.php?x=1' }, ['pending', 'completed'])
    const w = await payNow()

    expect(api.post).toHaveBeenCalledWith('/me/topup/orders', { amount: '100', method: 'alipay', promo_code: 'SAVE5' })
    expect(win.location.href).toBe('https://pay.example.com/submit.php?x=1')
    expect(find('pay-amount-due')?.textContent).toContain('693.50')

    await tick(3000)
    expect(find('pay-success')).not.toBeNull()
    expect(w.emitted('credited')).toHaveLength(1)
  })

  it('shows the QR code the server drew, opening no page', async () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    serve({ qr_image: 'data:image/png;base64,iVBORw0KGgo=' }, ['pending'])
    await payNow()
    expect(find('pay-qr')?.getAttribute('src')).toBe('data:image/png;base64,iVBORw0KGgo=')
    expect(win.close).toHaveBeenCalled()
    expect(find('pay-reopen')).toBeNull()
  })

  it('says so when the browser blocks the payment window', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    serve({ pay_url: 'https://pay.example.com/submit.php' }, ['pending'])
    await payNow()
    expect(document.body.textContent).toContain(i18n.global.t('topup.wallet.paying.blocked'))
    expect(find('pay-reopen')).not.toBeNull()
  })

  it('follows how opening the page again went: the warning goes once it opens, and comes back when blocked again', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    serve({ pay_url: 'https://pay.example.com/submit.php' }, ['pending'])
    await payNow()
    const reopen = async (win: object | null) => {
      vi.mocked(window.open).mockReturnValue(win as Window | null)
      ;(find('pay-reopen') as HTMLElement).click()
      await flushPromises()
      return find('pay-window')?.textContent
    }
    expect(await reopen(fakeWindow())).toContain(i18n.global.t('topup.wallet.paying.opened'))
    expect(await reopen(null)).toContain(i18n.global.t('topup.wallet.paying.blocked'))
  })

  it('never loads an address that is not https', async () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    serve({ pay_url: 'javascript:alert(document.domain)' }, ['pending'])
    await payNow()
    expect(win.location.href).toBe('about:blank')
    expect(win.close).toHaveBeenCalled()
    expect(find('pay-reopen')).toBeNull()
  })

  it('keeps watching an expired order a late payment may still credit, slowly, and says so', async () => {
    vi.spyOn(window, 'open').mockReturnValue(fakeWindow() as unknown as Window)
    serve({ pay_url: 'https://pay.example.com/p' }, [])
    api.get.mockImplementation(async (path: string) => (path === '/me/topup' ? CHECKOUT : { ...order('expired'), payable: true }))
    await payNow()
    expect(find('pay-ended')?.textContent).toContain(i18n.global.t('topup.wallet.paying.lateHint'))
    const reads = () => api.get.mock.calls.filter(([p]) => p !== '/me/topup').length
    const before = reads()
    await tick(3000)
    expect(reads()).toBe(before)
    await tick(12000)
    expect(reads()).toBe(before + 1)
  })

  it('shows no QR code but the PNG the core draws', async () => {
    vi.spyOn(window, 'open').mockReturnValue(fakeWindow() as unknown as Window)
    serve({ qr_image: 'data:image/svg+xml;base64,PHN2Zz4=' }, ['pending'])
    await payNow()
    expect(find('pay-qr')).toBeNull()
  })

  it('does not tell to order again while an order past its time may still be paid', async () => {
    vi.spyOn(window, 'open').mockReturnValue(fakeWindow() as unknown as Window)
    serve({ pay_url: 'https://pay.example.com/p', expires_at: new Date(Date.now() - 1000).toISOString() }, ['pending'])
    await payNow()
    const text = find('pay-ended')?.textContent ?? ''
    expect(text).toContain(i18n.global.t('topup.wallet.paying.lateHint'))
    expect(text).not.toContain(i18n.global.t('topup.wallet.paying.expired'))
  })
})

describe('wallet top-up confirmation', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  })
  afterEach(() => vi.useRealTimers())

  async function open() {
    const w = mount(WalletTopup, { attachTo: document.body, global: { plugins: [i18n], stubs: { TopupCard: true } } })
    await flushPromises()
    w.findComponent({ name: 'TopupCard' }).vm.$emit('pay', 100, METHOD)
    await tick()
    return w
  }
  const button = (testid: string) => find(testid) as HTMLButtonElement | null

  it('cannot be confirmed with a promo code the server refused', async () => {
    serve({}, [])
    api.post.mockImplementation(async (_: string, body: { promo_code?: string }) =>
      body.promo_code ? { ...PREVIEW, promo_error: 'Code expired' } : PREVIEW
    )
    await open()
    const input = find('pay-promo')?.querySelector('input') ?? (find('pay-promo') as HTMLInputElement)
    input.value = 'OLD'
    input.dispatchEvent(new Event('input'))
    await tick()
    button('pay-promo-apply')?.click()
    await tick()
    expect(document.body.textContent).toContain('Code expired')
    expect(button('pay-confirm')?.disabled).toBe(true)
  })

  it('offers to ask for the price again when the preview failed', async () => {
    serve({}, [])
    api.post.mockRejectedValueOnce(new Error('Service unavailable'))
    await open()
    expect(document.body.textContent).toContain('Service unavailable')
    expect(button('pay-confirm')).toBeNull()
    button('pay-confirm-retry')?.click()
    await tick()
    expect(api.post).toHaveBeenCalledTimes(2)
    expect(find('pay-confirm-total')?.textContent).toContain('730.00')
    expect(button('pay-confirm')?.disabled).toBe(false)
  })
})
