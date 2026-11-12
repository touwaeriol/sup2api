// Helpers for SchemaForm: JSON Schema subset + uiSchema conventions.
//
// uiSchema (per field; "ui:" prefix optional):
//   ui:order        ["a", "b", "*"]       field order of an object ("*" = rest)
//   ui:widget       text | secret | textarea | select | segmented | radio | switch |
//                   number | url-presets | key-value | model-mapping | tags |
//                   multi-select | object-list | json | hidden | date-time | date |
//                   time | <any key of the `widgets` prop or of the injected host
//                   widgets (e.g. the console's proxy-select / group-select)>; an
//                   unknown name falls back to the widget the schema type implies
//                   (with a dev warning)
//   ui:title        LocalizedText label           ui:help   LocalizedText hint
//   ui:details      LocalizedText longer explanation, collapsed behind "Details"
//   ui:placeholder  LocalizedText
//   ui:options      rows (textarea), mono (textarea / text: monospace), unit
//                   (text after a number / text field), presets (url-presets),
//                   keyPlaceholder / valuePlaceholder / keyLabel / valueLabel
//                   (key-value, model-mapping), itemTitle (object-list:
//                   LocalizedText, "{n}" = 1-based index), searchable (select:
//                   true / false; default true from 10 options)
//   ui:errorMessages {required|minLength|maxLength|pattern|format|minimum|maximum|
//                   minItems|maxItems: LocalizedText} replaces the default messages
//   ui:enumNames    [LocalizedText...] labels for enum values
//   ui:visibleWhen  {field: "a.b", equals|notEquals|in|notIn|truthy|falsy}
//                   or a plain map {"mode": "oauth"} (all must match)
//   ui:section      LocalizedText, or {title, description?, collapsed?, page?}: starts a
//                   titled group of the parent object at this field (until the
//                   next field with a section). collapsed groups open on click,
//                   and by themselves while they hold a value or an error. page
//                   is not read here: a native page that splits one form over
//                   several of its sections uses it (moderation settings).
// Secrets: widget "secret", format "password" or writeOnly: true. Existing
// values come back as "******" and are sent back unchanged to keep them.
//
// LocalizedText ({"en": "...", "zh": "..."} or a bare string) is resolved with
// `localizedText(v, locale)`; the helpers below take the locale as their last
// argument (SchemaField passes vue-i18n's current locale).

import { getPath, isBlank } from '../value'

export { getPath }

export const SECRET_MASK = '******'

export type JSONSchema = Record<string, any>
export type UISchema = Record<string, any>

/** Picks `locale` from a localized text ({en, zh} or a bare string), falling back to en. */
export function localizedText(v: unknown, locale = 'en'): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'object') {
    const o = v as Record<string, string>
    return o[locale] || o.en || Object.values(o)[0] || ''
  }
  return String(v)
}

export function uiGet<T = any>(ui: UISchema | undefined, key: string): T | undefined {
  if (!ui || typeof ui !== 'object') return undefined
  if (('ui:' + key) in ui) return ui['ui:' + key]
  const v = ui[key]
  // Unprefixed keys only count when they look like ui options (not a nested field ui).
  if (v === undefined) return undefined
  if (key === 'title' || key === 'help' || key === 'placeholder') {
    return typeof v === 'string' || isLText(v) ? v : undefined
  }
  if (key === 'widget') return typeof v === 'string' ? (v as T) : undefined
  if (key === 'order' || key === 'enumNames') return Array.isArray(v) ? (v as T) : undefined
  return v
}

function isLText(v: unknown): boolean {
  return !!v && typeof v === 'object' && !Array.isArray(v) && Object.values(v as object).every((x) => typeof x === 'string')
}

export interface FieldSection {
  title: string
  description: string
  collapsed: boolean
}

/** The ui:section a field starts, or null. Only the prefixed key counts: a bare `section` may be a nested field. */
export function fieldSection(ui: UISchema | undefined, locale = 'en'): FieldSection | null {
  const v = ui?.['ui:section']
  if (!v) return null
  if (typeof v === 'object' && !Array.isArray(v) && 'title' in v) {
    return { title: localizedText(v.title, locale), description: localizedText(v.description, locale), collapsed: v.collapsed === true }
  }
  return { title: localizedText(v, locale), description: '', collapsed: false }
}

export function childUI(ui: UISchema | undefined, key: string): UISchema | undefined {
  if (!ui || typeof ui !== 'object') return undefined
  const v = ui[key]
  return v && typeof v === 'object' && !Array.isArray(v) ? v : undefined
}

export function schemaType(s: JSONSchema | undefined): string {
  if (!s) return 'string'
  let t = s.type
  if (Array.isArray(t)) t = t.find((x) => x !== 'null') || t[0]
  if (!t) {
    if (s.properties || s.additionalProperties) return 'object'
    if (s.items) return 'array'
    if (s.enum) return typeof s.enum[0] === 'number' ? 'number' : 'string'
    if (s.oneOf?.every((o: any) => 'const' in o)) return typeof s.oneOf[0].const === 'number' ? 'number' : 'string'
  }
  return t || 'string'
}

