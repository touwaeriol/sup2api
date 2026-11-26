import { useTopup } from './topup'

// Money formatting shared by the payment pages: balance amounts are USD (the
// core's balance unit), what users pay is in the configured local currency.

type Amount = number | string | null | undefined

/** Rounds to cents (the gateways charge whole cents). */
export const cents = (v: number) => Math.round(v * 100) / 100

/** A balance amount in USD with two decimals ("$12.50"), '—' when missing. */
export function formatUsd(v: Amount): string {
  return useTopup().i18n.formatMoney(v, 2)
}

/** An amount in a local currency with its narrow symbol ("¥73.00", not "CN¥"); an unknown code is written before the number. */
export function formatLocal(v: Amount, currency: string): string {
  if (v === null || v === undefined || v === '') return '—'
  const n = Number(v)
  if (!Number.isFinite(n)) return String(v)
  const locale = useTopup().i18n.locale.value === 'zh' ? 'zh-CN' : 'en-US'
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency, currencyDisplay: 'narrowSymbol' }).format(n)
  } catch {
    return `${currency} ${n.toFixed(2)}`
  }
}

/** Both readings of a discount rate, for the locale's message to pick: 0.95 -> 9.5 (折) / 5 (% off). */
export const discountParams = (rate: number) => ({ zhe: Number((rate * 10).toFixed(2)), off: Number(((1 - rate) * 100).toFixed(2)) })

/** "9.5 折" / "5% off" of a rate below 1 (0.95), else "". The wording is the wallet's, which settings previews too. */
export function discountLabel(rate: number): string {
  if (!(rate > 0 && rate < 1)) return ''
  return useTopup().t('topup.wallet.discount', discountParams(rate))
}
