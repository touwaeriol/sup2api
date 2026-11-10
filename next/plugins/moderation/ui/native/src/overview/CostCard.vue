<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SAlert, SBadge, SChart, SEmpty, SGrid, SHint, SSegmented, STable, type TableColumn } from '@sub2api/ui'
import AsyncCard from './AsyncCard.vue'
import { onOverviewTick } from './tick'
import { barChart, bucketLabel } from '../charts'
import { enumLabel, enumOptions, money, num as n } from '../format'
import { fetchCosts, useModHost } from '../host'
import { useAsync } from '../useAsync'

// Cost over a range (§72.13.3): GET /costs?range=, totals, by source, by
// channel, trend; unpriced calls are counted but not in the amount.
const t = useModHost().t
const range = ref<'24h' | '7d' | '30d'>('7d')
const rangeOptions = computed(() => enumOptions('cost.range', ['24h', '7d', '30d']))

const { data, loading, error, reload } = useAsync(() => fetchCosts(range.value), () => t('cost.loadFailed'))
watch(range, reload, { immediate: true })
onOverviewTick(reload)

const totals = computed(() => data.value?.totals)
const sources = computed(() => (data.value?.by_source || []).filter((s) => s.summary.calls > 0))
const channels = computed(() => (data.value?.by_channel || []).filter((c) => c.summary.calls > 0))

const channelColumns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('channels.col.name') },
  { key: 'calls', label: t('cost.callsCol'), align: 'right', nowrap: true },
  { key: 'cost', label: t('analysis.cost'), align: 'right', nowrap: true }
])

// Day buckets of /costs start at the browser's midnight (tz_offset_minutes).
const chart = computed(() => {
  const trend = data.value?.trend || []
  const labels = trend.map((p) => bucketLabel(p.bucket, range.value === '24h' ? 'hour' : 'day'))
  return barChart(labels, { name: t('analysis.cost'), data: trend.map((p) => Number(p.cost) || 0) }, money)
})
const hasTrend = computed(() => (data.value?.trend || []).some((p) => Number(p.cost) > 0))
</script>

<template>
  <AsyncCard :title="t('cost.title')" :subtitle="t('cost.hint')" :error="error" :loading="loading" :empty="!data" :empty-text="t('cost.empty')" empty-icon="price">
    <template #actions><SSegmented v-model="range" :options="rangeOptions" size="sm" /></template>
    <SAlert v-if="totals?.unpriced_calls" tone="warning" class="mod-block">
      {{ t('cost.unpriced', { n: n(totals.unpriced_calls), tokens: n(totals.unpriced_tokens) }) }}
    </SAlert>
    <SGrid template="repeat(auto-fit, minmax(min(260px, 100%), 1fr))">
      <div>
        <div class="mod-cost-total">
          <span class="mod-cost-amount mod-num">{{ totals ? money(totals.cost) : '—' }}</span>
          <SHint inline size="xs">{{ t('cost.calls', { n: n(totals?.calls) }) }}<template v-if="totals?.free_calls"> · {{ t('cost.free', { n: n(totals.free_calls) }) }}</template></SHint>
        </div>
        <SHint v-if="totals" size="xs" class="mod-num">{{ t('cost.tokens', { in: n(totals.tokens_in), out: n(totals.tokens_out) }) }}</SHint>
        <p class="mod-cost-label">{{ t('cost.bySource') }}</p>
        <SEmpty v-if="!sources.length" :text="t('cost.empty')" icon="chart" />
        <ul v-else class="mod-scores">
          <li v-for="s in sources" :key="s.source">
            <span>{{ enumLabel('costSource', s.source) }} <span class="mod-sub mod-num">{{ t('cost.calls', { n: n(s.summary.calls) }) }}</span></span>
            <span class="mod-num">{{ money(s.summary.cost) }}</span>
          </li>
        </ul>
      </div>
      <div>
        <p class="mod-cost-label mod-cost-label-top">{{ t('cost.byChannel') }}</p>
        <STable :columns="channelColumns" :rows="channels" row-key="channel_id" :empty-text="t('cost.empty')" dense>
          <template #cell-name="{ row }">
            {{ row.name || `#${row.channel_id}` }}
            <SBadge v-if="row.deleted" size="xs" tone="gray" class="mod-ml">{{ t('cost.deleted') }}</SBadge>
            <div class="mod-sub">{{ row.billing_model || row.model }}</div>
          </template>
          <template #cell-calls="{ row }">
            <span class="mod-num">{{ n(row.summary.calls) }}</span>
            <div v-if="row.summary.unpriced_calls" class="mod-sub mod-num">{{ t('channels.unpricedN', { n: n(row.summary.unpriced_calls) }) }}</div>
          </template>
          <template #cell-cost="{ row }"><span class="mod-num">{{ money(row.summary.cost) }}</span></template>
        </STable>
      </div>
    </SGrid>
    <div v-if="hasTrend" class="mod-section">
      <p class="mod-cost-label mod-cost-label-top">{{ t('cost.trend') }}</p>
      <SChart :option="chart" :loading="loading" height="160px" />
    </div>
  </AsyncCard>
</template>
