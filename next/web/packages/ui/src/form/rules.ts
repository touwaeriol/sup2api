// Validation rules of the form framework (useForm), a subset of the
// async-validator rule shape that Element Plus, Ant Design Vue and Naive UI
// share: required, whitespace, type, len / min / max, pattern, enum, a custom
// (sync or async) validator, message, trigger, and `when` for conditionally
// shown fields. Implemented here, without a dependency.
//
// Evaluation of one field (the rules of the path, in order; the first
// failure is the field's message, as Ant Design Vue's validateFirst):
// - A stored secret (SECRET_MASK) counts as a present, unchanged value:
//   `required` passes and every other check, the validator included, is
//   skipped (the core puts the stored value back).
// - `required` fails on undefined, null, '' and []. `whitespace` fails a
//   string of spaces only.
// - An empty value that is not required skips the built-in checks (type,
//   len, min, max, pattern, enum), as async-validator does; a `validator`
//   still runs, so it can check emptiness against other fields.
// - `len` / `min` / `max`: string length in code points (as JSON Schema and
//   Go count), array length, or the number itself. `len` wins over min / max.
// - `pattern` applies to strings only.
// - A validator returns the message, or true / undefined when valid. A
//   rejected (or throwing) validator fails with the error's message.
import { isBlank } from '../value'

/** A stored secret as the console hands it back: "present, unchanged". */
export const SECRET_MASK = '******'

export type Trigger = 'change' | 'blur'
export type RuleType = 'string' | 'number' | 'integer' | 'boolean' | 'array' | 'object' | 'url' | 'email' | 'date-time'

export interface RuleContext<M = Record<string, unknown>> {
  /** Concrete path of the value ('a.b.0.c'). */
  path: string
  /** The whole form value. */
  model: M
  /** The label of the SField / SSettingRow showing the path, for messages. */
  label?: string
  /** Aborted when a newer run of the same field starts. */
  signal: AbortSignal
}

/** A message: literal text, or a function so it follows the current locale (host.t). */
export type RuleMessage = string | ((ctx: RuleContext<any>) => string)

export interface Rule<V = unknown, M = Record<string, unknown>> {
  /** '', null, undefined, [] fail; SECRET_MASK passes (a stored secret). */
  required?: boolean
  /** A string of spaces only fails. */
  whitespace?: boolean
  type?: RuleType
  /** string / array: length; number / integer: value. */
  min?: number
  max?: number
  len?: number
  /** Strings only; SECRET_MASK is never matched against it. */
  pattern?: RegExp
  enum?: readonly unknown[]
  /** Custom check: return an error message, or true / undefined when valid. May be async. */
  validator?: (value: V, ctx: RuleContext<M>) => string | true | void | Promise<string | true | void>
  /** Overrides the built-in message of this rule (and a validator's rejection). */
  message?: RuleMessage
  /** Runs only on these triggers (validate() runs every rule); unset: lazy, then eager. */
  trigger?: Trigger | Trigger[]
  /** The rule applies only while this holds (conditionally shown fields). */
  when?: (model: M) => boolean
}

export type Rules<M = Record<string, unknown>> = Record<string, Rule<any, M> | Rule<any, M>[]>

export type Translate = (key: string, params?: Record<string, unknown>) => string

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const URL_RE = /^https?:\/\/[^\s]+$/i
const DATE_TIME_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/i

/** One item or a list, as a list. */
export function toList<T>(v: T | readonly T[] | undefined): T[] {
  return v === undefined ? [] : Array.isArray(v) ? [...v] : [v as T]
}

/** The rule runs on `trigger` (undefined: validate(), every rule); `touched` decides the default. */
export function runsOn(rule: Rule<any, any>, trigger: Trigger | undefined, touched: boolean): boolean {
  if (!trigger) return true
  if (rule.trigger) return toList(rule.trigger).includes(trigger)
  return trigger === 'blur' || touched
}