export function isSecret(s: JSONSchema, ui?: UISchema): boolean {
  return uiGet(ui, 'widget') === 'secret' || s.format === 'password' || s.writeOnly === true || s['x-sensitive'] === true
}

export interface EnumOption {
  value: any
  label: string
}

export function enumOptions(s: JSONSchema, ui?: UISchema, locale = 'en'): EnumOption[] | null {
  const names = uiGet<any[]>(ui, 'enumNames') || s.enumNames
  if (Array.isArray(s.enum)) {
    return s.enum.map((v: any, i: number) => ({ value: v, label: names?.[i] !== undefined ? localizedText(names[i], locale) : String(v) }))
  }
  if (Array.isArray(s.oneOf) && s.oneOf.every((o: any) => o && 'const' in o)) {
    return s.oneOf.map((o: any) => ({ value: o.const, label: localizedText(o['x-title'] || o.title, locale) || String(o.const) }))
  }
  return null
}

/** Widget names SchemaField renders itself. */
export const BUILTIN_WIDGETS: readonly string[] = Object.freeze([
  'text',
  'secret',
  'textarea',
  'select',
  'segmented',
  'radio',
  'switch',
  'number',
  'url-presets',
  'key-value',
  'model-mapping',
  'tags',
  'multi-select',
  'object-list',
  'object',
  'json',
  'hidden',
  'date-time',
  'date',
  'time'
])

/**
 * Widget for a field: ui:widget when set (and, when `known` is given, one of
 * those names), else the one the schema implies.
 */
export function resolveWidget(s: JSONSchema, ui?: UISchema, known?: readonly string[]): string {
  const w = uiGet<string>(ui, 'widget')
  if (w && (!known || known.includes(w))) return w
  if (isSecret(s, ui)) return 'secret'
  const t = schemaType(s)
  if (enumOptions(s, ui) && t !== 'array') return 'select'
  switch (t) {
    case 'boolean':
      return 'switch'
    case 'number':
    case 'integer':
      return 'number'
    case 'array': {
      const it = s.items || {}
      if (enumOptions(it)) return 'multi-select'
      const itType = schemaType(it)
      if (itType === 'object') return 'object-list'
      return 'tags'
    }
    case 'object':
      if (s.properties) return 'object'
      if (s.additionalProperties) return 'key-value'
      return 'json'
    default:
      if (s.format === 'textarea' || (s.maxLength && s.maxLength > 500)) return 'textarea'
      if (s.format === 'date-time' || s.format === 'date' || s.format === 'time') return s.format
      return 'text'
  }
}

export function fieldLabel(key: string, s: JSONSchema, ui?: UISchema, locale = 'en'): string {
  const title = uiGet(ui, 'title') ?? s['x-title'] ?? s.title
  if (title) return localizedText(title, locale)
  return key.replace(/[_-]+/g, ' ').replace(/^\w/, (c) => c.toUpperCase())
}

export function fieldHelp(s: JSONSchema, ui?: UISchema, locale = 'en'): string {
  const h = uiGet(ui, 'help') ?? s['x-description'] ?? s.description
  return h ? localizedText(h, locale) : ''
}

export function orderedKeys(s: JSONSchema, ui?: UISchema): string[] {
  const keys = Object.keys(s.properties || {})
  const order = uiGet<string[]>(ui, 'order')
  if (!order?.length) return keys
  const rest = keys.filter((k) => !order.includes(k))
  const out: string[] = []
  for (const k of order) {
    if (k === '*') out.push(...rest)
    else if (keys.includes(k)) out.push(k)
  }
  if (!order.includes('*')) out.push(...rest)
  return out
}

export function isVisible(ui: UISchema | undefined, root: any): boolean {
  const cond = uiGet<any>(ui, 'visibleWhen')
  if (!cond || typeof cond !== 'object') return true
  if ('field' in cond) {
    const v = getPath(root, cond.field)
    if ('equals' in cond) return v === cond.equals
    if ('notEquals' in cond) return v !== cond.notEquals
    if (Array.isArray(cond.in)) return cond.in.includes(v)
    if (Array.isArray(cond.notIn)) return !cond.notIn.includes(v)
    if (cond.truthy) return !!v
    if (cond.falsy) return !v
    return true
  }
  return Object.entries(cond).every(([k, want]) => {
    const v = getPath(root, k)
    return Array.isArray(want) ? want.includes(v) : v === want
  })
}

