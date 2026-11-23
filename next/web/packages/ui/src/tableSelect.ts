import { computed, type Ref } from 'vue'
import type { KeyedRow } from './tableExpand'
import { useTableSwipeSelect } from './tableSwipeSelect'

export interface TableSelectOptions<T> {
  /** The keys selected now (v-model:selected). */
  selected: () => unknown[]
  /** Rows it is false for cannot be selected; every row can without it. */
  rowSelectable: () => ((row: T) => boolean) | undefined
  /** A drag across rows selects a range (selectable, swipeSelect, not loading). */
  swipe: () => boolean
  /** The new selection (update:selected). */
  update: (keys: unknown[]) => void
}

/** Shift+click on a row's checkbox sets the range from the row clicked before (the anchor) to this one. */
function useRowToggle<T>(keyed: Readonly<Ref<KeyedRow<T>[]>>, canSelect: (row: T) => boolean, options: TableSelectOptions<T>) {
  let anchor = -1
  let shiftClick = false
  const markShift = (e: MouseEvent) => {
    shiftClick = e.shiftKey
  }
  function toggleRow(key: unknown, on: boolean, index: number) {
    const range = shiftClick && anchor >= 0 && anchor < keyed.value.length && anchor !== index
    shiftClick = false
    const lo = range ? Math.min(anchor, index) : index
    const hi = range ? Math.max(anchor, index) : index
    anchor = index
    const keys = keyed.value
      .slice(lo, hi + 1)
      .filter(r => canSelect(r.row))
      .map(r => r.key)
    const touched = new Set(keys)
    const rest = options.selected().filter(x => !touched.has(x))
    options.update(on ? [...rest, ...keys] : rest)
  }
  return { markShift, toggleRow }
}

/** Which rows of STable are selected: the select-all state of the listed rows, a row's checkbox (with a Shift range) and a swipe. */
export function useTableSelect<T>(
  keyed: Readonly<Ref<KeyedRow<T>[]>>,
  table: Ref<HTMLTableElement | undefined>,
  options: TableSelectOptions<T>
) {
  const selectedSet = computed(() => new Set(options.selected()))
  const canSelect = (row: T) => {
    const can = options.rowSelectable()
    return !can || can(row)
  }
  const selectableKeys = computed(() => keyed.value.filter(r => canSelect(r.row)).map(r => r.key))
  const allSelected = computed(() => selectableKeys.value.length > 0 && selectableKeys.value.every(k => selectedSet.value.has(k)))
  const someSelected = computed(() => !allSelected.value && selectableKeys.value.some(k => selectedSet.value.has(k)))
  function toggleAll(on: boolean) {
    const pageKeys = new Set(selectableKeys.value)
    const rest = options.selected().filter(k => !pageKeys.has(k))
    options.update(on ? [...rest, ...selectableKeys.value] : rest)
  }
  const swipe = useTableSwipeSelect(table, {
    enabled: options.swipe,
    keys: () => keyed.value.map(r => r.key),
    canSelect: i => !!keyed.value[i] && canSelect(keyed.value[i].row),
    selected: options.selected,
    update: options.update
  })
  return { selectedSet, canSelect, selectableKeys, allSelected, someSelected, toggleAll, swipe, ...useRowToggle(keyed, canSelect, options) }
}
