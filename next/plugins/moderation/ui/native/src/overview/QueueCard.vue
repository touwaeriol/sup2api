<script setup lang="ts">
import { computed } from 'vue'
import { SCard, SDescriptions, SGrid, SProgress, type DescItem } from '@sub2api/ui'
import { mib, ms, num } from '../format'
import { useModHost } from '../host'
import type { QueueStatus, Runtime } from '../types'

// Background queues and runtime counters of the node that answered (new-api
// queue-status): the post queue, the pricing queue and the record writer.
// The verdict cache's memory and its KV errors are listed when the backend
// reports them (0.8.0), and so are the runs waiting to write to the KV and
// the runs that were not cached (0.8.1), and the count of the LLM auditor
// calls whose images an upstream refused.
const props = defineProps<{ queue: QueueStatus[]; runtime: Runtime }>()
const t = useModHost().t

const rows = computed<DescItem[]>(() => {
  const r = props.runtime
  const cache: DescItem[] = []
  if (typeof r.cache_bytes === 'number') cache.push({ key: 'cacheBytes', label: t('runtime.cacheBytes'), value: mib(r.cache_bytes) })
  if (typeof r.kv_errors === 'number') cache.push({ key: 'kvErrors', label: t('runtime.kvErrors'), value: num(r.kv_errors) })
  if (typeof r.kv_writing === 'number') cache.push({ key: 'kvWriting', label: t('runtime.kvWriting'), value: num(r.kv_writing) })
  if (typeof r.kv_skipped === 'number') cache.push({ key: 'kvSkipped', label: t('runtime.kvSkipped'), value: num(r.kv_skipped) })
  if (typeof r.images_refused === 'number') cache.push({ key: 'imagesRefused', label: t('runtime.imagesRefused'), value: num(r.images_refused) })
  return [
    { key: 'flows', label: t('runtime.flows'), value: `${num(r.flows)} / ${num(r.auditors)}` },
    { key: 'inflight', label: t('runtime.inflight'), value: num(r.inflight) },
    { key: 'calls', label: t('runtime.calls'), value: `${num(r.calls)} (${t('runtime.errorsN', { n: num(r.errors) })})` },
    { key: 'cacheHits', label: t('runtime.cacheHits'), value: `${num(r.cache_hits)} (${num(r.cache_entries)})` },
    ...cache,
    { key: 'avgLatency', label: t('runtime.avgLatency'), value: ms(r.avg_latency_ms) }
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
