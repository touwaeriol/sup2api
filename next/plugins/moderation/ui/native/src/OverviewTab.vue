<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { SAlert, SGrid, SSpinner, SStatCard } from '@sub2api/ui'
import StatusLine from './overview/StatusLine.vue'
import AttentionUsers from './overview/AttentionUsers.vue'
import TodayCostCard from './overview/TodayCostCard.vue'
import PerformanceCard from './overview/PerformanceCard.vue'
import CategoriesCard from './overview/CategoriesCard.vue'
import FunnelCard from './overview/FunnelCard.vue'
import TrendCard from './overview/TrendCard.vue'
import HealthCard from './overview/HealthCard.vue'
import QueueCard from './overview/QueueCard.vue'
import CostCard from './overview/CostCard.vue'
import { provideOverviewTick } from './overview/tick'
import { useCatalog } from './catalog'
import { num } from './format'
import { useModHost } from './host'
import { useStatus, watchNew } from './status'
import type { SectionKey } from './nav'

// Overview (new-api content-moderation overview): running state, today's
// KPIs (the suspicious count says how many were replayed cached hits), the users most in need of attention, AI review, cost, performance,
// categories, funnel, trend, health and queues. GET /status (shared with the
// page header) feeds the page; cards with their own query reload on the
// shared tick. Refreshes every minute while the tab is shown.
const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{
  (e: 'open-watch', userId: number): void
  (e: 'go-tab', tab: SectionKey): void
}>()
const t = useModHost().t
const catalog = useCatalog()
const status = useStatus()
const bump = provideOverviewTick()

async function reload() {
  bump()
  await Promise.all([status.reload(), catalog.refresh()])
}
defineExpose({ reload })
onMounted(status.reload)

const REFRESH_MS = 60_000
const timer = setInterval(() => props.active && document.visibilityState === 'visible' && reload(), REFRESH_MS)
onBeforeUnmount(() => clearInterval(timer))

const kpis = computed(() => {
  const s = status.data.value
  if (!s) return []
  // entries with new hits: under the default level "enforce" they are enforce-level ones
  const fresh = watchNew(s.watch)
  return [
    { key: 'audited', icon: 'eyeglass', tone: 'primary' as const, value: s.today.audited, sub: t('kpi.auditedSub') },
    { key: 'suspicious', icon: 'warning', tone: 'warning' as const, value: s.today.suspicious, sub: s.today.replayed ? `${t('kpi.suspiciousSub')} · ${t('kpi.replayedSub', { n: num(s.today.replayed) })}` : t('kpi.suspiciousSub') },
    { key: 'watching', icon: 'eye', tone: 'info' as const, value: s.watch.watching, sub: t('kpi.withNewSub', { n: num(fresh.watching) }) },
    { key: 'blocking', icon: 'shield', tone: 'danger' as const, value: s.watch.blocking, sub: fresh.blocking === null ? t('kpi.blockingSub') : `${t('kpi.blockingSub')} · ${t('kpi.withNewSub', { n: num(fresh.blocking) })}` },
    { key: 'blocked', icon: 'x', tone: 'danger' as const, value: s.today.blocked, sub: t('kpi.blockedSub') }
  ]
})
const goTab = (tab: SectionKey) => emit('go-tab', tab)
</script>

<template>
  <div>
    <SAlert v-if="status.error.value" tone="danger" class="mod-block">{{ status.error.value }}</SAlert>
    <SSpinner v-if="!status.data.value && status.loading.value" block />
    <template v-if="status.data.value">
      <StatusLine :status="status.data.value" @go-tab="goTab" />
      <SGrid variant="stats" md-template="repeat(auto-fit, minmax(170px, 1fr))">
        <SStatCard v-for="k in kpis" :key="k.key" :label="t(`kpi.${k.key}`)" :value="num(k.value)" :sub="k.sub" :icon="k.icon" :tone="k.tone" />
      </SGrid>
    </template>

    <AttentionUsers class="mod-section" @open-watch="emit('open-watch', $event)" @go-tab="goTab" />
    <TodayCostCard v-if="status.data.value" :costs="status.data.value.costs" class="mod-section" />
    <PerformanceCard class="mod-section" />
    <SGrid template="repeat(auto-fit, minmax(min(360px, 100%), 1fr))" class="mod-section">
      <CategoriesCard />
      <FunnelCard :funnels="status.data.value?.funnels || []" @go-tab="goTab" />
    </SGrid>
    <TrendCard :alert-percent="status.data.value?.error_rate_alert_percent ?? 5" class="mod-section" />
    <template v-if="status.data.value">
      <HealthCard :status="status.data.value" class="mod-section" @go-tab="goTab" />
      <QueueCard :queue="status.data.value.queue" :runtime="status.data.value.runtime" class="mod-section" />
    </template>
    <CostCard class="mod-section" />
  </div>
</template>
