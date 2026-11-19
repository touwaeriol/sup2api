<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, MAX_ACCOUNT_BATCH } from '@sub2api/host'
import { SAutoRefresh, SBadge, SBulkBar, SButton, SChip, SCode, SCopyButton, SDescItem, SDescriptions, SDropdown, SHint, SIcon, SInput, SLink, SModal, SPageHeader, SPagination, SSectionTitle, SSelect, SSpinner, SSwitch, STable, STabs, STooltip, confirm, toast, useAutoRefresh, type SelectOption, type TableColumn } from '@sub2api/ui'
import type { Account, AccountLastTest, AccountType } from '@/api/types'
import { activeFilterCount, filterFlag, useList } from '@/composables/useList'
import { useGroupsLookup } from '@/composables/lookups'
import { ACCOUNT_ACTIONS, useScope } from '@/composables/useScope'
import { P } from '@/generated/permissions'
import { useAuthStore } from '@/stores/auth'
import { usePluginStore, type PluginAccountActionEntry, type SlotEntry } from '@/stores/plugins'
import { notifyError } from '@/utils/errors'
import { formatDateTime, formatMoney, formatRelative, formatTime } from '@/utils/format'
import { lt } from '@/i18n'
import { useAccountTypes, typeKey } from './accountTypes'
import AccountTypePicker from './AccountTypePicker.vue'
import AccountTypeEndpoints from './AccountTypeEndpoints.vue'
import AccountTypePlatforms from './AccountTypePlatforms.vue'
import EntityPicker from '@/components/EntityPicker.vue'
import UserRef from '@/components/entity/UserRef.vue'
import AccountGroupsCell from './AccountGroupsCell.vue'
import AccountProtocolMarks from './AccountProtocolMarks.vue'
import AccountEditor from './AccountEditor.vue'
import AccountNumberCell from './AccountNumberCell.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { forAccountType, slotKey } from '@/components/plugin/slots'
import AccountLimitsCell from './AccountLimitsCell.vue'
import AccountSuccessRateCell from './AccountSuccessRateCell.vue'
import AccountUsageCell from './AccountUsageCell.vue'
import { todayQuery, usagePeriodLabel } from './accountUsage'
import AccountLimitsTable from './AccountLimitsTable.vue'
import { sameCreationGroup } from './accountTypeChoices'
import { accountControl } from './commonControls'
import AccountQuotaCell from './AccountQuotaCell.vue'
import AccountBalanceCell from './AccountBalanceCell.vue'
import AccountModelTest from './AccountModelTest.vue'
import AccountDeleteDialog from './AccountDeleteDialog.vue'
import AccountOwnerDialog, { type AccountOwner } from './AccountOwnerDialog.vue'
import LastTestCell from './AccountLastTest.vue'
import { actionAppliesTo, pluginAccountActionKey, rowActions, runRowAction, type RowActionDeps } from './accountRowActions'
import { usePluginAccountActions } from './usePluginAccountActions'
import { accountEnabled, accountStatusKey, accountStatusKeys, accountStatusTone, enabledPatch } from './accountStatus'
import { useBatchTest } from './useBatchTest'
import { useSelection } from '@/composables/useSelection'
import { useBulkRun } from '@/composables/useBulkRun'
import { bulkToast } from '@/composables/bulkToast'
import AccountBulkEditDialog from './AccountBulkEditDialog.vue'
import AccountBulkDeleteDialog from './AccountBulkDeleteDialog.vue'
import { hasBalance, hasQuota, refreshAccountCredentials, resetAccountStatus, useBalanceRefresh, useQuotaRefresh } from './accountQuota'

const { t } = useI18n()
const auth = useAuthStore()
const plugins = usePluginStore()
const scope = useScope()
const accountTypes = useAccountTypes()
accountTypes.load()
/** The account type of `a` (undefined until the types load or for an orphaned type). */
const typeOf = (a?: Pick<Account, 'plugin_key' | 'type'> | null) => (a ? accountTypes.find(a.plugin_key, a.type) : undefined)
const { groups } = useGroupsLookup()

// Scopes (CONTRACTS §21, §103.4): account:read sees every account, a narrower scope
// only the accounts in it (the server filters; row actions follow the same rule).
// The owner (所属) shows to a caller reaching more than its own accounts (every
// account, or its team's), the creator, shown only, to one reaching every account.
const readLevel = computed(() => scope.level(ACCOUNT_ACTIONS.read))
const showCreator = computed(() => readLevel.value === 'all')
const showOwner = computed(() => readLevel.value === 'all' || readLevel.value === 'team')
// One creator on the whole page says nothing per row: the column only shows when creators differ.
const mixedCreators = computed(() => new Set(list.items.value.map((a) => a.created_by)).size > 1)
const canCreate = computed(() => auth.hasAction(ACCOUNT_ACTIONS.create))
const canUpdate = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.update)
const canTest = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.test)
const canDelete = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.delete)
const canReveal = (a: Account) => scope.can(a, ACCOUNT_ACTIONS.reveal)

// `mine=true` (owned by the caller) and `owner_id=<id>` work at every level, `subordinates=true`
// (owned by the caller's team) with a team scope; `created_by=<id>` only at the all level (ignored otherwise).
// The usage column's "today" is the browser's day (CONTRACTS §73).
const list = useList<Account>(`/accounts?${new URLSearchParams(todayQuery())}`, {
  plugin_key: '', type: '', group_id: '', status: '', model: '', q: '', mine: '', owner_id: '', subordinates: '', created_by: '', orphaned: '', ids: ''
})
// The account.list.toolbar slot (CONTRACTS §23.3 UI 3.6) narrows the list to
// the ids plugins name. Each plugin keeps its own set and chip label (a
// plugin's `ids` prop is that set; an empty array or null clears it); the list
// gets the intersection of every set (`?ids=1,2`, at most MAX_ACCOUNT_BATCH:
// the rest are dropped and the chips say so). Sets without a common id ask
// for id 0, which no account has, so the list comes back empty.
const idsFilters = ref<Record<string, { ids: number[]; label: string }>>({})
const toolbarProps = (entry: SlotEntry) => ({
  ids: idsFilters.value[entry.pluginKey]?.ids ?? null,
  setIds(ids: number[] | null, label: string) {
    // Plugin input is clamped on the way in: positive integers only, no repeats.
    const clean = [...new Set((ids ?? []).filter((id) => Number.isInteger(id) && id > 0))]
    if (clean.length) idsFilters.value[entry.pluginKey] = { ids: clean, label }
    else clearIds(entry.pluginKey)
  }
})
/** Clears one plugin's id set, or every set. */
function clearIds(pluginKey?: string) {
  if (pluginKey) delete idsFilters.value[pluginKey]
  else idsFilters.value = {}
}
/** The ids common to every plugin's set (null without any), cut to MAX_ACCOUNT_BATCH. */
const idsCommon = computed(() => {
  const sets = Object.values(idsFilters.value).map((f) => f.ids)
  if (!sets.length) return null
  const common = sets.reduce((acc, ids) => {
    const own = new Set(ids)
    return acc.filter((id) => own.has(id))
  })
  return { ids: common.slice(0, MAX_ACCOUNT_BATCH), truncated: common.length > MAX_ACCOUNT_BATCH }
})
watch(idsCommon, (c) => {
  list.filters.ids = !c ? '' : c.ids.length ? c.ids.join(',') : '0'
})
const onlyMine = filterFlag(list.filters, 'mine')
const onlySubordinates = filterFlag(list.filters, 'subordinates')
// Accounts of disabled/uninstalled plugins are hidden by default; the switch
// asks for `orphaned=all`.
const showOrphaned = computed({
  get: () => list.filters.orphaned === 'all',
  set: (v: boolean) => {
    list.filters.orphaned = v ? 'all' : ''
  }
})

