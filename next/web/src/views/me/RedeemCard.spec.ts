import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import RedeemCard from './RedeemCard.vue'

const api = vi.hoisted(() => ({ post: vi.fn() }))
const ui = vi.hoisted(() => ({ toast: vi.fn() }))
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api }))
vi.mock('@sub2api/ui', async (orig) => ({ ...(await orig<object>()), toast: ui.toast }))
enableAutoUnmount(afterEach)

const render = () => mount(RedeemCard, { global: { plugins: [i18n] } })

async function submit(w: ReturnType<typeof render>, code: string) {
  await w.get('input').setValue(code)
  await w.get('form').trigger('submit')
  await flushPromises()
}

describe('redeem card', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
    api.post.mockReset()
    ui.toast.mockReset()
  })

  it('posts the code, toasts the amount and tells the wallet to refresh', async () => {
    api.post.mockResolvedValue({ amount: '50.00000000', balance: '70.00000000' })
    const w = render()
    await submit(w, '  ABCD-EF01-2345-6789-ABCD-EF01-2345-6789 ')
    expect(api.post).toHaveBeenCalledWith('/me/redeem', { code: 'ABCD-EF01-2345-6789-ABCD-EF01-2345-6789' })
    expect(ui.toast).toHaveBeenCalledWith(expect.stringContaining('50'), 'success')
    expect(w.emitted('credited')).toHaveLength(1)
    expect((w.get('input').element as HTMLInputElement).value).toBe('')
  })

  it("shows the server's one message for a bad code and sends nothing for an empty one", async () => {
    api.post.mockRejectedValue(new ApiError(400, { code: 'invalid_redeem_code', message: '兑换码无效或已使用' }))
    const w = render()
    await submit(w, '   ')
    expect(api.post).not.toHaveBeenCalled()
    await submit(w, 'nope')
    expect(w.text()).toContain('兑换码无效或已使用')
    expect(w.emitted('credited')).toBeUndefined()
  })
})
