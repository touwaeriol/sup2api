<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import { SDateTime, useAutoRefresh, SAlert, SBadge, SButton, SCard, SCode, SEmpty, SField, SHint, SPageHeader, SSectionTitle, SSelect, STable, confirm, createItemId, type TableColumn, type Tone } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import { useUpdatesStore, type UpdateIssue } from '@/stores/updates'
import { notifyError } from '@/utils/errors'
import TopologyGraph from '../nodes/TopologyGraph.vue'
import StepLanes from './StepLanes.vue'
import { useIssueText, useUpgradeLabel, type UpgradeEvent, type UpgradeStep } from './upgrades'
import ReleaseHistory from '../plugins/parts/ReleaseHistory.vue'
import type { ShellNode as Node } from '@/api/observability'

interface Release { digest: string; manifest: { release_id: string; build_id: string; source_commit: string; created_at: string } }
interface Plan { id: string; release_digest: string; status: string; cursor: number; error?: string; nodes: string[]; created_at: string; steps?: UpgradeStep[] }
interface Preflight { release_digest: string; expected_revision: number; primary_node: string; nodes: string[]; blocker_codes: UpdateIssue[]; strategy: string }
const { t } = useI18n()
const label = useUpgradeLabel(), issueText = useIssueText()
const auth = useAuthStore(), route = useRoute(), router = useRouter()
const releases = ref<Release[]>([]), nodes = ref<Node[]>([]), plans = ref<Plan[]>([]), events = ref<UpgradeEvent[]>([])
const target = ref<string | number | boolean | null>(''), selected = ref<Plan | null>(null), preflight = ref<Preflight | null>(null)
const loading = ref(true), busy = ref(false), available = ref(false), primary = ref(''), revision = ref(0), stale = ref(false)
const selectedID = ref(typeof route.query.upgrade === 'string' ? route.query.upgrade : '')
let inflight = false, disposed = false
let requestKey = createItemId()
const canExecute = computed(() => auth.has('system:update:execute'))
const canRecover = computed(() => auth.has('system:update:recover'))
const options = computed(() => releases.value.map(r => ({ value: r.digest, label: r.manifest.release_id + ' · ' + r.digest.slice(0, 12) })))
const active = computed(() => plans.value.some(p => !['completed', 'cancelled', 'failed', 'superseded'].includes(p.status)))
const columns = computed<TableColumn[]>(() => [{ key: 'node_id', label: t('upgrades.node') }, { key: 'release_digest', label: t('upgrades.version') }, { key: 'mode', label: t('upgrades.mode') }, { key: 'ready', label: t('upgrades.ready') }, { key: 'cpu_percent', label: t('common.cpu') }, { key: 'offloading', label: t('observe.offloadState') }, { key: 'last_seen', label: t('upgrades.lastSeen') }, { key: 'error', label: t('upgrades.reason') }, ...(canRecover.value ? [{ key: 'manage', label: t('upgrades.manageNode') }] : [])])
const stepColumns = computed<TableColumn[]>(() => [{ key: 'node_id', label: t('upgrades.node') }, { key: 'action', label: t('upgrades.step') }, { key: 'status', label: t('upgrades.status') }, { key: 'error', label: t('upgrades.reason') }])
const readyToCreate = computed(() => canExecute.value && available.value && !stale.value && !active.value && !!preflight.value && preflight.value.blocker_codes.length === 0 && preflight.value.expected_revision === revision.value && preflight.value.release_digest === target.value)
const STATUS_TONE: Record<string, Tone> = { completed: 'success', done: 'success', ready: 'success', failed: 'danger', error: 'danger', paused: 'warning', pending: 'warning', running: 'warning' }
const tone = (status: string): Tone => STATUS_TONE[status] ?? 'gray'
function version(digest: string) { return releases.value.find(r => r.digest === digest)?.manifest.release_id || digest.slice(0, 12) || '—' }
watch(target, () => { preflight.value = null; requestKey = createItemId() })
// The update check compares the latest releases with the installed versions,
// which a plan changes: read it again once the last active plan ends.
const updates = useUpdatesStore()
watch(active, (now, before) => { if (before && !now && updates.result) void updates.check() })
// A plan opened from elsewhere (the version badge's one-click update) while this page is shown.
watch(() => route.query.upgrade, value => { if (typeof value === 'string' && value && value !== selectedID.value) { selectedID.value = value; void load() } })
async function load(manual = false) {
  if (inflight || disposed) return
  inflight = true
  try {
    const [rs, state] = await Promise.all([api.get<{ releases: Release[] }>('/system/releases'), api.get<{ upgrades: Plan[]; nodes: Node[]; revision: number; primary_node: string }>('/system/upgrades')])
    if (disposed) return
    releases.value = rs.releases || []; plans.value = state.upgrades || []; nodes.value = state.nodes || []; primary.value = state.primary_node; revision.value = state.revision
    available.value = true; stale.value = false
    if (preflight.value && preflight.value.expected_revision !== revision.value) preflight.value = null
    if (!selectedID.value && plans.value.length) selectedID.value = plans.value[0].id
    if (selectedID.value) await loadPlan(selectedID.value)
  } catch (e) { if (manual || (!stale.value && !active.value)) notifyError(e); stale.value = true } finally { inflight = false; loading.value = false }
}
async function loadPlan(id: string) {
  const [plan, log] = await Promise.all([api.get<Plan>(`/system/upgrades/${encodeURIComponent(id)}`), api.get<{ events: UpgradeEvent[] }>(`/system/upgrades/${encodeURIComponent(id)}/events`)])
  if (disposed || id !== selectedID.value) return
  selected.value = plan; events.value = log.events || []
}
async function selectPlan(id: string) { selectedID.value = id; await router.replace({ query: { ...route.query, upgrade: id } }); try { await loadPlan(id) } catch (e) { notifyError(e) } }
async function check() {
  busy.value = true; preflight.value = null
  try { preflight.value = await api.post<Preflight>('/system/upgrades/preflight', { release_digest: target.value }) } catch (e) { notifyError(e) } finally { busy.value = false }
}
async function create() {
  if (!readyToCreate.value || !preflight.value) return
  busy.value = true
  try {
    const plan = await api.post<Plan>('/system/upgrades', { release_digest: preflight.value.release_digest, expected_revision: preflight.value.expected_revision, idempotency_key: requestKey })
    preflight.value = null; requestKey = createItemId(); await selectPlan(plan.id); await load()
  } catch (e) { notifyError(e) } finally { busy.value = false }
}
async function action(kind: 'pause' | 'resume' | 'cancel' | 'rollback') {
  if (!selected.value) return
  // Rolling back or cancelling cannot be undone from this page; ask first.
  if ((kind === 'rollback' || kind === 'cancel') && !(await confirm({ title: t(`upgrades.${kind}`), message: t(kind === 'rollback' ? 'upgrades.confirmRollback' : 'upgrades.confirmCancel'), danger: true }))) return
  busy.value = true
  try {
    const plan = await api.post<Plan>(`/system/upgrades/${encodeURIComponent(selected.value.id)}/${kind}`, {})
    if (kind === 'rollback') await selectPlan(plan.id)
    await load()
  } catch (e) { notifyError(e) } finally { busy.value = false }
}
async function setNodeEnabled(node: Node, enabled: boolean) {
  if (!enabled && !(await confirm({ title: t('upgrades.disableNode'), message: t('upgrades.confirmDisableNode', { node: node.node_id }), danger: true }))) return
  busy.value = true
  try {
    await api.post(`/system/nodes/${encodeURIComponent(node.node_id)}/${enabled ? 'enable' : 'disable'}`, {})
    await load()
  } catch (e) { notifyError(e) } finally { busy.value = false }
}
useAutoRefresh(load, 5000)
onMounted(() => { void load() })
onBeforeUnmount(() => { disposed = true })
</script>

