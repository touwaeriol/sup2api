import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { SFormModal } from '@sub2api/ui'
import { i18n } from '@/i18n'
import RedeemCodesView from './RedeemCodesView.vue'
import RedeemGenerateModal from './RedeemGenerateModal.vue'
import RedeemResultModal from './RedeemResultModal.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), post: vi.fn(), del: vi.fn() }))
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const PLAIN = ['0A1B-2C3D-4E5F-6071-8293-A4B5-C6D7-E8F9', 'FFEE-DDCC-BBAA-9988-7766-5544-3322-1100']
const listed = (tails: string[]) => ({
  items: tails.map((tail, i) => ({ id: i + 1, code_tail: tail, value: '0.00500000', status: 'unused', notes: '', created_at: '2026-11-26T00:00:00Z' })),
  page: { page: 1, page_size: 20, total: tails.length },
  paged: true
})

function render() {
  return mount(RedeemCodesView, { attachTo: document.body, global: { plugins: [i18n], directives: { permission: {} } } })
}

describe('redeem codes', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    api.list.mockResolvedValue(listed([]))
    api.post.mockResolvedValue({ codes: PLAIN })
  })

  it('shows the plain codes of a new batch in the result dialog only; the list keeps them masked', async () => {
    const w = render()
    await flushPromises()
    await w.get('[data-testid="redeem-generate"]').trigger('click')
    await flushPromises()
    api.list.mockResolvedValue(listed(['E8F9', '1100']))
    w.findComponent(RedeemGenerateModal).findComponent(SFormModal).vm.$emit('submit')
    await flushPromises()

    expect(api.post).toHaveBeenCalledWith('/redeem-codes', { count: 10, value: 10, expires_at: undefined, notes: undefined })
    const result = w.findComponent(RedeemResultModal)
    expect(result.props('codes')).toEqual(PLAIN)
    const table = w.get('table').text()
    expect(table).toContain('••••-••••-••••-E8F9')
    for (const code of PLAIN) expect(table).not.toContain(code)

    // Closing the result drops the codes: nothing on the page holds them any more.
    result.vm.$emit('update:open', false)
    await flushPromises()
    for (const code of PLAIN) expect(document.body.innerHTML).not.toContain(code)
  })

  it("shows a code's value to the ledger's 8 decimals", async () => {
    api.list.mockResolvedValue(listed(['ABCD']))
    const w = render()
    await flushPromises()
    expect(w.get('table').text()).toContain('$0.005')
  })
})
