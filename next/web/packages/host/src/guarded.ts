// Guarded account settings (CONTRACTS §21.3) as a native account form shows
// them: without account:settings:custom a guarded field only takes the
// type's official values. Display only: the core's checkGuarded enforces it.
import { computed, type ComputedRef } from 'vue'
import { useHost } from './index'

/** The `accountType` a native account form receives (GET /account-types item; CONTRACTS §23.5). */
export interface AccountTypeView {
  plugin_key: string
  type: string
  /** Settings fields restricted to official values without account:settings:custom. */
  guarded_settings?: { field: string; allowed: string[] }[]
  custom_upstream?: boolean
  sensitive_fields?: string[]
  [key: string]: unknown
}

/** Permission that lifts the guard (core permission catalogue). */
export const CUSTOM_SETTINGS_PERMISSION = 'account:settings:custom'

export interface GuardedSettings {
  /** null: not restricted for this caller; else the allowed values. */
  allowed(field: string): string[] | null
  /** Exactly one allowed value. */
  locked(field: string): boolean
  /** Any field restricted for this caller (the editor's hint). */
  guarded: ComputedRef<boolean>
}

export function useGuardedSettings(accountType: () => AccountTypeView | null | undefined): GuardedSettings {
  const host = useHost()
  const guards = computed(() => (host.permissions.has(CUSTOM_SETTINGS_PERMISSION) ? [] : (accountType()?.guarded_settings ?? [])))
  const allowed = (field: string) => guards.value.find((g) => g.field === field)?.allowed ?? null
  return {
    allowed,
    locked: (field) => allowed(field)?.length === 1,
    guarded: computed(() => guards.value.length > 0)
  }
}
