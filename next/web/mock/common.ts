// Helpers the resource mock areas share: a text search over a row's fields
// and a 400 naming one field.
import { fail } from './router'

export function match(q: string | undefined, ...fields: Array<string | undefined | null>) {
  if (!q) return true
  const s = q.toLowerCase()
  return fields.some((f) => (f || '').toLowerCase().includes(s))
}

export function invalid(field: string, message: string, code = 'invalid') {
  return fail(400, 'invalid_argument', message, { fields: [{ field, code, message }] })
}
