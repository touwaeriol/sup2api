<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAutoRefresh, SDateTime, statusTone, useAutoRefresh, SBadge, SButton, SChip, SCode, SGrid, SHint, SLink, SPageHeader, SStatCard, SStatusDot, STable, type TableColumn, type Tone, type StatusTone } from '@sub2api/ui'
import type { NodeInfo, PluginSandboxStatus } from '@/api/types'
import type { NodePluginState } from '@/api/admin'
import type { ShellNode } from '@/api/observability'
import { notifyError } from '@/utils/errors'
import { fetchFirstPage } from '@/composables/lookups'
import { useAuthStore } from '@/stores/auth'
import { nodeTaskCounts, type NodeTasks } from '../tasks/tasks'
import TopologyGraph from './TopologyGraph.vue'

const { t } = useI18n()

/** Heartbeats older than this are considered stale (node likely gone). */
const STALE_SECONDS = 30
const REFRESH_CHOICES = [0, 5, 10, 30, 60]

const nodes = ref<NodeInfo[]>([])
const auth = useAuthStore()
const canReadTasks = computed(() => auth.has('task:read'))
/** Node task states per node (System → Scheduled tasks), when readable. */
const nodeTasks = ref<NodeTasks[]>([])
const canReadGateways = computed(() => auth.has('system:update:read'))
/**
 * Gateway version per node id, from the gateways' heartbeats ('' = not
 * reported): undefined before the first answer, null when there was none (a
 * node the gateway does not manage answers 503), which hides the column and
 * asks again only on a manual refresh. A later failure keeps the last answer.
 */
const gatewayVersions = ref<Map<string, string> | null>()
const loading = ref(true)
const refreshSeconds = ref(5)
const tick = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
let inflight = false
let failures = 0

async function load(manual = false) {
  if (inflight) return
  inflight = true
  try {
    const items = await fetchFirstPage<NodeInfo>('/nodes')
    nodes.value = [...items].sort((a, b) => a.node_id.localeCompare(b.node_id))
    if (canReadTasks.value) {
      // The node list stays usable when the task overview cannot be read.
      nodeTasks.value = await api.get<{ nodes: NodeTasks[] }>('/system/tasks').then((o) => o.nodes || [], () => nodeTasks.value)
    }
    if (canReadGateways.value && (manual || gatewayVersions.value !== null)) {
      gatewayVersions.value = await api.get<{ nodes: ShellNode[] }>('/system/upgrades').then((o) => new Map((o.nodes || []).map((n) => [n.node_id, n.gateway_version || ''])), () => gatewayVersions.value ?? null)
    }
    failures = 0
  } catch (e) {
    // Only toast the first failure of a streak (or a manual refresh) to avoid a toast every 5s.
    if (manual || failures === 0) notifyError(e)
    failures++
  } finally {
    inflight = false
    loading.value = false
  }
}

useAutoRefresh(load, () => refreshSeconds.value * 1000)
onMounted(() => {
  load()
  clock = setInterval(() => (tick.value = Date.now()), 1000)
})
onBeforeUnmount(() => clearInterval(clock))

// ------------------------------------------------------------------ helpers

function ageSeconds(v: string | null | undefined): number {
  if (!v) return Infinity
  const ts = new Date(v).getTime()
  return Number.isNaN(ts) ? Infinity : (tick.value - ts) / 1000
}

function isStale(n: NodeInfo) {
  return ageSeconds(n.last_heartbeat) > STALE_SECONDS
}

function parsePlugin(v: string | Record<string, any> | null | undefined): NodePluginState {
  if (v === null || v === undefined) return {}
  if (typeof v === 'object') return v as NodePluginState
  const s = v.trim()
  if (s.startsWith('{')) {
    try {
      return JSON.parse(s) as NodePluginState
    } catch {
      /* fall through */
    }
  }
  // A bare string is treated as the state.
  return { state: s }
}

interface PluginBadge {
  key: string
  version?: string
  state: string
  tone: Tone
  detail: string
  fallback?: string
  standby?: string
}

