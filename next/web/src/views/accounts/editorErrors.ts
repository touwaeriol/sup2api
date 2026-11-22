// Server field errors of the account editor (ApiError.fields) and the section
// whose request fields they are; the common controls of each section.
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

/** Whether the field (or a part of it: `field.x`, `field[0]`) is one of these. */
const fieldOf = (fields: readonly string[]) => (k: string) => fields.some(p => k === p || k.startsWith(`${p}.`) || k.startsWith(`${p}[`))

/**
 * The request fields of each core section, as field prefixes: the part of the
 * save that sends them claims their server errors (useTabbedForm), and a
 * section holding an error of one is marked.
 */
export const SECTION_FIELDS = {
  connection: ['name', 'group_ids', 'proxy_id', 'proxy_url', 'settings', 'credentials'],
  /** The model list, the mapping and the two protocol fields (DESIGN-2026-11-17-PROTOCOL-BINDING §4). */
  models: ['models', 'model_mapping', 'protocols', 'model_protocols'],
  /** The scheduling section's controls and the session window (a model list's limits included). */
  scheduling: [...SCHEDULING_CONTROLS, 'spm_window_seconds'],
  /** Request forwarding: header passthrough (CONTRACTS §81), header override (§108), parameter override (§84), conversion loss policy (CONVERT-MATRIX §6.3), the account layer of the environment variables (`environment.<key>`). */
  advanced: ['header_passthrough', 'header_override', 'param_override', 'conversion_loss_policy', 'environment']
} as const satisfies Partial<Record<EditorSection, readonly string[]>>
export type CoreSection = keyof typeof SECTION_FIELDS

/** Whether a field belongs to the section: `protocols[1]` and `model_protocols[0].models[2]` to models, `environment.x` to advanced. */
export const isSectionField = (section: CoreSection) => fieldOf(SECTION_FIELDS[section])

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

const CREDENTIAL_RE = /^credentials[.[]?(.*?)]?$/
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

