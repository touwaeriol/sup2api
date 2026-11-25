import type { Tone } from '@sub2api/ui'
import type { OrderStatus, PromoCode, RedeemStatus } from '../host'

// Badge tones of the payment statuses; labels come from the messages
// (status.* for orders, admin.<kind>.status.* for the others).

export type StatusKind = 'order' | 'redeem' | 'promo' | 'refund'

/** What a promo code shows: its switch, or why an enabled one no longer applies. */
export type PromoState = 'active' | 'disabled' | 'expired' | 'exhausted'

export const ORDER_STATUSES: OrderStatus[] = ['pending', 'paid', 'recharging', 'completed', 'failed', 'expired', 'cancelled', 'refunding', 'partially_refunded', 'refunded']
export const REDEEM_STATUSES: RedeemStatus[] = ['unused', 'used', 'expired']

const TONES: Record<StatusKind, Record<string, Tone>> = {
  order: {
    pending: 'warning',
    paid: 'info',
    recharging: 'info',
    completed: 'success',
    failed: 'danger',
    expired: 'gray',
    cancelled: 'gray',
    refunding: 'purple',
    partially_refunded: 'info',
    refunded: 'gray'
  },
  redeem: { unused: 'success', used: 'info', expired: 'gray' },
  promo: { active: 'success', disabled: 'gray', expired: 'warning', exhausted: 'warning' },
  refund: { pending: 'purple', succeeded: 'success', failed: 'danger' }
}

/** Message key prefix of each kind's status labels. */
const LABEL_PREFIX: Record<StatusKind, string> = { order: 'status', redeem: 'admin.redeem.status', promo: 'admin.promo.status', refund: 'admin.refund.status' }

export const statusTones = (kind: StatusKind) => TONES[kind]

export const statusLabelKey = (kind: StatusKind, status: string) => `${LABEL_PREFIX[kind]}.${status}`

/** Orders an admin may complete by hand: not paid through, or paid and not credited. */
export const completable = (status: OrderStatus) => status === 'pending' || status === 'expired' || status === 'failed'

/** Orders an admin may refund (again): credited, with balance left to refund. */
export const refundable = (status: OrderStatus) => status === 'completed' || status === 'partially_refunded'

export function promoState(p: PromoCode, now = Date.now()): PromoState {
  if (p.status === 'disabled') return 'disabled'
  if (p.expires_at && Date.parse(p.expires_at) <= now) return 'expired'
  return p.max_uses > 0 && p.used_count >= p.max_uses ? 'exhausted' : 'active'
}
