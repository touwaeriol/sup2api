// The row's enable / disable button shows the new state at once and puts the
// old one back when the request fails. Enable is the manual enable (see
// enabledPatch): it also recovers an automatically disabled account, which
// offers Enable; the row menu offers such an account a manual disable.
import { ref } from 'vue'
import { api } from '@sub2api/host'
import { toast } from '@sub2api/ui'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { notifyError } from '@/utils/errors'
import type { AccountAbilities } from './accountAbilities'
import { accountEnabled, enabledPatch } from './accountStatus'
import { todayQuery } from './accountUsage'
import { accountControl } from './commonControls'

interface EnabledToggleDeps {
  can: Pick<AccountAbilities, 'canEnable' | 'toggleable' | 'lockedByAdmin'>
  typeOf: (a: Account) => AccountType | undefined
  /** Writes a change onto the rows that show the account. */
  patch: (id: number, change: Partial<Account>) => void
}

export function useEnabledToggle({ can, typeOf, patch }: EnabledToggleDeps) {
  const { t } = i18n.global
  const toggling = ref(new Set<number>())
  async function toggle(a: Account, on: boolean) {
    if (toggling.value.has(a.id) || !(on ? can.canEnable(a) : can.toggleable(a))) return
    toggling.value.add(a.id)
    const before = { status: a.status, schedulable: a.schedulable, status_reason: a.status_reason, auto_disabled_until: a.auto_disabled_until }
    patch(a.id, { ...enabledPatch(on), ...(on ? { status_reason: '', auto_disabled_until: null } : {}) })
    try {
      patch(a.id, await api.patch<Account>(`/accounts/${a.id}`, enabledPatch(on), { query: todayQuery() }))
      toast(t(on ? 'accounts.listUi.enabledDone' : 'accounts.listUi.disabledDone', { name: a.name }), 'success')
    } catch (e) {
      patch(a.id, before)
      notifyError(e)
    } finally {
      toggling.value.delete(a.id)
    }
  }
  /** Hover text of a row's enable / disable button: what it does. */
  function title(a: Account) {
    if (!accountControl(typeOf(a), 'schedulable').configurable) return t('accounts.editorUi.commonControlFixed')
    if (accountEnabled(a)) return t('accounts.listUi.disableHint')
    if (can.lockedByAdmin(a)) return t('accounts.listUi.adminLockedHint')
    return t(a.status === 'disabled' ? 'accounts.listUi.enableHint' : 'accounts.listUi.enableAutoHint')
  }
  return { toggling, toggle, title }
}
