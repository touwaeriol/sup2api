import { isApiError } from '@sub2api/host'
import { usePaymentHost, type GatewayKey, type Order, type RefundMode } from '../host'
import { formatUsd } from '../format'

// Amounts and modes of an order's refund. The plugin decides the final
// amounts; these mirror its rules so the refund dialog can show them first.

/** How each gateway's orders are refunded: Stripe through its refund API, Epay by hand at the provider. */
export const REFUND_MODES: Record<GatewayKey, RefundMode> = {
  stripe: 'gateway',
  epay: 'manual'
}

const cents = (n: number) => Math.round(n * 100) / 100

/** Balance left to refund on the order (USD). */
export const refundLeft = (o: Order) => cents(o.amount - o.refunded_amount)

/** The share of what the user paid that matches `amount` of the order's balance, in the order's currency. */
export const payShare = (o: Order, amount: number) => (o.amount > 0 ? cents((o.pay_amount * amount) / o.amount) : 0)

/** Whether refunding `amount` refunds what is left of the order. */
export const refundsAll = (o: Order, amount: number) => amount >= refundLeft(o)

/** Balance taken back for `amount`: the bonus goes with the refund that leaves nothing on the order. */
export const deductFor = (o: Order, amount: number) => cents(amount + (refundsAll(o, amount) ? o.bonus : 0))

/** A 409 insufficient_balance as the dialog shows it (the server's message, the balance and the debit); null for any other error. */
export function shortfallOf(e: unknown): { message: string; detail: string } | null {
  if (!isApiError(e) || e.code !== 'insufficient_balance') return null
  const { balance, deduct } = e.details
  const detail =
    balance === undefined || deduct === undefined
      ? ''
      : usePaymentHost().t('admin.refund.shortfall', {
          balance: formatUsd(balance),
          deduct: formatUsd(deduct)
        })
  return { message: e.message, detail }
}
