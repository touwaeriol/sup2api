<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  SAlert,
  SBadge,
  SButton,
  SCard,
  SField,
  SFilterBar,
  SHint,
  SInput,
  SLink,
  SRowAction,
  SSelect,
  STable,
  STableColumnSettings,
  useTableColumns,
  useViewport,
  type TableColumn
} from '@sub2api/ui'
import BindingEditModal from './BindingEditModal.vue'
import { bindingKey, bindingKind, fallbackOf, hasAccountBindings, resolveSteps, sortBindings } from './bindingOps'
import { useCatalog } from './catalog'
import { joinList } from './format'
import { defaultGroupCandidate, groupAccountsFirst, idNumber } from './forms'
import { canManage, deleteBinding, errorMessage, fetchAccountNames, toastError, useModHost } from './host'
import type { Binding, BindingKind } from './types'
import type { SectionKey } from './nav'
import { useAsync } from './useAsync'

// Bindings of flows to groups and accounts (CONTRACTS §72.17): every
// binding, most specific first, edited in a dialog; a preview tells which
// binding a request of a group sent to an account goes by (both picked with
// the host's EntityPicker). The table names the bound accounts through the
// core's lookup, asked for those accounts only. Group bindings are the same
// ones the flows list edits. A binding to a disabled,
// unpublished or deleted flow is inactive (GET /bindings `inactive`): it
// does not apply, its row says what its requests go by instead, and the
// preview passes over it. Each table cell keeps to one line: the scope
// reads "account · in group", the fallback of an inactive binding is in the
// tooltip (on a phone card, under the flow).
const host = useModHost()
const t = host.t
const { EntityPicker } = host.components
const manage = computed(() => canManage())
const emit = defineEmits<{ (e: 'go-tab', tab: SectionKey): void }>()
const { compact } = useViewport()

const catalog = useCatalog()
const rows = computed(() => sortBindings(catalog.state.bindings).map((b) => ({ ...b, key: bindingKey(b) })))
/** The accounts the bindings name; their names are asked for again when a binding names another one. */
const boundAccounts = computed(() => [...new Set(rows.value.map((b) => b.account_id).filter((id) => id > 0))])
const accounts = useAsync(() => fetchAccountNames(boundAccounts.value), () => t('bindings.loadFailed'))
watch(() => boundAccounts.value.join(), accounts.reload, { immediate: true })
const reload = () => Promise.all([catalog.refresh(['bindings', 'flows', 'groups']), accounts.reload()])
defineExpose({ reload })

const error = computed(() => (catalog.state.errors.bindings ? errorMessage(catalog.state.errors.bindings, t('bindings.loadFailed')) : ''))
const inactive = computed(() => !catalog.state.accountBindingsActive && hasAccountBindings(rows.value))

const accountLabel = (id: number) => {
  const name = accounts.data.value?.get(id)
  return name ? `${name} #${id}` : `#${id}`
}
const groupLabel = (id: number) => (id === 0 ? t('flows.defaultGroup') : catalog.groupName(id))

// ---- filters
const kindFilter = ref<BindingKind | null>(null)
const query = ref('')
const kindOptions = computed(() => [
  { value: null, label: t('bindings.allKinds') },
  ...(['group_account', 'account', 'group'] as const).map((k) => ({ value: k, label: t(`bindings.kind.${k}`) }))
])
function resetFilters() {
  kindFilter.value = null
  query.value = ''
}
const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return rows.value.filter((b) => {
    if (kindFilter.value && bindingKind(b.group_id, b.account_id) !== kindFilter.value) return false
    if (!q) return true
    const text = [b.account_id ? accountLabel(b.account_id) : '', b.group_id || !b.account_id ? groupLabel(b.group_id) : '', flowText(b.flow_id)]
    return text.join(' ').toLowerCase().includes(q)
  })
})

const columns = computed<TableColumn[]>(() => {
  const c: TableColumn[] = [
    { key: 'scope', label: t('bindings.col.scope'), ellipsis: '24rem', hideable: false, mobile: 'title' },
    { key: 'kind', label: t('bindings.col.kind'), nowrap: true, mobile: 'status' },
    { key: 'flow', label: t('bindings.col.flow'), grow: true, mobile: 'meta', mobileSpan: 2 }
  ]
  if (manage.value) c.push({ key: 'actions', label: t('actions'), align: 'right', hideable: false })
  return c
})
const columnState = useTableColumns(columns, 'moderation-bindings')

