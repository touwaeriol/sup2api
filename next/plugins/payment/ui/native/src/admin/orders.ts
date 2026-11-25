import { computed, ref } from 'vue'
import type { FilterChipItem, RangeKey, SelectOption } from '@sub2api/ui'
import { usePaymentHost } from '../host'
import { loadMethods } from '../components/methodCatalog'
import { ORDER_STATUSES } from '../components/status'

// Filters of the orders page.

export const ORDER_RANGES: RangeKey[] = ['all', 'today', '7d', '30d', 'month', 'lastMonth', 'custom']

export const emptyOrderFilters = () => ({ q: '', status: '', method: '', from: '', to: '' })
export type OrderFilters = ReturnType<typeof emptyOrderFilters>

/** Status and method options (method names from the checkout's methods), the chips and counts of the filter bar. */
export function useOrderFilters(filters: OrderFilters) {
  const host = usePaymentHost()
  const t = host.t
  const range = ref<RangeKey>('all')
  const methods = ref<SelectOption[]>([])
  // The enabled methods name the options; without them the filter offers only "any".
  loadMethods().then((list) => (methods.value = list.map((m) => ({ value: m.type, label: m.name }))), () => undefined)

  const any = (name: string): SelectOption => ({ value: '', label: t('admin.anyOf', { name }) })
  const statusOptions = computed(() => [any(t('admin.orders.fields.status')), ...ORDER_STATUSES.map((s) => ({ value: s, label: t(`status.${s}`) }))])
  const methodOptions = computed(() => [any(t('admin.orders.fields.method')), ...methods.value])
  const methodLabel = (v: string) => methods.value.find((m) => m.value === v)?.label ?? v

  const chips = computed<FilterChipItem[]>(() => (filters.method ? [{ key: 'method', label: t('admin.orders.fields.method'), value: methodLabel(filters.method) }] : []))
  const activeCount = computed(() => [filters.status, range.value !== 'all'].filter(Boolean).length)
  const advancedCount = computed(() => chips.value.length)
  const dirty = computed(() => activeCount.value + advancedCount.value > 0 || !!filters.q)

  function reset() {
    range.value = 'all'
    Object.assign(filters, emptyOrderFilters())
  }

  return { range, statusOptions, methodOptions, chips, activeCount, advancedCount, dirty, reset }
}
