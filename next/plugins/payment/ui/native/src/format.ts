import { usePaymentHost } from './host'

// Money formatting shared by the payment pages: balance amounts are USD (the
// core's balance unit), what users pay is in the configured local currency.

type Amount = number | string | null | undefined

/** A balance amount in USD with two decimals ("$12.50"), '—' when missing. */
export function formatUsd(v: Amount): string {
  return usePaymentHost().i18n.formatMoney(v, 2)
}

/** An amount in a local currency with its symbol ("¥73.00"); an unknown code is written before the number. */
export function formatLocal(v: Amount, currency: string): string {
  if (v === null || v === undefined || v === '') return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return String(v)
  const locale = usePaymentHost().i18n.locale.value === 'zh' ? 'zh-CN' : 'en-US'
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay: 'narrowSymbol' }).format(n)
  } catch {
    return `${currency} ${n.toFixed(2)}`
  }
}
