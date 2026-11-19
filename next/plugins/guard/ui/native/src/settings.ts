import type { Rules } from '@sub2api/ui'

// The guard settings (Go: guard.Settings, checked again by parseSettings,
// which is final).
export interface GuardSettingsValue extends Record<string, unknown> {
  webhook_url?: string
  record_snippets?: boolean
  alert_cooldown_sec?: number
}

/** Filled into absent keys; the cooldown is the plugin's own default (defaultAlertCooldownSec). */
export const SETTINGS_DEFAULTS: GuardSettingsValue = { webhook_url: '', record_snippets: false, alert_cooldown_sec: 60 }

/** The cooldown's bounds (Go: maxAlertCooldownSec). */
export const COOLDOWN_MAX = 300

export const SETTINGS_RULES: Rules<GuardSettingsValue> = {
  webhook_url: { type: 'string', pattern: /^(https?:\/\/\S+)?$/ },
  record_snippets: { type: 'boolean' },
  alert_cooldown_sec: { type: 'integer', min: 0, max: COOLDOWN_MAX }
}
