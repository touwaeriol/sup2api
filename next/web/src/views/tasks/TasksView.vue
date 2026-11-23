<script setup lang="ts">
// System → Scheduled tasks (TASK-FRAMEWORK-DESIGN §10.3): an overview, the
// cluster tasks, a task × node matrix of the node tasks, and a drawer with
// the history of one task.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, isApiError } from '@sub2api/host'
import { SAutoRefresh, SBadge, SButton, SDateTime, SDropdown, SFilterBar, SGrid, SHint, SInput, SPageHeader, SSelect, SStatCard, SStatusBadge, SSwitch, STable, STabs, toast, useAutoRefresh, type TableColumn } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import NodeTaskCell from './NodeTaskCell.vue'
import RecentRuns from './RecentRuns.vue'
import TaskDrawer from './TaskDrawer.vue'
import { STATUSES, clusterStatus, describeSchedule, formatDuration, nodeTaskNames, runTone, sortClusterTasks, taskTitle, type ClusterTask, type NodeTask, type TasksOverview } from './tasks'

const REFRESH_CHOICES = [0, 10, 30, 60]

const { t, te, locale } = useI18n()
const auth = useAuthStore()
const route = useRoute(), router = useRouter()
const canRun = computed(() => auth.has('task:run'))
const canManage = computed(() => auth.has('task:manage'))

const overview = ref<TasksOverview | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const refreshSeconds = ref(10)
const tab = ref(route.query.tab === 'nodes' ? 'nodes' : 'cluster')
const focusNode = ref(typeof route.query.node === 'string' ? route.query.node : '')
const search = ref('')
const source = ref<string | number | boolean | null>('')
const state = ref<string | number | boolean | null>('')
const submitted = ref(new Set<string>())
const drawer = ref({ open: false, name: '', node: '' })
let inflight = false
let failures = 0

async function load(manual = false) {
  if (inflight) return
  inflight = true
  refreshing.value = manual
  try {
    overview.value = await api.get<TasksOverview>('/system/tasks')
    failures = 0
  } catch (e) {
    if (manual || failures === 0) notifyError(e)
    failures++
  } finally {
    inflight = false
    loading.value = false
    refreshing.value = false
  }
}
useAutoRefresh(load, () => refreshSeconds.value * 1000)
void load()

watch(tab, (v) => { void router.replace({ query: { ...route.query, tab: v === 'cluster' ? undefined : v } }) })

const tabs = computed(() => [
  { key: 'cluster', label: t('tasks.tabs.cluster'), badge: overview.value?.cluster.length },
  { key: 'nodes', label: t('tasks.tabs.nodes'), badge: overview.value ? nodeTaskNames(overview.value.nodes).length : undefined }
])
const summary = computed(() => overview.value?.summary)
const statusLabels = computed(() => Object.fromEntries(STATUSES.map((s) => [s, t(`tasks.status.${s}`)])))
const statusTones = computed(() => Object.fromEntries(STATUSES.map((s) => [s, runTone(s)])))
const title = (task: { name: string; title?: Record<string, string> }) => taskTitle(task, locale.value)

// ------------------------------------------------------------------ cluster

const sourceOptions = computed(() => [
  { value: '', label: t('tasks.filter.sourceAll') },
  { value: 'core', label: t('tasks.filter.sourceCore') },
  { value: 'plugin', label: t('tasks.filter.sourcePlugin') }
])
const stateOptions = computed(() => [
  { value: '', label: t('tasks.filter.stateAll') },
  { value: 'failing', label: t('tasks.filter.stateFailing') },
  { value: 'running', label: t('tasks.filter.stateRunning') }
])
const filtersDirty = computed(() => !!search.value || !!source.value || !!state.value)
function resetFilters() { search.value = ''; source.value = ''; state.value = '' }

function matches(name: string, task: { title?: Record<string, string> }) {
  const q = search.value.trim().toLowerCase()
  if (!q) return true
  return name.toLowerCase().includes(q) || Object.values(task.title || {}).some((v) => v.toLowerCase().includes(q))
}

const clusterRows = computed(() => sortClusterTasks(overview.value?.cluster || []).filter((task) => {
  if (!matches(task.name, task)) return false
  if (source.value === 'core' && task.source !== 'core') return false
  if (source.value === 'plugin' && task.source === 'core') return false
  if (state.value === 'failing' && task.consecutive_failures === 0) return false
  if (state.value === 'running' && !task.running) return false
  return true
}))

const clusterColumns = computed<TableColumn[]>(() => [
  { key: 'task', label: t('tasks.col.task') },
  { key: 'schedule', label: t('tasks.col.schedule'), nowrap: true },
  { key: 'status', label: t('tasks.col.status'), nowrap: true },
  { key: 'recent', label: t('tasks.col.recent') },
  { key: 'last', label: t('tasks.col.last'), nowrap: true },
  { key: 'next', label: t('tasks.col.next'), nowrap: true },
  { key: 'stats', label: t('tasks.col.stats'), nowrap: true },
  { key: 'failures', label: t('tasks.col.failures'), align: 'right' },
  ...(canRun.value || canManage.value ? [{ key: 'actions', label: t('tasks.col.actions'), align: 'right' as const }] : [])
])

