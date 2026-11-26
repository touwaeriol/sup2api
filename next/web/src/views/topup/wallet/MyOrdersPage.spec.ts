import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import MyOrdersPage from './MyOrdersPage.vue'
import PayingDialog from './PayingDialog.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), list: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const later = () => new Date(Date.now() + 600_000).toISOString()
const order = (no: string, extra: object) => ({
  out_trade_no: no,
  amount: '10',
  bonus: '0',
  pay_amount: '73.00',
  currency: 'CNY',
  method: 'x',
  method_name: 'X',
  status: 'pending',
  payable: true,
  created_at: later(),
  expires_at: later(),
  ...extra
})
// The list carries no QR code; the order read on its own says where to pay.
const ORDERS = [
  order('s2a_page', { method: 'page', pay_url: 'https://pay.example.com/p' }),
  order('s2a_qr', { method: 'qr' }),
  order('s2a_late', { expires_at: new Date(Date.now() - 1000).toISOString() }),
  order('s2a_done', { status: 'completed', payable: false }),
  order('s2a_paid', { method: 'paid' })
]
const DETAIL: Record<string, object> = {
  s2a_page: ORDERS[0],
  s2a_qr: { ...ORDERS[1], qr_image: 'data:image/png;base64,iVBORw0KGgo=' },
  // Paid since the list was read: nowhere to pay any more.
  s2a_paid: { ...ORDERS[4], status: 'completed', payable: false }
}

async function render() {
  const w = mount(MyOrdersPage, { attachTo: document.body, global: { plugins: [i18n], stubs: { RouterLink: true, SButton: true } } })
  await flushPromises()
  return w
}

const resumeButtons = (w: Awaited<ReturnType<typeof render>>) => w.findAll('[data-testid="pay-resume"]')

describe('my top-up orders', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    api.list.mockResolvedValue({ items: ORDERS, page: { page: 1, page_size: 20, total: ORDERS.length }, paged: true })
    api.get.mockImplementation(async (path: string) => (path === '/me/topup' ? { methods: [] } : DETAIL[path.split('/').pop() ?? '']))
  })

  it('offers to pay a pending order not yet expired, nothing else', async () => {
    const w = await render()
    const offered = w.findAll('tbody tr').map(r => r.find('[data-testid="pay-resume"]').exists())
    expect(offered).toEqual([true, true, false, false, true])
  })

  it('opens the payment page again', async () => {
    const win = { closed: false, opener: {}, location: { href: 'about:blank' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    const w = await render()
    await resumeButtons(w)[0].trigger('click')
    await flushPromises()
    expect(api.get).toHaveBeenCalledWith('/me/topup/orders/s2a_page', { qr: 1 })
    expect(win.location.href).toBe('https://pay.example.com/p')
  })

  it('shows the QR code the order read on its own carries, closing the reserved window', async () => {
    const win = { closed: false, opener: {}, location: { href: 'about:blank' }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    const w = await render()
    await resumeButtons(w)[1].trigger('click')
    await flushPromises()
    expect(win.close).toHaveBeenCalled()
    expect(document.body.querySelector('[data-testid="pay-qr"]')).not.toBeNull()
  })

  it('closes the reserved window of an order paid meanwhile, keeps the method page-giving, and reads the list again', async () => {
    const win = { closed: false, opener: {}, location: { href: 'about:blank' }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    const w = await render()
    api.list.mockClear()
    await resumeButtons(w)[2].trigger('click')
    await flushPromises()
    expect(win.close).toHaveBeenCalled()
    expect(w.findComponent(PayingDialog).props('open')).toBe(false)
    expect(api.list).toHaveBeenCalledTimes(1)
    // Not remembered as a method without a page: the next order through it reserves a window again.
    await resumeButtons(w)[2].trigger('click')
    expect(open).toHaveBeenCalledTimes(2)
  })

  it('reads the list again once the payment dialog closes', async () => {
    const w = await render()
    await resumeButtons(w)[1].trigger('click')
    await flushPromises()
    api.list.mockClear()
    w.findComponent(PayingDialog).vm.$emit('update:open', false)
    await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(1)
  })
})
