import { computed, reactive, ref, watch } from 'vue'
import type { FilterChipItem, RangeKey, SelectOption } from '@sub2api/ui'
import { useTopup, type Order } from '../topup'
import { ORDER_STATUSES } from '../components/status'

// Filters of the orders page.

export const ORDER_RANGES: RangeKey[] = ['all', 'today', '7d', '30d', 'month', 'lastMonth', 'custom']

export const emptyOrderFilters = () => ({ q: '', status: '', method: '', from: '', to: '' })
export type OrderFilters = ReturnType<typeof emptyOrderFilters>

/**
 * The methods of the orders listed so far (type key → the name an order
 * keeps), for the method filter: the orders name their own methods, so the
 * page needs no access to the wallet's checkout (topup:use).
 */
function useSeenMethods(rows: () => Order[]) {
  const seen = reactive(new Map<string, string>())
  watch(rows, (list) => list.forEach((o) => seen.has(o.method) || seen.set(o.method, o.method_name || o.method)), { immediate: true })
  return computed<SelectOption[]>(() => [...seen].map(([value, label]) => ({ value, label })))
}

/** Status and method options, the chips and counts of the filter bar. */
export function useOrderFilters(filters: OrderFilters, rows: () => Order[]) {
  const host = useTopup()
  const t = host.t
  const range = ref<RangeKey>('all')
  const methods = useSeenMethods(rows)

  const any = (name: string): SelectOption => ({ value: '', label: t('topup.admin.anyOf', { name }) })
  const statusOptions = computed(() => [any(t('topup.admin.orders.fields.status')), ...ORDER_STATUSES.map((s) => ({ value: s, label: t(`topup.status.${s}`) }))])
  const methodOptions = computed(() => [any(t('topup.admin.orders.fields.method')), ...methods.value])
  const methodLabel = (v: string) => methods.value.find((m) => m.value === v)?.label ?? v

  const chips = computed<FilterChipItem[]>(() => (filters.method ? [{ key: 'method', label: t('topup.admin.orders.fields.method'), value: methodLabel(filters.method) }] : []))
  const activeCount = computed(() => [filters.status, range.value !== 'all'].filter(Boolean).length)
  const advancedCount = computed(() => chips.value.length)
  const dirty = computed(() => activeCount.value + advancedCount.value > 0 || !!filters.q)

  function reset() {
    range.value = 'all'
    Object.assign(filters, emptyOrderFilters())
  }

  return { range, statusOptions, methodOptions, chips, activeCount, advancedCount, dirty, reset }
}
