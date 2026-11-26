import type { Tone } from '@sub2api/ui'
import type { OrderStatus, PromoCode } from '../host'

// Badge tones of the payment statuses; labels come from the messages
// (status.* for orders, admin.<kind>.status.* for the others).

export type StatusKind = 'order' | 'promo' | 'usage'

/** What a promo code shows: its switch, or why an enabled one no longer applies. */
export type PromoState = 'active' | 'disabled' | 'expired' | 'exhausted'

export const ORDER_STATUSES: OrderStatus[] = ['pending', 'paid', 'recharging', 'completed', 'failed', 'expired', 'cancelled']

const TONES: Record<StatusKind, Record<string, Tone>> = {
  order: {
    pending: 'warning',
    paid: 'info',
    recharging: 'info',
    completed: 'success',
    failed: 'danger',
    expired: 'gray',
    cancelled: 'gray'
  },
  promo: { active: 'success', disabled: 'gray', expired: 'warning', exhausted: 'warning' },
  usage: { reserved: 'warning', used: 'success' }
}

/** Message key prefix of each kind's status labels. */
const LABEL_PREFIX: Record<StatusKind, string> = { order: 'status', promo: 'admin.promo.status', usage: 'admin.promo.usage.status' }

export const statusTones = (kind: StatusKind) => TONES[kind]

export const statusLabelKey = (kind: StatusKind, status: string) => `${LABEL_PREFIX[kind]}.${status}`

export function promoState(p: PromoCode, now = Date.now()): PromoState {
  if (p.status === 'disabled') return 'disabled'
  if (p.expires_at && Date.parse(p.expires_at) <= now) return 'expired'
  return p.max_uses > 0 && p.used_count >= p.max_uses ? 'exhausted' : 'active'
}
