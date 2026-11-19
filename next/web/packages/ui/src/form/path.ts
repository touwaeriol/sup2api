// Dotted paths ('a.b.0.c') into a form value. Rule keys may hold '*' for
// every item of an array (or key of an object): 'items.*.url'.
import { toRaw } from 'vue'
import { getPath } from '../value'

export { getPath }

const isIndex = (k: string) => /^\d+$/.test(k)

/**
 * Writes `value` at `path` without touching `obj`: every container on the
 * way is copied, everything else is shared. `undefined` removes an object
 * key (the key is not sent). Missing containers are created (an array when
 * the next step is an index).
 */
export function setPath<T>(obj: T, path: string, value: unknown): T {
  return write(obj, path.split('.'), value) as T
}

function write(node: unknown, [key, ...rest]: string[], value: unknown): unknown {
  const copy: any = Array.isArray(node) ? [...node] : node && typeof node === 'object' ? { ...node } : isIndex(key) ? [] : {}
  const next = rest.length ? write(copy[key], rest, value) : value
  if (next === undefined && !Array.isArray(copy)) delete copy[key]
  else copy[key] = next
  return copy
}

/** The concrete paths `pattern` names in `model` ('*' expanded over the current items). */
export function expandPath(pattern: string, model: unknown): string[] {
  const star = pattern.indexOf('*')
  if (star < 0) return [pattern]
  const head = pattern.slice(0, star).replace(/\.$/, '')
  const tail = pattern.slice(star + 1)
  const list = head ? getPath(model, head) : model
  if (!list || typeof list !== 'object') return []
  return Object.keys(list).flatMap((k) => expandPath(`${head ? head + '.' : ''}${k}${tail}`, model))
}

/** `pattern` (with '*' steps) names `path`. */
export function matchPath(pattern: string, path: string): boolean {
  return pattern.split('.').length === path.split('.').length && overlaps(pattern, path)
}

/**
 * One path holds the other (equal, ancestor or descendant; '*' matches any
 * step): a write to one changes the other.
 */
export function overlaps(a: string, b: string): boolean {
  const x = a.split('.')
  const y = b.split('.')
  return x.slice(0, y.length).every((k, i) => k === y[i] || k === '*' || y[i] === '*')
}

const isPlain = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)

/** `value` with the absent keys of `defaults` filled, nested objects too (the same object when nothing was missing). */
export function fillMissing<T extends Record<string, unknown>>(value: T, defaults: Record<string, unknown>): T {
  let out = value
  for (const [k, d] of Object.entries(defaults)) {
    const cur = value[k]
    const next = cur === undefined ? structuredClone(toRaw(d)) : isPlain(cur) && isPlain(d) ? fillMissing(cur, d) : cur
    if (next !== cur) out = { ...out, [k]: next }
  }
  return out
}
