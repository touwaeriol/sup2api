<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SStatusBadge, SBadge, SButton, SDescItem, SDescriptions, SDropdown, SHint, SLink, SModal, SPageHeader, SPagination, STable, type TableColumn } from '@sub2api/ui'
import type { Group } from '@/api/types'
import { deleteWithConfirm, toggleStatus } from '@/composables/crud'
import { useList } from '@/composables/useList'
import { useGroupsLookup } from '@/composables/lookups'
import { useAuthStore } from '@/stores/auth'
import { formatNumber } from '@/utils/format'
import { policyLabel } from '@/utils/conversion'
import GroupAccessSummary from './GroupAccessSummary.vue'
import GroupAccountCount from './GroupAccountCount.vue'
import GroupDeleteModal from './GroupDeleteModal.vue'
import GroupEditModal from './GroupEditModal.vue'
import GroupPricingModal from './GroupPricingModal.vue'
import GroupPricingSummary from './GroupPricingSummary.vue'
import type { GroupEditTab } from './groupForm'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import { rateText } from '@/components/pricing/pricingFormat'
import { canSimulatePrices } from '@/components/pricing/quoteSubjects'
import GroupServedProtocols from './GroupServedProtocols.vue'

const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('group:manage'))
const canSimulate = computed(() => canSimulatePrices(auth))
const list = useList<Group>('/groups')

const columns = computed<TableColumn[]>(() => {
  const cols: TableColumn[] = [
    { key: 'name', label: t('common.name'), mobile: 'title' },
    { key: 'access', label: t('groups.whoCanUse'), mobile: 'meta', mobileSpan: 2 },
    { key: 'rate_multiplier', label: t('groups.rateMultiplier'), align: 'right', mobile: 'meta' },
    { key: 'pricing', label: t('groups.modelPricing'), mobile: 'meta' },
    { key: 'model_allowlist', label: t('groups.modelRules'), mobile: 'meta', mobileSpan: 2 },
    { key: 'account_count', label: t('groups.accountCount'), align: 'right', mobile: 'meta' },
    { key: 'key_count', label: t('groups.keyCount'), align: 'right' },
    { key: 'status', label: t('common.status'), mobile: 'status' }
  ]
  if (canManage.value) cols.push({ key: 'actions', label: t('common.actions'), align: 'right' })
  return cols
})

/** API key count; the server calls it api_key_count. */
function keyCount(g: Group): number | undefined {
  return g.key_count ?? g.api_key_count
}

// ------------------------------------------------------------------ detail

const detail = ref<Group | null>(null)
const detailOpen = ref(false)
// Conversion loss policy (CONVERT-MATRIX §6.3): null leaves it to the accounts, effectively allow.
const conversionText = (g: Group) => (g.conversion_loss_policy ? policyLabel(t, g.conversion_loss_policy) : t('groups.conversionDefault'))

async function openDetail(g: Group) {
  detail.value = g
  detailOpen.value = true
  try {
    const full = await api.get<Group>(`/groups/${g.id}`)
    if (full && detail.value?.id === g.id) detail.value = { ...g, ...full }
  } catch {
    /* keep the list row */
  }
}

/** Closes the detail and goes on with its group (to edit it, to simulate a price in it). */
function fromDetail(next: (g: Group) => void) {
  const g = detail.value
  detailOpen.value = false
  if (g) next(g)
}

function changed() {
  list.reload()
  useGroupsLookup(true)
}

// ------------------------------------------------------------------ create / edit

const open = ref(false)
const editing = ref<Group | null>(null)
/** The tab the editor opens on. */
const editTab = ref<GroupEditTab>('basic')

function openCreate() {
  openEdit(null)
}

function openEdit(g: Group | null, tab: GroupEditTab = 'basic') {
  editing.value = g
  editTab.value = tab
  open.value = true
}

// ------------------------------------------------------------------ pricing

/** The group whose model prices a caller who may not manage groups looks at. */
const viewing = ref<Group | null>(null)
const viewOpen = ref(false)

/** Opens the model prices of a group: in the editor, or read-only without group:manage. */
function openPricing(g: Group) {
  if (canManage.value) return openEdit(g, 'pricing')
  viewing.value = g
  viewOpen.value = true
}

// ------------------------------------------------------------------ row menu

/** The group being deleted while it still has API keys (they move or are unbound). */
const deleting = ref<Group | null>(null)
const deleteOpen = ref(false)
/** What the price simulator starts from: the group of the row it was opened on. */
const simPreset = ref<{ groupId: number }>()
const simOpen = ref(false)

function simulate(g: Group) {
  simPreset.value = { groupId: g.id }
  simOpen.value = true
}

async function onAction(listed: Group, key: string) {
  if (key === 'toggle') return toggleStatus(`/groups/${listed.id}`, listed.status, changed)
  if (key === 'simulate') return simulate(listed)
  if (key !== 'delete') return
  // The list may be stale: a key created since would be unbound by a plain
  // confirm that never mentions keys, so ask the group for its current count.
  const g = (await api.get<Group>(`/groups/${listed.id}`).catch(() => null)) ?? listed
  if (keyCount(g)) {
    deleting.value = g
    deleteOpen.value = true
    return
  }
  const extra = g.account_count ? ' ' + t('groups.deleteWithAccounts', { a: g.account_count }) : ''
  return deleteWithConfirm(`/groups/${g.id}`, `${t('common.confirmDelete', { name: g.name })}${extra} ${t('groups.deleteWithPricing')}`, changed)
}
</script>

