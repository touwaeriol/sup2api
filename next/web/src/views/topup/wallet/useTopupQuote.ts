import { computed, ref, watch, type Ref } from 'vue'
import { roundTo } from '../rounding'
import type { CheckoutInfo, CheckoutMethod, DiscountTier } from '../topup'

/** Price of an amount before promo codes, as the server's preview computes it. */
export interface Quote {
  amount: number
  rate: number
  original: number
  pay: number
}

/** Best discount rate reached by `amount` (1 = none). */
export function rateOf(amount: number, tiers: DiscountTier[]): number {
  return tiers.filter(d => amount >= Number(d.amount)).reduce((r, d) => Math.min(r, Number(d.rate)), 1)
}

/** The price of `amount` at `price` and `rate`, each figure rounded once to `digits` decimals (the currency's). */
export function priceOf(amount: number, price: number, rate: number, digits: number): Pick<Quote, 'original' | 'pay'> {
  const raw = amount * price
  return { original: roundTo(raw, digits), pay: roundTo(raw * rate, digits) }
}

/** The estimate before the preview. */
export function quoteOf(amount: number, info: CheckoutInfo): Quote {
  const rate = rateOf(amount, info.discounts)
  return { amount, rate, ...priceOf(amount, Number(info.price), rate, info.currency_exponent) }
}

/** Minimum top-up through a method: the global one or the method's own, whichever is higher. */
export function methodMin(info: CheckoutInfo, m: CheckoutMethod): number {
  return Math.max(Number(info.min_topup), Number(m.min_topup || 0))
}

/** Whether the amount is too low for a method (an empty amount blocks none). */
export function belowMin(amount: number | null, info: CheckoutInfo, m: CheckoutMethod): boolean {
  return !!amount && amount < methodMin(info, m)
}

type AmountError = '' | 'required' | 'min' | 'max'

/** What is wrong with a typed amount against the minimum and the configured maximum. */
function amountError(v: number | null, min: number, info: CheckoutInfo | null): AmountError {
  if (!v) return 'required'
  if (v < min) return 'min'
  return info && v > Number(info.max_topup) ? 'max' : ''
}

/** A typed or picked amount: positive, kept to cents (the balance unit's), else none. */
function toAmount(v: unknown): number | null {
  const n = Number(v)
  return v !== null && v !== '' && n > 0 ? roundTo(n, 2) : null
}

/**
 * State of the top-up card: the amount (a preset or typed, in balance units
 * with cents), the method and the local price estimate. The confirm dialog
 * asks the server (preview) for the final price; this only drives the card.
 */
export function useTopupQuote(info: Ref<CheckoutInfo | null>) {
  const amount = ref<number | null>(null)
  const method = ref('')

  const methods = computed(() => info.value?.methods ?? [])
  // The server lists no method while top-up is off.
  const available = computed(() => methods.value.length > 0)
  const selected = computed(() => methods.value.find(m => m.type === method.value))
  const min = computed(() => (info.value && selected.value ? methodMin(info.value, selected.value) : Number(info.value?.min_topup ?? 0)))
  const quote = computed(() => (info.value && amount.value ? quoteOf(amount.value, info.value) : null))
  const error = computed<AmountError>(() => amountError(amount.value, min.value, info.value))
  const blocked = (m: CheckoutMethod) => !!info.value && belowMin(amount.value, info.value, m)

  // Start on the first preset and method; move off a method the amount no longer reaches.
  watch(info, v => {
    amount.value = v?.amount_options.map(Number).find(a => a >= Number(v.min_topup)) ?? null
    method.value = v?.methods[0]?.type ?? ''
  })
  watch(amount, () => {
    if (!selected.value || !blocked(selected.value)) return
    method.value = methods.value.find(m => !blocked(m))?.type ?? method.value
  })

  const setAmount = (v: unknown) => (amount.value = toAmount(v))
  return { amount, method, methods, available, selected, min, quote, error, blocked, setAmount }
}
