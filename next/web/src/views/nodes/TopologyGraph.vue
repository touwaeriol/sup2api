<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, requestWithHeaders } from '@sub2api/host'
import { SBadge, SCard, SEmpty, SHint, SSegmented, SStatusBadge, useAutoRefresh, type SelectOption } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import { parseNodePlugin } from '@/api/admin'
import { fetchFirstPage } from '@/composables/lookups'
import type { NodeInfo } from '@/api/types'
import type { Release, ShellNode } from '@/api/observability'
import NodeTopology from './NodeTopology.vue'
import { displayPluginId } from '@/views/plugins/pluginId'
const { t } = useI18n(), auth = useAuthStore()
const view = ref<'chart' | 'topology'>('topology')
const viewOptions = computed<SelectOption[]>(() => (['chart', 'topology'] as const).map((mode) => ({ value: mode, label: t(`observe.${mode}View`) })))
const marker = useId().replace(/:/g, '')
const registered = ref<NodeInfo[]>([]), shells = ref<ShellNode[]>([]), releases = ref<Release[]>([])
const primary = ref(''), shellOK = ref(false), failed = ref(false), tick = ref(Date.now())
const offload = ref<{ enabled: boolean; cpu_threshold_percent: number } | null>(null)
const visit = ref<{ entry: string; core: string; boot: string } | null>(null)
let inflight = false, disposed = false
const fresh = (date?: string, seconds = 30) => !!date && tick.value - Date.parse(date) < seconds * 1000
async function load() {
  if (inflight) return
  inflight = true
  const results = await Promise.allSettled([
    auth.has('node:read') ? fetchFirstPage<NodeInfo>('/nodes') : Promise.resolve(null),
    auth.has('system:update:read') ? api.get<{ nodes: ShellNode[]; primary_node: string }>('/system/upgrades') : Promise.resolve(null),
    auth.has('system:update:read') ? api.get<{ releases: Release[] }>('/system/releases') : Promise.resolve(null),
    auth.has('settings:read') ? api.get<{ enabled: boolean; cpu_threshold_percent: number }>('/system/offload') : Promise.resolve(null),
    requestWithHeaders<{ core_node_id?: string; core_boot_id?: string }>('GET', '/system/version')
  ])
  if (!disposed) {
    tick.value = Date.now()
    const [n, s, r, o, identity] = results
    visit.value = identity.status === 'fulfilled' ? { entry: identity.value.headers.get('X-Sub2api-Entry-Node')?.trim() || '', core: identity.value.data?.core_node_id || '', boot: identity.value.data?.core_boot_id || '' } : null
    failed.value = n.status === 'rejected' || s.status === 'rejected'
    if (n.status === 'fulfilled') registered.value = n.value || []
    shellOK.value = s.status === 'fulfilled' && !!s.value
    if (s.status === 'fulfilled' && s.value) { shells.value = s.value.nodes || []; primary.value = s.value.primary_node }
    else if (s.status === 'fulfilled') { shells.value = []; primary.value = '' }
    if (r.status === 'fulfilled') releases.value = r.value?.releases || []
    offload.value = o.status === 'fulfilled' ? o.value : null
  }
  inflight = false
}
const cards = computed(() => {
  const ids = [...new Set([...registered.value.map(n => n.node_id), ...shells.value.map(n => n.node_id)])].sort()
  return ids.map((id, index) => {
    const shell = shells.value.find(n => n.node_id === id)
    const core = registered.value.filter(n => n.node_id === id && (!shell || n.boot_id === shell.core_boot_id)).sort((a,b) => Date.parse(b.last_heartbeat) - Date.parse(a.last_heartbeat))[0]
    const plugins = Object.entries(core?.plugins || {}).map(([key, raw]) => ({ key, ...parseNodePlugin(raw) })).sort((a,b) => a.key.localeCompare(b.key))
    return { id, index, shell, core, plugins, coreStale: !fresh(core?.last_heartbeat), stale: !fresh(shell?.last_seen || core?.last_heartbeat), version: releases.value.find(r => r.digest === shell?.release_digest)?.manifest.release_id || core?.host_version || shell?.release_digest.slice(0,12) || '—' }
  })
})
const height = computed(() => 370 + Math.max(1, ...cards.value.map(n => n.plugins.length)) * 128)
const edges = computed(() => {
  if (!shellOK.value || failed.value) return []
  const result: Array<{ from: number; to: number; kind: string }> = []
  for (const from of cards.value) {
    const n = from.shell
    if (!n || from.stale || !n.enabled) continue
    for (const to of cards.value) {
      const target = to.shell
      if (!target || from.id === to.id || !fresh(target.last_seen, 20)) continue
      if (n.mode === 'forward' && to.id === primary.value) result.push({ from: from.index, to: to.index, kind: 'forwarding' })
      if (n.offloading && n.mode === 'local' && n.ready && offload.value?.enabled && target.enabled && target.ready && !target.stopped && target.mode === 'local' && !target.offloading && target.core_boot_id && target.route_revision && target.release_digest === n.release_digest && target.cpu_percent != null && target.cpu_percent < offload.value.cpu_threshold_percent - 10) result.push({ from: from.index, to: to.index, kind: 'offload' })
    }
  }
  return result
})
function path(from: number, to: number) {
  const x = from * 340 + 170, y = to * 340 + 170, top = 16 + Math.abs(to-from) * 9
  return `M ${x} 100 C ${x} ${top}, ${y} ${top}, ${y} 100`
}
const graphEdges = computed(() => edges.value.flatMap(edge => {
  const from = cards.value.find(card => card.index === edge.from)?.id
  const to = cards.value.find(card => card.index === edge.to)?.id
  return from && to ? [{ from, to, kind: edge.kind }] : []
}))
useAutoRefresh(load, 5000)
onMounted(() => { void load() })
onBeforeUnmount(() => { disposed = true })
</script>
<template>
  <SCard :title="t('observe.nodeView')" class="mb-5">
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <SSegmented :model-value="view" :options="viewOptions" :aria-label="t('observe.nodeView')" data-testid="topology-view" @update:model-value="view = $event as 'chart' | 'topology'" />
      <SHint size="xs">{{ t(view === 'topology' ? 'observe.topologyHint' : 'observe.chartHint') }}</SHint>
    </div>
    <SHint v-if="failed" tone="warning">{{ t('observe.unavailable') }}</SHint>
    <SHint v-if="!shellOK && !shells.length">{{ t('observe.partial') }}</SHint>
    <div class="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-sm" :title="t('observe.visitHint')"><span class="font-medium text-cyan-700 dark:text-cyan-300">{{ t('observe.entrySummary', { node: visit?.entry || t('observe.unknown') }) }}</span><span class="text-fg-muted">{{ t('observe.responseSummary', { node: visit?.core || t('observe.unknown') }) }}</span></div>
    <NodeTopology v-if="cards.length && view === 'topology'" :cards="cards" :edges="graphEdges" :primary="primary" :uncertain="failed" :visit="visit" />
    <div v-else-if="cards.length" class="overflow-x-auto">
      <SHint>{{ t('observe.legend') }}</SHint>
      <svg :width="cards.length * 340" :height="height" role="img" :aria-label="t('observe.topology')">
        <defs><marker :id="marker" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" /></marker></defs>
        <g v-for="edge in edges" :key="`${edge.from}-${edge.to}-${edge.kind}`" class="text-primary-500">
          <path :d="path(edge.from,edge.to)" fill="none" stroke="currentColor" stroke-width="2" :stroke-dasharray="edge.kind === 'offload' ? '7 5' : undefined" :marker-end="`url(#${marker})`"><title>{{ cards[edge.from]?.id }} → {{ cards[edge.to]?.id }} · {{ t(`observe.${edge.kind}`) }}</title></path>
        </g>
        <foreignObject v-for="card in cards" :key="card.id" :x="card.index * 340 + 8" y="100" width="324" :height="height - 100">
          <div class="rounded-2xl border-2 p-3 text-sm" :class="card.stale ? 'border-red-300 bg-red-50 dark:border-red-800 dark:bg-red-950/30' : 'border-primary-200 bg-primary-50/40 dark:border-primary-800 dark:bg-dark-900'">
            <div class="flex flex-wrap items-center gap-2"><strong>{{ card.id }}</strong><SBadge v-if="card.id === primary" tone="accent">{{ t('upgrades.primary') }}</SBadge><SBadge v-if="card.stale" tone="danger">{{ t('nodes.stale') }}</SBadge></div>
            <div v-if="card.shell" class="my-2 space-y-1">
              <div>{{ t('observe.gateway') }} · {{ card.shell.mode }} · {{ t(!card.shell.enabled ? 'upgrades.disabled' : card.shell.ready ? 'upgrades.serving' : 'upgrades.waiting') }}</div>
              <div>{{ t('common.cpu') }} {{ card.shell.cpu_percent == null ? '—' : card.shell.cpu_percent.toFixed(1) + '%' }} <SBadge :tone="card.shell.offloading ? 'warning' : 'gray'">{{ t(card.shell.offloading ? 'observe.offloading' : 'observe.notOffloading') }}</SBadge></div>
              <SHint size="xs">{{ t('observe.boot') }}: {{ card.shell.shell_boot_id?.slice(0,8) || '—' }}</SHint>
            </div>
            <div class="mt-3 rounded-xl border border-sky-300 bg-white/80 p-3 dark:border-sky-800 dark:bg-dark-800">
              <strong>{{ t('observe.core') }} · {{ card.version }}</strong>
              <SBadge v-if="card.core && !fresh(card.core.last_heartbeat)" tone="warning">{{ t('nodes.stale') }}</SBadge>
              <div class="my-1 text-xs">{{ t(card.shell?.stopped ? 'observe.stopped' : card.core ? 'observe.running' : 'observe.unknown') }} · {{ (card.shell?.core_boot_id || card.core?.boot_id)?.slice(0,8) || '—' }}</div>
              <div class="mt-3 text-xs font-semibold text-fg-muted">{{ t('observe.plugins') }}</div>
              <div v-for="plugin in card.plugins" :key="plugin.key" class="mt-2 rounded-lg border p-2" :class="plugin.fallback ? 'border-amber-400 bg-amber-50 dark:border-amber-700 dark:bg-amber-950/30' : 'border-line'">
                <div class="flex items-center justify-between gap-1"><strong>{{ displayPluginId(plugin.key) }}</strong><SStatusBadge :status="plugin.state" /></div>
                <div class="font-mono text-xs">{{ plugin.serving || '—' }}<span v-if="plugin.standby"> → {{ plugin.standby }}</span></div>
                <SHint v-if="plugin.fallback" tone="warning" size="xs">{{ t('observe.fallback') }}: {{ plugin.fallback }}</SHint>
                <div class="text-xs text-fg-muted">{{ t('observe.instances') }} {{ plugin.instances?.length || 0 }} · {{ t('observe.restarts') }} {{ plugin.instances?.reduce((sum, i) => sum + i.restarts, 0) || 0 }}</div>
                <SHint v-if="plugin.error" tone="danger" size="xs" class="truncate" :title="plugin.error">{{ plugin.error }}</SHint>
              </div>
              <SHint v-if="!card.plugins.length">{{ t('observe.noReport') }}</SHint>
            </div>
          </div>
        </foreignObject>
      </svg>
    </div>
    <SEmpty v-else :text="t('observe.empty')" />
  </SCard>
</template>
