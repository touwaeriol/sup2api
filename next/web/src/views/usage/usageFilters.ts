import { computed, type MaybeRefOrGetter, toValue } from 'vue'
import { useI18n } from 'vue-i18n'
import { logRangeBounds, rangeBounds, type RangeKey } from '@sub2api/ui'
import type { LocationQuery } from 'vue-router'
import { entityOf, isEntityId, type EntityKind } from '@/components/entity/entities'
import { lookupEntities, useEntityNames } from '@/components/entity/lookup'
import type { UsageView } from './view'

/**
 * The search bar's starting filters: the range, and the fields it always
 * sends. `log` opens on the request log's own range instead (today's midnight
 * to an hour from now, as new-api's usage log), whatever the preset shown.
 */
export function baseUsageFilters(range: RangeKey, log = false) {
  return {
    ...(log ? logRangeBounds() : rangeBounds(range)),
    model_contains: '',
    success: '',
    client_ip: '',
    // exact match on the client's X-Request-Id (CONTRACTS §14.4)
    client_request_id: ''
  }
}

/** Values of the operators' conversion filter (CONVERT-MATRIX §6.2): by grade, any converted grade, or with losses. */
export const CONVERSION_FILTERS = ['native', 'converted', 'good', 'fair', 'discouraged', 'local', 'lossy'] as const

/** Label key of a conversion filter value: the grade's, or the filter's own. */
export const conversionFilterLabel = (v: string) =>
  v === 'converted' || v === 'lossy' ? `usage.conversion.filters.${v}` : `accounts.conversion.grades.${v}`

const QUERY_FILTERS = ['user_id', 'group_id', 'group_name', 'model', 'model_contains', 'api_key_id', 'account_id', 'retried', 'from', 'to'] as const
/** Link filters of an operator only: a user cannot preset another user. */
const OPERATOR_QUERY_FILTERS: readonly string[] = ['user_id', 'group_name', 'account_id']
/** The link filters of the account view (CONTRACTS §103.5), which refuses the rest. */
const ACCOUNT_QUERY_FILTERS: readonly string[] = ['model', 'account_id', 'retried', 'from', 'to']

/**
 * The filters a link presets (?user_id=…&from=…&to=…) for `view`: an
 * operator's, the caller's own (no other user's), or the account view's own.
 * `accounts`: the operator may read accounts (the API refuses the account
 * filter to any other). An id that is not one (?user_id=abc), or a retried=
 * other than true / false, is dropped rather than sent to the list and the
 * lookup.
 */
export function queryUsageFilters(query: LocationQuery, view: UsageView, accounts = false): Record<string, string> {
  const out: Record<string, string> = {}
  for (const k of QUERY_FILTERS) {
    const v = query[k]
    if (typeof v !== 'string' || v === '') continue
    if (view === 'account' ? !ACCOUNT_QUERY_FILTERS.includes(k) : OPERATOR_QUERY_FILTERS.includes(k) && view !== 'all') continue
    if (k === 'account_id' && !accounts) continue
    if (NUMERIC_ID_PARAMS.has(k) && !isEntityId(v)) continue
    if (k === 'retried' && v !== 'true' && v !== 'false') continue
    out[k] = v
  }
  // The model picker shows one of the two: the exact model leaves no search beside it.
  if (out.model) delete out.model_contains
  return out
}

