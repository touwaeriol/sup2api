import { usePaymentHost } from '../host'

// Money formatting of the wallet: balance amounts are USD through the host's
// formatMoney; what the user pays is in the checkout's local currency.

/** Rounds to cents (the gateways charge whole cents). */
export const cents = (v: number) => Math.round(v * 100) / 100

/** A balance amount: "$10.00". */
export function balance(v: number): string {
  return usePaymentHost().i18n.formatMoney(v, 2)
}

/** An amount in the local currency: "¥73.00" (narrow symbol, so not "CN¥"). */
export function localMoney(v: number, currency: string): string {
  const locale = usePaymentHost().i18n.locale.value
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay: 'narrowSymbol' }).format(v)
  } catch {
    return `${v.toFixed(2)} ${currency}`
  }
}

/** "9.5 折" / "5% off" of a rate below 1 (0.95), else "". */
export function discountLabel(rate: number): string {
  if (!(rate > 0 && rate < 1)) return ''
  const trim = (n: number) => String(Number(n.toFixed(2)))
  return usePaymentHost().t('wallet.discount', { zhe: trim(rate * 10), off: trim((1 - rate) * 100) })
}