// One select drives both ?plugin_key= and ?type= (account type identity).
const typeFilter = computed({
  get: () => (list.filters.plugin_key && list.filters.type ? typeKey(list.filters.plugin_key, list.filters.type) : ''),
  set: (raw: SelectOption['value']) => {
    const v = typeof raw === 'string' ? raw : ''
    const i = v.indexOf('/')
    list.filters.plugin_key = i > 0 ? v.slice(0, i) : ''
    list.filters.type = i > 0 ? v.slice(i + 1) : ''
  }
})

// A quota column decoration declaring accountTypes (UI 3.8) is only for those types of its plugin.
const quotaSlots = computed(() => plugins.slotEntries('account.list.quota'))
const quotaSlotFor = (a: Account) => quotaSlots.value.some((e) => forAccountType(e, a))
const hasQuotaCell = (a: Account) => hasQuota(a.quota) || hasBalance(a.balance) || quotaSlotFor(a)
const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('accounts.listUi.identity'), width: '280px', mobile: 'title' },
  { key: 'groups', label: t('accounts.groups'), mobile: 'meta', mobileSpan: 2 },
  ...(showOwner.value ? [{ key: 'owner', label: t('accounts.owner') }] : []),
  ...(showCreator.value && mixedCreators.value ? [{ key: 'created_by', label: t('accounts.createdBy') }] : []),
  { key: 'status', label: t('common.status'), mobile: 'status' },
  { key: 'priority', label: t('accounts.priority'), width: '72px', mobile: 'meta' },
  // A plugin decoration (account.list.weight) sits beside the number on the same line, so the column grows when one is registered.
  { key: 'weight', label: t('accounts.weight'), width: plugins.slotEntries('account.list.weight').length ? '116px' : '72px', mobile: 'meta' },
  { key: 'limits', label: t('accounts.listUi.limitsColumn'), mobile: 'detail', mobileSpan: 2 },
  { key: 'success_rate', label: t('accounts.successRate.column'), mobile: 'meta' },
  // The core's usage record (CONTRACTS §73), every account type.
  { key: 'usage', label: t('accounts.usage.column'), mobile: 'meta' },
  // What the upstream says is left: plan windows, balance and
  // plugin decorations (account.list.quota), only when a row of this page has one.
  ...(list.items.value.some(hasQuotaCell) ? [{ key: 'quota', label: t('accounts.balance.column') }] : []),
  { key: 'last_test', label: t('accounts.lastTest.column') },
  { key: 'actions', label: t('common.actions'), align: 'right', width: '176px' }
])

const lookupGroup = (id: number) => groups.value.find((g) => g.id === id)
function groupName(id: number, refs?: Array<{ id: number; name: string }>) {
  return refs?.find((g) => g.id === id)?.name || lookupGroup(id)?.name || `#${id}`
}
function groupNames(ids: number[] | undefined, refs?: Array<{ id: number; name: string }>) {
  return ids?.length ? ids.map((id) => groupName(id, refs)).join(', ') : '—'
}
const groupTags = (a: Account) => (a.group_ids || []).map((id) => ({ id, name: groupName(id, a.groups), platforms: lookupGroup(id)?.platforms }))

/** Applies a server change to the list row and the open detail of the same account. */
function patchAccount(id: number, patch: Partial<Account>) {
  const row = list.items.value.find((x) => x.id === id)
  if (row) Object.assign(row, patch)
  if (detail.value?.id === id) Object.assign(detail.value, patch)
}

function coolingDown(a: Account) {
  return !!a.cooldown_until && new Date(a.cooldown_until).getTime() > Date.now()
}

/** detail: full explanation (list badge tooltip, detail drawer); note: short recovery time shown under the list badge. */
type StatusView = { tone: ReturnType<typeof accountStatusTone>; label: string; detail?: string; note?: string }
function statusOf(a: Account): StatusView {
  const key = accountStatusKey(a)
  const cooling = key === 'active' && coolingDown(a)
  // The usage limit's own reason is localized; without the limit's figures it reads generically.
  const limit = a.usage?.limit
  let statusReason = a.status_reason
  if (a.usage?.limit_disabled) {
    statusReason = limit
      ? t('accounts.usage.limitReached', { used: formatMoney(limit.used, 2), amount: formatMoney(limit.amount, 2), period: usagePeriodLabel(t, limit.period, limit.period_count) })
      : t('accounts.usage.limitReachedPlain')
  }
  const reason = a.orphaned ? t('accounts.orphanedHint', { plugin: a.plugin_key }) : (cooling && a.cooldown_reason) || statusReason
  const view: StatusView = { tone: accountStatusTone(a), label: t(`accounts.status.${key}`), detail: reason || undefined }
  // Auto-disabled reads as one badge, yellow with a release time and red without; why, and when it comes back, show in its
  // tooltip (hover or keyboard focus) and in the detail drawer.
  if (key === 'auto_disabled' && a.auto_disabled_until) {
    const release = t('accounts.autoReleaseAt', { time: formatDateTime(a.auto_disabled_until) })
    view.detail = view.detail ? `${view.detail}\n${release}` : release
  }
  if (cooling) view.note = t('accounts.cooldownUntil', { time: formatTime(a.cooldown_until) })
  return view
}