// A search field is named after the query parameter it sets, except those
// that pick an entity (EntityPicker): they set the picked id (`id`) or, where
// the entity is also searched by name, the text typed in place of a pick
// (`text`). The two exclude each other; reading typed digits as an id is the
// picker's business alone.
export interface EntityField {
  kind: EntityKind
  id: string
  text?: string
}
export const ENTITY_FIELDS: Record<string, EntityField> = {
  model: { kind: 'model', id: 'model', text: 'model_contains' },
  user: { kind: 'user', id: 'user_id', text: 'user_email' },
  api_key: { kind: 'api_key', id: 'api_key_id', text: 'api_key_name' },
  // Picked only: group_name stays a parameter a link may bring (HIDDEN).
  group: { kind: 'group', id: 'group_id' },
  account: { kind: 'account', id: 'account_id', text: 'account_name' }
}
/** The entity an id parameter names (group_id: a group), for showing its value by name. */
const ID_PARAM_KINDS: Record<string, EntityKind> = Object.fromEntries(Object.values(ENTITY_FIELDS).map(e => [e.id, e.kind]))
/** The id parameters whose value is a number (all but the model's, which is its name). */
const NUMERIC_ID_PARAMS = new Set(Object.keys(ID_PARAM_KINDS).filter(param => entityOf(ID_PARAM_KINDS[param]).numeric))

/** The query parameters a search field sets. */
export function fieldParams(field: string): string[] {
  const entity = ENTITY_FIELDS[field]
  if (!entity) return [field]
  return entity.text ? [entity.id, entity.text] : [entity.id]
}

/** The parameters that set an entity field to a picked id or to a name search, the other one cleared; neither clears both. */
export function entityPatch(field: string, picked: { id?: string; text?: string }): Record<string, string> {
  const { id, text } = ENTITY_FIELDS[field]
  return { [id]: picked.id ?? '', ...(text ? { [text]: picked.text ?? '' } : {}) }
}

/**
 * Whether the API key filter holds once the user filter changed. Without a
 * user or without a key there is nothing to settle. A picked key stays when
 * the lookup lists it among the picked user's keys; where that cannot be told
 * (a searched email, a searched key name, a failed lookup) the key goes.
 */
export async function keyHoldsForUser({ user_id, user_email, api_key_id, api_key_name }: Record<string, string>): Promise<boolean> {
  if (!(user_id || user_email) || !(api_key_id || api_key_name)) return true
  if (!user_id || !api_key_id) return false
  try {
    const { items } = await lookupEntities('api_key', { ids: [api_key_id], scope: { user_id } })
    return items.length > 0
  } catch {
    return false
  }
}

/** Who searches, and under which log type. */
export interface UsageSearchContext {
  /** The account view searches the account's facts only: no user, key, group, IP or billing. */
  view: UsageView
  /** An operator who may read accounts gets the account filter. */
  accounts: boolean
  /** The failed requests log type: the status is settled, the HTTP status and error type join. */
  errors: boolean
}

/** Parameters the search keeps without a field of its own: set by a link or a table action, shown as removable chips. */
const HIDDEN = ['group_name', 'success', 'status_code', 'error_type', 'client_request_id', 'platform', 'endpoint', 'stream']
const ADMIN_HIDDEN = ['plugin_key', 'account_type', 'node_id', 'upstream_model', 'upstream_protocol']
const ACCOUNT_HIDDEN = ['success', 'status_code', 'error_type', 'platform', 'endpoint', 'stream', 'plugin_key', 'account_type']

/**
 * The usage search, after new-api's log filters: the common fields on the
 * first row (`primary`, after the range and log type), the less used behind
 * More filters (`advanced`), every parameter it manages (`params`; Reset
 * clears them all) and those shown as chips when set (`tagged`: all but the
 * first row's, and the status the failed requests type sets itself).
 */
export function usageSearchLayout(c: UsageSearchContext) {
  const outcome = [...(c.errors ? [] : ['success']), 'retried']
  const failure = c.errors ? ['status_code', 'error_type'] : []
  if (c.view === 'account') return searchLayout(['model', 'account'], ['request_id', 'protocol', ...outcome, ...failure], ACCOUNT_HIDDEN, c.errors)
  const admin = c.view === 'all'
  const primary = ['model', admin ? 'user' : 'group', 'api_key']
  const advanced = [
    ...(admin ? ['group', 'request_id'] : ['request_id']),
    ...(admin && c.accounts ? ['account'] : []),
    'client_ip',
    'protocol',
    ...(admin ? ['conversion'] : []),
    ...outcome,
    'billing_status',
    ...failure
  ]
  return searchLayout(primary, advanced, [...HIDDEN, ...(admin ? ADMIN_HIDDEN : [])], c.errors)
}

