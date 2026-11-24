// Plugin versions and plugin ids as the plugin endpoints describe them
// (CONTRACTS §55.1, §136.2, §137).

/** A version's signature status (pkg.Sig*): indexed is a store install, vouched for by the store index. */
export type SignatureStatus = 'valid' | 'unsigned' | 'indexed' | 'revoked'

/** GET /plugins/retired-keys item (CONTRACTS §55.1). */
export interface RetiredKey {
  /** The plugin id; display_id is how people see it (manifest.DisplayID). */
  key: string
  display_id: string
  /** Publisher that owned the key; empty for an unsigned plugin. */
  publisher: string
  retired_at: string
  /** The same publisher installed the key again. */
  installed: boolean
  /** Accounts left behind (not deleted). */
  accounts: number
  /** Tables named with the key's prefix (plg_<key>__) are left in the core's schema. */
  schema: boolean
}

/** Response of POST /plugins/retired-keys/:key/release. */
export interface ReleaseRetiredKeyResult {
  accounts_deleted: number
  kv_deleted: number
}
