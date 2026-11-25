import type { CreatedOrder, Order } from '../host'

/** One tile of ChoiceTiles; `title` explains a disabled one. */
export interface Tile<T> {
  value: T
  disabled?: boolean
  title?: string
}

/** What the waiting dialog shows of an order: a new one (created + previewed) or a pending one from the history. */
export type PayingOrder = Pick<Order, 'out_trade_no' | 'amount' | 'bonus' | 'pay_amount' | 'currency' | 'expires_at' | 'method_name'> &
  Pick<CreatedOrder, 'pay_url' | 'qr_code'>
