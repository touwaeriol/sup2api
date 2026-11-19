// Server field errors of the account editor (ApiError.fields) and the section
// that holds each field; the common controls of each section.
import type { CommonControlName } from '@/api/types'

/** The core sections and the plugin ones (`plugin:<plugin>:<component>`, account.editor.section). */
export type EditorSection = 'connection' | 'models' | 'scheduling' | 'advanced' | `plugin:${string}`
export const pluginSection = (key: string): EditorSection => `plugin:${key}`

/**
 * Rows of the scheduling group and of the limits group. The session window is
 * part of the spm_limit row, not a row: a type hiding every limit hides the group.
 */
export const SCHEDULING_ROWS: readonly CommonControlName[] = ['priority', 'weight', 'schedulable', 'auto_disable']
export const LIMIT_ROWS: readonly CommonControlName[] = ['max_concurrency', 'rpm_limit', 'tpm_limit', 'spm_limit']
const SCHEDULING_CONTROLS: readonly CommonControlName[] = [...SCHEDULING_ROWS, ...LIMIT_ROWS, 'usage_limit', 'model_limits']
/**
 * Sections made only of common controls: hidden when every one of them is.
 * The advanced section is not: its conversion loss policy is always shown.
 */
export const SECTION_CONTROLS: Partial<Record<EditorSection, readonly CommonControlName[]>> = { scheduling: SCHEDULING_CONTROLS }

export const CONNECTION_FIELDS = ['name', 'group_ids', 'proxy_id', 'proxy_url', 'settings']

/** Whether the field (or a part of it: `field.x`, `field[0]`) is one of these. */
const fieldOf = (fields: readonly string[]) => (k: string) => fields.some(p => k === p || k.startsWith(`${p}.`) || k.startsWith(`${p}[`))

/** The advanced section (request forwarding): header passthrough rules (CONTRACTS §81), the header override (§108), the parameter override (§84), the conversion loss policy (CONVERT-MATRIX §6.3) and the account layer of the environment variables (`environment.<key>`). */
export const isAdvancedField = fieldOf(['header_passthrough', 'header_override', 'param_override', 'conversion_loss_policy', 'environment'])

/** Scheduling and limit fields: the scheduling section's controls and the session window (a model list's limits included). */
export const isSchedulingField = fieldOf([...SCHEDULING_CONTROLS, 'spm_window_seconds'])

const CREDENTIAL_RE = /^credentials[.[]?(.*?)]?$/
/** The two protocol fields (DESIGN-2026-11-17-PROTOCOL-BINDING §4): `protocols[i]`, `model_protocols[i].models[j]`, `model_protocols[i].protocols[j]`. */
export const isProtocolField = fieldOf(['protocols', 'model_protocols'])
/** The "protocols and models" section: the model list, the mapping and the two protocol fields. */
const isModelsField = fieldOf(['models', 'model_mapping', 'protocols', 'model_protocols'])

/** The server's error of the model list / mapping, as `field: text` ('' when there is none). */
export function modelsErrorText(errors: Record<string, string>): string {
  for (const [k, v] of Object.entries(errors)) if (/^models(\[|$)/.test(k)) return `${k}: ${v}`
  return ''
}

export function mappingErrorText(errors: Record<string, string>): string {
  for (const [k, v] of Object.entries(errors)) if (/^model_mapping([.[]|$)/.test(k)) return k === 'model_mapping' ? v : `${k}: ${v}`
  return ''
}

/** The server's errors of `protocols`: an item's is worded with the protocol id that was sent. */
export function protocolsErrorTexts(errors: Record<string, string>, sent: readonly string[] | null): string[] {
  const out: string[] = []
  for (const [k, v] of Object.entries(errors)) {
    const item = /^protocols\[(\d+)\]$/.exec(k)
    if (item) out.push(`${sent?.[Number(item[1])] ?? k}: ${v}`)
    else if (k === 'protocols') out.push(v)
  }
  return out
}

/** The server's errors of `model_protocols`: of the field itself, and of each saved entry (`model_protocols[i]...`) by its index. */
export function ruleErrors(errors: Record<string, string>): { general: string[]; byEntry: Record<number, string[]> } {
  const general: string[] = []
  const byEntry: Record<number, string[]> = {}
  for (const [k, v] of Object.entries(errors)) {
    const entry = /^model_protocols\[(\d+)\]/.exec(k)
    if (entry) (byEntry[Number(entry[1])] ??= []).push(v)
    else if (k === 'model_protocols') general.push(v)
  }
  return { general, byEntry }
}

/** Splits field errors into the plugin form's (`credentials.x` → `x`) and the editor's own. */
export function splitFieldErrors(all: Record<string, string>): { cred: Record<string, string>; base: Record<string, string> } {
  const cred: Record<string, string> = {}
  const base: Record<string, string> = {}
  for (const [k, v] of Object.entries(all)) {
    const m = CREDENTIAL_RE.exec(k)
    if (m && k.startsWith('credentials')) cred[m[1].replace(/^\./, '')] = v
    else base[k] = v
  }
  return { cred, base }
}

/** The first section holding one of these errors, or null. */
export function sectionOfErrors(base: Record<string, string>, cred: Record<string, string>): EditorSection | null {
  if (Object.keys(cred).length || CONNECTION_FIELDS.some(k => k in base)) return 'connection'
  if (Object.keys(base).some(isModelsField)) return 'models'
  if (Object.keys(base).some(isAdvancedField) && !Object.keys(base).some(isSchedulingField)) return 'advanced'
  return Object.keys(base).length ? 'scheduling' : null
}
