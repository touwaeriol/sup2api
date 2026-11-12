<script setup lang="ts">
import { computed } from 'vue'
import { SBadge, SDescriptions, type DescItem } from '@sub2api/ui'
import CategoryChips from './CategoryChips.vue'
import FlowRef from './FlowRef.vue'
import { useCatalog } from './catalog'
import { joinList, num } from './format'
import { recentFrom, useModHost } from './host'
import type { SourceFilter, UserDetail, WatchItem } from './types'

// Summary of a user (new-api user-detail-summary and context bar): all
// suspicious records by severity, handling status and AI verdict, the group
// and flow now, and the group → flow@version sources of the last 30 days.
// Main categories come from the watch list row when there is one. A source
// picks the timeline filter (pick-source), limited to the same last 30 days
// so the timeline matches the count.
const props = defineProps<{ detail: UserDetail; item?: WatchItem | null }>()
const emit = defineEmits<{ (e: 'pick-source', s: SourceFilter): void }>()
const t = useModHost().t
const catalog = useCatalog()

const REVIEW = ['pending', 'confirmed', 'false_positive'] as const
const nonZero = <K extends string>(keys: readonly K[], counts: Record<K, number>) => keys.filter((k) => counts[k] > 0).map((k) => ({ key: k, n: counts[k] }))

const review = computed(() => nonZero(REVIEW, props.detail.summary))

const fields = computed<DescItem[]>(() => {
  const { summary: s, context: c } = props.detail
  const groups = c.group_id === null ? [] : [c.group_id, ...(props.item?.other_groups ?? [])]
  const items: DescItem[] = [
    { key: 'suspicious', label: t('watchList.col.suspicious'), value: `${num(s.total)} · ${t('watchList.sev.high', { n: s.by_severity.high })} / ${t('watchList.sev.medium', { n: s.by_severity.medium })} / ${t('watchList.sev.low', { n: s.by_severity.low })}` },
    { key: 'review', label: t('watchSummary.review') },
    { key: 'categories', label: t('watchList.col.categories') },
    { key: 'group', label: t('watchList.col.group'), value: groups.length ? joinList(groups.map(catalog.groupName)) : '—' },
    { key: 'flow', label: t('events.col.flow') }
  ]
  if (c.sources.length) items.push({ key: 'sources', label: t('watchSummary.sources'), full: true })
  return items
})
</script>

<template>
  <SDescriptions :items="fields" :columns="2" dense>
    <template #item-review>
      <span class="mod-chips">
        <SBadge v-for="r in review" :key="r.key" size="xs">{{ t(`review.${r.key}`) }} {{ num(r.n) }}</SBadge>
      </span>
    </template>
    <template #item-categories>
      <CategoryChips v-if="item?.top_categories.length" :categories="item.top_categories" :other="item.top_other" wrap />
      <span v-else>—</span>
    </template>
    <template #item-flow>
      <FlowRef v-if="detail.context.flow" :flow-id="detail.context.flow.flow_id" :version="detail.context.flow.version" :name="detail.context.flow.name" />
      <span v-else class="mod-sub">{{ t('watchList.noFlow') }}</span>
    </template>
    <template #item-sources>
      <ul class="mod-sources">
        <li v-for="src in detail.context.sources" :key="`${src.group_id}-${src.flow_id}-${src.version}`">
          <button
            type="button"
            class="mod-source"
            :title="t('watchSummary.pickSource')"
            @click="emit('pick-source', { group_id: src.group_id, flow_id: src.flow_id, flow_version: src.version, from: recentFrom() })"
          >
            <span>{{ catalog.groupName(src.group_id) }}</span>
            <span class="mod-sub">→</span>
            <FlowRef :flow-id="src.flow_id" :version="src.version" :name="src.flow_name" />
            <span class="mod-num">×{{ num(src.count) }}</span>
            <SBadge v-if="src.current" tone="info" size="xs">{{ t('flows.current') }}</SBadge>
          </button>
        </li>
      </ul>
    </template>
  </SDescriptions>
</template>
