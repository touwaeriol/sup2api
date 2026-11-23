<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, MAX_ACCOUNT_BATCH } from '@sub2api/host'
import { SAutoRefresh, SBadge, SBulkBar, SButton, SChip, SDropdown, SHint, SIcon, SInput, SLink, SModal, SPageHeader, SPagination, SSelect, SSpinner, SSwitch, STable, STooltip, confirm, toast, useAutoRefresh, type TableColumn } from '@sub2api/ui'
import type { Account, AccountLastTest } from '@/api/types'
import { activeFilterCount, useList } from '@/composables/useList'
import { useGroupsLookup } from '@/composables/lookups'
import { ACCOUNT_ACTIONS } from '@/composables/useScope'
import { P } from '@/generated/permissions'
import { useAuthStore } from '@/stores/auth'
import { usePluginStore } from '@/stores/plugins'
import { notifyError } from '@/utils/errors'
import { formatRelative } from '@/utils/format'
import { useAccountTypes } from './accountTypes'
import AccountTypePicker from './AccountTypePicker.vue'
import EntityPicker from '@/components/EntityPicker.vue'
import UserRef from '@/components/entity/UserRef.vue'
import AccountGroupsCell from './AccountGroupsCell.vue'
import AccountProtocolMarks from './AccountProtocolMarks.vue'
import AccountEditor from './AccountEditor.vue'
import AccountNumberCell from './AccountNumberCell.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { forAccountType } from '@/components/plugin/slots'
import AccountLimitsCell from './AccountLimitsCell.vue'
import AccountSuccessRateCell from './AccountSuccessRateCell.vue'
import AccountUsageCell from './AccountUsageCell.vue'
import { todayQuery } from './accountUsage'
import { sameCreationGroup } from './accountTypeChoices'
import AccountQuotaCell from './AccountQuotaCell.vue'
import AccountBalanceCell from './AccountBalanceCell.vue'
import AccountModelTest from './AccountModelTest.vue'
import AccountDeleteDialog from './AccountDeleteDialog.vue'
import AccountDetailModal from './AccountDetailModal.vue'
import AccountOwnerDialog, { type AccountOwner } from './AccountOwnerDialog.vue'
import AccountRevealModal from './AccountRevealModal.vue'
import LastTestCell from './AccountLastTest.vue'
import { rowActions, runRowAction, type RowActionDeps } from './accountRowActions'
import { usePluginAccountActions } from './usePluginAccountActions'
import { accountEnabled, coolingDown, statusView } from './accountStatus'
import { useAccountAbilities } from './accountAbilities'
import { useAccountBulk } from './useAccountBulk'
import { useAccountEditorDialog } from './useAccountEditorDialog'
import { useBatchTest } from './useBatchTest'
import { useCredRefresh } from './useCredRefresh'
import { useEnabledToggle } from './useEnabledToggle'
import { useIdsFilter } from './useIdsFilter'
import { useAccountFilters } from './useAccountFilters'
import { useSelection } from '@/composables/useSelection'
import AccountBulkEditDialog from './AccountBulkEditDialog.vue'
import AccountBulkDeleteDialog from './AccountBulkDeleteDialog.vue'
import { hasBalance, hasQuota, resetAccountStatus, useBalanceRefresh, useQuotaRefresh } from './accountQuota'

const { t } = useI18n()
const auth = useAuthStore()
const plugins = usePluginStore()
const accountTypes = useAccountTypes()
accountTypes.load()
/** The account type of `a` (undefined until the types load or for an orphaned type). */
const typeOf = (a?: Pick<Account, 'plugin_key' | 'type'> | null) => (a ? accountTypes.find(a.plugin_key, a.type) : undefined)
const { groups } = useGroupsLookup()
const can = useAccountAbilities(typeOf)
const { canUpdate, canTest, canDelete, canResetStatus, canEnable, toggleable, editable, selectable } = can

// Scopes (CONTRACTS §21, §103.4): account:read sees every account, a narrower scope
// only the accounts in it (the server filters; row actions follow the same rule).
// The owner (所属) shows to a caller reaching more than its own accounts (every
// account, or its team's), the creator, shown only, to one reaching every account.
const readLevel = computed(() => can.scope.level(ACCOUNT_ACTIONS.read))
const showCreator = computed(() => readLevel.value === 'all')
const showOwner = computed(() => readLevel.value === 'all' || readLevel.value === 'team')
// One creator on the whole page says nothing per row: the column only shows when creators differ.
const mixedCreators = computed(() => new Set(list.items.value.map((a) => a.created_by)).size > 1)
const canCreate = computed(() => auth.hasAction(ACCOUNT_ACTIONS.create))

