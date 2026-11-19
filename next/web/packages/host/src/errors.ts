// Error wording shared by the console and native plugin UIs: what a toast
// says for a failed request, and which failures say nothing.
import { isApiError, VERIFICATION_CANCELLED } from './http'

type Translate = (key: string, params?: Record<string, unknown>) => string

/**
 * Refusals worded by the console whatever the server's message (console
 * i18n keys): by code, or by code and the permission a permission_denied
 * names (resource scopes, CONTRACTS §21).
 */
const KNOWN_ERRORS: Record<string, string> = {
  would_leave_scope: 'accounts.scopeErrors.wouldLeaveScope',
  package_gone: 'plugins.errors.packageGone',
  cluster_change_busy: 'plugins.errors.clusterChangeBusy',
  'permission_denied proxy:manage:own': 'proxies.cannotCreate'
}

/** Human readable, localized error message (`t`: the console's translation, host.i18n.t). */
export function errorMessage(e: unknown, t: Translate): string {
  if (isApiError(e)) {
    const known = KNOWN_ERRORS[e.code] ?? KNOWN_ERRORS[`${e.code} ${e.details.permission}`]
    if (known) return t(known)
    return e.message && e.message !== e.code ? e.message : t(`auth.errors.${e.code}`)
  }
  if (e instanceof TypeError) return t('auth.errors.network')
  if (e instanceof Error) return e.message
  return String(e)
}

/** Failures worth no toast: the session ended (the login page follows), a dismissed verification, an aborted request. */
export function isSilentError(e: unknown): boolean {
  if (isApiError(e)) return e.code === 'unauthenticated' || e.code === VERIFICATION_CANCELLED
  return e instanceof DOMException && e.name === 'AbortError'
}
