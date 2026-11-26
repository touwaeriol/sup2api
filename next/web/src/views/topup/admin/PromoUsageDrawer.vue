<script setup lang="ts">
import { computed } from 'vue'
import { SDateTime, SDrawer, SHint, SPagination, STable, type TableColumn } from '@sub2api/ui'
import { paymentApi, useTopup, type PromoCode, type PromoUsage } from '../topup'
import { formatUsd } from '../format'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import { columnOf, useAdminList } from './actions'

// Who used one promo code (GET /promo-codes/:id/usages): each user once,
// with the order that carried it. A use held by an unpaid order is released
// when the order expires or is cancelled. The caller mounts one drawer per
// code (keyed by id), so the list path is fixed.
const props = defineProps<{ open: boolean; promo: PromoCode }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const host = useTopup()
const t = host.t

const list = useAdminList<PromoUsage>(paymentApi.promoUsagesPath(props.promo.id), {})

const columns = computed<TableColumn[]>(() => {
  const col = columnOf('admin.promo.usage.fields')
  return [
    col('user', { grow: true, mobile: 'title' }),
    col('out_trade_no', { mobile: 'subtitle' }),
    col('bonus', { align: 'right', mobile: 'meta' }),
    col('status', { mobile: 'status' }),
    col('created_at', { mobile: 'meta' }),
    col('used_at', { mobile: 'meta' })
  ]
})
</script>

<template>
  <SDrawer :open="open" :title="t('topup.admin.promo.usage.title', { code: promo.code })" width="xl" @update:open="emit('update:open', $event)">
    <p class="payment-usage-summary">{{ t('topup.admin.promo.usage.summary', { count: promo.used_count }) }}</p>
    <STable :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :empty-text="t('topup.admin.promo.usage.empty')" @retry="list.reload">
      <template #cell-user="{ row }">{{ row.user_email || `#${row.user_id}` }}</template>
      <template #cell-out_trade_no="{ row }"><span class="payment-mono">{{ row.out_trade_no }}</span></template>
      <template #cell-bonus="{ row }"><span class="payment-num">{{ formatUsd(row.bonus) }}</span></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="usage" :status="row.status" /></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-used_at="{ row }"><SDateTime :value="row.used_at" mode="absolute" muted /></template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>
    <SHint size="xs" class="payment-usage-hint">{{ t('topup.admin.promo.usage.hint') }}</SHint>
  </SDrawer>
</template>

<style scoped src="./admin.css"></style>
<style scoped>
.payment-usage-summary {
  margin: 0 0 0.75rem;
  font-size: 0.875rem;
  color: rgb(var(--fg-muted));
}
.payment-usage-hint {
  margin-top: 0.75rem;
}
</style>