// `mine=true` (owned by the caller) and `owner_id=<id>` work at every level, `subordinates=true`
// (owned by the caller's team) with a team scope; `created_by=<id>` only at the all level (ignored otherwise).
// The usage column's "today" is the browser's day (CONTRACTS §73).
const list = useList<Account>(`/accounts?${new URLSearchParams(todayQuery())}`, {
  plugin_key: '', type: '', group_id: '', status: '', model: '', q: '', mine: '', owner_id: '', subordinates: '', created_by: '', orphaned: '', ids: ''
})
const { idsFilters, idsCommon, toolbarProps, clearIds } = useIdsFilter(list.filters)
const { onlyMine, onlySubordinates, showOrphaned, typeFilter, statusOptions: statusSelectOptions, typeOptions, advancedOpen: advancedFilters, filterCount, hasAdvanced: hasAdvancedFilters, reset: resetFilters } = useAccountFilters(list.filters, clearIds)

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

const statusOf = (a: Account) => statusView(t, a)

// ---------------------------------------------------------------- auto refresh (in_use / cooldown)
const refreshSeconds = ref(10)
useAutoRefresh(list.reload, () => refreshSeconds.value * 1000, { enabled: () => !editorOpen.value })

// ---------------------------------------------------------------- create / edit
const { open: editorOpen, step, pickedType, editing, loading: editorLoading, title: editorTitle, openCreate, pick, openEdit, onSaved } = useAccountEditorDialog(list.reload)

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

const { batchTesting, batchRunning, batchDone, batchTotal, batchTest, stopBatch } = useBatchTest({ tested: onTested })
const declaredActions = usePluginAccountActions(list.reload)
const { bulk, picked, more: moreBulkActions, onMore: onBulkMore } = useAccountBulk({ selected: selection.rows, can, patch: patchAccount, reload: list.reload, runDeclared: declaredActions.run })
const bulkBusy = computed(() => batchRunning.value || bulk.running.value || declaredActions.running.value)
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

// ---------------------------------------------------------------- detail
const detailOpen = ref(false)
const detail = ref<Account | null>(null)

async function openDetail(a: Account) {
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

// ---------------------------------------------------------------- quota / balance / credential refresh, reset status
// The snapshot of every row arrives with the list; "refresh" asks the upstream
// for one account (force), with duplicate in-flight clicks coalesced. The
// balance works the same (CONTRACTS §52).
const quotaRefresh = useQuotaRefresh({ onSnapshot: (id, quota) => patchAccount(id, { quota }), onError: (_id, e) => notifyError(e) })
const balanceRefresh = useBalanceRefresh({ onSnapshot: (id, balance) => patchAccount(id, { balance }), onError: (_id, e) => notifyError(e) })
const credRefresh = useCredRefresh(patchAccount)

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

// ---------------------------------------------------------------- enable / disable (actions column)
const enabledToggle = useEnabledToggle({ can, typeOf, patch: patchAccount })
const togglingEnabled = enabledToggle.toggling

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
const revealModal = ref<InstanceType<typeof AccountRevealModal>>()

const rowActionDeps: RowActionDeps = {
  canReveal: can.canReveal,
  canResetStatus,
  canDisable: can.canDisable,
  canRefreshCreds: can.canRefreshCreds,
  canUpdate,
  canDelete,
  canChangeOwner: () => auth.has(P.AccountOwnerUpdate),
  openDetail,
  reveal: (a) => revealModal.value?.reveal(a),
  resetStatus,
  disable: (a) => enabledToggle.toggle(a, false),
  refreshCreds: credRefresh.refresh,
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

const denseRows = ref(true)
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
        <SButton class="max-md:hidden" icon="refresh" :loading="list.loading.value" :title="t('accounts.listUi.refresh')" data-testid="account-refresh" @click="list.reload()">{{ t('common.refresh') }}</SButton>
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
            :title="enabledToggle.title(row)"
            data-testid="account-enable-toggle"
            @click="enabledToggle.toggle(row, !accountEnabled(row))"
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

    <AccountRevealModal ref="revealModal" />
    <AccountDetailModal
      v-model:open="detailOpen"
      :account="detail"
      :groups-text="detail ? groupNames(detail.group_ids, detail.groups) : ''"
      :show-owner="showOwner"
      :show-creator="showCreator"
      :testing="!!detail && batchTesting.has(detail.id)"
      :quota-refreshing="!!detail && quotaRefresh.refreshing.has(detail.id)"
      :balance-refreshing="!!detail && balanceRefresh.refreshing.has(detail.id)"
      :cred-refreshing="!!detail && credRefresh.refreshing.value.has(detail.id)"
      :can-reset-status="!!detail && canResetStatus(detail)"
      :can-refresh-creds="!!detail && can.canRefreshCreds(detail)"
      @reset-status="detail && resetStatus(detail)"
      @refresh-quota="detail && quotaRefresh.refresh(detail.id)"
      @refresh-balance="detail && balanceRefresh.refresh(detail.id)"
      @refresh-creds="detail && credRefresh.refresh(detail)"
    />
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
