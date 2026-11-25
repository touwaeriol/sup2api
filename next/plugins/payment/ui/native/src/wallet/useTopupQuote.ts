import { computed, ref, watch, type Ref } from 'vue'
import type { CheckoutInfo, DiscountTier } from '../host'
import { cents } from './money'

export type CheckoutMethod = CheckoutInfo['methods'][number]

/** Price of an amount before promo codes, as the server's preview computes it. */
export interface Quote {
  amount: number
  rate: number
  original: number
  pay: number
}

/** Best discount rate reached by `amount` (1 = none). */
export function rateOf(amount: number, tiers: DiscountTier[]): number {
  return tiers.filter((d) => amount >= d.amount).reduce((r, d) => Math.min(r, d.rate), 1)
}

export function quoteOf(amount: number, info: CheckoutInfo): Quote {
  const rate = rateOf(amount, info.discounts)
  const original = cents(amount * info.price)
  return { amount, rate, original, pay: cents(original * rate) }
}

/** Minimum top-up through a method: the global one or the method's own, whichever is higher. */
export function methodMin(info: CheckoutInfo, m: CheckoutMethod): number {
  return Math.max(info.min_topup, m.min_topup || 0)
}

/** Whether the amount is too low for a method (an empty amount blocks none). */
export function belowMin(amount: number | null, info: CheckoutInfo, m: CheckoutMethod): boolean {
  return !!amount && amount < methodMin(info, m)
}

export type AmountError = '' | 'required' | 'min' | 'max'

/**
 * State of the top-up card: the amount (a preset or typed), the method and
 * the local price estimate. The confirm dialog asks the server (preview) for
 * the final price; this only drives the card.
 */
export function useTopupQuote(info: Ref<CheckoutInfo | null>) {
  const amount = ref<number | null>(null)
  const method = ref('')

  const methods = computed(() => info.value?.methods ?? [])
  const available = computed(() => !!info.value?.enabled && methods.value.length > 0)
  const selected = computed(() => methods.value.find((m) => m.type === method.value))
  const min = computed(() => (info.value && selected.value ? methodMin(info.value, selected.value) : (info.value?.min_topup ?? 0)))
  const quote = computed(() => (info.value && amount.value ? quoteOf(amount.value, info.value) : null))

  const error = computed<AmountError>(() => {
    const v = amount.value
    if (!v) return 'required'
    if (v < min.value) return 'min'
    return info.value && v > info.value.max_topup ? 'max' : ''
  })

  const blocked = (m: CheckoutMethod) => !!info.value && belowMin(amount.value, info.value, m)

  // Start on the first preset and method; move off a method the amount no longer reaches.
  watch(info, (v) => {
    amount.value = v?.amount_options.find((a) => a >= v.min_topup) ?? null
    method.value = v?.methods[0]?.type ?? ''
  })
  watch(amount, () => {
    if (!selected.value || !blocked(selected.value)) return
    method.value = methods.value.find((m) => !blocked(m))?.type ?? method.value
  })

  return { amount, method, methods, available, selected, min, quote, error, blocked }
}
