// Common account controls (docs/COMMON-CONTROLS.md §1, §2): what an account
// type declares about each core field — shown or hidden, editable or fixed,
// a new account's default, a locked value, a required proxy. The account
// editor, its save pipeline, the list and the bulk edit read the declaration
// only through these helpers; none of them knows a plugin.
import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useI18n } from 'vue-i18n'
import { COMMON_CONTROL_NAMES, type Account, type AccountType, type CommonControlName } from '@/api/types'
import { newAccountConcurrency } from './accountLimits'

export type { CommonControlName } from '@/api/types'

export const COMMON_CONTROLS: readonly CommonControlName[] = COMMON_CONTROL_NAMES

/** Values of the controls, in the account API's shapes. */
export type ControlValues = Partial<Record<CommonControlName, unknown>>

/** The resolved declaration of one control. */
export interface AccountControl {
  visible: boolean
  /** The value may be changed: configurable and not locked. */
  configurable: boolean
  locked: boolean
  required: boolean
  /** Declared default (undefined: the core's). */
  default: unknown
}

/** What a control looks like in a form: hidden, disabled with a reason, required. */
export interface ControlView {
  visible: boolean
  disabled: boolean
  required: boolean
  /** Why it is disabled ('' when editable): replaces the control's hint. */
  reason: string
}
/** The view of every control of an account type. */
export type ControlViews = Record<CommonControlName, ControlView>

export function accountControl(type: AccountType | null | undefined, name: CommonControlName): AccountControl {
  const d = type?.common_controls?.[name] ?? {}
  // proxy only supports `required` (§1).
  if (name === 'proxy') return { visible: true, configurable: true, locked: false, required: !!d.required, default: undefined }
  const locked = !!d.locked
  return { visible: d.visible ?? true, configurable: (d.configurable ?? true) && !locked, locked, required: false, default: d.default }
}

/** JSON with sorted object keys. */
const sortedJSON = (v: unknown): string =>
  Array.isArray(v)
    ? `[${v.map(sortedJSON).join(',')}]`
    : v && typeof v === 'object'
      ? `{${Object.keys(v)
          .sort()
          .map(k => `${JSON.stringify(k)}:${sortedJSON((v as Record<string, unknown>)[k])}`)
          .join(',')}}`
      : JSON.stringify(v ?? null)
const isEmpty = (v: unknown) => v == null || (typeof v === 'object' && Object.keys(v).length === 0)

/** Whether two control values are equal as the core compares them: key order does not matter, null equals an empty list or object. */
export const sameControlValue = (a: unknown, b: unknown) => (isEmpty(a) && isEmpty(b)) || sortedJSON(a) === sortedJSON(b)

/** A value in a short hint. */
function shown(v: unknown): string {
  const s = v !== null && typeof v === 'object' ? JSON.stringify(v) : String(v)
  return s.length > 40 ? `${s.slice(0, 39)}…` : s
}

/**
 * The view of every control for a form (pure; `t` resolves the reason). A
 * saved account showing a locked field with another stored value says that
 * saving sets the locked value (the server converges it on every save).
 */
export function controlViews(
  type: AccountType | null | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
  saved?: Account | null
): ControlViews {
  const view = (name: CommonControlName): ControlView => {
    const c = accountControl(type, name)
    const pending =
      !!saved && c.locked && c.default !== undefined && name in saved && !sameControlValue(saved[name as keyof Account], c.default)
    const reason = pending
      ? t('accounts.editorUi.commonControlLockedPending', { value: shown(c.default) })
      : c.locked
        ? t('accounts.editorUi.commonControlLocked')
        : c.configurable
          ? ''
          : t('accounts.editorUi.commonControlFixed')
    return { visible: c.visible, disabled: !c.configurable, required: c.required, reason }
  }
  return Object.fromEntries(COMMON_CONTROLS.map(n => [n, view(n)])) as ControlViews
}

/** controlViews of a reactive account type and the saved account being edited. */
export function useCommonControls(
  type: MaybeRefOrGetter<AccountType | null | undefined>,
  saved?: MaybeRefOrGetter<Account | null | undefined>
) {
  const { t } = useI18n()
  return computed(() => controlViews(toValue(type), t, toValue(saved)))
}

/** Declared defaults. */
const declaredDefaults = (type: AccountType | null | undefined): ControlValues =>
  Object.fromEntries(COMMON_CONTROLS.map(n => [n, accountControl(type, n).default] as const).filter(([, d]) => d !== undefined))

const clamp = (lo: number, hi: number) => (v: unknown) => Math.min(Math.max(Number(v) || 0, lo), hi)
/** An own-level caller's caps (CONTRACTS §15.9): the server fits the type's defaults of a new account into them, so the form does too. */
const OWN_CAPS: Partial<Record<CommonControlName, (v: unknown) => number>> = {
  priority: clamp(0, 100),
  weight: clamp(1, 100),
  max_concurrency: v => (Number(v) > 0 ? Math.min(Number(v), 1000) : newAccountConcurrency('own'))
}

/**
 * A new account's values: core defaults, then the plugin's default_limits
 * (§68.5), then the declared defaults; for an own-level caller (`own`) fitted
 * into the own-level caps, except the fixed ones.
 */
export function newAccountValues(type: AccountType | null | undefined, core: ControlValues, own = false): ControlValues {
  const limits = Object.fromEntries(Object.entries(type?.default_limits ?? {}).filter(([, v]) => v !== undefined))
  const values: ControlValues = { ...core, ...limits, ...declaredDefaults(type) }
  if (own) {
    for (const [name, fit] of Object.entries(OWN_CAPS) as Array<[CommonControlName, (v: unknown) => number]>) {
      if (accountControl(type, name).configurable) values[name] = fit(values[name])
    }
  }
  return values
}

/** A saved account's values: its own, absent ones falling back to `core` (a locked one shows what is stored until saved). */
export function savedAccountValues(a: Account, core: ControlValues): ControlValues {
  const own = Object.fromEntries(
    Object.keys(core)
      .map(n => [n, a[n as keyof Account]])
      .filter(([, v]) => v != null)
  )
  return { ...core, ...own }
}

/** The model mapping a new account starts from: the declared default, else the plugin's default_model_mapping (§41). */
export function defaultMappingOf(type: AccountType | null | undefined): Record<string, string> {
  return (accountControl(type, 'model_mapping').default as Record<string, string> | undefined) ?? type?.default_model_mapping ?? {}
}

/** Drops the request fields the type does not let change: the server fills (create) or keeps (update) them. */
export function withoutFixed(body: Record<string, unknown>, type: AccountType | null | undefined): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(body).filter(
      ([k]) => !COMMON_CONTROLS.includes(k as CommonControlName) || accountControl(type, k as CommonControlName).configurable
    )
  )
}

/** How many of the accounts have a type that fixes any of the controls (bulk edit). */
export function fixedCount(accounts: Account[], typeOf: (a: Account) => AccountType | undefined, ...names: CommonControlName[]): number {
  return accounts.filter(a => names.some(n => !accountControl(typeOf(a), n).configurable)).length
}
