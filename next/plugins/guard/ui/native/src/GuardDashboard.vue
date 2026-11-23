<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { SAlert, SButton, SCard, SChart, SEmpty, SGrid, SPageHeader, SProgress, SStatCard, STable, STimeRange, type RangeKey, type TableColumn } from '@sub2api/ui'
import RulesEditor from './RulesEditor.vue'
import { errorMessage, fetchStats, useGuardHost, type Stats } from './host'

// "Request guard" dashboard (wireframe A.13): blocking trend, top rules,
// recent blocks; rule management in a modal.
const host = useGuardHost()
const t = host.t

const range = ref<RangeKey>('today')
const stats = ref<Stats | null>(null)
const loading = ref(false)
const error = ref('')
const rulesOpen = ref(false)

const rangeKeys: RangeKey[] = ['today', '24h', '7d', '30d']

async function load() {
  loading.value = true
  error.value = ''
  try {
    stats.value = await fetchStats({ range: range.value })
  } catch (e) {
    error.value = errorMessage(e, t('loadFailed'))
  } finally {
    loading.value = false
  }
}

watch(range, load)
onMounted(load)

function label(ts: string, bucket: 'hour' | 'day') {
  const d = new Date(ts)
  const p = (x: number) => String(x).padStart(2, '0')
  return bucket === 'day' ? `${p(d.getMonth() + 1)}-${p(d.getDate())}` : `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:00`
}

const chart = computed(() => {
  const s = stats.value
  const trend = s?.trend || []
  return {
    tooltip: { trigger: 'axis' },
    legend: { data: [t('blocked'), t('requests')], top: 0 },
    grid: { left: 48, right: 48, top: 36, bottom: 32 },
    xAxis: { type: 'category', data: trend.map((p) => label(p.ts, s?.bucket || 'hour')) },
    yAxis: [
      { type: 'value', minInterval: 1 },
      { type: 'value', minInterval: 1, splitLine: { show: false } }
    ],
    series: [
      { name: t('blocked'), type: 'line', smooth: true, areaStyle: { opacity: 0.15 }, color: '#ef4444', data: trend.map((p) => p.blocked) },
      { name: t('requests'), type: 'line', smooth: true, yAxisIndex: 1, color: '#14b8a6', lineStyle: { type: 'dashed' }, data: trend.map((p) => p.total) }
    ]
  }
})

const rate = computed(() => {
  const s = stats.value
  if (!s || !s.requests_total) return '0%'
  return ((s.blocked_total / s.requests_total) * 100).toFixed(2) + '%'
})

const maxHits = computed(() => Math.max(1, ...(stats.value?.top_rules || []).map((r) => r.hits)))

const recentColumns = computed<TableColumn[]>(() => [
  { key: 'occurred_at', label: t('col.time') },
  { key: 'rule_name', label: t('col.rule') },
  { key: 'model', label: t('col.model') },
  { key: 'user_id', label: t('col.user') },
  { key: 'snippet', label: t('col.snippet'), ellipsis: true },
  { key: 'request_id', label: t('col.request') }
])
</script>

<template>
  <div>
    <SPageHeader :title="t('title')" :description="t('subtitle')">
      <template #filters-primary>
        <STimeRange v-model:range="range" from="" to="" :keys="rangeKeys" variant="segmented" size="sm" :label="false" />
      </template>
      <template #actions>
        <SButton icon="refresh" :loading="loading" @click="load">{{ t('refresh') }}</SButton>
        <SButton v-if="host.can('rules:read')" variant="primary" icon="shield" :fab="false" @click="rulesOpen = true">{{ t('manageRules') }}</SButton>
      </template>
    </SPageHeader>

    <SAlert v-if="error" tone="danger" class="guard-block">{{ error }}</SAlert>

    <SGrid template="repeat(auto-fit, minmax(220px, 1fr))">
      <SStatCard :label="t('blocked')" :value="host.i18n.formatNumber(stats?.blocked_total ?? 0)" icon="shield" tone="danger" :loading="loading && !stats" />
      <SStatCard :label="t('requests')" :value="host.i18n.formatNumber(stats?.requests_total ?? 0)" icon="usage" :loading="loading && !stats" />
      <SStatCard :label="t('blockRate')" :value="rate" icon="chart" tone="warning" :loading="loading && !stats" />
    </SGrid>

    <SCard :title="t('trend')" class="guard-section">
      <SChart :option="chart" :loading="loading" height="280px" />
    </SCard>

    <SGrid template="repeat(auto-fit, minmax(360px, 1fr))" class="guard-section">
      <SCard :title="t('topRules')">
        <SEmpty v-if="!stats?.top_rules?.length" :text="t('noBlocks')" icon="shield" />
        <ol v-else class="guard-top">
          <li v-for="(r, i) in stats.top_rules" :key="r.rule_id">
            <div class="guard-top-row">
              <span class="guard-rank">{{ i + 1 }}</span>
              <span class="guard-rule-name" :title="r.pattern">
                {{ r.name }}
                <code>{{ r.kind === 'regex' ? `/${r.pattern}/` : `"${r.pattern}"` }}</code>
              </span>
              <span class="guard-hits">{{ t('hits', { n: host.i18n.formatNumber(r.hits) }) }}</span>
            </div>
            <SProgress :value="r.hits" :max="maxHits" tone="danger" class="guard-bar" aria-hidden="true" />
          </li>
        </ol>
      </SCard>

      <SCard :title="t('recent')" :padded="false">
        <STable :columns="recentColumns" :rows="stats?.recent || []" row-key="request_id" dense>
          <template #cell-occurred_at="{ row }">{{ host.i18n.formatDateTime(row.occurred_at) }}</template>
          <template #cell-request_id="{ row }">
            <code class="guard-code" :title="row.request_id || undefined">{{ (row.request_id || '').slice(0, 10) || '—' }}</code>
          </template>
        </STable>
      </SCard>
    </SGrid>

    <RulesEditor v-model:open="rulesOpen" @saved="load" />
  </div>
</template>

<style scoped>
.guard-section {
  margin-top: 1.25rem;
}
.guard-block {
  margin-bottom: 1rem;
}
.guard-top {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin: 0;
  padding: 0;
  list-style: none;
}
.guard-top-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.875rem;
}
.guard-rank {
  width: 1.25rem;
  color: rgb(var(--fg-subtle));
  font-variant-numeric: tabular-nums;
}
.guard-rule-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.guard-rule-name code,
.guard-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.75rem;
  opacity: 0.7;
  margin-left: 0.25rem;
}
.guard-hits {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
.guard-bar {
  margin-top: 0.25rem;
  padding-left: 1.75rem;
}
</style>
