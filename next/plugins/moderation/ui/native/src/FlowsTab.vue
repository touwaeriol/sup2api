<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { isApiError } from '@sub2api/host'
import {
  SAlert,
  SBadge,
  SButton,
  SCard,
  SChipList,
  SDateTime,
  SDropdown,
  SFilterBar,
  SHint,
  SInput,
  SLink,
  SRowAction,
  SSwitch,
  STable,
  STableColumnSettings,
  STooltip,
  useTableColumns,
  useViewport,
  type MenuAction,
  type Tone,
  type TableColumn
} from '@sub2api/ui'
import EnabledBadge from './EnabledBadge.vue'
import FlowEditor from './FlowEditor.vue'
import FlowGroupsDialog from './FlowGroupsDialog.vue'
import FlowVersions from './FlowVersions.vue'
import FlowSimulate from './FlowSimulate.vue'
import ImportDialog from './ImportDialog.vue'
import { useCatalog } from './catalog'
import { enumLabel, joinList, ms, num, pct } from './format'
import { defaultGroupCandidate, idNumber } from './forms'
import { canManage, confirmDelete, deleteFlow, errorMessage, setFlowEnabled, toastError, useModHost } from './host'
import type { Flow, Group, Spec } from './types'
import type { SectionKey } from './nav'
import { exemptGroups, flowBindingsInactive, flowBound, groupCoverage, type GroupOwner } from './bindingOps'

// Flows (CONTRACTS §72.7): the list (one line a row: groups as chips, 24 h
// stats, edit / groups and a More menu) and the full-width canvas editor of
// one flow (?flow=<id> or ?flow=new). Flows, auditors and groups come from
// the shared catalog; flow changes refresh the flows. Manage users import a
// configuration (channels, auditors) here.
const host = useModHost()
const t = host.t
const { EntityPicker } = host.components
const manage = computed(() => canManage())
const emit = defineEmits<{ (e: 'go-tab', tab: SectionKey): void }>()
const { compact } = useViewport()

const catalog = useCatalog()
const rows = computed(() => catalog.state.flows)
const groups = computed(() => catalog.state.groups)
const loading = computed(() => catalog.state.loading)
const error = computed(() => (catalog.state.errors.flows ? errorMessage(catalog.state.errors.flows, t('flows.loadFailed')) : ''))
const refreshFlows = () => catalog.refresh(['flows', 'bindings'])
defineExpose({ reload: () => catalog.refresh() })

const groupLabel = (id: number) => (id === 0 ? t('flows.defaultGroup') : catalog.groupName(id))
const exempt = computed(() => exemptGroups(catalog.state.bindings))

/** Every group incl. the default; [] when the group list is not readable. */
const allGroups = computed<Group[]>(() => (groups.value.length ? [{ id: 0, name: t('flows.defaultGroup') }, ...groups.value] : []))
const owners = computed(() => {
  const m = new Map<number, GroupOwner>()
  for (const f of rows.value) for (const g of f.groups) m.set(g, { flowId: f.id, flowName: f.name })
  return m
})

function stagesSummary(s: Spec | null): string {
  if (!s?.stages?.length) return '—'
  return s.stages.map((st) => `${st.auditors.map(catalog.auditorName).join(' ∥ ')}${st.auditors.length > 1 ? ` (${enumLabel('combine', st.combine || 'any')}${st.combine === 'at_least' ? ' ' + st.n : ''})` : ''}`).join(' → ')
}
const groupChips = (f: Flow) => f.groups.map((id) => ({ key: String(id), label: groupLabel(id), title: groupLabel(id) }))

// ---- filters
const nameQuery = ref('')
/** The group picker's id ('' = every group). */
const groupFilter = ref('')
const defaultGroup = computed(() => defaultGroupCandidate(t('flows.defaultGroup')))
const filtered = computed(() => {
  const q = nameQuery.value.trim().toLowerCase()
  const group = idNumber(groupFilter.value)
  return rows.value.filter((f) => (!groupFilter.value || f.groups.includes(group)) && (!q || f.name.toLowerCase().includes(q)))
})
const filtersOn = computed(() => groupFilter.value !== '')
function resetFilters() {
  nameQuery.value = ''
  groupFilter.value = ''
}