function pluginBadges(n: NodeInfo): PluginBadge[] {
  return Object.entries(n.plugins || {})
    .map(([key, raw]) => {
      const p = parsePlugin(raw)
      const state = String(p.state || p.status || 'unknown')
      const detail = [
        `${key}${p.serving ? ' ' + p.serving : ''}`,
        p.standby ? `${t('observe.standby')}: ${p.standby}` : '',
        p.fallback ? `${t('observe.fallback')}: ${p.fallback}` : '',
        `${t('observe.instances')}: ${p.instances?.length || 0}; ${t('observe.restarts')}: ${p.instances?.reduce((sum, i) => sum + i.restarts, 0) || 0}`,
        `${t('nodes.state')}: ${state}`,
        p.error ? `${t('nodes.error')}: ${p.error}` : ''
      ]
        .filter(Boolean)
        .join('\n')
      return { key, version: p.serving, fallback: p.fallback, standby: p.standby, state, tone: p.fallback ? 'warning' as Tone : pluginTone(state), detail }
    })
    .sort((a, b) => a.key.localeCompare(b.key))
}

function pluginTone(state: string): Tone {
  const s = state.toLowerCase()
  if (s === 'running' || s === 'ready' || s === 'active' || s === 'serving' || s === 'healthy') return 'success'
  return statusTone(s)
}

function dotTone(tone: Tone): StatusTone {
  return tone === 'success' || tone === 'warning' || tone === 'danger' ? tone : 'neutral'
}

/** Plugin badges per node, built once per load. */
const badges = computed(() => new Map(nodes.value.map((n) => [n.boot_id, pluginBadges(n)])))

/** The sandbox cell: in force, off, or how many Landlock layers the kernel lacks. */
function sandboxBadge(sb: PluginSandboxStatus): { text: string; tone: Tone; detail: string } {
  const names = (ls: string[]) => ls.map((l) => t(`nodes.sandboxLayers.${l}`)).join(', ')
  const detail = [
    `Landlock ABI ${sb.landlock_abi}`,
    sb.active.length ? `${t('nodes.sandboxActive')}: ${names(sb.active)}` : '',
    sb.missing.length ? `${t('nodes.sandboxMissing')}: ${names(sb.missing)}` : ''
  ].filter(Boolean).join('\n')
  if (!sb.wrapped || !sb.landlock) return { text: t('nodes.sandboxOff'), tone: 'danger', detail }
  if (!sb.missing.length) return { text: t('nodes.sandboxAll'), tone: 'success', detail }
  return { text: t('nodes.sandboxPartial', { n: sb.missing.length }), tone: 'warning', detail }
}

// ------------------------------------------------------------------ table

const columns = computed<TableColumn[]>(() => [
  { key: 'node_id', label: t('nodes.node'), mobile: 'title' },
  { key: 'addr', label: t('nodes.addr'), mobile: 'subtitle' },
  { key: 'host_version', label: t('nodes.coreVersion'), mobile: 'meta' },
  ...(gatewayVersions.value ? [{ key: 'gateway_version', label: t('nodes.gatewayVersion'), mobile: 'meta' } satisfies TableColumn] : []),
  { key: 'plugin_sandbox', label: t('nodes.sandbox'), mobile: 'meta' },
  { key: 'started_at', label: t('nodes.uptime'), mobile: 'meta' },
  { key: 'last_heartbeat', label: t('nodes.heartbeat'), mobile: 'meta' },
  { key: 'plugins', label: t('nodes.plugins'), mobile: 'meta', mobileSpan: 2 },
  ...(canReadTasks.value ? [{ key: 'tasks', label: t('tasks.nodesColumn'), nowrap: true, mobile: 'meta' } satisfies TableColumn] : [])
])

const healthy = computed(() => nodes.value.filter((n) => !isStale(n)).length)
const pluginIssues = computed(() => [...badges.value.values()].flat().filter((p) => p.tone === 'danger' || p.tone === 'warning').length)
const versions = computed(() => new Set(nodes.value.map((n) => n.host_version)).size)
/** Distinct gateway versions; a gateway too old to report one is not a version. */
const gatewayVersionCount = computed(() => new Set([...(gatewayVersions.value?.values() || [])].filter(Boolean)).size)
const mixedVersions = computed(() => versions.value > 1 || gatewayVersionCount.value > 1)
const versionsText = computed(() => (gatewayVersions.value?.size ? t('nodes.versionCounts', { core: versions.value, gateway: gatewayVersionCount.value }) : versions.value))
</script>

