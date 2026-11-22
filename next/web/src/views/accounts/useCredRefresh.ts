// The manual credential refresh of an account (CONTRACTS §48): the core renews
// expiring tokens by itself; the detail shows the state and offers one
// refresh, and a click on an account already refreshing does nothing.
import { ref } from 'vue'
import { toast } from '@sub2api/ui'
import type { Account } from '@/api/types'
import { i18n } from '@/i18n'
import { notifyError } from '@/utils/errors'
import { refreshAccountCredentials } from './accountQuota'

type Outcome = Awaited<ReturnType<typeof refreshAccountCredentials>>

/** Tells how a refresh went; nothing when it changed nothing worth saying. */
function report(out: Outcome) {
  const { t } = i18n.global
  if (out.refreshed) toast(t('accounts.credRefresh.done'), 'success')
  else if (out.skipped === 'in_progress') toast(t('accounts.credRefresh.inProgress'), 'info')
  else if (out.skipped === 'changed') toast(t('accounts.credRefresh.changed'), 'info')
  else if (out.error) toast(t('accounts.credRefresh.failed', { error: out.error }), 'error')
}

/** `patch` writes the new refresh state onto the rows that show the account. */
export function useCredRefresh(patch: (id: number, change: Pick<Account, 'refresh'>) => void) {
  const refreshing = ref(new Set<number>())
  async function refresh(a: Account) {
    if (refreshing.value.has(a.id)) return
    refreshing.value.add(a.id)
    try {
      const out = await refreshAccountCredentials(a.id)
      a.refresh = out.refresh
      patch(a.id, { refresh: out.refresh })
      report(out)
    } catch (e) {
      notifyError(e)
    } finally {
      refreshing.value.delete(a.id)
    }
  }
  return { refreshing, refresh }
}
