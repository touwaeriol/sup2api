<script setup lang="ts">
import { SGrid, SStatCard } from '@sub2api/ui'
import type { OrderStats } from '../topup'
import { useTopup } from '../topup'
import { formatUsd } from '../format'

// The top-up totals above the orders; the pending and failed cards filter
// the list by that status (clicking the selected one clears it).
defineProps<{ stats: OrderStats | null; loading: boolean; status: string }>()
const emit = defineEmits<{ filter: [status: string] }>()
const host = useTopup()
const t = host.t
const count = (n: number | undefined) => host.i18n.formatNumber(n ?? 0)
</script>

<template>
  <SGrid variant="stats" :cols="2" :md-cols="4" class="payment-stats">
    <SStatCard
      :label="t('topup.admin.orders.stats.today')"
      :value="formatUsd(stats?.today_amount ?? 0)"
      :sub="t('topup.admin.orders.stats.count', { n: count(stats?.today_count) })"
      icon="balance"
      tone="success"
      :loading="loading && !stats"
    />
    <SStatCard :label="t('topup.admin.orders.stats.month')" :value="formatUsd(stats?.month_amount ?? 0)" icon="chart" :loading="loading && !stats" />
    <SStatCard
      :label="t('topup.admin.orders.stats.pending')"
      :value="count(stats?.pending_count)"
      icon="clock"
      tone="warning"
      :selected="status === 'pending'"
      :loading="loading && !stats"
      @click="emit('filter', status === 'pending' ? '' : 'pending')"
    />
    <SStatCard
      :label="t('topup.admin.orders.stats.failed')"
      :value="count(stats?.failed_count)"
      icon="warning"
      :tone="stats?.failed_count ? 'danger' : 'primary'"
      :selected="status === 'failed'"
      :loading="loading && !stats"
      @click="emit('filter', status === 'failed' ? '' : 'failed')"
    />
  </SGrid>
</template>

<style scoped>
.payment-stats {
  margin-bottom: 1rem;
}
</style>
