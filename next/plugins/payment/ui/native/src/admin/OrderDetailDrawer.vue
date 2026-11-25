<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SButton, SDateTime, SDescriptions, SDrawer, type DescItem } from '@sub2api/ui'
import { usePaymentHost, type Order } from '../host'
import { formatLocal, formatUsd } from '../format'
import PayStatusBadge from './PayStatusBadge.vue'
import { completable } from './status'

// Every field of one order, with the manual completion for the orders that
// may take it.
const props = defineProps<{ open: boolean; order: Order | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; complete: [order: Order] }>()
const host = usePaymentHost()
const t = host.t

const TIMES = ['created_at', 'paid_at', 'completed_at', 'expires_at'] as const

const items = computed<DescItem[]>(() => {
  const o = props.order
  if (!o) return []
  const label = (k: string) => t(`admin.orders.fields.${k}`)
  return [
    { key: 'out_trade_no', label: label('out_trade_no'), value: o.out_trade_no, mono: true, copyable: true },
    { key: 'status', label: label('status'), value: o.status },
    { key: 'user', label: label('user'), value: o.user_email || `#${o.user_id}` },
    { key: 'amount', label: label('amount'), value: formatUsd(o.amount) },
    { key: 'bonus', label: label('bonus'), value: o.bonus ? formatUsd(o.bonus) : '' },
    { key: 'pay_amount', label: label('pay_amount'), value: formatLocal(o.pay_amount, o.currency) },
    { key: 'method', label: label('method'), value: `${o.method_name} · ${t(`gateway.${o.gateway}`)}` },
    { key: 'promo_code', label: label('promo_code'), value: o.promo_code, mono: !!o.promo_code },
    { key: 'trade_no', label: label('trade_no'), value: o.trade_no, mono: !!o.trade_no, copyable: true },
    ...TIMES.map((k) => ({ key: k, label: label(k), value: o[k] }))
  ]
})
</script>

<template>
  <SDrawer :open="open" :title="t('admin.orders.detailTitle')" :description="order?.out_trade_no" width="md" @update:open="emit('update:open', $event)">
    <template v-if="order">
      <SAlert v-if="order.failed_reason" tone="danger" :title="t('admin.orders.fields.failed_reason')" class="payment-detail-alert">{{ order.failed_reason }}</SAlert>
      <SDescriptions :items="items" label-width="7rem" divided>
        <template #item-status><PayStatusBadge kind="order" :status="order.status" /></template>
        <template #item-user>
          {{ order.user_email || '—' }}
          <span class="payment-muted">#{{ order.user_id }}</span>
        </template>
        <template v-for="k in TIMES" :key="k" #[`item-${k}`]><SDateTime :value="order[k]" mode="absolute" /></template>
      </SDescriptions>
    </template>
    <template v-if="order && completable(order.status)" #footer>
      <SButton variant="primary" @click="emit('complete', order)">{{ t('admin.orders.complete') }}</SButton>
    </template>
  </SDrawer>
</template>

<style scoped>
.payment-detail-alert {
  margin-bottom: 1rem;
}
.payment-muted {
  margin-left: 0.25rem;
  color: rgb(var(--fg-subtle));
  font-size: 0.75rem;
}
</style>
