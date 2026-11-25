<script setup lang="ts">
import { computed } from 'vue'
import { SDateTime, SSectionTitle, STable, type TableColumn } from '@sub2api/ui'
import { usePaymentHost, type Order } from '../host'
import { formatLocal, formatUsd } from '../format'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import { columnOf } from './actions'

// The refunds of an order (its audit trail), newest first, under its details.
const props = defineProps<{ order: Order }>()
const host = usePaymentHost()
const t = host.t

const columns = computed<TableColumn[]>(() => {
  const col = columnOf('admin.refund.fields')
  return [
    col('created_at', { mobile: 'meta' }),
    col('amount', { align: 'right', mobile: 'title' }),
    col('mode', { mobile: 'meta' }),
    col('reason', { nowrap: false, mobile: 'subtitle' }),
    col('status', { mobile: 'status' })
  ]
})
const rows = computed(() => props.order.refunds ?? [])
</script>

<template>
  <section class="payment-refunds">
    <SSectionTitle tag="h4" :title="t('admin.refund.records')" />
    <STable :columns="columns" :rows="rows" dense>
      <template #cell-created_at="{ row }">
        <SDateTime :value="row.created_at" mode="absolute" />
        <span class="payment-refund-sub">{{ row.operator }}</span>
      </template>
      <template #cell-amount="{ row }">
        <span class="payment-refund-num">{{ formatUsd(row.amount) }}</span>
        <span class="payment-refund-sub">{{ formatLocal(row.pay_amount, order.currency) }}</span>
      </template>
      <template #cell-mode="{ row }">
        {{ t(`admin.refund.modeShort.${row.mode}`) }}
        <span class="payment-refund-sub">{{
          row.deduct_balance
            ? t('admin.refund.deducted', {
                amount: formatUsd(row.deducted_amount)
              })
            : t('admin.refund.kept')
        }}</span>
      </template>
      <template #cell-status="{ row }"><PayStatusBadge kind="refund" :status="row.status" /></template>
    </STable>
  </section>
</template>

<style scoped>
.payment-refunds {
  margin-top: 1.25rem;
}
.payment-refund-num {
  font-variant-numeric: tabular-nums;
}
.payment-refund-sub {
  display: block;
  color: rgb(var(--fg-subtle));
  font-size: 0.75rem;
  font-variant-numeric: tabular-nums;
}
</style>
