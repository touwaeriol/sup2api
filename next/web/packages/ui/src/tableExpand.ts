import { ref, watch, type Ref } from 'vue'

/** A row of the table with its key (rowKey, else the index). */
export interface KeyedRow<T> {
  row: T
  key: unknown
}

export interface TableExpandOptions<T> {
  expandable: () => boolean
  /** Opening a row closes the others. */
  single: () => boolean
  /** Rows it is true for start open the first time they are listed (UI 4.2). */
  initially: () => ((row: T) => boolean) | undefined
  /** A row opened or closed: the ones expandSingle closes, before the one that opens. */
  onExpand: (row: T, open: boolean) => void
}

/** Opens the rows `initially` picks the first time their key is listed; with `single`, only the first, while none is open. */
function openInitially<T>(keyed: Readonly<Ref<KeyedRow<T>[]>>, expanded: Ref<Set<unknown>>, options: TableExpandOptions<T>) {
  const seen = new Set<unknown>()
  watch(
    keyed,
    (rows) => {
      const starts = options.initially()
      const fresh = rows.filter((k) => !seen.has(k.key))
      fresh.forEach((k) => seen.add(k.key))
      const picked = starts && options.expandable() ? fresh.filter((k) => starts(k.row)) : []
      const open = options.single() ? picked.slice(0, expanded.value.size ? 0 : 1) : picked
      if (open.length) expanded.value = new Set([...expanded.value, ...open.map((k) => k.key)])
    },
    { immediate: true }
  )
}

/**
 * Which rows of STable are open. A row seen once keeps what the user made
 * of it: `initially` only opens a key the table has not listed before.
 */
export function useTableExpand<T>(keyed: Readonly<Ref<KeyedRow<T>[]>>, options: TableExpandOptions<T>) {
  const expanded = ref(new Set<unknown>())
  openInitially(keyed, expanded, options)

  function toggle(row: T, key: unknown) {
    const open = !expanded.value.has(key)
    const single = open && options.single()
    // expandSingle closes the other rows; the ones still listed report it, before the row that opens.
    const closed = single ? keyed.value.filter((k) => k.key !== key && expanded.value.has(k.key)) : []
    const s = new Set(single ? [] : expanded.value)
    if (open) s.add(key)
    else s.delete(key)
    expanded.value = s
    for (const k of closed) options.onExpand(k.row, false)
    options.onExpand(row, open)
  }

  /** What a cell slot gets of its row's expansion, in the table and in a card: closed, and a toggle that does nothing, when rows do not expand. */
  const expansion = (row: T, key: unknown) => ({
    expanded: options.expandable() && expanded.value.has(key),
    toggleExpand: () => (options.expandable() ? toggle(row, key) : undefined)
  })

  return { expanded, toggle, expansion }
}
