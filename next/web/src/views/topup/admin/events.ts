import type { Tone } from '@sub2api/ui'
import type { PaymentEvent } from '../topup'
import { formatLocal } from '../format'

// How an order's gateway events read in its detail: kinds with a label
// (others as the core wrote them), the detail fields worth showing, and the
// amount from the currency's minor unit.

/** Kinds with a translated label (topup.admin.orders.events.kind.*). */
const LABELLED = new Set([
  'NOTIFY_OK', 'SIGNATURE_INVALID', 'ORDER_NOT_FOUND', 'PAYMENT_AMOUNT_MISMATCH', 'SECOND_PAYMENT', 'DUPLICATE',
  'TRADE_NO_REUSED', 'TRADE_NO_MISSING', 'GATEWAY_UNAVAILABLE', 'GATEWAY_MISMATCH', 'MERCHANT_MISMATCH',
  'METHOD_MISMATCH', 'SESSION_MISMATCH', 'STATUS_NOT_PAYABLE', 'FIELD_TOO_LONG', 'STATUS_UNKNOWN'
])

/** The detail fields shown, in order. */
export const EVENT_FIELDS = ['trade_no', 'status', 'amount', 'type', 'error'] as const

/** Currencies without a minor unit (core.PaymentMinorUnits); the others have two decimals. */
const WHOLE = new Set(['JPY', 'KRW', 'VND', 'CLP'])

export function eventKindLabel(t: (key: string) => string, kind: string): string {
  return LABELLED.has(kind) ? t(`topup.admin.orders.events.kind.${kind}`) : kind
}

/** Accepted is fine, a duplicate is noise, anything else needs a look. */
export function eventTone(kind: string): Tone {
  if (kind === 'NOTIFY_OK') return 'success'
  return kind === 'DUPLICATE' ? 'gray' : 'warning'
}

/** A detail field as shown: the amount in the currency's major unit, the others as kept. */
export function eventField(e: PaymentEvent, key: (typeof EVENT_FIELDS)[number]): string {
  const v = e.detail[key] ?? ''
  if (key !== 'amount' || !v || !/^-?\d+$/.test(v)) return v
  const currency = (e.detail.currency ?? '').toUpperCase()
  if (!currency) return v
  return formatLocal(Number(v) / (WHOLE.has(currency) ? 1 : 100), currency)
}
