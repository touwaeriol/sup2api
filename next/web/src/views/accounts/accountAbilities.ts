// What the caller may do with an account, row by row: the scope of each action
// (CONTRACTS §21, §103.4; the server checks every request the same way), and
// what that leaves of the row actions and the bulk actions.
import type { Account, AccountType } from '@/api/types'
import { ACCOUNT_ACTIONS, useScope } from '@/composables/useScope'
import { accountEnabled } from './accountStatus'
import { accountControl } from './commonControls'

export function useAccountAbilities(typeOf: (a: Account) => AccountType | undefined) {
  const scope = useScope()
  const canUpdate = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.update)
  const canTest = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.test)
  const canDelete = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.delete)
  const canReveal = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.reveal)
  const editable = (a: Account) => canUpdate(a) && !a.orphaned
  // An account an administrator disabled (admin_locked, still disabled): a caller with a restricted scope can
  // neither enable it nor reset its status (the server answers 403 account_locked_by_admin), so both are shown
  // disabled or left out. A stale lock on an account in another status holds nothing back, as on the server.
  const lockedByAdmin = (a: Account) => !!a.admin_locked && a.status === 'disabled' && scope.level(ACCOUNT_ACTIONS.update) !== 'all'
  const toggleable = (a: Account) => editable(a) && accountControl(typeOf(a), 'schedulable').configurable
  const base = { canUpdate, canTest, canDelete, canReveal, editable, lockedByAdmin, toggleable, testable: (a: Account) => canTest(a) && !a.orphaned }
  return { scope, ...base, ...derived(base) }
}

/** What the row and bulk actions take, from the scopes. */
function derived(can: { editable: (a: Account) => boolean; toggleable: (a: Account) => boolean; lockedByAdmin: (a: Account) => boolean; testable: (a: Account) => boolean; canDelete: (a: Account) => boolean }) {
  return {
    canEnable: (a: Account) => can.toggleable(a) && !can.lockedByAdmin(a),
    // A manual disable is undone with Enable; Reset status clears what the gateway set.
    canResetStatus: (a: Account) => can.editable(a) && a.status !== 'disabled' && !can.lockedByAdmin(a),
    canDisable: (a: Account) => can.toggleable(a) && a.status !== 'disabled' && !accountEnabled(a),
    canRefreshCreds: (a: Account) => !!a.refresh && can.editable(a),
    /** Rows the caller may act on in some way; each action runs on the selected accounts it applies to. */
    selectable: (a: Account) => can.testable(a) || can.editable(a) || can.canDelete(a)
  }
}

export type AccountAbilities = ReturnType<typeof useAccountAbilities>
