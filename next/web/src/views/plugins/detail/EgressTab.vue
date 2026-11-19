<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SCard, SCode, SHint, SLink, SSegmented, SSettingRow, SSettingsGroup, STable, toast, type TableColumn } from '@sub2api/ui'
import { formatBytes, formatDateTime, formatNumber } from '@/utils/format'
import { notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import type { EgressDomain } from '@/api/types'
import { asArray, formatDuration, pick, type PluginDetail } from '../pluginUtil'
import { useOptionalT } from '@/composables/optionalText'

interface EgressSummary {
  host: string
  port?: number | null
  count: number
  bytes_in: number
  bytes_out: number
  results: Record<string, number>
  last_seen?: string | null
}

interface EgressLog {
  id?: number
  node_id?: string
  host: string
  port?: number
  started_at?: string
  duration_ms?: number | null
  bytes_in?: number | null
  bytes_out?: number | null
  /** "open" while the connection is alive (CONTRACTS §14.2). */
  result?: string
  error?: string
}

const props = defineProps<{ detail: PluginDetail }>()
const emit = defineEmits<{ (e: 'changed'): void }>()
const { t } = useI18n()
const tr = useOptionalT()
const auth = useAuthStore()

const canRead = computed(() => auth.has('plugin:egress:read'))
const canManage = computed(() => auth.has('plugin:manage'))
/** The administrator's domains for every plugin (settings, CONTRACTS §113) apply on top of this policy. */
const canReadNetworkAccess = computed(() => auth.has('settings:read'))

const policy = ref<string>(props.detail.egress_policy || 'allow_all')
const savingPolicy = ref(false)
watch(
  () => props.detail.egress_policy,
  v => (policy.value = v || 'allow_all')
)

const policyOptions = computed(() => [
  { value: 'allow_all', label: t('plugins.egress.policiesShort.allow_all') },
  { value: 'allowlist', label: t('plugins.egress.policiesShort.allowlist') }
])

/** Domains approved through the "net" grant (effective in allowlist mode). */
const allowedDomains = computed(() => {
  const g = (props.detail.grants || []).find(x => x.permission === 'net' && x.status !== 'revoked')
  return asArray<string>(pick(g?.scope, 'domains'))
})

const range = ref<'1h' | '24h' | '7d'>('24h')
const rangeOptions = computed(() => [
  { value: '1h', label: t('plugins.egress.last1h') },
  { value: '24h', label: t('plugins.egress.last24h') },
  { value: '7d', label: t('common.last7d') }
])

const loading = ref(false)
const summary = ref<EgressSummary[]>([])
const logs = ref<EgressLog[]>([])
/** Hosts the plugin connected to (null: not reported by the server). */
const domains = ref<EgressDomain[] | null>(null)

function aggregate(list: EgressLog[]): EgressSummary[] {
  const m = new Map<string, EgressSummary>()
  for (const l of list) {
    const k = `${l.host}:${l.port ?? ''}`
    let s = m.get(k)
    if (!s) {
      s = { host: l.host, port: l.port, count: 0, bytes_in: 0, bytes_out: 0, results: {}, last_seen: null }
      m.set(k, s)
    }
    s.count++
    s.bytes_in += Number(l.bytes_in || 0)
    s.bytes_out += Number(l.bytes_out || 0)
    const r = l.result || 'unknown'
    s.results[r] = (s.results[r] || 0) + 1
    if (l.started_at && (!s.last_seen || l.started_at > s.last_seen)) s.last_seen = l.started_at
  }
  return [...m.values()].sort((a, b) => b.count - a.count)
}

function normalizeSummary(x: Record<string, any>): EgressSummary {
  let results = pick<Record<string, number> | Array<{ result: string; count: number }>>(x, 'results', 'by_result') || {}
  if (Array.isArray(results)) results = Object.fromEntries(results.map(r => [r.result, r.count]))
  if (!Object.keys(results).length) {
    // Server shape: separate ok / denied / errors (/ open) counters.
    const r: Record<string, number> = {}
    for (const [k, name] of [
      ['ok', 'ok'],
      ['denied', 'denied'],
      ['errors', 'error'],
      ['open', 'open']
    ] as const) {
      const n = Number(x[k] ?? 0)
      if (n > 0) r[name] = n
    }
    results = r
  }
  return {
    host: String(pick(x, 'host', 'domain') ?? ''),
    port: pick<number>(x, 'port') ?? null,
    count: Number(pick(x, 'count', 'connections', 'total') ?? 0),
    bytes_in: Number(pick(x, 'bytes_in') ?? 0),
    bytes_out: Number(pick(x, 'bytes_out') ?? 0),
    results,
    last_seen: pick<string>(x, 'last_seen', 'last_at') ?? null
  }
}

const DAY_MS = 24 * 3600_000

/** `new` from the server, or first seen within the last 24 hours. */
function isNewDomain(d: EgressDomain): boolean {
  if (typeof d.new === 'boolean') return d.new
  const at = d.first_seen_at ? new Date(d.first_seen_at).getTime() : NaN
  return Number.isFinite(at) && Date.now() - at < DAY_MS
}

function normalizeDomain(x: Record<string, any>): EgressDomain {
  const d: EgressDomain = {
    host: String(pick(x, 'host', 'domain') ?? ''),
    first_seen_at: String(pick(x, 'first_seen_at', 'first_seen') ?? ''),
    last_seen_at: String(pick(x, 'last_seen_at', 'last_seen') ?? ''),
    connections: Number(pick(x, 'connections', 'count') ?? 0),
    new: x.new as boolean
  }
  d.new = isNewDomain(d)
  return d
}

async function load() {
  if (!canRead.value) return
  loading.value = true
  const hours = range.value === '1h' ? 1 : range.value === '24h' ? 24 : 24 * 7
  const to = new Date()
  const from = new Date(to.getTime() - hours * 3600_000)
  try {
    const r = await api.get<any>(`/plugins/${encodeURIComponent(props.detail.key)}/egress`, {
      from: from.toISOString(),
      to: to.toISOString()
    })
    if (Array.isArray(r)) {
      logs.value = r
      summary.value = aggregate(r)
      domains.value = null
    } else {
      logs.value = asArray<EgressLog>(pick(r, 'items', 'details', 'logs', 'detail'))
      const s = asArray<Record<string, any>>(pick(r, 'summary', 'by_domain'))
      summary.value = s.length ? s.map(normalizeSummary) : aggregate(logs.value)
      const d = pick<unknown>(r, 'domains')
      domains.value = Array.isArray(d)
        ? d
            .map(x => normalizeDomain(x as Record<string, any>))
            .sort((a, b) => Number(b.new) - Number(a.new) || b.last_seen_at.localeCompare(a.last_seen_at))
        : null
    }
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

async function savePolicy() {
  savingPolicy.value = true
  try {
    await api.put(`/plugins/${encodeURIComponent(props.detail.key)}/egress-policy`, { policy: policy.value })
    toast(t('common.saved'), 'success')
    emit('changed')
  } catch (e) {
    notifyError(e)
  } finally {
    savingPolicy.value = false
  }
}

function resultTone(r: string) {
  if (r === 'open') return 'info' as const
  return r === 'ok' || r === 'success'
    ? ('success' as const)
    : r === 'denied' || r === 'blocked'
      ? ('warning' as const)
      : ('danger' as const)
}

/** Localized result label; "open" = connection still alive. */
function resultLabel(r: string | undefined): string {
  if (!r) return '—'
  return tr(`plugins.egress.resultLabels.${r}`, undefined, r)
}

const domainColumns = computed<TableColumn[]>(() => [
  { key: 'host', label: t('plugins.egress.domain') },
  { key: 'first_seen_at', label: t('plugins.egress.firstSeen') },
  { key: 'last_seen_at', label: t('plugins.egress.lastSeen') },
  { key: 'connections', label: t('plugins.egress.connections'), align: 'right' }
])
const domainRows = computed(() => domains.value || [])
const newDomainCount = computed(() => domainRows.value.filter(d => d.new).length)

const summaryColumns = computed<TableColumn[]>(() => [
  { key: 'host', label: t('plugins.egress.destination') },
  { key: 'count', label: t('plugins.egress.connections'), align: 'right' },
  { key: 'bytes', label: t('plugins.egress.traffic'), align: 'right' },
  { key: 'results', label: t('plugins.egress.results') },
  { key: 'last_seen', label: t('plugins.egress.lastSeen') }
])

const logColumns = computed<TableColumn[]>(() => [
  { key: 'started_at', label: t('common.time') },
  { key: 'node_id', label: t('plugins.nodes.node') },
  { key: 'host', label: t('plugins.egress.destination') },
  { key: 'duration_ms', label: t('plugins.jobs.duration'), align: 'right' },
  { key: 'bytes', label: t('plugins.egress.traffic'), align: 'right' },
  { key: 'result', label: t('plugins.egress.result') }
])

const summaryRows = computed(() => summary.value.map(s => ({ ...s, _k: `${s.host}:${s.port ?? ''}` })))
const logRows = computed(() => logs.value.slice(0, 500).map((l, i) => ({ ...l, _k: l.id ?? i })))

watch(range, load)
onMounted(load)
</script>

<template>
  <div class="space-y-4">
    <SSettingsGroup :title="t('plugins.egress.policy')" data-testid="egress-policy">
      <template v-if="canManage" #actions>
        <SButton
          variant="primary"
          size="sm"
          :loading="savingPolicy"
          :disabled="policy === (detail.egress_policy || 'allow_all')"
          data-testid="egress-policy-save"
          @click="savePolicy"
        >
          {{ t('common.save') }}
        </SButton>
      </template>
      <SSettingRow :label="t('plugins.egress.policyMode')" :description="t(`plugins.egress.policyHint.${policy}`)">
        <SSegmented
          :model-value="policy"
          :options="policyOptions"
          :disabled="!canManage"
          size="sm"
          :aria-label="t('plugins.egress.policyMode')"
          data-testid="egress-policy-mode"
          @update:model-value="v => (policy = String(v))"
        />
      </SSettingRow>
      <SSettingRow :label="t('plugins.egress.approvedDomainsTitle')" :description="t('plugins.egress.approvedDomainsHint')" stacked dense>
        <template v-if="allowedDomains.length">
          <SCode v-for="d in allowedDomains" :key="d" inline :text="d" />
        </template>
        <SHint v-else inline>{{ t('common.none') }}</SHint>
      </SSettingRow>
      <SSettingRow v-if="canReadNetworkAccess" :label="t('pluginEgress.title')" :description="t('pluginEgress.egressTabHint')" dense data-testid="egress-network-access">
        <SLink to="/settings?tab=plugin-egress">{{ t('pluginEgress.egressTabLink') }}</SLink>
      </SSettingRow>
    </SSettingsGroup>

    <template v-if="canRead">
      <SCard :title="t('plugins.egress.byDomain')" :padded="false">
        <template #actions>
          <SSegmented
            :model-value="range"
            :options="rangeOptions"
            size="sm"
            :aria-label="t('plugins.egress.range')"
            @update:model-value="v => (range = v as '1h' | '24h' | '7d')"
          />
          <SButton size="sm" variant="ghost" :loading="loading" icon="refresh" @click="load">{{ t('common.refresh') }}</SButton>
        </template>
        <STable :columns="summaryColumns" :rows="summaryRows" :loading="loading" row-key="_k">
          <template #cell-host="{ row }">
            <span class="font-mono text-sm"
              >{{ row.host }}<SHint v-if="row.port" inline>:{{ row.port }}</SHint></span
            >
            <SBadge v-if="detail.egress_policy === 'allowlist' && !allowedDomains.includes(row.host)" tone="warning" class="ml-2">
              {{ t('plugins.egress.notAllowlisted') }}
            </SBadge>
          </template>
          <template #cell-count="{ row }">{{ formatNumber(row.count) }}</template>
          <template #cell-bytes="{ row }">
            <span class="whitespace-nowrap text-xs">↑{{ formatBytes(row.bytes_out) }} ↓{{ formatBytes(row.bytes_in) }}</span>
          </template>
          <template #cell-results="{ row }">
            <div class="flex flex-wrap gap-1">
              <SBadge v-for="(n, r) in row.results" :key="r" :tone="resultTone(String(r))"
                >{{ resultLabel(String(r)) }} × {{ formatNumber(n) }}</SBadge
              >
            </div>
          </template>
          <template #cell-last_seen="{ row }">
            <span class="whitespace-nowrap text-xs">{{ formatDateTime(row.last_seen) }}</span>
          </template>
        </STable>
      </SCard>

      <SCard
        v-if="domains"
        :title="t('plugins.egress.domains')"
        :subtitle="t('plugins.egress.domainsHint')"
        :padded="false"
        data-testid="egress-domains"
      >
        <template #actions>
          <SBadge v-if="newDomainCount" tone="warning">{{ t('plugins.egress.newDomainCount', { n: newDomainCount }) }}</SBadge>
        </template>
        <STable :columns="domainColumns" :rows="domainRows" :loading="loading" row-key="host" dense>
          <template #cell-host="{ row }">
            <span class="font-mono text-sm">{{ row.host }}</span>
            <SBadge v-if="row.new" tone="warning" class="ml-2" data-testid="egress-new-domain">{{ t('plugins.egress.newDomain') }}</SBadge>
            <SBadge v-if="detail.egress_policy === 'allowlist' && !allowedDomains.includes(row.host)" tone="gray" class="ml-1">
              {{ t('plugins.egress.notAllowlisted') }}
            </SBadge>
          </template>
          <template #cell-first_seen_at="{ row }"
            ><span class="whitespace-nowrap text-xs">{{ formatDateTime(row.first_seen_at) }}</span></template
          >
          <template #cell-last_seen_at="{ row }"
            ><span class="whitespace-nowrap text-xs">{{ formatDateTime(row.last_seen_at) }}</span></template
          >
          <template #cell-connections="{ row }">{{ formatNumber(row.connections) }}</template>
        </STable>
      </SCard>

      <SCard v-if="logRows.length" :title="t('plugins.egress.details')" :padded="false">
        <STable :columns="logColumns" :rows="logRows" row-key="_k" dense>
          <template #cell-started_at="{ row }"
            ><span class="whitespace-nowrap text-xs">{{ formatDateTime(row.started_at) }}</span></template
          >
          <template #cell-host="{ row }">
            <span class="font-mono text-xs"
              >{{ row.host }}<SHint v-if="row.port" inline size="xs">:{{ row.port }}</SHint></span
            >
          </template>
          <template #cell-duration_ms="{ row }">{{ row.result === 'open' ? '—' : formatDuration(row.duration_ms) }}</template>
          <template #cell-bytes="{ row }">
            <SHint v-if="row.result === 'open' && !row.bytes_in && !row.bytes_out" inline>—</SHint>
            <span v-else class="whitespace-nowrap text-xs"
              >↑{{ formatBytes(row.bytes_out ?? 0) }} ↓{{ formatBytes(row.bytes_in ?? 0) }}</span
            >
          </template>
          <template #cell-result="{ row }">
            <SBadge :tone="resultTone(String(row.result))" :title="row.error || undefined" :data-result="row.result">{{
              resultLabel(row.result)
            }}</SBadge>
          </template>
        </STable>
      </SCard>
    </template>
    <SHint v-else>{{ t('plugins.egress.noReadPermission') }}</SHint>
  </div>
</template>