/** Fills schema defaults for missing properties (recursively). */
export function withDefaults(s: JSONSchema, value: any): any {
  const t = schemaType(s)
  if (t === 'object' && s.properties) {
    const out: Record<string, any> = { ...(value && typeof value === 'object' ? value : {}) }
    for (const [k, ps] of Object.entries<JSONSchema>(s.properties)) {
      if (out[k] === undefined) {
        if (ps.default !== undefined) out[k] = clone(ps.default)
        else if (schemaType(ps) === 'object' && ps.properties) out[k] = withDefaults(ps, undefined)
      } else if (schemaType(ps) === 'object' && ps.properties) {
        out[k] = withDefaults(ps, out[k])
      }
    }
    return out
  }
  return value === undefined && s.default !== undefined ? clone(s.default) : value
}

function clone<T>(v: T): T {
  return v === undefined ? v : JSON.parse(JSON.stringify(v))
}

export interface ValidateMessages {
  required: string
  minLength: (n: number) => string
  maxLength: (n: number) => string
  pattern: string
  minimum: (n: number) => string
  maximum: (n: number) => string
  integer: string
  url: string
  /** format: email (falls back to `pattern`). */
  email?: string
  /** Array minItems / maxItems (minItems falls back to `required`, maxItems to `pattern`). */
  minItems?: (n: number) => string
  maxItems?: (n: number) => string
  /** Resolves ui:errorMessages entries (LocalizedText); default localizedText(v, 'en'). */
  text?: (v: unknown) => string
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const URL_RE = /^https?:\/\/[^\s]+$/i

/** Client-side validation of visible fields. Returns path -> message. */
export function validate(s: JSONSchema, ui: UISchema | undefined, value: any, msgs: ValidateMessages, root = value, path = ''): Record<string, string> {
  const errs: Record<string, string> = {}
  const t = schemaType(s)
  if (t === 'object' && s.properties) {
    const req: string[] = s.required || []
    for (const k of Object.keys(s.properties)) {
      const cs = s.properties[k]
      const cu = childUI(ui, k)
      if (!isVisible(cu, root) || uiGet(cu, 'widget') === 'hidden') continue
      const p = path ? `${path}.${k}` : k
      const v = value?.[k]
      if (req.includes(k) && (isBlank(v) || (schemaType(cs) === 'boolean' && v === undefined))) {
        errs[p] = custom(cu, 'required', msgs) ?? msgs.required
        continue
      }
      Object.assign(errs, validate(cs, cu, v, msgs, root, p))
    }
    return errs
  }
  if (isBlank(value)) return errs
  if (t === 'string' && typeof value === 'string' && value !== SECRET_MASK) {
    let patternOK = true
    if (s.pattern) {
      try {
        patternOK = new RegExp(s.pattern).test(value)
      } catch {
        /* invalid pattern in schema: skip */
      }
    }
    if (s.minLength && value.length < s.minLength) errs[path] = custom(ui, 'minLength', msgs) ?? msgs.minLength(s.minLength)
    else if (s.maxLength && value.length > s.maxLength) errs[path] = custom(ui, 'maxLength', msgs) ?? msgs.maxLength(s.maxLength)
    else if (!patternOK) errs[path] = custom(ui, 'pattern', msgs) ?? msgs.pattern
    else if ((s.format === 'uri' || s.format === 'url') && !URL_RE.test(value)) errs[path] = custom(ui, 'format', msgs) ?? msgs.url
    else if (s.format === 'email' && !EMAIL_RE.test(value)) errs[path] = custom(ui, 'format', msgs) ?? msgs.email ?? msgs.pattern
  }
  if (t === 'number' || t === 'integer') {
    const n = Number(value)
    if (t === 'integer' && !Number.isInteger(n)) errs[path] = msgs.integer
    else if (s.minimum !== undefined && n < s.minimum) errs[path] = custom(ui, 'minimum', msgs) ?? msgs.minimum(s.minimum)
    else if (s.maximum !== undefined && n > s.maximum) errs[path] = custom(ui, 'maximum', msgs) ?? msgs.maximum(s.maximum)
  }
  if (t === 'array' && Array.isArray(value)) {
    if (s.minItems && value.length < s.minItems) errs[path] = custom(ui, 'minItems', msgs) ?? (msgs.minItems ? msgs.minItems(s.minItems) : msgs.required)
    else if (s.maxItems !== undefined && value.length > s.maxItems) errs[path] = custom(ui, 'maxItems', msgs) ?? (msgs.maxItems ? msgs.maxItems(s.maxItems) : msgs.pattern)
    if (s.items && schemaType(s.items) === 'object') {
      value.forEach((item, i) => Object.assign(errs, validate(s.items, childUI(ui, 'items'), item, msgs, root, `${path}.${i}`)))
    }
  }
  return errs
}

/** ui:errorMessages[kind] of a field, resolved, or undefined. */
function custom(ui: UISchema | undefined, kind: string, msgs: ValidateMessages): string | undefined {
  const m = uiGet<Record<string, unknown>>(ui, 'errorMessages')
  if (!m || typeof m !== 'object' || m[kind] === undefined) return undefined
  return msgs.text ? msgs.text(m[kind]) : localizedText(m[kind])
}
