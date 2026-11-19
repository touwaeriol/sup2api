import type { Rules } from '@sub2api/ui'

// The fields of the account types and their rules: the bounds the plugin's
// ValidateCredentials holds them to (internal/vertex/credentials.go), for
// immediate feedback; the save is checked there.

export type KeyFileType = 'vertex_sa' | 'vertex_authorized_user'

/** The credentials field holding the JSON text of each key file type (vertex.keyKinds). */
export const KEY_FIELD: Record<KeyFileType, string> = {
  vertex_sa: 'service_account_json',
  vertex_authorized_user: 'authorized_user_json'
}

/** Project and location, empty allowed: the key file's project / the default location, or the express address. */
export const TARGET: Rules = {
  project_id: { pattern: /^([a-z][a-z0-9-]{4,28}[a-z0-9])?$/ },
  location: { pattern: /^([a-z0-9-]{2,32})?$/ }
}

/** The rules of a key file type. */
export const keyFileRules = (type: KeyFileType): Rules => ({ [KEY_FIELD[type]]: { required: true, min: 2, max: 32768 }, ...TARGET })

/** Filled into a new key file account: vertex.LocationGlobal. An API key account has no default (the express address, apiKeyForm). */
export const KEY_FILE_DEFAULTS = { location: 'global' }

/** The key file type of the form (CONTRACTS §23.5: accountType, else the edited account's). */
export function keyFileType(accountType?: { type?: string } | null, account?: { type?: string } | null): KeyFileType {
  return (accountType?.type || account?.type) === 'vertex_authorized_user' ? 'vertex_authorized_user' : 'vertex_sa'
}