const kindTone = (k: BindingKind) => (k === 'group_account' ? 'accent' : k === 'account' ? 'info' : 'gray')
/** What a binding leads to: its flow, or "not moderated" (flow 0). */
const flowText = (id: number) => (id ? catalog.flowName(id) : t('bindings.exempt'))
/** Why the flow of an inactive binding does not run. */
function flowState(id: number): string {
  const f = catalog.flow(id)
  return t(`bindings.flowState.${!f ? 'missing' : !f.enabled ? 'disabled' : 'unpublished'}`)
}
/** What the requests of an inactive binding go by now (the same lookup as the preview). */
function fallbackText(b: Binding): string {
  // an account bound in any group: each request falls back within its own group
  if (bindingKind(b.group_id, b.account_id) === 'account') return t('bindings.fallbackPerGroup')
  const next = fallbackOf(rows.value, b)
  return next ? t('bindings.fallbackBy', { scope: scopeText(next), target: flowText(next.flow_id) }) : t('bindings.fallbackNone')
}

async function openFlow(id: number) {
  await host.router.push({ query: { ...host.router.currentRoute.value.query, tab: 'flows', flow: String(id) } })
  emit('go-tab', 'flows')
}

// ---- edit
const editOpen = ref(false)
const editing = ref<Binding | null>(null)
function openEdit(b: Binding | null) {
  editing.value = b
  editOpen.value = true
}
const refreshBindings = () => catalog.refresh(['bindings', 'flows'])

async function remove(b: Binding) {
  const message = t('bindings.deleteConfirm', { scope: scopeText(b) })
  if (!(await host.confirm({ message, danger: true, confirmText: t('delete') }))) return
  try {
    await deleteBinding(b)
    host.toast(t('deleted'), 'success')
    await refreshBindings()
  } catch (e) {
    toastError(e)
  }
}

/** The second part of a scope: the group an account binding holds in, or whom a group binding covers. */
function scopeNote(b: Binding): string {
  if (b.account_id) return b.group_id ? t('bindings.inGroup', { group: groupLabel(b.group_id) }) : t('bindings.anyGroup')
  return b.group_id ? t('bindings.allAccounts') : t('bindings.defaultScope')
}
/** The tooltip of an inactive binding: why it does not apply and what its requests go by now. */
const inactiveText = (b: Binding) => `${t('bindings.rowInactive', { state: flowState(b.flow_id) })} · ${fallbackText(b)}`

function scopeText(b: Binding): string {
  switch (bindingKind(b.group_id, b.account_id)) {
    case 'group':
      return groupLabel(b.group_id)
    case 'account':
      return t('bindings.accountAnyGroup', { account: accountLabel(b.account_id) })
    default:
      return t('bindings.accountInGroup', { account: accountLabel(b.account_id), group: groupLabel(b.group_id) })
  }
}

// ---- preview: which binding a request goes by
const preview = ref<{ group: string; account: string }>({ group: '', account: '' })
// The default group (0) stands for a group without a binding of its own.
const defaultGroup = computed(() => defaultGroupCandidate(t('flows.defaultGroup')))
const previewResult = computed(() => {
  const g = idNumber(preview.value.group)
  if (Number.isNaN(g)) return null
  const a = idNumber(preview.value.account) || 0
  const { hit, skipped } = resolveSteps(rows.value, g, a)
  const note = skipped.length ? t('bindings.previewSkipped', { scopes: joinList(skipped.map(scopeText)) }) : ''
  if (!hit) return { text: t('bindings.previewNone'), note, tone: 'gray' as const }
  return { text: t('bindings.previewBy', { scope: scopeText(hit), target: flowText(hit.flow_id) }), note, tone: hit.flow_id ? ('success' as const) : ('warning' as const) }
})
</script>