// ---------------------------------------------------------------- auto refresh (in_use / cooldown)
const refreshSeconds = ref(10)
useAutoRefresh(list.reload, () => refreshSeconds.value * 1000, { enabled: () => !editorOpen.value })

// ---------------------------------------------------------------- create / edit
const editorOpen = ref(false)
const step = ref<1 | 2>(1)
const pickedType = ref<AccountType | null>(null)
const editing = ref<Account | null>(null)
const editorLoading = ref(false)

const editorTitle = computed(() => {
  if (editing.value) return `${t('accounts.editTitle')} · ${editing.value.name}`
  if (step.value === 1) return t('accounts.newStep1')
  return `${t('accounts.newTitle')} · ${pickedType.value ? accountTypes.label(pickedType.value.plugin_key, pickedType.value.type) : ''}`
})

function openCreate() {
  editing.value = null
  pickedType.value = null
  step.value = 1
  editorOpen.value = true
}

function pick(at: AccountType) {
  pickedType.value = at
  step.value = 2
}

async function openEdit(a: Pick<Account, 'id'>) {
  editorLoading.value = true
  editorOpen.value = true
  step.value = 2
  try {
    const full = await api.get<Account>(`/accounts/${a.id}`, todayQuery())
    editing.value = full
    await accountTypes.load()
    pickedType.value = typeOf(full) || null
  } catch (e) {
    notifyError(e)
    editorOpen.value = false
  } finally {
    editorLoading.value = false
  }
}

function onSaved() {
  list.reload()
  editorOpen.value = false
}

// Deep link (/accounts?edit=<id>): opens the editor.
const route = useRoute()
const router = useRouter()
watch(
  () => route.query.edit,
  (raw) => {
    const id = Number(Array.isArray(raw) ? raw[0] : raw)
    if (!raw || !Number.isInteger(id) || id <= 0) return
    void openEdit({ id })
    const { edit: _e, ...rest } = route.query
    void router.replace({ query: rest })
  },
  { immediate: true }
)

// ---------------------------------------------------------------- test (model test dialog, CONTRACTS: POST /accounts/:id/test)
const testOpen = ref(false)
const testTarget = ref<Account | null>(null)
const testTargetType = computed(() => typeOf(testTarget.value) || null)

function openTest(a: Account) {
  testTarget.value = a
  testOpen.value = true
}

/** Writes a test outcome onto the list row (the server records it as last_test too). */
const onTested = (id: number, last: AccountLastTest) => patchAccount(id, { last_test: last })

function onTestUpdated(saved: Account) {
  testTarget.value = { ...(testTarget.value || saved), ...saved }
  const row = list.items.value.find((x) => x.id === saved.id)
  if (row) Object.assign(row, { models: saved.models, model_mapping: saved.model_mapping })
  if (editing.value?.id === saved.id) editing.value = { ...editing.value, models: saved.models, model_mapping: saved.model_mapping }
}

// ---------------------------------------------------------------- selection + bulk actions
// Rows the caller may act on in some way; each action runs on the selected
// accounts it applies to (the button shows how many), and the server checks
// every request as it does for a single row.
const testable = (a: Account) => canTest(a) && !a.orphaned
const editable = (a: Account) => canUpdate(a) && !a.orphaned
const toggleable = (a: Account) => editable(a) && accountControl(typeOf(a), 'schedulable').configurable
// An account an administrator disabled (admin_locked, still disabled): a caller with a restricted scope can
// neither enable it nor reset its status (the server answers 403 account_locked_by_admin), so both are shown
// disabled or left out. A stale lock on an account in another status holds nothing back, as on the server.
const lockedByAdmin = (a: Account) => !!a.admin_locked && a.status === 'disabled' && scope.level(ACCOUNT_ACTIONS.update) !== 'all'
const canEnable = (a: Account) => toggleable(a) && !lockedByAdmin(a)
const selectable = (a: Account) => testable(a) || editable(a) || canDelete(a)
const selection = useSelection<Account>({ items: list.items, key: (a) => a.id, selectable, loadAll: loadAllAccounts })
const selectionKeys = computed({ get: () => selection.keys.value as unknown[], set: (v: unknown[]) => selection.update(v) })
const anySelectable = computed(() => list.items.value.some(selectable))
/** Every account matching the current filters (pages of 200). */
async function loadAllAccounts(): Promise<Account[]> {
  const out: Account[] = []
  for (let page = 1; page <= 100; page++) {
    const res = await api.list<Account>('/accounts', { ...list.filters, page, page_size: 200 })
    out.push(...res.items)
    if (res.items.length < 200 || out.length >= (res.page?.total ?? 0)) break
  }
  return out
}
// A changed filter starts a new selection: rows outside it would be acted on unseen.
watch(() => ({ ...list.filters }), () => selection.clear(), { deep: true })
const picked = computed(() => ({
  test: selection.rows.value.filter(testable),
  edit: selection.rows.value.filter(editable),
  toggle: selection.rows.value.filter(toggleable),
  enable: selection.rows.value.filter(canEnable),
  reset: selection.rows.value.filter(canResetStatus),
  remove: selection.rows.value.filter(canDelete)
}))

const { batchTesting, batchRunning, batchDone, batchTotal, batchTest, stopBatch } = useBatchTest({ tested: onTested })