<template>
  <div>
    <SPageHeader :title="t('nodes.title')" :description="t('nodes.description')">
      <template #actions>
        <SAutoRefresh v-model="refreshSeconds" :choices="REFRESH_CHOICES" @refresh="load(true)" />
        <SButton class="max-md:hidden" icon="refresh" :loading="loading" @click="load(true)">{{ t('common.refresh') }}</SButton>
      </template>
    </SPageHeader>

    <SGrid variant="stats" :md-cols="3" class="mb-4">
      <SStatCard :label="t('nodes.online')" :value="`${healthy}/${nodes.length}`" icon="node" :tone="healthy < nodes.length ? 'warning' : 'success'" :loading="loading" />
      <SStatCard :label="t('nodes.pluginIssues')" :value="pluginIssues" icon="plugin" :tone="pluginIssues ? 'danger' : 'primary'" :loading="loading" />
      <SStatCard :label="t('nodes.hostVersions')" :value="versionsText" icon="cpu" :tone="mixedVersions ? 'warning' : 'primary'" :sub="mixedVersions ? t('nodes.mixedVersions') : undefined" :loading="loading" />
    </SGrid>

    <TopologyGraph />
    <STable card :columns="columns" :rows="nodes" :loading="loading" row-key="boot_id">
      <template #cell-node_id="{ row }">
        <div class="flex items-center gap-2">
          <SStatusDot :tone="isStale(row) ? 'danger' : 'success'" :label="isStale(row) ? t('nodes.stale') : t('nodes.alive')" sr-only />
          <span class="font-medium text-fg">{{ row.node_id }}</span>
        </div>
        <SHint size="xs" class="pl-4 font-mono" :title="row.boot_id">{{ t('observe.boot') }} {{ String(row.boot_id || '').slice(0, 8) }}</SHint>
      </template>
      <template #cell-addr="{ row }">
        <SCode inline :text="row.addr" />
      </template>
      <template #cell-host_version="{ row }">
        <SBadge tone="gray">{{ row.host_version }}</SBadge>
      </template>
      <template #cell-gateway_version="{ row }">
        <SBadge v-if="gatewayVersions?.get(row.node_id)" tone="gray" data-testid="node-gateway-version">{{ gatewayVersions.get(row.node_id) }}</SBadge>
        <SHint v-else inline data-testid="node-gateway-version">{{ t('nodes.unknownVersion') }}</SHint>
      </template>
      <template #cell-plugin_sandbox="{ row }">
        <SBadge v-if="row.plugin_sandbox" :tone="sandboxBadge(row.plugin_sandbox).tone" :title="sandboxBadge(row.plugin_sandbox).detail" data-testid="node-sandbox">
          {{ sandboxBadge(row.plugin_sandbox).text }}
        </SBadge>
        <SHint v-else inline data-testid="node-sandbox">—</SHint>
      </template>
      <template #cell-started_at="{ row }">
        <SDateTime muted :value="row.started_at" mode="relative" live />
      </template>
      <template #cell-last_heartbeat="{ row }">
        <SDateTime :value="row.last_heartbeat" mode="relative" live :class="isStale(row) ? 'font-medium text-danger-600 dark:text-danger-400' : 'text-fg'" />
        <SBadge v-if="isStale(row)" tone="danger" class="ml-2">{{ t('nodes.stale') }}</SBadge>
      </template>
      <template #cell-plugins="{ row }">
        <div class="flex flex-wrap gap-1.5">
          <SChip v-for="p in badges.get(row.boot_id)" :key="p.key" :title="p.detail" data-testid="node-plugin">
            <span class="inline-flex items-center gap-1.5">
              <SStatusDot :tone="dotTone(p.tone)" :label="p.state" sr-only />
              <span>{{ p.key }}</span>
              <span v-if="p.version" class="font-mono text-fg-muted">{{ p.version }}</span>
              <span v-if="p.standby">→ {{ p.standby }}</span>
              <SBadge v-if="p.fallback" tone="warning">{{ t('observe.fallback') }}</SBadge>
            </span>
          </SChip>
          <SHint v-if="!badges.get(row.boot_id)?.length" inline>—</SHint>
        </div>
      </template>
      <template #cell-tasks="{ row }">
        <SLink v-if="nodeTaskCounts(nodeTasks, row.node_id)" :to="{ path: '/system/tasks', query: { tab: 'nodes', node: row.node_id } }" data-testid="node-tasks">
          <span :class="nodeTaskCounts(nodeTasks, row.node_id)!.failing ? 'text-danger-600 dark:text-danger-400' : ''">
            {{ t('tasks.nodesCount', nodeTaskCounts(nodeTasks, row.node_id)!) }}
          </span>
        </SLink>
        <SHint v-else inline>—</SHint>
      </template>
    </STable>
  </div>
</template>
