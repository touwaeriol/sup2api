// Translation helper for the library's own `ui.*` messages. Components call
// t('ui.key') as with vue-i18n; outside an i18n-enabled app (unit tests, a
// plugin preview) or when the `ui` namespace is missing, the English text from
// ./messages is used instead, so a component never renders a raw key.
import { useI18n } from 'vue-i18n'
import messages from './messages'

type Params = Record<string, unknown>

function lookup(key: string): string | undefined {
  let cur: unknown = messages.en
  for (const part of key.replace(/^ui\./, '').split('.')) {
    if (!cur || typeof cur !== 'object') return undefined
    cur = (cur as Record<string, unknown>)[part]
  }
  return typeof cur === 'string' ? cur : undefined
}

function format(s: string, params?: Params): string {
  return params ? s.replace(/\{(\w+)\}/g, (m, k) => (params[k] === undefined ? m : String(params[k]))) : s
}

/** Fallback translation of a `ui.*` key from the bundled English messages. */
function uiFallback(key: string, params?: Params): string {
  return format(lookup(key) ?? key, params)
}

/** Returns `t` for `ui.*` keys that works with or without vue-i18n installed. */
export function useUiT(): (key: string, params?: Params) => string {
  let t: ((key: string, params?: Params) => string) | null = null
  try {
    t = useI18n().t as (key: string, params?: Params) => string
  } catch {
    t = null
  }
  return (key, params) => {
    if (!t) return uiFallback(key, params)
    const r = params ? t(key, params) : t(key)
    return r === key ? uiFallback(key, params) : r
  }
}

/** Current locale ('en' when vue-i18n is not installed). */
export function useUiLocale(): () => string {
  try {
    const { locale } = useI18n()
    return () => String(locale.value || 'en')
  } catch {
    return () => 'en'
  }
}

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