const bulk = useBulkRun(4)
const declaredActions = usePluginAccountActions(list.reload)
const bulkBusy = computed(() => batchRunning.value || bulk.running.value || declaredActions.running.value)
// Same request and same rows as the row switch: enable takes every account that is not enabled (an automatic
// disable included), disable every one not disabled by hand.
async function bulkToggle(enable: boolean) {
  const rows = enable ? picked.value.enable.filter((a) => !accountEnabled(a)) : picked.value.toggle.filter((a) => a.status !== 'disabled')
  if (!rows.length) return toast(t('accounts.bulk.nothingToChange'), 'info')
  const out = await bulk.run(rows, async (a, signal) => {
    patchAccount(a.id, await api.patch<Account>(`/accounts/${a.id}`, enabledPatch(enable), { signal, query: todayQuery() }))
  })
  bulkToast('accounts.bulk', out.ok.length, out.failed)
}
async function bulkReset() {
  const rows = picked.value.reset
  const ok = await confirm({ title: t('accounts.resetStatusTitle'), message: t('accounts.bulk.resetConfirm', { n: rows.length }), confirmText: t('accounts.resetStatus') })
  if (!ok) return
  const out = await bulk.run(rows, async (a) => {
    patchAccount(a.id, await resetAccountStatus(a.id))
  })
  bulkToast('accounts.bulk', out.ok.length, out.failed)
  list.reload()
}
const bulkEditOpen = ref(false)
const bulkDeleteOpen = ref(false)
function onBulkSaved(rows: Account[]) {
  for (const r of rows) patchAccount(r.id, r)
  list.reload()
}
function onBulkDeleted(ids: number[]) {
  selection.drop(ids)
  list.reload()
}
/** The selected rows a declared bulk action targets: the same rows its row-menu entry shows on (CONTRACTS §23.3). */
const declaredTargets = (x: PluginAccountActionEntry) => selection.rows.value.filter((a) => actionAppliesTo(x, a, canUpdate))
const moreBulkActions = computed(() => [
  { key: 'enable', label: t('accounts.bulk.enable', { n: picked.value.enable.length }), disabled: !picked.value.enable.length },
  { key: 'disable', label: t('accounts.bulk.disable', { n: picked.value.toggle.length }), disabled: !picked.value.toggle.length },
  { key: 'reset', label: t('accounts.bulk.reset', { n: picked.value.reset.length }), disabled: !picked.value.reset.length },
  ...plugins.accountActions.filter((x) => x.bulk).map((x) => ({ key: pluginAccountActionKey(x), label: t('accounts.bulk.pluginAction', { name: lt(x.label) || x.id, n: declaredTargets(x).length }), icon: x.icon, danger: x.danger, disabled: !declaredTargets(x).length }))
])
function onBulkMore(key: string) {
  if (key === 'enable' || key === 'disable') void bulkToggle(key === 'enable')
  else if (key === 'reset') void bulkReset()
  else {
    const declared = plugins.accountActions.find((x) => pluginAccountActionKey(x) === key)
    if (declared) void declaredActions.run(declared, declaredTargets(declared))
  }
}

// ---------------------------------------------------------------- reveal credentials
const revealOpen = ref(false)
const revealed = ref<Record<string, unknown> | null>(null)
async function reveal(a: Account) {
  try {
    revealed.value = await api.post<Record<string, unknown>>(`/accounts/${a.id}/credentials/reveal`)
    revealOpen.value = true
  } catch (e) {
    notifyError(e)
  }
}
const revealedText = computed(() => JSON.stringify(revealed.value, null, 2))

// ---------------------------------------------------------------- detail
const detailOpen = ref(false)
const detail = ref<Account | null>(null)
const detailTab = ref('overview')
// A tab declaring accountTypes (UI 3.8) is only for those types of its plugin; openDetail starts every account on the overview.
const detailSlots = computed(() => plugins.slotEntries('account.detail.tabs').filter((e) => forAccountType(e, detail.value)))
const detailTabs = computed(() => [{ key: 'overview', label: t('accounts.overview') }, ...detailSlots.value.map((e) => ({ key: slotKey(e), label: tabLabel(e.pluginKey, e.name) }))])
function tabLabel(pluginKey: string, name: string) {
  const msgKey = `plugin.${pluginKey}.tabs.${name}`
  const translated = t(msgKey)
  return translated !== msgKey ? translated : `${pluginKey} · ${name}`
}
const detailSlot = computed(() => detailSlots.value.find((e) => slotKey(e) === detailTab.value))
const detailType = computed(() => typeOf(detail.value))

async function openDetail(a: Account) {
  detailTab.value = 'overview'
  detail.value = a
  detailOpen.value = true
  try {
    const full = await api.get<Account>(`/accounts/${a.id}`, todayQuery())
    // The quota snapshot is a list field; keep the row's when the detail omits it.
    detail.value = full.quota === undefined ? { ...full, quota: a.quota } : full
  } catch (e) {
    notifyError(e)
  }
}

// ---------------------------------------------------------------- quota refresh / reset status
// The snapshot of every row arrives with the list; "refresh" asks the upstream
// for one account (force), with duplicate in-flight clicks coalesced.
const quotaRefresh = useQuotaRefresh({
  onSnapshot: (id, quota) => patchAccount(id, { quota }),
  onError: (_id, e) => notifyError(e)
})

// ---------------------------------------------------------------- balance refresh
// Similar to quota refresh, but for account balance (CONTRACTS §52).
const balanceRefresh = useBalanceRefresh({
  onSnapshot: (id, balance) => patchAccount(id, { balance }),
  onError: (_id, e) => notifyError(e)
})

// A manual disable is undone with Enable; Reset status clears what the gateway set.
const canResetStatus = (a: Account) => canUpdate(a) && !a.orphaned && a.status !== 'disabled' && !lockedByAdmin(a)

async function resetStatus(a: Account) {
  const ok = await confirm({ title: t('accounts.resetStatusTitle'), message: t('accounts.resetStatusConfirm', { name: a.name }), confirmText: t('accounts.resetStatus') })
  if (!ok) return
  try {
    const updated = await resetAccountStatus(a.id)
    Object.assign(a, updated)
    patchAccount(a.id, updated)
    toast(t('accounts.resetStatusDone'), 'success')
    list.reload()
  } catch (e) {
    notifyError(e)
  }
}

// ---------------------------------------------------------------- credential refresh (CONTRACTS §48)
// The core renews expiring tokens by itself; the detail shows the state and
// offers one manual refresh.
const refreshingCreds = ref(new Set<number>())
const canRefreshCreds = (a: Account) => !!a.refresh && canUpdate(a) && !a.orphaned

function credRefreshText(a: Account): { text: string; warn: boolean } {
  const r = a.refresh
  if (!r) return { text: '', warn: false }
  if (r.error_type === 'auth_rejected') return { text: t('accounts.credRefresh.authRejected'), warn: true }
  const parts: string[] = []
  if (!r.expires_at) parts.push(t('accounts.credRefresh.unknown'))
  else if (new Date(r.expires_at).getTime() <= Date.now()) parts.push(t('accounts.credRefresh.expired'))
  else parts.push(t('accounts.credRefresh.expiresAt', { time: formatDateTime(r.expires_at) }))
  if (r.last_success_at) parts.push(t('accounts.credRefresh.lastSuccess', { time: formatRelative(r.last_success_at, t) }))
  if (r.error_type === 'transient') parts.push(t('accounts.credRefresh.transient', { error: r.error }))
  return { text: parts.join(' · '), warn: r.error_type === 'transient' }
}