const hasStats = computed(() => rows.value.some((f) => f.stats_24h))
const columns = computed<TableColumn[]>(() => {
  const c: TableColumn[] = [
    { key: 'name', label: t('flows.col.name'), nowrap: true, hideable: false, mobile: 'title' },
    { key: 'status', label: t('flows.col.status'), nowrap: true, mobile: 'status' },
    { key: 'groups', label: t('flows.col.groups'), nowrap: true, mobile: 'meta', mobileSpan: 2 },
    { key: 'stages', label: t('flows.col.stages'), grow: true, mobile: 'meta', mobileSpan: 2 }
  ]
  if (hasStats.value) {
    c.push(
      { key: 'audited', label: t('flows.col.audited'), align: 'right', nowrap: true, mobile: 'meta' },
      { key: 'rate', label: t('flows.col.suspiciousRate'), align: 'right', nowrap: true, mobile: 'meta' },
      { key: 'pre', label: t('flows.col.preAvg'), align: 'right', nowrap: true, defaultHidden: true, mobile: 'detail' }
    )
  }
  c.push(
    { key: 'published_at', label: t('flows.col.published'), nowrap: true, mobile: 'detail' },
    { key: 'actions', label: t('actions'), align: 'right', nowrap: true, hideable: false }
  )
  return c
})
const columnState = useTableColumns(columns, 'moderation-flows')

// group coverage under the table (new-api flows-table.tsx): short counts, the group names in a tooltip
const coverage = computed(() => {
  if (!groups.value.length) return null
  const c = groupCoverage(groups.value.map((g) => g.id), rows.value, exempt.value)
  const names = (ids: number[]) => ({ groups: joinList(ids.map(groupLabel)) })
  const notes: Array<{ key: string; n: number; tone: Tone; tip: string }> = [
    { key: 'unbound', n: c.unbound.length, tone: 'warning', tip: t('flows.unboundGroups', names(c.unbound)) },
    { key: 'stale', n: c.stale.length, tone: 'danger', tip: t('flows.staleGroups', names(c.stale)) },
    { key: 'inactive', n: c.inactive.length, tone: 'gray', tip: t(c.defaultLive ? 'flows.disabledGroups' : 'flows.disabledGroupsOff', names(c.inactive)) },
    { key: 'exempt', n: c.exempt.length, tone: 'gray', tip: t('flows.exemptGroups', names(c.exempt)) }
  ]
  return { summary: t('flows.groupsSummary', { total: groups.value.length, bound: c.own.length }), notes: notes.filter((x) => x.n > 0) }
})

// ---- dialogs: groups, versions, simulation, import
const dialogs = reactive({ groups: false, versions: false, simulate: false, import: false })
const target = ref<Flow | null>(null)
function openDialog(f: Flow, which: 'groups' | 'versions' | 'simulate') {
  target.value = f
  dialogs[which] = true
}

/** Import and New of a manage user: in the filter bar's end on wide screens, under it on phones. */
const createActions = computed(() => {
  if (!manage.value) return []
  const noAuditor = !catalog.state.auditors.length
  return [
    { key: 'import', label: t('import.button'), icon: 'upload', variant: 'secondary' as const, disabled: false, title: '', run: (): void => void (dialogs.import = true) },
    { key: 'add', label: t('flows.add'), icon: 'plus', variant: 'primary' as const, disabled: noAuditor, title: noAuditor ? t('flows.needAuditor') : '', run: (): void => openEditor(null) }
  ]
})

/** The More menu of a row; a manage user's only (a viewer sees View and Versions as buttons). */
const menu = (f: Flow): MenuAction[] => [
  { key: 'simulate', label: t('flows.simulate'), icon: 'play' },
  { key: 'versions', label: t('flows.versions'), icon: 'clock', hidden: !f.version },
  { key: 'accounts', label: t('flows.openBindings', { n: f.accounts?.length ?? 0 }), icon: 'link', hidden: !f.accounts?.length },
  { key: 'delete', label: t('delete'), icon: 'trash', danger: true, divider: true }
]
function onMenu(f: Flow, key: string) {
  if (key === 'simulate' || key === 'versions') openDialog(f, key)
  else if (key === 'accounts') emit('go-tab', 'bindings')
  else if (key === 'delete') remove(f)
}

