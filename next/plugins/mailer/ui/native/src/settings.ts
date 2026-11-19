import type { Rules } from '@sub2api/ui'

// The mailer settings (Go: mailer.Settings, checked again by settingsRules
// in internal/mailer, which is final).
export interface MailerSettingsValue extends Record<string, unknown> {
  host?: string
  port?: number
  security?: Security
  username?: string
  password?: string
  from_address?: string
  from_name?: string
  timeout_seconds?: number
}

export const SECURITY = ['starttls', 'tls', 'none'] as const
export type Security = (typeof SECURITY)[number]

/** Filled into absent keys; the same as the plugin's own (mailer.parseSettings). */
export const DEFAULTS: MailerSettingsValue = {
  host: '',
  port: 587,
  security: 'starttls',
  username: '',
  password: '',
  from_address: '',
  from_name: 'Sub2API',
  timeout_seconds: 20
}

/** Example values shown in empty boxes (the same in every language). */
export const PLACEHOLDERS = { host: 'smtp.example.com', from_address: 'noreply@example.com' }

export const RULES: Rules<MailerSettingsValue> = {
  host: { type: 'string', max: 253 },
  port: { type: 'integer', min: 1, max: 65535 },
  security: { enum: SECURITY },
  username: { type: 'string', max: 320 },
  password: { type: 'string', max: 1000 },
  from_address: { type: 'string', max: 320 },
  from_name: { type: 'string', max: 100 },
  timeout_seconds: { type: 'integer', min: 5, max: 25 }
}
