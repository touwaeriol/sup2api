import type { Rules } from '@sub2api/ui'

// The plugin settings (Go: codex.Settings, checked again by parseSettings,
// which is final): the client headers of the accounts that set none, ""
// for the built-in ones.
export interface CodexSettingsValue extends Record<string, unknown> {
  user_agent?: string
  originator?: string
}

export const SETTINGS_DEFAULTS: CodexSettingsValue = { user_agent: '', originator: '' }

/** A header value: no control character but tab (Go: paramoverride.ValidHeaderValue). */
const HEADER_VALUE = /^[^\x00-\x08\x0A-\x1F\x7F]*$/

export const SETTINGS_RULES: Rules<CodexSettingsValue> = {
  user_agent: { type: 'string', pattern: HEADER_VALUE },
  originator: { type: 'string', pattern: HEADER_VALUE }
}