// ------------------------------------------------------------------ nodes

interface MatrixRow { name: string; title?: Record<string, string>; schedule: string; cells: Record<string, NodeTask | undefined> }

const nodeColumns = computed(() => {
  const nodes = overview.value?.nodes || []
  return focusNode.value ? [...nodes].sort((a, b) => Number(b.node_id === focusNode.value) - Number(a.node_id === focusNode.value)) : nodes
})
const matrixRows = computed<MatrixRow[]>(() => {
  const nodes = overview.value?.nodes || []
  return nodeTaskNames(nodes).map((name) => {
    const cells: Record<string, NodeTask | undefined> = {}
    let first: NodeTask | undefined
    for (const n of nodes) {
      const task = n.tasks.find((x) => x.name === name)
      cells[n.node_id] = task
      first ||= task
    }
    return { name, title: first?.title, schedule: first?.schedule || '', cells }
  }).filter((row) => matches(row.name, row))
})
const matrixColumns = computed<TableColumn[]>(() => [
  { key: 'task', label: t('tasks.col.task') },
  { key: 'schedule', label: t('tasks.col.schedule'), nowrap: true },
  ...nodeColumns.value.map((n) => ({ key: `node:${n.node_id}`, label: n.node_id, nowrap: true, headerClass: n.alive ? '' : 'opacity-50' })),
  ...(canRun.value ? [{ key: 'actions', label: t('tasks.col.actions'), align: 'right' as const }] : [])
])
function runActions() {
  return [
    { key: '', label: t('tasks.runAll'), icon: 'play' },
    ...nodeColumns.value.filter((n) => n.alive).map((n) => ({ key: n.node_id, label: t('tasks.runOn', { node: n.node_id }) }))
  ]
}

// ------------------------------------------------------------------ actions

function taskError(e: unknown) {
  if (isApiError(e) && te(`tasks.errors.${e.code}`)) toast(t(`tasks.errors.${e.code}`), 'error')
  else notifyError(e)
}

async function runTask(name: string, nodeID = '') {
  const key = `${name}@${nodeID}`
  submitted.value = new Set(submitted.value).add(key)
  try {
    await api.post(`/system/tasks/${encodeURIComponent(name)}/run`, nodeID ? { node_id: nodeID } : {})
    toast(t('tasks.submitted'), 'success')
    setTimeout(() => void load(), 1500)
  } catch (e) {
    taskError(e)
  } finally {
    setTimeout(() => {
      const next = new Set(submitted.value)
      next.delete(key)
      submitted.value = next
    }, 3000)
  }
}

// Plugin cluster tasks can be switched off (task:manage); every node stops
// registering a stopped task.
const switching = ref('')
async function setEnabled(task: ClusterTask, enabled: boolean) {
  switching.value = task.name
  try {
    await api.patch(`/system/tasks/${encodeURIComponent(task.name)}`, { enabled })
    task.enabled = enabled
    toast(t(enabled ? 'tasks.switchedOn' : 'tasks.switchedOff', { name: title(task) }), 'success')
    setTimeout(() => void load(), 1500)
  } catch (e) {
    taskError(e)
  } finally {
    switching.value = ''
  }
}

function openTask(name: string, node = '') {
  drawer.value = { open: true, name, node }
}

</script>