function searchLayout(primary: string[], advanced: string[], hidden: string[], errors: boolean) {
  const params = [...new Set([...primary, ...advanced, ...hidden].flatMap(fieldParams))]
  const untagged = [...primary.flatMap(fieldParams), ...(errors ? ['success'] : [])]
  return { primary, advanced, params, tagged: params.filter(p => !untagged.includes(p)) }
}

/** The parameters removing the chip `key` clears: account_type goes with its plugin_key (the API refuses it alone). */
export const chipParams = (key: string): string[] => (key === 'plugin_key' ? [key, 'account_type'] : [key])

/** Parameters labelled outside usage.search. */
const LABEL_KEYS: Record<string, string> = { success: 'common.status', billing_status: 'usage.billing.statusLabel' }

/**
 * Labels of the usage search: `label` names a field or parameter, `options`
 * holds the choices of those picked from a list (the empty first choice names
 * the filter, as the fields carry no labels), `valueLabel` reads a set value
 * as the page shows it: a picked entity as "name #id" (looked up among the
 * caller's own with `mine`), a listed choice by its label, an error type the
 * console words (usage.errors) by its words. `suggestions` are the values a
 * text field offers as it is typed in: the error types the console words, such
 * as usage_unreported (the plugin reported no usage). `protocolOptions` (the
 * request types under the search) fill the type list; a type already set stays
 * listed.
 */
export function useUsageSearchLabels(
  protocolOptions: MaybeRefOrGetter<string[]>,
  protocol: MaybeRefOrGetter<string>,
  mine: MaybeRefOrGetter<boolean> = false
) {
  const { t, te, tm } = useI18n()
  const names = useEntityNames({ mine })
  const label = (key: string) => t(LABEL_KEYS[key] ?? `usage.search.${key}`)
  const choices = (key: string, list: Array<{ value: string; label: string }>) => [{ value: '', label: label(key) }, ...list]
  const options = computed<Record<string, Array<{ value: string; label: string }>>>(() => {
    const types = toValue(protocolOptions)
    const current = toValue(protocol)
    return {
      protocol: choices('protocol', [...types, ...(current && !types.includes(current) ? [current] : [])].map(v => ({ value: v, label: v }))),
      conversion: choices('conversion', CONVERSION_FILTERS.map(v => ({ value: v, label: t(conversionFilterLabel(v)) }))),
      success: choices('success', [{ value: 'true', label: t('usage.success.ok') }, { value: 'false', label: t('usage.success.failed') }]),
      retried: choices('retried', [{ value: 'true', label: t('usage.attempts.filterYes') }, { value: 'false', label: t('usage.attempts.filterNo') }]),
      billing_status: choices('billing_status', ['billed', 'free', 'pending', 'failed'].map(v => ({ value: v, label: t(`usage.billing.status.${v}`) }))),
      stream: choices('stream', [{ value: 'true', label: t('usage.stream') }, { value: 'false', label: t('usage.nonStream') }])
    }
  })
  const suggestions = computed<Record<string, Array<{ value: string; label: string }>>>(() => ({
    error_type: Object.keys(tm('usage.errors')).map(v => ({ value: v, label: t(`usage.errors.${v}`) }))
  }))
  function valueLabel(key: string, value: string) {
    const kind = ID_PARAM_KINDS[key]
    if (kind) return names.label(kind, value)
    if (key === 'error_type' && te(`usage.errors.${value}`)) return t(`usage.errors.${value}`)
    return options.value[key]?.find(o => o.value === value && value !== '')?.label ?? value
  }
  return { label, options, suggestions, valueLabel }
}
