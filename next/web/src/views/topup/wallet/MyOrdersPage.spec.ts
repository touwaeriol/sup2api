import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { provideTopupHost } from '@/testing/topupHost'
import MyOrdersPage from './MyOrdersPage.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), list: vi.fn() }))
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const later = () => new Date(Date.now() + 600_000).toISOString()
const pending = (no: string, extra: object) => ({ out_trade_no: no, amount: 10, bonus: 0, pay_amount: 73, currency: 'CNY', method: 'x', method_name: 'X', status: 'pending', created_at: later(), expires_at: later(), ...extra })
const ORDERS = [
  pending('s2a_page', { pay_url: 'https://pay.example.com/p' }),
  pending('s2a_qr', { qr_code: 'weixin://wxpay/bizpayurl?pr=abc' }),
  pending('s2a_js', { pay_url: 'javascript:alert(1)' }),
  pending('s2a_old', { pay_url: 'https://pay.example.com/o', expires_at: new Date(Date.now() - 1000).toISOString() })
]

async function render() {
  const w = mount(MyOrdersPage, { attachTo: document.body, global: { stubs: { RouterLink: true, SButton: true } } })
  await flushPromises()
  return w
}

const resumeButtons = (w: Awaited<ReturnType<typeof render>>) => w.findAll('[data-testid="pay-resume"]')

describe('my top-up orders', () => {
  beforeEach(() => {
    provideTopupHost()
    api.list.mockResolvedValue({ items: ORDERS, page: { page: 1, page_size: 20, total: ORDERS.length }, paged: true })
    api.get.mockImplementation(async (path: string) => (path === '/me/topup' ? { methods: [] } : ORDERS[1]))
  })

  it('offers to pay a pending order with an https page or a QR code, not one expired or with another address', async () => {
    const w = await render()
    const rows = w.findAll('tbody tr')
    const offered = rows.map((r) => r.find('[data-testid="pay-resume"]').exists())
    expect(offered).toEqual([true, true, false, false])
  })

  it('opens the payment page again', async () => {
    const win = { closed: false, opener: {}, location: { href: 'about:blank' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    const w = await render()
    await resumeButtons(w)[0].trigger('click')
    await flushPromises()
    expect(win.location.href).toBe('https://pay.example.com/p')
  })

  it('shows the QR code of an order without a payment page, opening no window', async () => {
    const open = vi.spyOn(window, 'open')
    const w = await render()
    await resumeButtons(w)[1].trigger('click')
    await flushPromises()
    expect(open).not.toHaveBeenCalled()
    expect(document.body.querySelector('.pay-qr')).not.toBeNull()
  })
})
