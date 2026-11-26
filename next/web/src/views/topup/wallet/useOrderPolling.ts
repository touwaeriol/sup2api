import { computed, ref, watch } from 'vue'
import { useAutoRefresh, type AutoRefresh } from '@sub2api/ui'
import { topupApi } from '../api'
import type { Order, OrderStatus } from '../topup'

/** How often an order on its way (pending, paid) is read, and one past its time that a payment may still credit. */
export const POLL_MS = 3000
const LATE_POLL_MS = 15000

/**
 * How an order is watched: an order on its way (pending, or paid and being
 * credited) often; an expired or cancelled one that a late payment may still
 * credit (the server's `payable`) slowly; a completed or failed one, or one
 * nothing can credit any more, not at all.
 */
export function pollInterval(order: Order | null): number {
  if (!order || order.status === 'pending' || order.status === 'paid') return POLL_MS
  return order.payable && (order.status === 'expired' || order.status === 'cancelled') ? LATE_POLL_MS : 0
}

/**
 * The live state of one order while `active()`: read at once, then as
 * pollInterval says. A failed read keeps the last state (the next one tries
 * again); a different order starts from "pending".
 */
export function useOrderPolling(outTradeNo: () => string | null, active: () => boolean) {
  const order = ref<Order | null>(null)
  const status = computed<OrderStatus>(() => order.value?.status ?? 'pending')
  /** A late payment may still credit this expired or cancelled order. */
  const late = computed(() => pollInterval(order.value) === LATE_POLL_MS)

  async function poll() {
    const no = outTradeNo()
    if (!no) return
    try {
      const o = await topupApi.order(no)
      if (no === outTradeNo()) order.value = o
    } catch {
      // keep the last state; the next tick retries
    }
  }

  const on = () => !!outTradeNo() && active()
  const readNow = refreshAfterRunning(
    useAutoRefresh(poll, () => pollInterval(order.value), { enabled: on }),
    on
  )
  watch(outTradeNo, () => (order.value = null))
  // The first read of each order is ours (useAutoRefresh only ticks after one).
  watch(
    () => (on() ? outTradeNo() : null),
    no => no && readNow(),
    { immediate: true }
  )

  return { order, status, late }
}

/**
 * refresh() that is not lost while a read runs: useAutoRefresh skips it then
 * (the read may be the previous order's, and the new one would wait a whole
 * tick), so it runs once that read ends, if still `wanted()`.
 */
function refreshAfterRunning(timer: AutoRefresh, wanted: () => boolean) {
  let again = false
  watch(timer.running, running => {
    if (running || !again) return
    again = false
    if (wanted()) void timer.refresh()
  })
  return () => (timer.running.value ? (again = true) : void timer.refresh())
}