/** Groups moderated now that no flow would moderate once this one is disabled (its own groups fall back to the default group's flow when that is live). */
function lostGroups(f: Flow): number[] {
  const ids = groups.value.length ? [0, ...groups.value.map((g) => g.id)] : f.groups
  const before = new Set(groupCoverage(ids, rows.value, exempt.value).moderated)
  const after = new Set(groupCoverage(ids, rows.value.map((x) => (x.id === f.id ? { ...x, enabled: false } : x)), exempt.value).moderated)
  return [...before].filter((id) => !after.has(id))
}

async function toggle(f: Flow, on: boolean) {
  if (!on) {
    const lost = lostGroups(f)
    // its bindings stop applying: the groups and accounts fall back to wider bindings
    const message = [t('flows.disableConfirm', { name: f.name }), flowBound(f) ? t('flows.disableFallsBack') : '', lost.length ? t('flows.disableLoses', { groups: joinList(lost.map(groupLabel)) }) : ''].filter(Boolean).join(' ')
    if (!(await host.confirm({ message, danger: lost.length > 0, confirmText: t('flows.disable') }))) return
  }
  try {
    await setFlowEnabled(f.id, on)
    await refreshFlows()
  } catch (e) {
    toastError(e)
  }
}

function remove(f: Flow) {
  // an enabled flow with bindings cannot be deleted: say why instead of asking first
  if (f.enabled && flowBound(f)) {
    host.toast(t('flows.deleteBlocked'), 'warning')
    return
  }
  void confirmDelete(
    t('flows.deleteConfirm', { name: f.name }),
    () => deleteFlow(f.id),
    () => void refreshFlows(),
    (e) => (isApiError(e) && e.code === 'flow_in_use' ? t('flows.deleteBlocked') : errorMessage(e, t('actionFailed')))
  )
}

// ---- editor (?flow=<id> | ?flow=new)
const route = host.router.currentRoute
const editor = ref<{ id: number | null; key: number } | null>(null)
let editorKey = 0

/** undefined = no editor, null = a new flow */
function parseFlow(v: unknown): number | null | undefined {
  if (v === 'new') return null
  const n = typeof v === 'string' ? Number(v) : NaN
  return Number.isSafeInteger(n) && n > 0 ? n : undefined
}

watch(
  () => route.value.query.flow,
  (v) => {
    const want = parseFlow(v)
    if (want === undefined) editor.value = null
    else if (!editor.value || editor.value.id !== want) editor.value = { id: want, key: ++editorKey }
  },
  { immediate: true }
)

function openEditor(id: number | null) {
  host.router.push({ query: { ...route.value.query, flow: id === null ? 'new' : String(id) } })
}

function closeEditor() {
  editor.value = null
  const { flow: _drop, ...query } = route.value.query
  host.router.replace({ query })
  void refreshFlows()
}

function onCreated(id: number) {
  if (editor.value) editor.value = { ...editor.value, id }
  host.router.replace({ query: { ...route.value.query, flow: String(id) } })
}
</script>

