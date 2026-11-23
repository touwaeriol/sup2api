<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, listAll } from '@sub2api/host'
import { SAlert, SBulkBar, SButton, SCode, SCopyButton, SDateTime, SEmpty, SInput, SModal, SPageHeader, SPagination, SRowAction, SSelect, SStatusBadge, STable, toast, type TableColumn } from '@sub2api/ui'
import type { ApiKey, Group } from '@/api/types'
import { deleteWithConfirm } from '@/composables/crud'
import { activeFilterCount, useList } from '@/composables/useList'
import { fetchMyGroups } from '@/composables/lookups'
import { usePlatforms } from '@/composables/platforms'
import { useSelection } from '@/composables/useSelection'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import ApiKeyCell from './ApiKeyCell.vue'
import KeyEditor from './KeyEditor.vue'
import KeyGroupCell from './KeyGroupCell.vue'
import KeyGroupSwitch from './KeyGroupSwitch.vue'
import KeyLimitsCell from './KeyLimitsCell.vue'
import KeyListChip from './KeyListChip.vue'
import KeyModelsCell from './KeyModelsCell.vue'
import KeyQuotaCell from './KeyQuotaCell.vue'
import KeyUsageCell from './KeyUsageCell.vue'
import KeyUsageDrawer from './KeyUsageDrawer.vue'
import KeyUseGuide from './KeyUseGuide.vue'
import KeyUseMenu from './KeyUseMenu.vue'
import { useKeyBulk } from './keyBulk'
import { gatewayBase } from './keyClients'
import { groupOptions } from './keyForm'
import { KEY_STATE_FILTERS, KEY_STATE_TONES, KEY_STATES, isExpired, keyState } from './keyState'
import { myRate } from './myGroup'
import { useKeyUsage } from './keyUsage'
import { searchQuery } from './reveal'

const { t } = useI18n()
const list = useList<ApiKey>('/me/api-keys', { q: '', status: '', group_id: '' })
/** What the search box holds; the list is asked for searchQuery() of it. */
const search = ref('')
watch(search, (text) => (list.filters.q = searchQuery(text)))
const platforms = usePlatforms()
platforms.load()
const canReadUsage = useAuthStore().has('usage:self:read')
const usage = useKeyUsage()
if (canReadUsage) usage.load()
const base = gatewayBase()

const groups = ref<Group[]>([])
async function loadGroups() {
  try {
    groups.value = await fetchMyGroups()
  } catch (e) {
    notifyError(e)
  }
}
loadGroups()

const groupOf = (k: ApiKey) => (k.group_id == null ? undefined : groups.value.find((g) => g.id === k.group_id))
/** The platforms a key reaches, as reported on the key (none once its group was deleted), else those of its group. */
const platformsOf = (k: ApiKey) => k.platforms ?? groupOf(k)?.platforms

/** The key's group cell: name and group state as reported on the key; the owner's rate, the platforms and the exclusive mark of its group. */
function groupCell(k: ApiKey) {
  const g = groupOf(k)
  return {
    name: k.group_name || g?.name || (k.group_id == null ? '' : `#${k.group_id}`),
    multiplier: g && myRate(g),
    platforms: platformsOf(k),
    status: k.group_status,
    exclusive: g?.exclusive
  }
}
const moveOptions = computed(() => groupOptions(groups.value, platforms.label, t('pricing.exclusive')))

const stateLabels = computed(() => Object.fromEntries(KEY_STATES.map((s) => [s, t(`apikeys.state.${s}`)])))
const statusFilterOptions = computed(() => [{ value: '', label: t('apikeys.filter.allStatus') }, ...KEY_STATE_FILTERS.map((s) => ({ value: s, label: stateLabels.value[s] }))])
const groupFilterOptions = computed(() => [{ value: '', label: t('apikeys.filter.allGroups') }, ...groups.value.map((g) => ({ value: g.id, label: g.name }))])
const filterCount = computed(() => activeFilterCount(list.filters))
/** Whether the list is narrowed at all, the search box included (it is not a counted filter). */
const filtered = computed(() => activeFilterCount(list.filters, []) > 0)

