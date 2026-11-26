import type { CreatedOrder, Order } from '../topup'

/** One tile of ChoiceTiles; `title` explains a disabled one. */
export interface Tile<T> {
  value: T
  disabled?: boolean
  title?: string
}

/**
 * What the waiting dialog starts from: a new order (the create answer, which
 * has no amounts) or a pending one from the history. The amounts shown are
 * always the order's own, read back from the server.
 */
export type PayingOrder = Pick<Order, 'out_trade_no' | 'expires_at' | 'method_name'> &
  Partial<Pick<Order, 'amount' | 'bonus' | 'pay_amount' | 'currency'>> &
  Pick<CreatedOrder, 'pay_url' | 'qr_code'>
