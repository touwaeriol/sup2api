<script setup lang="ts">
import { computed, watch } from 'vue'
import { SAlert, SBadge, SBulkBar, SButton, SDateTime, STable, type TableColumn } from '@sub2api/ui'
import CategoryChips from './CategoryChips.vue'
import FlowRef from './FlowRef.vue'
import ResultBadge from './ResultBadge.vue'
import ReviewActions from './ReviewActions.vue'
import VerdictBadge from './VerdictBadge.vue'
import { useCatalog } from './catalog'
import { canManage, fetchUserEvents, RECENT_DAYS, useModHost } from './host'
import type { ReviewStatus, SourceFilter } from './types'
import { useAsync } from './useAsync'
import { useBulkReview } from './useBulkReview'

// The suspicious records of a user (new-api user timeline), newest first,
// optionally of one source (group → flow@version), as a table of one-line
// rows (the AI reason in the text's tooltip); a row opens the record, manage
// users review checked records in bulk. Reloads when `version` changes; a
// review here emits `reviewed` so the drawer refreshes the counts.
const props = defineProps<{ userId: number; version: number; source: SourceFilter | null }>()
const emit = defineEmits<{
  (e: 'open-record', id: number): void
  (e: 'total', n: number): void
  (e: 'clear-source'): void
  (e: 'show-user', id: number): void
  (e: 'reviewed'): void
}>()
const t = useModHost().t
const catalog = useCatalog()
const manage = computed(() => canManage())

const { data, loading, error, reload } = useAsync(async () => {
  const source = props.source
  const r = await fetchUserEvents(props.userId, source)
  return { items: r.items, total: r.page.total ?? r.items.length, all: !source }
}, () => t('watch.loadFailed'))
const records = computed(() => data.value?.items ?? [])
const total = computed(() => data.value?.total ?? 0)
watch(() => [props.userId, props.version, props.source], reload, { immediate: true })
// the tab count is of all sources
watch(data, (d) => d?.all && emit('total', d.total))

function reviewed(ids: number[], status: ReviewStatus) {
  for (const r of records.value) if (ids.includes(r.id)) r.review_status = status
}
function removed(id: number) {
  const d = data.value
  if (!d) return
  d.items = d.items.filter((r) => r.id !== id)
  d.total = Math.max(0, d.total - 1)
  if (d.all) emit('total', d.total)
}
defineExpose({ reviewed, removed })

const { checked, reviewing, review } = useBulkReview((ids, status) => {
  reviewed(ids, status)
  emit('reviewed')
})
watch(records, () => (checked.value = []))

const columns = computed<TableColumn[]>(() => [
  { key: 'created_at', label: t('events.col.time'), nowrap: true, mobile: 'subtitle' },
  { key: 'result', label: t('events.col.result'), nowrap: true, mobile: 'status' },
  { key: 'verdict', label: t('events.col.verdict'), nowrap: true, mobile: 'meta' },
  { key: 'hit_categories', label: t('events.col.categories'), nowrap: true, mobile: 'meta' },
  { key: 'text_excerpt', label: t('events.col.text'), grow: true, mobile: 'title' }
])
</script>

<template>
  <div class="mod-stack">
    <div v-if="source" class="mod-counts">
      <SBadge tone="info">
        {{ catalog.groupName(source.group_id) }} → <FlowRef :flow-id="source.flow_id" :version="source.flow_version" /><template v-if="source.from"> · {{ t('watchSummary.recentDays', { n: RECENT_DAYS }) }}</template>
      </SBadge>
      <SButton size="xs" variant="ghost" icon="x" @click="emit('clear-source')">{{ t('watchSummary.allSources') }}</SButton>
    </div>
    <SBulkBar v-if="manage && records.length" :count="checked.length" :total="total" :page-selected="checked.length === records.length" @select-page="checked = records.map((r) => r.id)" @clear="checked = []">
      <template #idle>{{ t('review.hint') }}</template>
      <ReviewActions :loading="reviewing" @review="review" />
    </SBulkBar>
    <SAlert v-if="error" tone="danger" dense>{{ error }}</SAlert>
    <STable
      v-else
      v-model:selected="checked"
      :columns="columns"
      :rows="records"
      :loading="loading && !data"
      :empty-text="t('watch.noSuspicious')"
      :selectable="manage"
      card
      dense
      clickable
      @row-click="emit('open-record', $event.id)"
    >
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="time" /></template>
      <template #cell-result="{ row }"><ResultBadge :result="row.result" :replayed="row.replayed" /></template>
      <template #cell-verdict="{ row }"><VerdictBadge :event="row" /></template>
      <template #cell-hit_categories="{ row }"><CategoryChips :categories="row.hit_categories" :max="1" /></template>
      <template #cell-text_excerpt="{ row }">
        <span class="mod-ellipsis" :title="row.text_excerpt">{{ row.text_excerpt || '—' }}</span>
      </template>
    </STable>
    <div v-if="total > records.length" class="mod-center">
      <SButton size="sm" variant="ghost" @click="emit('show-user', userId)">{{ t('watch.allRecords') }}</SButton>
    </div>
  </div>
</template>
