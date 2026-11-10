<script setup lang="ts">
import { computed } from 'vue'
import { SCard, SDescriptions, SGrid, SProgress, type DescItem } from '@sub2api/ui'
import { ms, num } from '../format'
import { useModHost } from '../host'
import type { QueueStatus, Runtime } from '../types'

// Background queues and runtime counters of the node that answered (new-api
// queue-status): the post queue, the pricing queue and the record writer.
const props = defineProps<{ queue: QueueStatus[]; runtime: Runtime }>()
const t = useModHost().t

const rows = computed<DescItem[]>(() => {
  const r = props.runtime
  const analysis = r.analysis
  return [
    { key: 'flows', label: t('runtime.flows'), value: `${num(r.flows)} / ${num(r.auditors)}` },
    { key: 'inflight', label: t('runtime.inflight'), value: num(r.inflight) },
    { key: 'calls', label: t('runtime.calls'), value: `${num(r.calls)} (${t('runtime.errorsN', { n: num(r.errors) })})` },
    { key: 'cacheHits', label: t('runtime.cacheHits'), value: `${num(r.cache_hits)} (${num(r.cache_entries)})` },
    { key: 'avgLatency', label: t('runtime.avgLatency'), value: ms(r.avg_latency_ms) },
    {
      key: 'analysis',
      label: t('runtime.analysis'),
      value: analysis?.channel_configured ? t('runtime.analysisValue', { pending: num(analysis.pending), running: num(analysis.running) }) : t('runtime.analysisOff')
    }
  ]
})
</script>

<template>
  <SCard :title="t('queue.title')" :subtitle="t('queue.hint')">
    <SGrid template="repeat(auto-fit, minmax(min(260px, 100%), 1fr))">
      <ul class="mod-queue-list">
        <li v-for="q in queue" :key="q.name">
          <SProgress v-if="q.capacity" :value="q.length" :max="q.capacity" :tone="q.length >= q.capacity ? 'danger' : 'info'">
            {{ t(`queue.name.${q.name}`) }} <span class="mod-num mod-sub">{{ num(q.length) }} / {{ num(q.capacity) }}</span>
          </SProgress>
          <span v-else>{{ t(`queue.name.${q.name}`) }}</span>
          <div class="mod-sub" :class="q.dropped ? 'mod-text-danger' : ''">{{ t('queue.dropped', { n: num(q.dropped) }) }}</div>
        </li>
      </ul>
      <SDescriptions :items="rows" label-width="9rem" dense class="mod-num" />
    </SGrid>
  </SCard>
</template>
