import type { Tone } from '@sub2api/ui'
import { formatCurrency } from '@/utils/format'
import type { PaymentEvent } from '../topup'

// How an order's gateway events read in its detail: the kind's label (a kind
// without one as the core wrote it) and the detail fields worth showing.

/** The detail fields shown, in order. */
export const EVENT_FIELDS = ['trade_no', 'status', 'amount', 'type', 'error'] as const

/** A kind's label where the messages have one (topup.admin.orders.events.kind.*), else the kind itself. */
export function eventKindLabel(t: (key: string) => string, te: (key: string) => boolean, kind: string): string {
  const key = `topup.admin.orders.events.kind.${kind}`
  return te(key) ? t(key) : kind
}

/** Accepted is fine, a duplicate is noise, anything else needs a look. */
export function eventTone(kind: string): Tone {
  if (kind === 'NOTIFY_OK') return 'success'
  return kind === 'DUPLICATE' ? 'gray' : 'warning'
}

/** A detail field as shown: the amount (the server gives it in the major unit) with its currency, the others as kept. */
export function eventField(e: PaymentEvent, key: (typeof EVENT_FIELDS)[number]): string {
  const v = e.detail[key] ?? ''
  const currency = e.detail.currency
  return key === 'amount' && v && currency ? formatCurrency(v, currency) : v
}
