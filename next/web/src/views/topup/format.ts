import { i18n } from '@/i18n'

// How a discount rate reads, shared by the wallet and the settings preview.

/** Both readings of a discount rate, for the locale's message to pick: 0.95 -> 9.5 (折) / 5 (% off). */
const discountParams = (rate: number) => ({ zhe: Number((rate * 10).toFixed(2)), off: Number(((1 - rate) * 100).toFixed(2)) })

/** "9.5 折" / "5% off" of a rate below 1 (0.95, or "0.95" as the server sends it), else "". */
export function discountLabel(rate: number | string): string {
  const r = Number(rate)
  return r > 0 && r < 1 ? i18n.global.t('topup.wallet.discount', discountParams(r)) : ''
}