const columns = computed(() =>
  ([
    { key: 'name', label: t('common.name'), mobile: 'title' },
    { key: 'status', label: t('common.status'), mobile: 'status' },
    { key: 'quota', label: t('apikeys.quota.title'), mobile: 'meta' },
    { key: 'group', label: t('common.group'), mobile: 'meta' },
    { key: 'key_prefix', label: t('apikeys.key'), mobile: 'subtitle' },
    { key: 'models', label: t('apikeys.models.title'), mobile: 'detail' },
    { key: 'ips', label: t('apikeys.ipLimit'), mobile: 'detail' },
    ...(canReadUsage ? [{ key: 'usage', label: t('apikeys.usage.title'), mobile: 'meta', mobileSpan: 2 } satisfies TableColumn] : []),
    { key: 'limits', label: t('apikeys.limits.title'), mobile: 'detail', defaultHidden: true },
    { key: 'created_at', label: t('common.createdAt'), mobile: 'detail', defaultHidden: true },
    { key: 'last_used_at', label: t('apikeys.lastUsed'), mobile: 'meta' },
    { key: 'expires_at', label: t('apikeys.expiresAt'), mobile: 'meta' },
    { key: 'actions', label: t('common.actions'), align: 'right' }
  ] satisfies TableColumn[]).map((c): TableColumn => ({ ...c, nowrap: true }))
)

// ------------------------------------------------------------------ selection and bulk actions

/** Every key the filters match, for "select all results". */
const loadMatching = () => listAll<ApiKey>(api, '/me/api-keys', { query: { ...list.filters } })
const selection = useSelection<ApiKey>({ items: list.items, key: (k) => k.id, selectable: () => true, loadAll: loadMatching })
const selectionKeys = computed({ get: () => selection.keys.value as unknown[], set: (v: unknown[]) => selection.update(v) })
// A changed filter starts a new selection: rows outside it would be acted on unseen.
watch(() => ({ ...list.filters }), () => selection.clear(), { deep: true })
/** Deleted keys leave the selection, which outlives the pages it was made on. */
const deselect = (keys: ApiKey[]) => selection.drop(keys.map((k) => k.id))
const bulk = useKeyBulk({ changed: list.reload, removed: deselect })
/** Under a status filter the keys switched on or off leave the list; left selected, the next action would reach them unseen. */
async function bulkSetStatus(status: 'active' | 'disabled') {
  const changed = await bulk.setStatus(selection.rows.value, status)
  if (changed.length && list.filters.status) selection.clear()
}

// ------------------------------------------------------------------ status and group

const updating = ref(new Set<number>())

/**
 * Puts a key as the server answered into the row the list holds of it now:
 * the row that was acted on may have been replaced by a reload since (a bulk
 * action finishing, the search), and what is written to the old one is never
 * seen. A key the page no longer shows is fetched with the list.
 */
function refresh(k: ApiKey) {
  const row = list.items.value.find((r) => r.id === k.id)
  if (row) Object.assign(row, k)
  else list.reload()
  return row
}

/** Applies a PATCH to the key's row in place (the answer is the updated key). */
async function update(k: ApiKey, body: { status?: string; group_id?: number }, done: string) {
  if (updating.value.has(k.id)) return
  updating.value = new Set(updating.value).add(k.id)
  try {
    refresh(await api.patch<ApiKey>(`/me/api-keys/${k.id}`, body))
    toast(done, 'success')
  } catch (e) {
    notifyError(e)
  } finally {
    const rest = new Set(updating.value)
    rest.delete(k.id)
    updating.value = rest
  }
}
const toggleEnabled = (k: ApiKey) => {
  const on = k.status !== 'active'
  return update(k, { status: on ? 'active' : 'disabled' }, t(on ? 'apikeys.enabledToast' : 'apikeys.disabledToast'))
}
const moveToGroup = (k: ApiKey, groupId: number) => update(k, { group_id: groupId }, t('apikeys.groupChanged'))

// ------------------------------------------------------------------ create, edit, use

/** The key whose usage guide is open. */
const useKey = ref<ApiKey | null>(null)
/** The key whose usage drawer is open. */
const usageKey = ref<ApiKey | null>(null)
const editorOpen = ref(false)
/** The key being edited; null while creating. */
const editKey = ref<ApiKey | null>(null)
/** A key shown once in full, just created or regenerated; an empty key closes the window. */
const issued = ref({ key: '', rotated: false })