<template>
  <div>
    <FlowEditor v-if="editor" :key="editor.key" :flow-id="editor.id" :group-label="groupLabel" @close="closeEditor" @created="onCreated" @changed="refreshFlows" @manage="emit('go-tab', 'auditors')" />
    <template v-else>
      <SHint class="mod-hint">{{ t('flows.hint') }}</SHint>
      <SFilterBar :aria-label="t('filter')" :active-count="filtersOn ? 1 : 0" :dirty="filtersOn || !!nameQuery" :on-reset="resetFilters" class="mod-block">
        <template #primary>
          <SInput v-model="nameQuery" type="search" size="sm" icon="search" :placeholder="t('flows.searchName')" :aria-label="t('flows.searchName')" class="mod-flows-search" clearable />
        </template>
        <EntityPicker v-model:id="groupFilter" kind="group" size="sm" :extra="defaultGroup" :placeholder="t('flows.groupFilter')" class="mod-flows-group" />
        <template #end>
          <STableColumnSettings :state="columnState" />
          <template v-if="!compact">
            <SButton v-for="a in createActions" :key="a.key" size="sm" :variant="a.variant" :icon="a.icon" :disabled="a.disabled" :title="a.title" @click="a.run">{{ a.label }}</SButton>
          </template>
        </template>
      </SFilterBar>
      <div v-if="compact && createActions.length" class="mod-toolbar">
        <SButton v-for="a in createActions" :key="a.key" :variant="a.variant" :icon="a.icon" :disabled="a.disabled" :title="a.title" @click="a.run">{{ a.label }}</SButton>
      </div>
      <SAlert v-if="error" tone="danger" class="mod-block">{{ error }}</SAlert>

      <SCard :padded="false">
        <STable :columns="columnState.visibleColumns.value" :rows="filtered" :loading="loading" :empty-text="rows.length ? t('flows.noMatch') : t('flows.empty')" dense>
          <template #cell-name="{ row }">
            <span class="mod-cell-line">
              <SLink as="button" class="mod-ellipsis" :title="row.name" @click="openEditor(row.id)">{{ row.name }}</SLink>
              <span class="mod-sub mod-num">#{{ row.id }}</span>
            </span>
          </template>
          <template #cell-status="{ row }">
            <span class="mod-cell-line" :title="flowBindingsInactive(row) ? t('flows.bindingsInactiveHint') : undefined">
              <SSwitch v-if="manage" :model-value="row.enabled" size="sm" :aria-label="t('enabled')" @update:model-value="toggle(row, $event)" />
              <EnabledBadge v-else :enabled="row.enabled" size="xs" />
              <SBadge v-if="!row.version" tone="gray" size="xs">{{ t('flows.unpublished') }}</SBadge>
              <SBadge v-else-if="row.dirty" tone="warning" size="xs" :title="t('flows.badgeChanges')">v{{ row.version }} · {{ t('flows.changedShort') }}</SBadge>
              <SBadge v-else tone="info" size="xs">v{{ row.version }}</SBadge>
            </span>
          </template>
          <template #cell-groups="{ row }">
            <span class="mod-cell-line">
              <SChipList v-if="row.groups.length" :items="groupChips(row)" :max="2" />
              <SBadge v-else tone="warning" size="xs">{{ t('flows.noGroups') }}</SBadge>
              <span v-if="row.accounts?.length" class="mod-sub" :title="t('flows.accountBindings', { n: row.accounts.length })">{{ t('flows.accountsN', { n: row.accounts.length }) }}</span>
            </span>
          </template>
          <template #cell-stages="{ row }"><span :title="stagesSummary(row.draft)">{{ stagesSummary(row.published || row.draft) }}</span></template>
          <template #cell-audited="{ row }"><span class="mod-num">{{ row.stats_24h ? num(row.stats_24h.audited) : '—' }}</span></template>
          <template #cell-rate="{ row }">
            <span class="mod-num">{{ row.stats_24h?.audited ? pct(row.stats_24h.suspicious_rate) : '—' }}</span>
          </template>
          <template #cell-pre="{ row }">
            <span class="mod-num">{{ row.stats_24h?.pre_avg_ms ? ms(Math.round(row.stats_24h.pre_avg_ms)) : '—' }}</span>
          </template>
          <template #cell-published_at="{ row }"><SDateTime :value="row.published_at" /></template>
          <template #cell-actions="{ row }">
            <span class="mod-cell-line">
              <SRowAction :label="manage ? t('edit') : t('flows.view')" @click="openEditor(row.id)" />
              <template v-if="manage">
                <SRowAction :label="t('flows.groupsAction')" :title="t('flows.editGroups', { name: row.name })" @click="openDialog(row, 'groups')" />
                <SDropdown :actions="menu(row)" @select="onMenu(row, $event)" />
              </template>
              <SRowAction v-else-if="row.version" :label="t('flows.versions')" @click="openDialog(row, 'versions')" />
            </span>
          </template>
        </STable>
      </SCard>

      <div v-if="coverage" class="mod-flows-coverage">
        <span>{{ coverage.summary }}</span>
        <STooltip v-for="c in coverage.notes" :key="c.key" :content="c.tip">
          <SBadge :tone="c.tone" size="xs">{{ t(`flows.coverage.${c.key}`, { n: c.n }) }}</SBadge>
        </STooltip>
        <span class="mod-sub">{{ t('flows.statsNote') }}</span>
      </div>

      <FlowGroupsDialog v-model:open="dialogs.groups" :flow="target" :all-groups="allGroups" :owners="owners" :group-label="groupLabel" @saved="refreshFlows" />
      <FlowVersions v-model:open="dialogs.versions" :flow="target" @rolled-back="refreshFlows" />
      <FlowSimulate v-model:open="dialogs.simulate" :flow="target" />
      <ImportDialog v-model:open="dialogs.import" />
    </template>
  </div>
</template>