<template>
  <div>
    <SPageHeader :title="t('tasks.title')" :description="t('tasks.description')">
      <template #actions>
        <SAutoRefresh v-model="refreshSeconds" :choices="REFRESH_CHOICES" button :loading="refreshing" @refresh="load(true)" />
      </template>
    </SPageHeader>

    <SGrid variant="stats" :cols="2" :md-cols="5" class="mb-4">
      <SStatCard :label="t('tasks.summary.total')" :value="summary?.total ?? 0" icon="clock" :loading="loading" />
      <SStatCard :label="t('tasks.summary.running')" :value="summary?.running ?? 0" icon="play" tone="info" :loading="loading" />
      <SStatCard :label="t('tasks.summary.failing')" :value="summary?.failing ?? 0" icon="warning" :tone="summary?.failing ? 'danger' : 'success'" :loading="loading" data-testid="tasks-failing" />
      <SStatCard :label="t('tasks.summary.failed24h')" :value="summary?.failed_24h ?? 0" icon="chart" :tone="summary?.failed_24h ? 'warning' : 'primary'" :loading="loading" />
      <SStatCard :label="t('tasks.summary.leader')" :value="summary?.leader || t('tasks.summary.noLeader')" icon="node" :loading="loading" />
    </SGrid>

    <SFilterBar class="mb-3" :dirty="filtersDirty" :on-reset="resetFilters">
      <SInput v-model="search" size="sm" icon="search" :placeholder="t('tasks.filter.search')" class="w-56" data-testid="tasks-search" />
      <template v-if="tab === 'cluster'">
        <SSelect v-model="source" :options="sourceOptions" size="sm" class="w-36" :aria-label="t('tasks.filter.source')" />
        <SSelect v-model="state" :options="stateOptions" size="sm" class="w-36" :aria-label="t('tasks.filter.state')" />
      </template>
    </SFilterBar>

    <STabs v-model="tab" :tabs="tabs">
      <STable v-if="tab === 'cluster'" card clickable :columns="clusterColumns" :rows="clusterRows" :loading="loading" row-key="name" :empty-text="t('tasks.noClusterTasks')" data-testid="tasks-cluster" @row-click="openTask($event.name)">
        <template #cell-task="{ row }">
          <div class="font-medium text-fg">{{ title(row) }}</div>
          <div class="flex items-center gap-1.5">
            <SHint size="xs" class="font-mono">{{ row.name }}</SHint>
            <SBadge v-if="row.plugin_key" tone="purple">{{ t('tasks.plugin') }} {{ row.plugin_key }}</SBadge>
            <SBadge v-if="!row.enabled" tone="gray">{{ t('tasks.disabled') }}</SBadge>
          </div>
        </template>
        <template #cell-schedule="{ row }">{{ describeSchedule(row.schedule, t) }}</template>
        <template #cell-status="{ row }">
          <SStatusBadge :status="clusterStatus(row)" :labels="statusLabels" :tones="statusTones" dot size="xs" />
          <SHint v-if="row.running && row.lease_node" inline size="xs" class="ml-1">{{ t('tasks.onNode', { node: row.lease_node }) }}</SHint>
        </template>
        <template #cell-recent="{ row }"><RecentRuns :statuses="row.recent" /></template>
        <template #cell-last="{ row }">
          <template v-if="row.last">
            <SDateTime :value="row.last.finished_at || row.last.started_at" mode="relative" />
            <SHint size="xs">{{ row.last.node }} · {{ formatDuration(row.last.duration_ms) }}</SHint>
          </template>
          <span v-else class="text-fg-muted">{{ t('tasks.empty') }}</span>
        </template>
        <template #cell-next="{ row }">
          <SDateTime v-if="row.next_run_at" :value="row.next_run_at" mode="relative" />
          <span v-else class="text-fg-muted">{{ t('tasks.empty') }}</span>
        </template>
        <template #cell-stats="{ row }">
          <div>{{ t('tasks.statsRuns', { runs: row.stats_24h.runs }) }}</div>
          <SHint size="xs">
            <span :class="row.stats_24h.failed ? 'text-danger-600 dark:text-danger-400' : ''">{{ t('tasks.statsFailed', { failed: row.stats_24h.failed }) }}</span>
            · {{ t('tasks.statsAvg', { avg: formatDuration(row.stats_24h.avg_ms) }) }}
          </SHint>
        </template>
        <template #cell-failures="{ row }">
          <span :class="row.consecutive_failures ? 'font-semibold text-danger-600 dark:text-danger-400' : 'text-fg-muted'" data-testid="task-failures">{{ row.consecutive_failures }}</span>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex items-center justify-end gap-2" @click.stop>
            <SSwitch
              v-if="canManage && row.source !== 'core'"
              :model-value="row.enabled"
              size="sm"
              :disabled="switching === row.name"
              :aria-label="t('tasks.enabledLabel')"
              :title="t('tasks.enabledLabel')"
              :data-testid="`task-enabled-${row.name}`"
              @update:model-value="setEnabled(row, $event)"
            />
            <SButton v-if="canRun" size="xs" variant="ghost" icon="play" :disabled="submitted.has(`${row.name}@`) || !row.registered" :data-testid="`task-run-${row.name}`" @click.stop="runTask(row.name)">
              {{ submitted.has(`${row.name}@`) ? t('tasks.submitted') : t('tasks.runNow') }}
            </SButton>
          </div>
        </template>
      </STable>

      <STable v-else card clickable :columns="matrixColumns" :rows="matrixRows" :loading="loading" row-key="name" :empty-text="t('tasks.noNodeTasks')" data-testid="tasks-nodes" @row-click="openTask($event.name, focusNode)">
        <template #cell-task="{ row }">
          <div class="font-medium text-fg">{{ title(row) }}</div>
          <SHint size="xs" class="font-mono">{{ row.name }}</SHint>
        </template>
        <template #cell-schedule="{ row }">{{ describeSchedule(row.schedule, t) }}</template>
        <template v-for="n in nodeColumns" :key="n.node_id" #[`cell-node:${n.node_id}`]="{ row }">
          <NodeTaskCell v-if="row.cells[n.node_id]" :task="row.cells[n.node_id]!" :alive="n.alive" :data-testid="`task-cell-${row.name}-${n.node_id}`" @open="openTask(row.name, n.node_id)" />
          <span v-else class="text-fg-muted" :class="n.alive ? '' : 'opacity-50'">{{ n.alive ? t('tasks.empty') : t('tasks.nodeOffline') }}</span>
        </template>
        <template #cell-actions="{ row }">
          <SDropdown :actions="runActions()" :label="t('tasks.runNow')" @select="runTask(row.name, $event)" @click.stop />
        </template>
      </STable>
    </STabs>

    <TaskDrawer v-model:open="drawer.open" :name="drawer.name" :node-i-d="drawer.node" :can-run="canRun" @run="runTask" />
  </div>
</template>
