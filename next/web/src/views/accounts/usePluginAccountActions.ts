// Declarative plugin account actions (manifest `ui.accountActions`, CONTRACTS
// §23.3 UI 3.6): the row menu and the bulk bar both run one through `run`,
// which confirms, POSTs `{account_ids}` to the plugin's own route (a single
// row is a one-element array; at most MAX_ACCOUNT_BATCH ids per request, each
// request given 95 s) and reports like the other bulk actions.
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, MAX_ACCOUNT_BATCH } from '@sub2api/host'
import { confirm, toast } from '@sub2api/ui'
import type { Account } from '@/api/types'
import { lt } from '@/i18n'
import type { PluginAccountActionEntry } from '@/stores/plugins'
import { bulkToast } from '@/composables/bulkToast'

/** Parses a manifest route reference such as "POST /reset" into method + plugin API path. */
function parseRouteRef(ref: string | undefined, defMethod = 'GET'): { method: string; path: string } | null {
  if (!ref) return null
  const m = /^\s*(?:(GET|POST|PUT|PATCH|DELETE)\s+)?(\/\S*)\s*$/i.exec(ref)
  if (!m) return null
  return { method: (m[1] || defMethod).toUpperCase(), path: m[2] }
}

export function usePluginAccountActions(reload: () => unknown) {
  const { t } = useI18n()
  const running = ref(false)

  /** Runs `entry` on `accounts` (one for a row, the selection for the bulk bar). */
  async function run(entry: PluginAccountActionEntry, accounts: Account[]) {
    if (!accounts.length || running.value) return
    // The manifest route is "POST /reset" or a bare "/reset" (both POST); manifest validation rejects anything else.
    const target = parseRouteRef(entry.route, 'POST')
    if (!target) {
      console.warn(`[accounts] declared action ${entry.pluginKey}:${entry.id} has no POST route: ${entry.route}`)
      return
    }
    const name = lt(entry.label) || entry.id
    if (entry.confirm) {
      const message = lt(entry.confirm).replace(/\{n\}/g, String(accounts.length))
      if (!(await confirm({ title: name, message, confirmText: name, danger: entry.danger }))) return
    }
    const path = `/p/${entry.pluginKey}${target.path}`
    const batches: Account[][] = []
    for (let i = 0; i < accounts.length; i += MAX_ACCOUNT_BATCH) batches.push(accounts.slice(i, i + MAX_ACCOUNT_BATCH))
    running.value = true
    let ok = 0
    try {
      for (const batch of batches) {
        await api.post(path, { account_ids: batch.map((a) => a.id) }, { signal: AbortSignal.timeout(95000) })
        ok += batch.length
      }
      if (accounts.length === 1) toast(t('accounts.pluginActions.done', { name }), 'success')
      else bulkToast('accounts.bulk', ok, [])
    } catch (error) {
      // Batches run in order: the accounts from the failed one on were not processed.
      bulkToast('accounts.bulk', ok, accounts.slice(ok).map((item) => ({ item, error })))
    } finally {
      running.value = false
    }
    if (ok) reload()
  }

  return { running, run }
}