const TYPE_CHECK: Record<RuleType, (v: unknown) => boolean> = {
  string: (v) => typeof v === 'string',
  number: (v) => typeof v === 'number' && Number.isFinite(v),
  integer: (v) => Number.isInteger(v),
  boolean: (v) => typeof v === 'boolean',
  array: Array.isArray,
  object: (v) => typeof v === 'object' && v !== null && !Array.isArray(v),
  url: (v) => typeof v === 'string' && URL_RE.test(v),
  email: (v) => typeof v === 'string' && EMAIL_RE.test(v),
  'date-time': (v) => typeof v === 'string' && DATE_TIME_RE.test(v) && !Number.isNaN(Date.parse(v))
}
const TYPE_MESSAGE: Partial<Record<RuleType, string>> = { integer: 'integer', number: 'number', url: 'url', email: 'email', 'date-time': 'dateTime' }

/** Message keys (ui.form.v.*) of a range check by the kind of value. */
const RANGE_KEYS = {
  string: { min: 'minLength', max: 'maxLength', len: 'lenString' },
  array: { min: 'minItems', max: 'maxItems', len: 'lenItems' },
  number: { min: 'minimum', max: 'maximum', len: 'lenNumber' }
} as const

function measure(v: unknown): { kind: keyof typeof RANGE_KEYS; size: number } | null {
  if (typeof v === 'string') return { kind: 'string', size: [...v].length }
  if (Array.isArray(v)) return { kind: 'array', size: v.length }
  if (typeof v === 'number') return { kind: 'number', size: v }
  return null
}

/** The built-in check of one rule that fails: [message key, params], or null. */
function builtinFailure(rule: Rule<any, any>, value: unknown): [string, Record<string, unknown>?] | null {
  const blank = isBlank(value)
  if (rule.required && blank) return ['required']
  if (rule.whitespace && typeof value === 'string' && value !== '' && value.trim() === '') return ['whitespace']
  if (blank) return null
  if (rule.type && !TYPE_CHECK[rule.type](value)) return [`type.${TYPE_MESSAGE[rule.type] ?? 'invalid'}`]
  const m = measure(value)
  if (m && rule.len !== undefined) {
    if (m.size !== rule.len) return [RANGE_KEYS[m.kind].len, { n: rule.len }]
  } else if (m) {
    if (rule.min !== undefined && m.size < rule.min) return [RANGE_KEYS[m.kind].min, { n: rule.min }]
    if (rule.max !== undefined && m.size > rule.max) return [RANGE_KEYS[m.kind].max, { n: rule.max }]
  }
  if (rule.pattern && typeof value === 'string') {
    rule.pattern.lastIndex = 0
    if (!rule.pattern.test(value)) return ['pattern']
  }
  if (rule.enum && !rule.enum.includes(value)) return ['enum']
  return null
}

/** The rule's own message, if it has one. */
function ownMessage(rule: Rule<any, any>, ctx: RuleContext<any>): string | undefined {
  return typeof rule.message === 'function' ? rule.message(ctx) : rule.message
}

function messageOf(rule: Rule<any, any>, ctx: RuleContext<any>, t: Translate, key: string, params: Record<string, unknown> = {}): string {
  return ownMessage(rule, ctx) ?? t(`ui.form.v.${key}`, { label: ctx.label ?? '', ...params })
}

/** Runs `rules` on `value`; resolves the first failure's message, or undefined when valid. */
export async function checkValue<M>(rules: Rule<any, M>[], value: unknown, ctx: RuleContext<M>, t: Translate): Promise<string | undefined> {
  if (value === SECRET_MASK) return undefined
  for (const rule of rules) {
    const failed = builtinFailure(rule, value)
    if (failed) return messageOf(rule, ctx, t, ...failed)
    if (!rule.validator) continue
    let out: string | true | void
    try {
      out = await rule.validator(value, ctx)
    } catch (e) {
      if (ctx.signal.aborted) return undefined
      return ownMessage(rule, ctx) ?? (e instanceof Error ? e.message : String(e))
    }
    if (typeof out === 'string') return out
  }
  return undefined
}

/** Whether `rules` make the value required (SField's star). */
export function requires<M>(rules: Rule<any, M>[]): boolean {
  return rules.some((r) => r.required)
}
