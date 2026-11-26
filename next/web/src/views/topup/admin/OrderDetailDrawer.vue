<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SDateTime, SDescriptions, SDrawer, SSectionTitle, type DescItem } from '@sub2api/ui'
import { gatewayName, useTopup, type Order } from '../topup'
import { formatLocal, formatUsd } from '../format'
import MethodName from '../components/MethodName.vue'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import OrderEvents from './OrderEvents.vue'

// Every field of one order, read-only, grouped for reconciling it with the
// gateway: the order, the payment (method, amount paid, the gateway's
// transaction number and time), its timeline and the gateway events.
const props = defineProps<{ open: boolean; order: Order | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const host = useTopup()
const t = host.t

const SECTIONS = {
  order: ['out_trade_no', 'status', 'user', 'amount', 'bonus', 'promo_code'],
  payment: ['method', 'pay_amount', 'trade_no', 'paid_at'],
  timeline: ['created_at', 'completed_at', 'expires_at']
} as const satisfies Record<string, readonly (keyof Order | 'user')[]>

const TIMES = ['created_at', 'paid_at', 'completed_at', 'expires_at'] as const

type Field = (typeof SECTIONS)[keyof typeof SECTIONS][number]

/** Formatted values; the other fields show as-is or through a slot (status, user, method, times). */
const VALUES: Partial<Record<Field, (o: Order) => Partial<DescItem>>> = {
  out_trade_no: (o) => ({ value: o.out_trade_no, mono: true, copyable: true }),
  amount: (o) => ({ value: formatUsd(o.amount) }),
  bonus: (o) => ({ value: o.bonus ? formatUsd(o.bonus) : '' }),
  promo_code: (o) => ({ value: o.promo_code, mono: !!o.promo_code }),
  pay_amount: (o) => ({ value: formatLocal(o.pay_amount, o.currency) }),
  trade_no: (o) => ({ value: o.trade_no, mono: !!o.trade_no, copyable: !!o.trade_no })
}

function itemOf(o: Order, key: Field): DescItem {
  const value = key === 'user' ? o.user_id : o[key]
  return { key, label: t(`topup.admin.orders.fields.${key}`), value, ...VALUES[key]?.(o) }
}

const sections = computed(() => {
  const o = props.order
  if (!o) return []
  return Object.entries(SECTIONS).map(([key, fields]) => ({ key, items: fields.map((f) => itemOf(o, f)) }))
})
</script>

<template>
  <SDrawer :open="open" :title="t('topup.admin.orders.detailTitle')" :description="order?.out_trade_no" width="lg" @update:open="emit('update:open', $event)">
    <template v-if="order">
      <SAlert v-if="order.failed_reason" tone="danger" :title="t('topup.admin.orders.fields.failed_reason')" class="payment-detail-alert">{{ order.failed_reason }}</SAlert>
      <section v-for="s in sections" :key="s.key" class="payment-detail-section">
        <SSectionTitle tag="h4" :title="t(`topup.admin.orders.sections.${s.key}`)" />
        <SDescriptions :items="s.items" label-width="7rem" divided>
          <template #item-status><PayStatusBadge kind="order" :status="order.status" /></template>
          <template #item-user>
            {{ order.user_email || '—' }}
            <span class="payment-muted">#{{ order.user_id }}</span>
          </template>
          <template #item-method>
            <MethodName :method="{ name: order.method_name || order.method }"><span class="payment-muted">{{ gatewayName(t, order.gateway) }}</span></MethodName>
          </template>
          <template v-for="k in TIMES" :key="k" #[`item-${k}`]><SDateTime :value="order[k]" mode="absolute" /></template>
        </SDescriptions>
      </section>
      <OrderEvents :order-id="order.id" />
    </template>
  </SDrawer>
</template>

<style scoped>
.payment-detail-alert {
  margin-bottom: 1rem;
}
.payment-detail-section + .payment-detail-section {
  margin-top: 1.25rem;
}
.payment-muted {
  margin-left: 0.25rem;
  color: rgb(var(--fg-subtle));
  font-size: 0.75rem;
}
</style>