<template>
  <div>
    <SPageHeader :title="t('groups.title')" :description="t('groups.description')">
      <template #actions>
        <SButton v-if="canManage" variant="primary" icon="plus" @click="openCreate">{{ t('groups.create') }}</SButton>
      </template>
    </SPageHeader>

    <STable card :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" @retry="list.reload">
      <template #cell-name="{ row }">
        <SLink as="button" class="text-left font-medium" data-testid="group-name" @click="openDetail(row)">{{ row.name }}</SLink>
        <SHint v-if="row.description" size="xs" class="max-w-xs truncate">{{ row.description }}</SHint>
      </template>
      <template #cell-access="{ row }"><GroupAccessSummary :group="row" /></template>
      <template #cell-rate_multiplier="{ row }">
        <span class="font-mono" data-testid="group-rate">{{ rateText(row.rate_multiplier) }}</span>
      </template>
      <template #cell-pricing="{ row }"><GroupPricingSummary :group="row" link @open="openPricing(row)" /></template>
      <template #cell-model_allowlist="{ row }">
        <SHint v-if="row.model_count !== undefined" size="xs" class="whitespace-nowrap" data-testid="group-model-count">{{ t('groups.modelCount', { n: row.model_count }, row.model_count) }}</SHint>
        <div v-if="row.model_allowlist && row.model_allowlist.length" class="flex max-w-xs flex-wrap items-center gap-1">
          <SBadge :tone="row.model_filter_mode === 'blacklist' ? 'danger' : 'primary'">{{ t(row.model_filter_mode === 'blacklist' ? 'groups.blacklist' : 'groups.whitelist') }}</SBadge>
          <ModelBadge v-for="m in row.model_allowlist.slice(0, 4)" :key="m" :model="m" />
          <SHint v-if="row.model_allowlist.length > 4" inline size="xs" :title="row.model_allowlist.join(', ')">
            +{{ row.model_allowlist.length - 4 }}
          </SHint>
        </div>
        <SHint v-else inline>{{ t('common.unlimited') }}</SHint>
      </template>
      <template #cell-account_count="{ row }"><GroupAccountCount :count="row.account_count" /></template>
      <template #cell-key_count="{ row }">{{ formatNumber(keyCount(row)) }}</template>
      <template #cell-status="{ row }">
        <SStatusBadge :status="row.status" />
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <SButton variant="ghost" size="sm" @click="openEdit(row)">{{ t('common.edit') }}</SButton>
          <SDropdown
            :actions="[
              { key: 'simulate', label: t('pricingTools.sim.title'), hidden: !canSimulate },
              { key: 'toggle', label: row.status === 'active' ? t('common.disable') : t('common.enable') },
              { key: 'delete', label: t('common.delete'), danger: true }
            ]"
            @select="onAction(row, $event)"
          />
        </div>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <GroupEditModal v-model:open="open" :group="editing" :initial-tab="editTab" @saved="changed" />
    <GroupPricingModal v-model:open="viewOpen" :group="viewing" />

    <GroupDeleteModal v-model:open="deleteOpen" :group="deleting" :key-count="deleting ? keyCount(deleting) ?? 0 : 0" @deleted="changed" />
    <PriceSimulatorModal v-if="canSimulate" v-model:open="simOpen" :preset="simPreset" />

    <!-- detail -->
    <SModal v-model:open="detailOpen" :title="detail?.name || ''" width="lg">
      <template v-if="detail">
        <SDescriptions data-testid="group-detail" dense>
          <SDescItem :label="t('common.id')" :value="detail.id" />
          <SDescItem v-if="detail.description" :label="t('common.description')" :value="detail.description" />
          <SDescItem :label="t('common.status')"><SStatusBadge :status="detail.status" /></SDescItem>
          <SDescItem :label="t('groups.visibility')">{{ detail.visibility === 'public' ? t('groups.public') : t('groups.restricted') }}</SDescItem>
          <SDescItem :label="t('groups.whoCanUse')"><GroupAccessSummary :group="detail" /></SDescItem>
          <SDescItem :label="t('groups.rateMultiplier')"><span class="font-mono">{{ rateText(detail.rate_multiplier) }}</span></SDescItem>
          <SDescItem :label="t('groups.modelPricing')"><GroupPricingSummary :group="detail" /></SDescItem>
          <SDescItem><template #label>{{ t('groups.modelRules') }} · {{ t(detail.model_filter_mode === 'blacklist' ? 'groups.blacklist' : 'groups.whitelist') }}</template>{{ detail.model_allowlist?.length ? detail.model_allowlist.join(', ') : t('common.unlimited') }}</SDescItem>
          <SDescItem :label="t('accounts.conversion.title')"><span data-testid="group-detail-conversion">{{ conversionText(detail) }}</span></SDescItem>
          <SDescItem :label="t('groups.accountCount')"><GroupAccountCount :count="detail.account_count" /></SDescItem>
          <SDescItem :label="t('groups.keyCount')">{{ formatNumber(keyCount(detail)) }}</SDescItem>
          <!-- From GET /groups/:id: absent on the list row the detail opens with. -->
          <SDescItem v-if="detail.served_protocols && detail.account_count !== 0" :label="t('platforms.endpoints')"><GroupServedProtocols :served="detail.served_protocols" /></SDescItem>
        </SDescriptions>
      </template>
      <template #footer>
        <SButton v-if="canSimulate && detail" data-testid="group-detail-simulate" @click="fromDetail(simulate)">{{ t('pricingTools.sim.title') }}</SButton>
        <SButton v-if="canManage && detail" @click="fromDetail(openEdit)">{{ t('common.edit') }}</SButton>
        <SButton v-else-if="detail" data-testid="group-detail-pricing" @click="openPricing(detail)">{{ t('groups.viewPricing') }}</SButton>
        <SButton variant="primary" @click="detailOpen = false">{{ t('common.close') }}</SButton>
      </template>
    </SModal>
  </div>
</template>