function openEditor(k: ApiKey | null) {
  loadGroups()
  editKey.value = k
  editorOpen.value = true
}

/** The editor saved its form: an edited row is updated in place, a created key shows its plaintext. */
function saved({ key, ...k }: ApiKey) {
  if (editKey.value) {
    refresh(k)
    toast(t('common.saved'), 'success')
    return
  }
  list.reload()
  if (key) issued.value = { key, rotated: false }
  else toast(t('common.created'), 'success')
}

/** The editor regenerated the key being edited: its row takes the new prefix, the new plaintext shows. */
function rotated({ key = '', ...k }: ApiKey) {
  refresh(k)
  issued.value = { key, rotated: true }
}

/** The editor changed its key and stays open: it goes on with the key as it is now. */
const edited = (k: ApiKey) => (editKey.value = refresh(k) ?? k)

function remove(k: ApiKey) {
  return deleteWithConfirm(`/me/api-keys/${k.id}`, t('apikeys.confirmDelete', { name: k.name }), () => {
    deselect([k])
    return list.reload()
  })
}
</script>

<template>
  <div>
    <SPageHeader :title="t('nav.items.myApiKeys')" :description="t('apikeys.myDescription')" :filters-active-count="filterCount">
      <template #filters-primary>
        <SInput v-model="search" icon="search" clearable class="w-56" :aria-label="t('apikeys.filter.search')" :placeholder="t('apikeys.filter.search')" data-testid="key-search" />
      </template>
      <template #filters>
        <SSelect v-model="list.filters.status" :options="statusFilterOptions" class="w-32" :aria-label="t('common.status')" data-testid="key-status-filter" />
        <SSelect v-model="list.filters.group_id" :options="groupFilterOptions" class="w-36" :aria-label="t('common.group')" data-testid="key-group-filter" />
      </template>
      <template #actions>
        <SButton variant="primary" icon="plus" data-testid="create-key" @click="openEditor(null)">{{ t('apikeys.create') }}</SButton>
      </template>
    </SPageHeader>

    <SBulkBar
      class="mb-3"
      :count="selection.keys.value.length"
      :total="list.total.value"
      :page-selected="selection.pageSelected.value"
      :all-selected="selection.allSelected.value"
      :selecting-all="selection.selectingAll.value"
      :disabled="bulk.running.value"
      data-testid="key-bulk-bar"
      @select-page="selection.selectPage()"
      @select-all="selection.selectAll().catch(notifyError)"
      @clear="selection.clear()"
    >
      <SButton size="sm" icon="copy" :disabled="bulk.running.value" data-testid="bulk-copy-keys" @click="bulk.copy(selection.rows.value)">{{ t('apikeys.bulk.copy') }}</SButton>
      <SButton size="sm" :disabled="bulk.running.value" data-testid="bulk-enable-keys" @click="bulkSetStatus('active')">{{ t('common.enable') }}</SButton>
      <SButton size="sm" :disabled="bulk.running.value" data-testid="bulk-disable-keys" @click="bulkSetStatus('disabled')">{{ t('common.disable') }}</SButton>
      <SButton size="sm" variant="danger" :disabled="bulk.running.value" data-testid="bulk-delete-keys" @click="bulk.remove(selection.rows.value)">{{ t('common.delete') }}</SButton>
    </SBulkBar>

    <STable
      v-model:selected="selectionKeys"
      card
      dense
      selectable
      column-settings-key="my-api-keys"
      :columns="columns"
      :rows="list.items.value"
      :loading="list.loading.value"
      :error="list.error.value"
      @retry="list.reload"
    >
      <template #toolbar>
        <span class="inline-flex items-center gap-1.5 text-xs text-fg-muted" data-testid="api-base">
          {{ t('apikeys.use.base') }}
          <SCode inline :text="base" />
          <SCopyButton :text="base" icon-only size="xs" />
        </span>
      </template>
      <template #cell-name="{ row }"><span class="font-medium text-fg">{{ row.name }}</span></template>
      <template #cell-status="{ row }"><SStatusBadge :status="keyState(row)" :labels="stateLabels" :tones="KEY_STATE_TONES" /></template>
      <template #cell-quota="{ row }"><KeyQuotaCell :api-key="row" /></template>
      <template #cell-group="{ row, layout }">
        <KeyGroupSwitch :group-id="row.group_id" :options="moveOptions" :disabled="updating.has(row.id)" @change="moveToGroup(row, $event)">
          <KeyGroupCell v-bind="groupCell(row)" :class="{ 'flex-wrap': layout === 'card' }" />
        </KeyGroupSwitch>
      </template>
      <template #cell-key_prefix="{ row }"><ApiKeyCell :api-key="row" /></template>
      <template #cell-models="{ row }"><KeyModelsCell :group="groupOf(row)" /></template>
      <template #cell-ips="{ row }">
        <KeyListChip :items="row.allowed_ips" :empty-label="t('apikeys.unlimited')" :label="t('apikeys.ipAllowlistShort', row.allowed_ips?.length ?? 0)" data-testid="key-ip-restricted" />
      </template>
      <template #cell-usage="{ row }">
        <button type="button" class="-m-1 rounded-md p-1 text-left hover:bg-surface-2" :title="t('stats.key.open')" data-testid="open-key-usage" @click="usageKey = row">
          <KeyUsageCell :usage="usage.of(row.id)" />
        </button>
      </template>
      <template #cell-limits="{ row }"><KeyLimitsCell :api-key="row" /></template>
      <template #cell-created_at="{ row }"><SDateTime mode="absolute" muted :value="row.created_at" /></template>
      <template #cell-last_used_at="{ row }"><SDateTime muted :value="row.last_used_at" :empty="t('common.never')" /></template>
      <template #cell-expires_at="{ row }">
        <SDateTime mode="absolute" :value="row.expires_at" :empty="t('apikeys.noExpiry')" :muted="!isExpired(row)" :class="{ 'text-red-600 dark:text-red-400': isExpired(row) }" />
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-0.5">
          <KeyUseMenu :api-key="row" :platforms="platformsOf(row)" @guide="useKey = row" />
          <SRowAction :label="t(row.status === 'active' ? 'common.disable' : 'common.enable')" :loading="updating.has(row.id)" data-testid="toggle-key" @click="toggleEnabled(row)" />
          <SRowAction :label="t('common.edit')" data-testid="edit-key" @click="openEditor(row)" />
          <SRowAction :label="t('common.delete')" tone="danger" data-testid="delete-key" @click="remove(row)" />
        </div>
      </template>
      <template #empty>
        <SEmpty v-if="filtered" icon="search" :text="t('apikeys.emptyFiltered')" data-testid="keys-empty-filtered" />
        <SEmpty v-else-if="list.total.value === 0" icon="key" :title="t('apikeys.empty')" :text="t('apikeys.emptyHint')" data-testid="keys-empty">
          <template #actions><SButton variant="primary" icon="plus" @click="openEditor(null)">{{ t('apikeys.create') }}</SButton></template>
        </SEmpty>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <KeyEditor v-model:open="editorOpen" :api-key="editKey" :groups="groups" @saved="saved" @rotated="rotated" @updated="edited" />

    <KeyUseGuide :api-key="useKey" :platforms="useKey ? platformsOf(useKey) : null" :served="useKey ? groupOf(useKey)?.served_protocols : null" @close="useKey = null" />

    <KeyUsageDrawer v-if="canReadUsage && usageKey" :api-key="usageKey" @close="usageKey = null" />

    <SModal :open="!!issued.key" :title="t(issued.rotated ? 'apikeys.rotatedTitle' : 'apikeys.createdTitle')" persistent :closable="false" @update:open="!$event && (issued.key = '')">
      <div class="space-y-4">
        <SAlert tone="warning">{{ t('apikeys.onceWarning') }}</SAlert>
        <div class="flex items-center gap-2">
          <SCode class="flex-1 select-all text-sm">{{ issued.key }}</SCode>
          <SCopyButton :text="issued.key" variant="secondary" size="md" :label="t('common.copy')" data-testid="copy-plaintext" />
        </div>
      </div>
      <template #footer>
        <SButton variant="primary" data-testid="close-key-issued" @click="issued.key = ''">{{ t('apikeys.savedIt') }}</SButton>
      </template>
    </SModal>
  </div>
</template>
