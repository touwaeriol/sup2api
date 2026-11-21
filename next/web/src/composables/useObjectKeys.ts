import { toRaw } from 'vue'

/**
 * Keys for the objects of an edited list (`v-for … :key="keyOf(item)"`): an
 * object keeps its key, and its row keeps its own state, when ones before it
 * go or move. A reactive proxy has its object's key. The keys are held here,
 * never in the objects, so nothing of them is saved.
 */
export function useObjectKeys(): (item: object) => number {
  const keys = new WeakMap<object, number>()
  let last = 0
  return (item) => {
    const raw = toRaw(item)
    let key = keys.get(raw)
    if (key === undefined) keys.set(raw, (key = ++last))
    return key
  }
}