async function refreshCreds(a: Account) {
  if (refreshingCreds.value.has(a.id)) return
  refreshingCreds.value.add(a.id)
  try {
    const out = await refreshAccountCredentials(a.id)
    a.refresh = out.refresh
    patchAccount(a.id, { refresh: out.refresh })
    if (out.refreshed) toast(t('accounts.credRefresh.done'), 'success')
    else if (out.skipped === 'in_progress') toast(t('accounts.credRefresh.inProgress'), 'info')
    else if (out.skipped === 'changed') toast(t('accounts.credRefresh.changed'), 'info')
    else if (out.error) toast(t('accounts.credRefresh.failed', { error: out.error }), 'error')
  } catch (e) {
    notifyError(e)
  } finally {
    refreshingCreds.value.delete(a.id)
  }
}
// ---------------------------------------------------------------- enable / disable (actions column)
// The row's enable / disable button shows the new state at once and puts the
// old one back when the request fails. Enable is the manual enable (see
// enabledPatch): it also recovers an automatically disabled account, which
// offers Enable; the row menu offers such an account a manual disable.
const togglingEnabled = ref(new Set<number>())
async function toggleEnabled(a: Account, on: boolean) {
  if (togglingEnabled.value.has(a.id) || !(on ? canEnable(a) : toggleable(a))) return
  togglingEnabled.value.add(a.id)
  const before = { status: a.status, schedulable: a.schedulable, status_reason: a.status_reason, auto_disabled_until: a.auto_disabled_until }
  patchAccount(a.id, { ...enabledPatch(on), ...(on ? { status_reason: '', auto_disabled_until: null } : {}) })
  try {
    patchAccount(a.id, await api.patch<Account>(`/accounts/${a.id}`, enabledPatch(on), { query: todayQuery() }))
    toast(t(on ? 'accounts.listUi.enabledDone' : 'accounts.listUi.disabledDone', { name: a.name }), 'success')
  } catch (e) {
    patchAccount(a.id, before)
    notifyError(e)
  } finally {
    togglingEnabled.value.delete(a.id)
  }
}
/** Hover text of a row's enable / disable button: what it does. */
function enabledToggleTitle(a: Account) {
  if (!accountControl(typeOf(a), 'schedulable').configurable) return t('accounts.editorUi.commonControlFixed')
  if (accountEnabled(a)) return t('accounts.listUi.disableHint')
  if (lockedByAdmin(a)) return t('accounts.listUi.adminLockedHint')
  return t(a.status === 'disabled' ? 'accounts.listUi.enableHint' : 'accounts.listUi.enableAutoHint')
}
const canDisable = (a: Account) => toggleable(a) && a.status !== 'disabled' && !accountEnabled(a)

// ---------------------------------------------------------------- row actions
const deleteOpen = ref(false)
const deleteTarget = ref<Account | null>(null)
function remove(a: Account) {
  deleteTarget.value = a
  deleteOpen.value = true
}

// The owner (所属, CONTRACTS §103.4): account:owner:update moves an account to another user.
const ownerOpen = ref(false)
const ownerTarget = ref<Account | null>(null)
function changeOwner(a: Account) {
  ownerTarget.value = a
  ownerOpen.value = true
}
const ownerChanged = ({ id, ...owner }: AccountOwner) => patchAccount(id, owner)

const rowActionDeps: RowActionDeps = {
  canReveal,
  canResetStatus,
  canDisable,
  canRefreshCreds,
  canUpdate,
  canDelete,
  canChangeOwner: () => auth.has(P.AccountOwnerUpdate),
  openDetail,
  reveal,
  resetStatus,
  disable: (a) => toggleEnabled(a, false),
  refreshCreds,
  changeOwner,
  remove,
  // Read on each menu build: the plugin list may change after a refresh.
  get declaredActions() {
    return plugins.accountActions
  },
  runPluginAccountAction: (a, entry) => declaredActions.run(entry, [a])
}
const actionsFor = (a: Account) => rowActions(a, rowActionDeps)
function onAction(a: Account, key: string) {
  void runRowAction(a, key, rowActionDeps)
}

const statusSelectOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('accounts.allStatus') },
  ...accountStatusKeys.map((s) => ({ value: s, label: t(`accounts.status.${s}`) }))
])
// Account types grouped per plugin (<optgroup>), "all types" first.
const typeOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('accounts.allTypes') },
  ...accountTypes.grouped.value.map((g) => ({
    value: g.plugin_key,
    label: lt(g.plugin_name) || g.plugin_key,
    options: g.types.map((at) => ({ value: typeKey(at.plugin_key, at.type), label: lt(at.label) || at.type }))
  }))
])
const advancedFilters = ref(false)
const denseRows = ref(true)
const filterCount = computed(() => activeFilterCount(list.filters, ['plugin_key']))
const hasAdvancedFilters = computed(() => ['model', 'owner_id', 'created_by', 'mine', 'subordinates', 'orphaned'].some((key) => !!list.filters[key]))
function resetFilters() {
  for (const key of Object.keys(list.filters)) list.filters[key] = ''
  clearIds()
}
</script>

