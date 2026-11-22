// The form of an account group ("group") in its editor: its own fields, the
// tab each of them is on, and what the request sends of them. The prices and
// rates on the other tabs are parts of their own (ModelPriceTable,
// GroupSegmentsEditor) that the same request sends.
import type { ConversionLossPolicy, EnvironmentValues, Group } from '@/api/types'
import { validRate } from '@/components/pricing/pricingFormat'

export const GROUP_EDIT_TABS = ['basic', 'models', 'pricing', 'segments', 'advanced'] as const
export type GroupEditTab = (typeof GROUP_EDIT_TABS)[number]

export interface GroupForm {
  name: string
  description: string
  status: string
  rate_multiplier: string
  visibility: Group['visibility']
  model_allowlist: string[]
  model_filter_mode: 'whitelist' | 'blacklist'
  environment: EnvironmentValues
  conversion_loss_policy: ConversionLossPolicy | null
}
export type GroupField = keyof GroupForm

/** The group's own fields by the tab they are on; the segments tab has none. */
export const TAB_FIELDS: Record<GroupEditTab, readonly GroupField[]> = {
  basic: ['name', 'description', 'status', 'visibility'],
  models: ['model_allowlist', 'model_filter_mode'],
  pricing: ['rate_multiplier'],
  segments: [],
  advanced: ['environment', 'conversion_loss_policy']
}

export const blankForm = (): GroupForm => ({ name: '', description: '', status: 'active', rate_multiplier: '1', visibility: 'public', model_allowlist: [], model_filter_mode: 'blacklist', environment: {}, conversion_loss_policy: null })

export const formOf = (g: Group): GroupForm => ({
  name: g.name,
  description: g.description || '',
  status: g.status,
  rate_multiplier: String(g.rate_multiplier ?? '1'),
  visibility: g.visibility,
  model_allowlist: [...(g.model_allowlist || [])],
  model_filter_mode: g.model_filter_mode || 'whitelist',
  environment: { ...g.environment },
  conversion_loss_policy: g.conversion_loss_policy ?? null
})

/** A field as the request takes it: text trimmed, the rate a decimal string (CONTRACTS §3.2). */
function sent(form: GroupForm, key: GroupField): unknown {
  const v = form[key]
  return typeof v === 'string' && key !== 'status' && key !== 'visibility' ? v.trim() : v
}

/** Whether a field differs from the form as it was opened. */
export const fieldChanged = (form: GroupForm, opened: GroupForm, key: GroupField) => JSON.stringify(sent(form, key)) !== JSON.stringify(sent(opened, key))

/** What the request sends of the fields `keys`: all of them for a new group, of a saved one only those that changed (PATCH). */
export function fieldsBody(form: GroupForm, opened: GroupForm, keys: readonly GroupField[], creating: boolean): Record<string, unknown> {
  return Object.fromEntries(keys.filter((k) => creating || fieldChanged(form, opened, k)).map((k) => [k, sent(form, k)]))
}

/** What is wrong with the fields `keys`, by field: a name is required, the base rate must be a rate. */
export function fieldProblems(form: GroupForm, keys: readonly GroupField[], t: (key: string) => string): Record<string, string> {
  const out: Record<string, string> = {}
  if (keys.includes('name') && !form.name.trim()) out.name = t('common.required')
  if (keys.includes('rate_multiplier') && !validRate(form.rate_multiplier.trim())) out.rate_multiplier = t('pricing.rate.invalid')
  return out
}

/** The errors without those of the fields `keys` (and their parts, "environment.X"). */
export function withoutFields(errors: Record<string, string>, keys: readonly string[]): Record<string, string> {
  return Object.fromEntries(Object.entries(errors).filter(([k]) => !keys.some((key) => k === key || k.startsWith(`${key}.`))))
}
