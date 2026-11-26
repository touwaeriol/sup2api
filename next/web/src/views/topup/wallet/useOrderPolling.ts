import { computed, onScopeDispose, ref, watch } from 'vue'
import { paymentApi, type Order, type OrderStatus } from '../topup'

/** Statuses after which an order no longer changes by itself. */
export const FINAL_STATUSES: ReadonlySet<OrderStatus> = new Set(['completed', 'failed', 'expired', 'cancelled'])

/**
 * The live state of one order: polled at once and then every `intervalMs`
 * while `active()` and the order is not final. A failed poll keeps the last
 * state (the next one tries again); a different order starts from "pending".
 */
export function useOrderPolling(outTradeNo: () => string | null, active: () => boolean, intervalMs = 3000) {
  const order = ref<Order | null>(null)
  const status = computed<OrderStatus>(() => order.value?.status ?? 'pending')
  let timer: ReturnType<typeof setInterval> | undefined

  async function poll() {
    const no = outTradeNo()
    if (!no) return
    try {
      const o = await paymentApi.order(no)
      if (no === outTradeNo()) order.value = o
    } catch {
      // keep the last state; the next tick retries
    }
  }

  const stop = () => clearInterval(timer)
  watch(outTradeNo, () => (order.value = null))
  watch(
    () => !!outTradeNo() && active() && !FINAL_STATUSES.has(status.value),
    (on) => {
      stop()
      if (!on) return
      poll()
      timer = setInterval(poll, intervalMs)
    },
    { immediate: true }
  )
  onScopeDispose(stop)

  return { order, status, poll }
}
