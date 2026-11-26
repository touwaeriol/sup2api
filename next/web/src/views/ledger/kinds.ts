import { computed } from 'vue'
import type { RangeKey, SelectOption, Tone } from '@sub2api/ui'
import { i18n } from '@/i18n'

export const LEDGER_KINDS = ['usage', 'admin_adjust', 'topup', 'promo_bonus', 'redeem', 'plugin_credit', 'plugin_debit', 'refund'] as const

/** Time ranges offered by the ledger filters. */
export const LEDGER_RANGES: RangeKey[] = ['all', 'today', '7d', '30d', 'month', 'custom']

export const KIND_TONE: Record<string, Tone> = { admin_adjust: 'primary', topup: 'success', promo_bonus: 'success', redeem: 'success', refund: 'success', plugin_credit: 'success', plugin_debit: 'warning' }

/** "All" plus every ledger kind, for the kind filter. */
export function useKindOptions() {
  const t = i18n.global.t
  return computed<SelectOption[]>(() => [{ value: '', label: t('common.all') }, ...LEDGER_KINDS.map((k) => ({ value: k, label: t(`ledger.kinds.${k}`) }))])
}