<template>
  <div>
    <SHint class="mod-hint">{{ t('bindings.hint') }}</SHint>
    <SAlert v-if="inactive" tone="warning" class="mod-block">{{ t('bindings.inactive') }}</SAlert>
    <SAlert v-if="error" tone="danger" class="mod-block">{{ error }}</SAlert>

    <SFilterBar :aria-label="t('filter')" :active-count="kindFilter ? 1 : 0" :dirty="!!kindFilter || query.trim() !== ''" :on-reset="resetFilters">
      <template #primary>
        <SInput v-model="query" type="search" size="sm" icon="search" :placeholder="t('bindings.search')" :aria-label="t('bindings.search')" class="mod-bindings-search" clearable />
      </template>
      <SSelect
        :model-value="kindFilter"
        :options="kindOptions"
        size="sm"
        class="mod-filter-sm"
        :aria-label="t('bindings.col.kind')"
        @update:model-value="kindFilter = $event as BindingKind | null"
      />
      <template #end>
        <STableColumnSettings :state="columnState" />
        <SButton v-if="manage && !compact" variant="primary" size="sm" icon="plus" @click="openEdit(null)">{{ t('bindings.add') }}</SButton>
      </template>
    </SFilterBar>
    <SButton v-if="manage && compact" fab icon="plus" :aria-label="t('bindings.add')" @click="openEdit(null)" />

    <SCard :padded="false" class="mod-section">
      <STable
        :columns="columnState.visibleColumns.value"
        :rows="filtered"
        :loading="catalog.state.loading"
        row-key="key"
        :empty-text="rows.length ? t('bindings.noMatch') : t('bindings.empty')"
        dense
      >
        <template #cell-scope="{ row, layout }">
          <!-- one line in the table; a phone card title may wrap -->
          <span :class="layout === 'card' ? 'mod-inline mod-wrap mod-gap' : 'mod-cell-line'" :title="scopeText(row)">
            <span class="mod-ellipsis">{{ row.account_id ? accountLabel(row.account_id) : groupLabel(row.group_id) }}</span>
            <span class="mod-sub">{{ scopeNote(row) }}</span>
          </span>
        </template>
        <template #cell-kind="{ row }">
          <SBadge :tone="kindTone(row.kind)">{{ t(`bindings.kind.${row.kind}`) }}</SBadge>
        </template>
        <template #cell-flow="{ row, layout }">
          <SBadge v-if="!row.flow_id" tone="warning">{{ t('bindings.exempt') }}</SBadge>
          <span v-else class="mod-cell-line">
            <SLink as="button" class="mod-ellipsis" @click="openFlow(row.flow_id)">{{ flowText(row.flow_id) }}</SLink>
            <SBadge v-if="row.inactive" tone="gray" :title="inactiveText(row)">{{ t('bindings.rowInactive', { state: flowState(row.flow_id) }) }}</SBadge>
          </span>
          <div v-if="row.flow_id && row.inactive && layout === 'card'" class="mod-sub">{{ fallbackText(row) }}</div>
        </template>
        <template #cell-actions="{ row }">
          <span class="mod-row-actions">
            <SRowAction :label="t('edit')" @click="openEdit(row)" />
            <SRowAction :label="t('delete')" tone="danger" @click="remove(row)" />
          </span>
        </template>
      </STable>
    </SCard>

    <SCard :title="t('bindings.previewTitle')" class="mod-section">
      <div class="mod-bindings-preview">
        <SField :label="t('bindings.col.group')">
          <EntityPicker v-model:id="preview.group" kind="group" :extra="defaultGroup" />
        </SField>
        <SField :label="t('bindings.col.account')" :hint="t('bindings.previewNoAccount')">
          <EntityPicker v-model:id="preview.account" kind="account" :scope="groupAccountsFirst(preview.group)" />
        </SField>
      </div>
      <p v-if="previewResult" class="mod-bindings-result">
        <SBadge :tone="previewResult.tone" dot>{{ previewResult.text }}</SBadge>
        <span v-if="previewResult.note" class="mod-sub mod-ml">{{ previewResult.note }}</span>
      </p>
      <SHint v-else size="xs">{{ t('bindings.previewHint') }}</SHint>
    </SCard>

    <BindingEditModal v-model:open="editOpen" :binding="editing" @saved="refreshBindings" />
  </div>
</template>
