<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import { SAlert, SDescriptions, SDrawer, SSectionTitle, SSpinner, STabs, type DescItem } from '@sub2api/ui'
import EventDetail from './EventDetail.vue'
import UserAnalyses from './UserAnalyses.vue'
import UserAiReview from './UserAiReview.vue'
import UserDailyChart from './UserDailyChart.vue'
import UserStateSection from './UserStateSection.vue'
import UserTimeline from './UserTimeline.vue'
import WatchHistory from './WatchHistory.vue'
import WatchSummary from './WatchSummary.vue'
import { dt, enumLabel, num } from './format'
import { canManage, useModHost } from './host'
import type { ReviewStatus, SourceFilter, Watch, WatchItem } from './types'
import { useUserDetail } from './useUserDetail'

// User detail drawer (§72.13.6, new-api user-detail-drawer), for any user:
// state (allowlist / blocklist mark included) and inline watch level, the AI
// review (latest assessment and its proposal), the entry, a summary with the
// last 30 days and its sources, then the suspicious records (filterable by
// source, bulk review), the analyses and the watch history. The
// record detail opened here links no user (this drawer is the user).
const props = defineProps<{ open: boolean; userId: number | null; item?: WatchItem | null }>()
const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'changed', w: Partial<Watch> | null): void
  (e: 'show-user', userId: number): void
}>()
const t = useModHost().t
const manage = computed(() => canManage())

/** Bumped when the lists must reload: an analysis finished or the watch entry changed. */
const version = ref(0)
const { detail, loading, error, entry, state, reload } = useUserDetail(toRef(props, 'open'), toRef(props, 'userId'), {
  manage,
  changed: (w) => emit('changed', w),
  finished: () => version.value++
})
function refreshWatch() {
  void reload()
  version.value++
  emit('changed', null)
}
/** A review or delete changes the counts of the drawer and of the watch list row. */
function refreshCounts() {
  void reload()
  emit('changed', null)
}

const section = ref('records')
const source = ref<SourceFilter | null>(null)
const totals = ref({ analyses: 0, records: 0, history: 0 })
watch(
  () => props.userId,
  () => {
    section.value = 'records'
    source.value = null
    totals.value = { analyses: 0, records: 0, history: 0 }
  }
)
function pickSource(s: SourceFilter) {
  source.value = s
  section.value = 'records'
}

const fields = computed<DescItem[]>(() => {
  const w = entry.value
  if (!w) return []
  const items: DescItem[] = [
    { key: 'source', label: t('watch.col.source'), value: enumLabel('watchSource', w.source) },
    { key: 'violations', label: t('watch.col.violations'), value: num(w.violation_count) },
    { key: 'hits', label: t('watch.col.hits'), value: num(w.hit_count) },
    { key: 'since', label: t('watch.since'), value: dt(w.since_at) },
    { key: 'expires', label: t('watch.col.expires'), value: w.expires_at ? dt(w.expires_at) : t('watch.noExpiry') },
    { key: 'last', label: t('watch.col.lastHit'), value: dt(w.last_hit_at) }
  ]
  if (w.note) items.push({ key: 'note', label: t('watch.col.note'), value: w.note, full: true })
  return items
})

const tabs = computed(() => [
  { key: 'records', label: `${t('watch.suspicious')} (${num(totals.value.records)})` },
  { key: 'analyses', label: `${t('watch.analyses')} (${num(totals.value.analyses)})` },
  { key: 'history', label: `${t('watch.history')} (${num(totals.value.history)})` }
])

// Record detail on top of the drawer; review and delete update the timeline.
const timeline = ref<InstanceType<typeof UserTimeline> | null>(null)
const recordId = ref<number | null>(null)
const recordOpen = ref(false)
function openRecord(id: number) {
  recordId.value = id
  recordOpen.value = true
}
function onReviewed(ids: number[], status: ReviewStatus) {
  timeline.value?.reviewed(ids, status)
  refreshCounts()
}
function onDeleted(id: number) {
  timeline.value?.removed(id)
  refreshCounts()
}
</script>

<template>
  <SDrawer :open="open" :title="t('watch.detailTitle', { id: userId ?? '' })" width="xl" @update:open="emit('update:open', $event)">
    <SSpinner v-if="loading && !detail" block />
    <SAlert v-else-if="error && !detail" tone="warning">{{ error }}</SAlert>
    <div v-else-if="detail && userId" class="mod-detail">
      <!-- a failed reload (polling, refresh) keeps the user shown; the next load clears it -->
      <SAlert v-if="error" tone="warning" dense>{{ t('watch.refreshFailed', { error }) }}</SAlert>
      <UserStateSection :user-id="detail.user_id" :state="detail" @watch-changed="refreshWatch" @show-user="emit('show-user', $event)" />
      <UserAiReview :detail="detail" :state="state" @changed="refreshWatch" @open-record="openRecord" />

      <div v-if="entry">
        <SSectionTitle tag="p">{{ t('watch.info') }}</SSectionTitle>
        <SDescriptions :items="fields" :columns="2" dense />
      </div>

      <div>
        <SSectionTitle tag="p">{{ t('watchSummary.title') }}</SSectionTitle>
        <WatchSummary :detail="detail" :item="item" @pick-source="pickSource" />
        <UserDailyChart v-if="detail.daily.length" :daily="detail.daily" />
      </div>

      <STabs v-model="section" :tabs="tabs" size="sm">
        <UserAnalyses v-show="section === 'analyses'" :user-id="userId" :version="version" @open-record="openRecord" @total="totals.analyses = $event" />
        <UserTimeline
          v-show="section === 'records'"
          ref="timeline"
          :user-id="userId"
          :version="version"
          :source="source"
          @open-record="openRecord"
          @total="totals.records = $event"
          @clear-source="source = null"
          @reviewed="refreshCounts"
          @show-user="emit('show-user', $event)"
        />
        <WatchHistory v-show="section === 'history'" :user-id="userId" :version="version" @total="totals.history = $event" />
      </STabs>
    </div>
  </SDrawer>
  <EventDetail v-model:open="recordOpen" :event-id="recordId" :user-link="false" @reviewed="onReviewed" @deleted="onDeleted" @show-user="emit('show-user', $event)" />
</template>
