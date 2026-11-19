import { ApiError, errorMessage as hostErrorMessage, isApiError, isSilentError, VERIFICATION_CANCELLED } from '@sub2api/host'
import { i18n } from '@/i18n'
import { toast } from '@sub2api/ui'

/** Human readable, localized error message (@sub2api/host errorMessage in the console language). */
export function errorMessage(e: unknown): string {
  return hostErrorMessage(e, i18n.global.t)
}

/** The user dismissed the step-up verification of the request: nothing to report. */
export const verificationCancelled = (e: unknown) => isApiError(e) && e.code === VERIFICATION_CANCELLED

/** Shows an error toast unless it is an auth redirect, a dismissed verification or an aborted request. */
export function notifyError(e: unknown) {
  if (!isSilentError(e)) toast(errorMessage(e), 'error')
}

/**
 * i18n keys of the field error codes a page knows: code → key. A code that is
 * not listed shows the server's message (it may be English only).
 */
export type FieldCodeMessages = Record<string, string>

/**
 * Extracts field errors of an invalid_argument error. `strip` removes a
 * prefix such as "credentials." so plugin form fields line up. With `codes`,
 * a field whose code is listed shows that translation instead of the
 * server's message.
 */
export function fieldErrors(e: unknown, strip?: string, codes?: FieldCodeMessages): Record<string, string> {
  if (!(e instanceof ApiError) || e.code !== 'invalid_argument') return {}
  const t = i18n.global.t
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(e.fields)) {
    if (strip && !k.startsWith(strip)) continue
    const key = codes?.[e.fieldCodes?.[k] ?? '']
    out[strip ? k.slice(strip.length) : k] = key ? t(key) : v
  }
  return out
}
