import { useAuthStore } from '@/stores/auth'

// Ownership of accounts and proxies (CONTRACTS §21.1): an "all" key
// (account:read, proxy:manage, ...) acts on every row, the matching "own" key
// (account:own:read, proxy:own:manage, ...) only on rows whose created_by is
// the caller. Rows with created_by = null (legacy data) are only reachable
// with the all-level key. The server enforces this in SQL; the console uses
// the same rule to show or hide row actions and columns.

export type OwnerScope = 'all' | 'own' | null

/** A row that carries an owner (Account, Proxy). */
export interface Owned {
  created_by?: number | null
}

/** Permission key pairs (all-level, own-level) of the owned resources. */
export const ACCOUNT_KEYS = {
  read: ['account:read', 'account:own:read'],
  create: ['account:create', 'account:own:create'],
  update: ['account:update', 'account:own:update'],
  delete: ['account:delete', 'account:own:delete'],
  test: ['account:test', 'account:own:test'],
  reveal: ['account:credential:view', 'account:own:credential:view']
} as const satisfies Record<string, readonly [string, string]>

export const PROXY_KEYS = {
  read: ['proxy:read', 'proxy:own:read'],
  manage: ['proxy:manage', 'proxy:own:manage']
} as const satisfies Record<string, readonly [string, string]>

/** Any of these opens the accounts page (menus, routes). */
export const ACCOUNT_PAGE_PERMS = ['account:read', 'account:own:read', 'account:own:create']
/** Any of these opens the proxies page. */
export const PROXY_PAGE_PERMS = ['proxy:read', 'proxy:own:read', 'proxy:own:manage']

export function useOwnership() {
  const auth = useAuthStore()

  /** 'all' with the all-level key (or superuser), 'own' with only the own-level key, null with neither. */
  function scope(allKey: string, ownKey: string): OwnerScope {
    if (auth.has(allKey)) return 'all'
    if (auth.has(ownKey)) return 'own'
    return null
  }

  /** Whether the caller may act on `row` with either key. */
  function canOn(row: Owned | null | undefined, allKey: string, ownKey: string): boolean {
    const s = scope(allKey, ownKey)
    if (s === 'all') return true
    if (s === 'own') return isMine(row)
    return false
  }

  /** created_by is the caller (null / unknown owners are not "mine"). */
  function isMine(row: Owned | null | undefined): boolean {
    const me = auth.me?.id
    return me !== undefined && row?.created_by != null && row.created_by === me
  }

  /** Same as canOn for a [allKey, ownKey] pair. */
  function can(row: Owned | null | undefined, pair: readonly [string, string]): boolean {
    return canOn(row, pair[0], pair[1])
  }

  /** Same as scope for a [allKey, ownKey] pair. */
  function scopeOf(pair: readonly [string, string]): OwnerScope {
    return scope(pair[0], pair[1])
  }

  return { scope, scopeOf, canOn, can, isMine }
}
