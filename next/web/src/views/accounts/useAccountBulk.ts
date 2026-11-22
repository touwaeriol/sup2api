// The bulk actions of the accounts list on the selected rows: each runs on the
// selected accounts it applies to (its button counts them), one request per
// account as the row actions send, and the server checks each the same way.
import { computed, type Ref } from 'vue'
import { api } from '@sub2api/host'
import { confirm, toast } from '@sub2api/ui'
import type { Account } from '@/api/types'
import { bulkToast } from '@/composables/bulkToast'
import { useBulkRun } from '@/composables/useBulkRun'
import { i18n, lt } from '@/i18n'
import { usePluginStore, type PluginAccountActionEntry } from '@/stores/plugins'
import type { AccountAbilities } from './accountAbilities'
import { actionAppliesTo, pluginAccountActionKey } from './accountRowActions'
import { accountEnabled, enabledPatch } from './accountStatus'
import { resetAccountStatus } from './accountQuota'
import { todayQuery } from './accountUsage'

interface AccountBulkDeps {
  selected: Ref<Account[]>
  can: AccountAbilities
  /** Writes a change onto the rows that show the account. */
  patch: (id: number, change: Partial<Account>) => void
  reload: () => void
  runDeclared: (entry: PluginAccountActionEntry, rows: Account[]) => Promise<unknown>
}

/** The selected rows each action takes. */
function pickedOf(rows: Account[], can: AccountAbilities) {
  return {
    test: rows.filter(can.testable),
    edit: rows.filter(can.editable),
    toggle: rows.filter(can.toggleable),
    enable: rows.filter(can.canEnable),
    reset: rows.filter(can.canResetStatus),
    remove: rows.filter(can.canDelete)
  }
}

export function useAccountBulk(deps: AccountBulkDeps) {
  const bulk = useBulkRun(4)
  const picked = computed(() => pickedOf(deps.selected.value, deps.can))
  const run = bulkRequests(deps, bulk, picked)
  const menu = bulkMenu(deps, picked, run)
  return { bulk, picked, ...menu }
}

type Picked = Ref<ReturnType<typeof pickedOf>>

/** Enable, disable and reset status on the rows they take. */
function bulkRequests(deps: AccountBulkDeps, bulk: ReturnType<typeof useBulkRun>, picked: Picked) {
  const { t } = i18n.global
  // Same request and same rows as the row switch: enable takes every account that is not enabled (an automatic
  // disable included), disable every one not disabled by hand.
  async function toggle(enable: boolean) {
    const rows = enable ? picked.value.enable.filter((a) => !accountEnabled(a)) : picked.value.toggle.filter((a) => a.status !== 'disabled')
    if (!rows.length) return toast(t('accounts.bulk.nothingToChange'), 'info')
    const out = await bulk.run(rows, async (a, signal) => {
      deps.patch(a.id, await api.patch<Account>(`/accounts/${a.id}`, enabledPatch(enable), { signal, query: todayQuery() }))
    })
    bulkToast('accounts.bulk', out.ok.length, out.failed)
  }
  async function reset() {
    const rows = picked.value.reset
    const ok = await confirm({ title: t('accounts.resetStatusTitle'), message: t('accounts.bulk.resetConfirm', { n: rows.length }), confirmText: t('accounts.resetStatus') })
    if (!ok) return
    const out = await bulk.run(rows, async (a) => deps.patch(a.id, await resetAccountStatus(a.id)))
    bulkToast('accounts.bulk', out.ok.length, out.failed)
    deps.reload()
  }
  return { toggle, reset }
}

/** The "more" menu of the bulk bar: enable, disable, reset, and the plugins' declared bulk actions. */
function bulkMenu(deps: AccountBulkDeps, picked: Picked, run: ReturnType<typeof bulkRequests>) {
  const { t } = i18n.global
  const plugins = usePluginStore()
  /** The selected rows a declared bulk action targets: the same rows its row-menu entry shows on (CONTRACTS §23.3). */
  const declaredTargets = (x: PluginAccountActionEntry) => deps.selected.value.filter((a) => actionAppliesTo(x, a, deps.can.canUpdate))
  const declaredItem = (x: PluginAccountActionEntry) => ({ key: pluginAccountActionKey(x), label: t('accounts.bulk.pluginAction', { name: lt(x.label) || x.id, n: declaredTargets(x).length }), icon: x.icon, danger: x.danger, disabled: !declaredTargets(x).length })
  const countItem = (key: 'enable' | 'disable' | 'reset', rows: Account[]) => ({ key, label: t(`accounts.bulk.${key}`, { n: rows.length }), disabled: !rows.length })
  const more = computed(() => [
    countItem('enable', picked.value.enable),
    countItem('disable', picked.value.toggle),
    countItem('reset', picked.value.reset),
    ...plugins.accountActions.filter((x) => x.bulk).map(declaredItem)
  ])
  function onMore(key: string) {
    if (key === 'enable' || key === 'disable') return void run.toggle(key === 'enable')
    if (key === 'reset') return void run.reset()
    const declared = plugins.accountActions.find((x) => pluginAccountActionKey(x) === key)
    if (declared) void deps.runDeclared(declared, declaredTargets(declared))
  }
  return { more, onMore }
}