<template>
  <div class="space-y-5">
    <SPageHeader :title="t('upgrades.title')" :description="t('upgrades.description')"><template #actions><SButton :loading="loading" icon="refresh" @click="load(true)">{{ t('common.refresh') }}</SButton></template></SPageHeader>
    <SAlert v-if="stale" tone="warning" dense data-testid="upgrades-stale">{{ t(active ? 'observe.reconnect' : available ? 'upgrades.stale' : 'upgrades.unavailable') }}</SAlert>
    <TopologyGraph />
    <template v-if="available">
      <STable card :columns="columns" :rows="nodes" row-key="node_id" data-testid="upgrade-nodes">
        <template #toolbar>
          <SSectionTitle tag="h3" class="mb-0" :title="t('upgrades.nodes')" />
          <SHint inline class="ml-auto">{{ t('upgrades.primary') }}: <span class="font-mono">{{ primary || '—' }}</span></SHint>
        </template>
        <template #cell-release_digest="{ row }"><span :title="row.release_digest">{{ version(row.release_digest) }}</span></template>
        <template #cell-mode="{ row }">{{ label('modes', row.mode) }}</template>
        <template #cell-cpu_percent="{ row }">{{ row.cpu_percent == null ? '—' : row.cpu_percent.toFixed(1) + '%' }}</template>
        <template #cell-offloading="{ row }"><SBadge :tone="row.offloading ? 'warning' : 'gray'">{{ t(row.offloading ? 'observe.offloading' : 'observe.notOffloading') }}</SBadge></template>
        <template #cell-ready="{ row }"><SBadge :tone="row.ready ? 'success' : 'warning'">{{ t(!row.enabled ? 'upgrades.disabled' : row.ready ? 'upgrades.serving' : row.stopped ? 'upgrades.stopped' : 'upgrades.waiting') }}</SBadge></template>
        <template #cell-last_seen="{ row }"><SDateTime mode="absolute" :value="row.last_seen" /></template>
        <template #cell-manage="{ row }"><SButton size="sm" :disabled="busy || stale" @click="setNodeEnabled(row, !row.enabled)">{{ t(row.enabled ? 'upgrades.disableNode' : 'upgrades.enableNode') }}</SButton></template>
      </STable>
      <SCard v-if="canExecute" :title="t('upgrades.newPlan')">
        <div class="space-y-4">
          <SAlert v-if="preflight && preflight.strategy === 'rolling-v1' && !preflight.blocker_codes.length" tone="info" dense>{{ t('upgrades.rollingWindow') }}</SAlert>
          <SAlert v-else tone="warning" dense>{{ t('upgrades.maintenanceWindow') }}</SAlert>
          <SHint>{{ t('upgrades.independentPlugins') }}</SHint>
          <SField :label="t('upgrades.target')"><SSelect v-model="target" :options="options" :placeholder="t('upgrades.choose')" :disabled="busy || active" /></SField>
          <SHint v-if="!releases.length">{{ t('upgrades.noReleases') }}</SHint>
          <SHint v-if="active">{{ t('upgrades.activePlan') }}</SHint>
          <div class="flex gap-2"><SButton :disabled="!target || active || stale" :loading="busy" @click="check">{{ t('upgrades.preflight') }}</SButton><SButton variant="primary" :disabled="!readyToCreate" :loading="busy" @click="create">{{ t('upgrades.start') }}</SButton></div>
          <div v-if="preflight" class="space-y-2">
            <p class="text-sm">{{ t('upgrades.order') }}: {{ preflight.nodes.join(' → ') }}</p>
            <p class="text-sm text-fg-muted">{{ t(preflight.strategy === 'rolling-v1' ? 'upgrades.rollingSequence' : 'upgrades.sequence') }}</p>
            <SAlert v-if="!preflight.blocker_codes.length" tone="success" dense>{{ t('upgrades.preflightOK') }}</SAlert>
            <SAlert v-else tone="danger" dense data-testid="upgrade-blockers">
              <ul class="list-disc space-y-1 pl-5"><li v-for="(blocker, i) in preflight.blocker_codes" :key="i">{{ issueText(blocker) }}</li></ul>
            </SAlert>
          </div>
        </div>
      </SCard>
      <SCard :title="t('upgrades.history')">
        <div v-if="plans.length" class="mb-4 flex flex-wrap gap-2"><SButton v-for="plan in plans" :key="plan.id" size="sm" :variant="selectedID === plan.id ? 'primary' : 'secondary'" @click="selectPlan(plan.id)">{{ version(plan.release_digest) }} · {{ label('states', plan.status) }}</SButton></div>
        <SEmpty v-else :text="t('upgrades.noPlans')" />
        <div v-if="selected" class="space-y-4">
          <div class="flex flex-wrap items-center gap-3"><SBadge :tone="tone(selected.status)">{{ label('states', selected.status) }}</SBadge><SDateTime mode="absolute" :value="selected.created_at" class="text-sm" /><SCode inline :text="selected.id" /></div>
          <SHint v-if="selected.error" tone="warning">{{ selected.error }}</SHint>
          <StepLanes :steps="selected.steps || []" :events="events" :cursor="selected.cursor" />
          <SHint v-if="selected.status === 'paused'">{{ t('upgrades.recovery') }}</SHint>
          <div v-if="canRecover" class="flex gap-2">
            <SButton v-if="selected.status === 'running'" :loading="busy" :disabled="stale" @click="action('pause')">{{ t('upgrades.pause') }}</SButton>
            <SButton v-if="selected.status === 'paused'" :loading="busy" :disabled="stale" @click="action('resume')">{{ t('upgrades.resume') }}</SButton>
            <SButton v-if="selected.status === 'paused'" :loading="busy" :disabled="stale" @click="action('rollback')">{{ t('upgrades.rollback') }}</SButton>
            <SButton v-if="['running', 'paused'].includes(selected.status)" :loading="busy" :disabled="stale" @click="action('cancel')">{{ t('upgrades.cancel') }}</SButton>
          </div>
          <STable :columns="stepColumns" :rows="selected.steps || []" row-key="step_id"><template #cell-action="{ row }">{{ label('actions', row.action) }}</template><template #cell-status="{ row }"><SBadge :tone="tone(row.status)">{{ label('states', row.status) }}</SBadge></template></STable>
          <ol class="space-y-2 text-sm"><li v-for="event in events" :key="event.id" class="flex flex-wrap gap-2"><SDateTime mode="absolute" muted :value="event.created_at" /><span>{{ event.message }}</span></li></ol>
          <ReleaseHistory v-if="auth.has('plugin:read')" :since="selected.created_at" />
        </div>
      </SCard>
    </template>
  </div>
</template>
