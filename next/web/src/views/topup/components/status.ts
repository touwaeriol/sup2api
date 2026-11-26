import type { SelectOption, Tone } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { OrderStatus } from '../topup'

// Badge tones of the payment statuses; labels come from the messages
// (status.* for orders, admin.<kind>.status.* for the others).

export type StatusKind = 'order' | 'promo' | 'usage'

export const ORDER_STATUSES: OrderStatus[] = ['pending', 'paid', 'completed', 'failed', 'expired', 'cancelled']

const TONES: Record<StatusKind, Record<string, Tone>> = {
  order: {
    pending: 'warning',
    paid: 'info',
    completed: 'success',
    failed: 'danger',
    expired: 'gray',
    cancelled: 'gray'
  },
  promo: { active: 'success', disabled: 'gray', expired: 'warning', exhausted: 'warning' },
  usage: { reserved: 'warning', used: 'success' }
}

/** Message key prefix of each kind's status labels. */
const LABEL_PREFIX: Record<StatusKind, string> = {
  order: 'topup.status',
  promo: 'topup.admin.promo.status',
  usage: 'topup.admin.promo.usage.status'
}

export const statusTones = (kind: StatusKind) => TONES[kind]

export const statusLabelKey = (kind: StatusKind, status: string) => `${LABEL_PREFIX[kind]}.${status}`

/** The options of an order status filter: "any" (labelled `any`) first, then each status. */
export const orderStatusOptions = (any: string): SelectOption[] => [
  { value: '', label: any },
  ...ORDER_STATUSES.map(s => ({ value: s, label: i18n.global.t(statusLabelKey('order', s)) }))
]
