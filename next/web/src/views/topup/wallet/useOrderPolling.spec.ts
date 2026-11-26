import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { pollInterval, useOrderPolling } from './useOrderPolling'

const api = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))

describe('order polling', () => {
  const order = (status: string, payable: boolean) => ({ status, payable }) as Parameters<typeof pollInterval>[0]

  beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }))
  afterEach(() => vi.useRealTimers())

  it('watches an order on its way often, one a late payment may credit slowly, others not at all', () => {
    expect(pollInterval(null)).toBe(3000)
    expect([pollInterval(order('pending', true)), pollInterval(order('paid', false))]).toEqual([3000, 3000])
    expect([pollInterval(order('expired', true)), pollInterval(order('cancelled', true))]).toEqual([15000, 15000])
    expect([pollInterval(order('expired', false)), pollInterval(order('completed', false)), pollInterval(order('failed', false))]).toEqual([
      0, 0, 0
    ])
  })

  it('reads a new order at once even while a read of the previous one runs', async () => {
    const answers: Array<(v: object) => void> = []
    api.get.mockImplementation(() => new Promise(resolve => answers.push(resolve)))
    const no = ref<string | null>('s2a_a')
    const scope = effectScope()
    const live = scope.run(() =>
      useOrderPolling(
        () => no.value,
        () => true
      )
    )!
    await nextTick()
    expect(api.get).toHaveBeenCalledTimes(1)
    // A poll asks for no QR code (?qr=1): the dialog has it already.
    expect(api.get.mock.calls[0]).toEqual(['/me/topup/orders/s2a_a', undefined])

    no.value = 's2a_b'
    await nextTick()
    answers[0]({ out_trade_no: 's2a_a', status: 'completed' })
    await vi.advanceTimersByTimeAsync(0)
    expect(api.get).toHaveBeenCalledTimes(2)
    expect(api.get.mock.calls[1][0]).toContain('s2a_b')
    // The previous order's answer is not taken for the new one.
    expect(live.order.value).toBeNull()
    scope.stop()
  })
})
