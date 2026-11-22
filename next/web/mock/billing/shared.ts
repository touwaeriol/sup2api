// What the billing mock's modules share: bilingual messages, money as the
// server writes it, hashes, the seeded random numbers and field errors.
import { createHash } from 'node:crypto'
import { fail } from '../router'

export type LText = { en: string; zh: string }
export const msg = (en: string, zh: string): LText => ({ en, zh })
export const money = (n: number) => n.toFixed(8)
export const hashOf = (s: string) => createHash('sha256').update(s).digest('hex')

/** A number in [0, 1) that depends only on the seed. */
export function rnd(seed: number) {
  const x = Math.sin(seed) * 10000
  return x - Math.floor(x)
}

export type FieldError = { field: string; code: string; message: string }
export const fieldFail = (fields: FieldError[]) => fail(400, 'invalid_argument', 'invalid', { fields })