<template>
  <div>
    <SPageHeader :title="t('accounts.title')" :description="t('accounts.subtitle')" :filters-active-count="activeFilterCount(list.filters, ['q', 'plugin_key'])">
      <template #filters-primary>
        <SInput v-model="list.filters.q" icon="search" clearable class="w-44 2xl:w-56" :aria-label="t('accounts.listUi.search')" :placeholder="t('accounts.listUi.search')" />
        <!-- Plugin toolbar items and the chips of the ids they set stay in sight on a phone too. -->
        <PluginSlot name="account.list.toolbar" :props="toolbarProps" />
        <SChip v-for="(f, key, i) in idsFilters" :key="key" tone="primary" closable :close-label="t('accounts.listUi.clearIds')" data-testid="ids-filter" @close="clearIds(String(key))">{{ i === 0 && idsCommon?.truncated ? t('accounts.listUi.idsTruncated', { label: f.label, n: MAX_ACCOUNT_BATCH }) : f.label }}</SChip>
      </template>
      <template #filters>
        <SSelect v-model="typeFilter" :options="typeOptions" class="w-36 2xl:w-44" :aria-label="t('accounts.type')" />
        <EntityPicker v-model:id="list.filters.group_id" kind="group" class="w-48" data-testid="group-filter" />
        <SSelect v-model="list.filters.status" :options="statusSelectOptions" class="w-28 2xl:w-32" :aria-label="t('common.status')" />
        <SButton :variant="hasAdvancedFilters ? 'primary' : 'secondary'" :aria-expanded="advancedFilters" @click="advancedFilters = !advancedFilters">{{ t('accounts.listUi.advanced') }}</SButton>
        <SButton v-if="filterCount" variant="ghost" data-testid="clear-filters" @click="resetFilters">{{ t('accounts.listUi.clear', { count: filterCount }) }}</SButton>
      </template>
      <template #actions>
        <!-- Row density means nothing to phone cards; there the auto-refresh menu holds "Refresh now". -->
        <SSwitch v-model="denseRows" class="max-md:hidden" :label="t('accounts.listUi.compact')" />
        <SAutoRefresh v-model="refreshSeconds" @refresh="list.reload()" />
        <SButton class="max-md:hidden" :loading="list.loading.value" :title="t('accounts.listUi.refresh')" data-testid="account-refresh" @click="list.reload()">{{ t('common.refresh') }}</SButton>
        <SButton v-if="canCreate" variant="primary" icon="plus" fab data-testid="account-new" @click="openCreate">{{ t('accounts.new') }}</SButton>
      </template>
    </SPageHeader>

    <section v-if="advancedFilters" class="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-line bg-surface px-3 py-2" :aria-label="t('accounts.listUi.filters')">
      <EntityPicker v-model:id="list.filters.model" kind="model" class="w-full sm:w-64" data-testid="model-filter" />
      <EntityPicker v-if="showOwner" v-model:id="list.filters.owner_id" kind="user" class="w-full sm:w-72" data-testid="owner-filter" :placeholder="t('accounts.owner')" />
      <EntityPicker v-if="showCreator" v-model:id="list.filters.created_by" kind="user" class="w-full sm:w-72" data-testid="created-by-filter" :placeholder="t('accounts.createdBy')" />
      <SSwitch v-model="onlyMine" :label="t('common.onlyMine')" data-testid="only-mine" />
      <SSwitch v-if="readLevel === 'team'" v-model="onlySubordinates" :label="t('accounts.onlySubordinates')" data-testid="only-subordinates" />
      <SSwitch v-model="showOrphaned" :label="t('accounts.showOrphaned')" data-testid="show-orphaned" />
    </section>
    <p v-else-if="hasAdvancedFilters" class="mb-3 text-xs text-primary-600 dark:text-primary-300">{{ t('accounts.listUi.advancedApplied') }}</p>
    <SHint v-if="list.error.value" size="xs" tone="danger" role="alert" class="mb-3">{{ t(list.items.value.length ? 'accounts.listUi.loadFailed' : 'accounts.listUi.loadFailedEmpty') }}</SHint>
    <SBulkBar
      v-if="anySelectable || selection.keys.value.length"
      class="mb-3"
      :persistent="batchRunning || bulk.running.value"
      :count="selection.keys.value.length"
      :total="list.total.value"
      :page-selected="selection.pageSelected.value"
      :all-selected="selection.allSelected.value"
      :selecting-all="selection.selectingAll.value"
      :disabled="bulkBusy"
      data-testid="account-batch-bar"
      @select-page="selection.selectPage()"
      @select-all="selection.selectAll().catch(notifyError)"
      @clear="selection.clear()"
    >
      <template v-if="selection.keys.value.length || batchRunning || bulk.running.value">
        <SHint v-if="bulk.running.value" inline size="xs" aria-live="polite">{{ t('accounts.bulk.progress', { done: bulk.done.value, total: bulk.total.value }) }}</SHint>
        <SButton size="sm" variant="primary" :disabled="!picked.edit.length || bulkBusy" data-testid="account-bulk-edit" @click="bulkEditOpen = true">{{ t('accounts.bulk.edit', { n: picked.edit.length }) }}</SButton>
        <SButton size="sm" :loading="batchRunning" :disabled="!picked.test.length || bulk.running.value" icon="play" :title="t('accounts.batchTest.hint')" data-testid="account-batch-test" @click="batchTest(picked.test.map((a) => a.id))">
          {{ batchRunning ? t('accounts.batchTest.progress', { done: batchDone, total: batchTotal }) : t('accounts.batchTest.run', { n: picked.test.length }) }}
        </SButton>
        <SButton v-if="batchRunning" size="sm" variant="danger" icon="stop" @click="stopBatch">{{ t('accounts.modelTest.stop') }}</SButton>
        <SButton v-if="bulk.running.value" size="sm" variant="danger" icon="stop" @click="bulk.stop()">{{ t('accounts.modelTest.stop') }}</SButton>
        <SDropdown :actions="moreBulkActions" :label="t('accounts.bulk.more')" :disabled="bulkBusy" width="13rem" data-testid="account-bulk-more" @select="onBulkMore" />
        <SButton size="sm" variant="danger" :disabled="!picked.remove.length || bulkBusy" data-testid="account-bulk-delete" @click="bulkDeleteOpen = true">{{ t('accounts.bulk.delete', { n: picked.remove.length }) }}</SButton>
      </template>
    </SBulkBar>
    <STable
      v-model:selected="selectionKeys"
      class="account-table"
      :columns="columns"
      :rows="list.items.value"
      :loading="list.loading.value"
      :dense="denseRows"
      :selectable="anySelectable"
      :row-selectable="selectable"
      :empty-text="t(filterCount ? 'accounts.listUi.emptyFiltered' : 'accounts.listUi.empty')"
    >
      <template #cell-last_test="{ row }">
        <LastTestCell :last="row.last_test" :testing="batchTesting.has(row.id)" />
      </template>
      <template #cell-name="{ row }">
        <div class="min-w-56 max-w-[17rem] space-y-0.5">
          <div class="flex items-center gap-2"><span class="shrink-0 font-mono text-xxs text-fg-subtle">#{{ row.id }}</span><SLink as="button" class="min-w-0 truncate font-semibold" :title="row.name" @click="openDetail(row)">{{ row.name }}</SLink></div>
          <div class="flex items-center gap-1.5 whitespace-nowrap text-xs text-fg-muted"><AccountProtocolMarks :account="row" :account-type="typeOf(row)" /><span :title="row.plugin_key">{{ accountTypes.pluginName(row.plugin_key) }}</span><span aria-hidden="true">/</span><span class="truncate" :title="accountTypes.typeLabel(row.plugin_key, row.type, row.type_label)">{{ accountTypes.typeLabel(row.plugin_key, row.type, row.type_label) }}</span><span v-if="row.last_used_at" class="shrink-0 text-xxs text-fg-subtle">· {{ formatRelative(row.last_used_at, t) }}</span></div>
        </div>
      </template>
      <template #cell-groups="{ row }"><AccountGroupsCell :groups="groupTags(row)" /></template>
      <template #cell-owner="{ row }"><UserRef :id="row.owner_id" :email="row.owner_email" /></template>
      <template #cell-created_by="{ row }"><UserRef :id="row.created_by" :email="row.created_by_email" /></template>
      <template #cell-status="{ row }">
        <STooltip :content="statusOf(row).detail" :disabled="!statusOf(row).detail" placement="bottom" :delay="150" max-width="24rem">
          <!-- Focusable when there is a reason: keyboard focus shows it too, not only hover (§73.4); the detail drawer lists it for touch. -->
          <span :tabindex="statusOf(row).detail ? 0 : undefined" class="inline-flex rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40" data-testid="account-status-tip">
            <SBadge :tone="statusOf(row).tone" dot data-testid="account-status">{{ statusOf(row).label }}</SBadge>
          </span>
        </STooltip>
        <!-- Icon only, beside the badge, to keep the status column narrow; the name lives in the tooltip and aria-label. -->
        <SLink v-if="(row.status !== 'active' || row.status_reason || coolingDown(row)) && canResetStatus(row)" as="button" class="ml-1 inline-flex items-center align-middle" :title="`${t('accounts.resetStatus')} · ${t('accounts.resetStatusHint')}`" :aria-label="t('accounts.resetStatus')" data-testid="account-reset-status" @click="resetStatus(row)">
          <SIcon name="refresh" class="h-3.5 w-3.5" />
        </SLink>
        <SHint v-if="statusOf(row).note" size="xs" class="mt-0.5 block" data-testid="account-status-note">{{ statusOf(row).note }}</SHint>
      </template>
      <template v-for="field in (['priority', 'weight'] as const)" :key="field" #[`cell-${field}`]="{ row }">
        <!-- Plugin decorations stay on the number's line: wrapping them below made every decorated row taller. -->
        <div v-if="field === 'weight'" class="flex items-center gap-1.5 whitespace-nowrap">
          <AccountNumberCell :account="row" :field="field" :editable="canUpdate(row) && !row.orphaned" @saved="patchAccount(row.id, $event)" />
          <PluginSlot name="account.list.weight" :props="{ account: row }" item-class="flex shrink-0 items-center" />
        </div>
        <AccountNumberCell v-else :account="row" :field="field" :editable="canUpdate(row) && !row.orphaned" @saved="patchAccount(row.id, $event)" />
      </template>
      <template #cell-limits="{ row }">
        <AccountLimitsCell :account="row" />
      </template>
      <template #cell-success_rate="{ row }">
        <AccountSuccessRateCell :outcomes="row.outcomes" />
      </template>
      <template #cell-usage="{ row }">
        <AccountUsageCell :account-id="row.id" :usage="row.usage" />
      </template>
      <template #cell-quota="{ row }">
        <div v-if="hasQuotaCell(row)" class="space-y-2">
          <AccountQuotaCell v-if="hasQuota(row.quota)" compact :quota="row.quota" :refreshing="quotaRefresh.refreshing.has(row.id)" @refresh="quotaRefresh.refresh(row.id)" />
          <AccountBalanceCell v-if="hasBalance(row.balance)" :balance="row.balance" :refreshing="balanceRefresh.refreshing.has(row.id)" @refresh="balanceRefresh.refresh(row.id)" />
          <!-- Plugin decorations share one line under the windows, so a decorated row grows by one line at most. -->
          <div v-if="quotaSlotFor(row)" class="flex items-center gap-1.5">
            <PluginSlot name="account.list.quota" :props="{ account: row }" :only="(e) => forAccountType(e, row)" item-class="flex shrink-0 items-center" />
          </div>
        </div>
        <!-- nothing from the upstream (API keys): show nothing, not even the table's "—" fallback -->
        <span v-else />
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <!-- Text actions, as new-api's channel rows: test, disable (red) / enable (blue), edit, then the menu. Readers and orphaned accounts get the menu only. -->
          <SButton v-if="canTest(row) && !row.orphaned" size="xs" data-testid="account-test" @click="openTest(row)">{{ t('common.test') }}</SButton>
          <SButton
            v-if="editable(row)"
            size="xs"
            :tone="accountEnabled(row) ? 'danger' : 'info'"
            :disabled="togglingEnabled.has(row.id) || !(accountEnabled(row) ? toggleable(row) : canEnable(row))"
            :aria-label="t(accountEnabled(row) ? 'accounts.listUi.disableAccount' : 'accounts.listUi.enableAccount', { name: row.name })"
            :title="enabledToggleTitle(row)"
            data-testid="account-enable-toggle"
            @click="toggleEnabled(row, !accountEnabled(row))"
          >
            {{ t(accountEnabled(row) ? 'common.disable' : 'common.enable') }}
          </SButton>
          <SButton v-if="canUpdate(row) && !row.orphaned" size="xs" data-testid="account-edit" @click="openEdit(row)">{{ t('common.edit') }}</SButton>
          <SDropdown :actions="actionsFor(row)" @select="onAction(row, $event)" />
        </div>
      </template>
    </STable>
    <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />

    <!-- create / edit -->
    <SModal v-model:open="editorOpen" :title="editorTitle" width="2xl" fixed persistent>
      <SSpinner v-if="editorLoading" block />
      <AccountTypePicker v-else-if="!editing && step === 1" @pick="pick" />
      <AccountEditor
        v-else
        :account-type="pickedType"
        :account-type-options="accountTypes.types.value.filter(at => sameCreationGroup(at, pickedType))"
        :account="editing"
        @change-type="pickedType = $event"
        @saved="onSaved"
        @cancel="editorOpen = false"
        @back="step = 1"
        @test="openTest"
      />
    </SModal>

    <!-- test -->
    <AccountModelTest v-model:open="testOpen" :account="testTarget" :account-type="testTargetType" @tested="onTested" @updated="onTestUpdated" />
    <AccountBulkEditDialog v-model:open="bulkEditOpen" :accounts="picked.edit" :type-of="typeOf" @saved="onBulkSaved" />
    <AccountBulkDeleteDialog v-model:open="bulkDeleteOpen" :accounts="picked.remove" @deleted="onBulkDeleted" />
    <AccountDeleteDialog v-model:open="deleteOpen" :account="deleteTarget" @deleted="list.reload" />
    <AccountOwnerDialog v-model:open="ownerOpen" :account="ownerTarget" @changed="ownerChanged" />

    <!-- reveal -->
    <SModal v-model:open="revealOpen" :title="t('accounts.revealCredentials')" width="lg" @close="revealed = null">
      <SHint tone="warning" class="mb-3">{{ t('accounts.revealWarning') }}</SHint>
      <SCode>{{ revealedText }}</SCode>
      <template #footer>
        <SCopyButton :text="revealedText" :label="t('common.copy')" variant="secondary" size="md" />
        <SButton variant="primary" @click="revealOpen = false">{{ t('common.close') }}</SButton>
      </template>
    </SModal>

    <!-- detail -->
    <SModal v-model:open="detailOpen" :title="detail?.name || ''" width="xl">
      <template v-if="detail">
        <STabs v-model="detailTab" :tabs="detailTabs">
          <div v-if="detailTab === 'overview'" class="space-y-4">
            <SDescriptions label-width="9rem">
              <SDescItem label="ID">{{ detail.id }}</SDescItem>
              <SDescItem :label="t('accounts.type')">
                {{ accountTypes.typeLabel(detail.plugin_key, detail.type, detail.type_label) }}
                <SHint inline size="xs">· {{ accountTypes.pluginName(detail.plugin_key) }} <span class="font-mono">({{ detail.plugin_key }}/{{ detail.type }})</span></SHint>
              </SDescItem>
              <SDescItem v-if="detailType" :label="t('platforms.supported')"><AccountTypePlatforms :items="detailType.platforms" /></SDescItem>
              <SDescItem :label="t('accounts.servesEndpoints')">
                <AccountTypeEndpoints v-if="detailType" :endpoints="detailType.endpoints" :platforms="detailType.platforms.map((p) => p.id)" :served="detail.served_protocols" />
                <SHint v-else inline size="xs">{{ t('accounts.typeUnavailable') }}</SHint>
              </SDescItem>
              <SDescItem :label="t('common.status')">
                <SBadge :tone="statusOf(detail).tone" dot>{{ statusOf(detail).label }}</SBadge>
                <SHint v-if="statusOf(detail).note" inline size="xs" class="ml-2">{{ statusOf(detail).note }}</SHint>
                <SHint v-if="statusOf(detail).detail" inline size="xs" class="ml-2 whitespace-pre-line" data-testid="account-detail-status-reason">{{ statusOf(detail).detail }}</SHint>
                <SLink v-if="coolingDown(detail) && canResetStatus(detail)" as="button" class="ml-2 text-xs" :title="t('accounts.resetStatusHint')" @click="resetStatus(detail)">{{ t('accounts.resetStatus') }}</SLink>
              </SDescItem>
              <SDescItem :label="t('accounts.groups')">{{ groupNames(detail.group_ids, detail.groups) }}</SDescItem>
              <SDescItem :label="t('accounts.priority')">{{ detail.priority }}</SDescItem>
              <SDescItem :label="t('accounts.weight')">{{ detail.weight ?? 1 }}</SDescItem>
              <SDescItem :label="t('accounts.listUi.limitsColumn')"><AccountLimitsTable :account="detail" class="max-w-xl" /></SDescItem>
              <SDescItem v-if="detail.refresh" :label="t('accounts.credRefresh.label')" data-testid="account-cred-refresh">
                  <span :class="credRefreshText(detail).warn ? 'text-warning-600' : ''" class="text-sm">{{ credRefreshText(detail).text }}</span>
                  <SLink v-if="canRefreshCreds(detail)" as="button" class="ml-2 text-xs" :title="t('accounts.credRefresh.actionHint')" :disabled="refreshingCreds.has(detail.id)" @click="refreshCreds(detail)">{{ t('accounts.credRefresh.action') }}</SLink>
              </SDescItem>
              <SDescItem :label="t('accounts.usage.column')"><AccountUsageCell :account-id="detail.id" :usage="detail.usage" /></SDescItem>
              <SDescItem v-if="hasQuota(detail.quota)" :label="t('accounts.quota.column')"><AccountQuotaCell :quota="detail.quota" :refreshing="quotaRefresh.refreshing.has(detail.id)" @refresh="quotaRefresh.refresh(detail.id)" /></SDescItem>
              <SDescItem v-if="hasBalance(detail.balance)" :label="t('accounts.balance.accountLabel')"><AccountBalanceCell :balance="detail.balance" :refreshing="balanceRefresh.refreshing.has(detail.id)" @refresh="balanceRefresh.refresh(detail.id)" /></SDescItem>
              <SDescItem :label="t('accounts.models')">
                <span v-if="detail.models?.length" class="flex flex-wrap gap-1">
                  <SBadge v-for="m in detail.models" :key="m" tone="gray"><span class="font-mono">{{ m }}</span></SBadge>
                </span>
                <SHint v-else inline>{{ t('accounts.allModels') }}</SHint>
              </SDescItem>
              <SDescItem :label="t('accounts.modelMapping')">
                <ul v-if="detail.model_mapping && Object.keys(detail.model_mapping).length" class="space-y-0.5">
                  <li v-for="(to, from) in detail.model_mapping" :key="from" class="font-mono text-xs">
                    {{ from }} <SHint inline size="xs">→</SHint> {{ to }}
                  </li>
                </ul>
                <SHint v-else inline>—</SHint>
              </SDescItem>
              <SDescItem :label="t('accounts.schedulable')">{{ detail.schedulable ? t('common.yes') : t('common.no') }}</SDescItem>
              <SDescItem :label="t('accounts.lastUsed')">{{ formatDateTime(detail.last_used_at) }}</SDescItem>
              <SDescItem :label="t('accounts.lastTest.column')"><LastTestCell :last="detail.last_test" :testing="batchTesting.has(detail.id)" /></SDescItem>
              <SDescItem v-if="showOwner" :label="t('accounts.owner')"><UserRef :id="detail.owner_id" :email="detail.owner_email" /></SDescItem>
              <SDescItem v-if="showCreator" :label="t('accounts.createdBy')"><UserRef :id="detail.created_by" :email="detail.created_by_email" /></SDescItem>
              <SDescItem :label="t('common.createdAt')">{{ formatDateTime(detail.created_at) }}</SDescItem>
            </SDescriptions>
            <div v-if="detail.credentials">
              <SSectionTitle :title="t('accounts.credentials')" />
              <SCode>{{ JSON.stringify(detail.credentials, null, 2) }}</SCode>
            </div>
          </div>
          <component :is="detailSlot.component" v-else-if="detailSlot" :account="detail" />
        </STabs>
      </template>
    </SModal>
  </div>
</template>

<style scoped>
.account-table :deep(td) {
  white-space: nowrap;
}
/* Compact rows are a dozen columns wide: tighter side padding keeps them on screen. */
.account-table :deep(.table-dense td),
.account-table :deep(.table-dense th) {
  padding: 0.375rem 0.5rem;
}
</style>
