<script setup lang="ts">
// The gateway events of one order (GET /topup/orders/:id/events), newest
// first: what the core made of each notification or query, whether it was
// verified, and the fields it kept, for reconciling a disputed payment.
import { ref, watch } from 'vue'
import { SBadge, SDateTime, SEmpty, SSectionTitle, SSkeleton } from '@sub2api/ui'
import { gatewayName, paymentApi, useTopup, type PaymentEvent } from '../topup'
import { EVENT_FIELDS, eventField, eventKindLabel, eventTone } from './events'

const props = defineProps<{ orderId: number }>()
const t = useTopup().t
const PAGE_SIZE = 50

const events = ref<PaymentEvent[]>([])
const total = ref(0)
const loading = ref(false)
const failed = ref(false)

async function load(id: number) {
  loading.value = true
  failed.value = false
  try {
    const res = await paymentApi.orderEvents(id, { page: '1', page_size: String(PAGE_SIZE) })
    if (id !== props.orderId) return
    events.value = res.items
    total.value = res.page.total
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
watch(() => props.orderId, load, { immediate: true })

const fields = (e: PaymentEvent) => EVENT_FIELDS.map((key) => ({ key, value: eventField(e, key) })).filter((f) => f.value)
</script>

<template>
  <section class="payment-events-list" data-testid="payment-order-events">
    <SSectionTitle tag="h4" :title="t('topup.admin.orders.events.title')" />
    <SSkeleton v-if="loading && !events.length" :lines="2" />
    <SEmpty v-else-if="failed || !events.length" size="sm" icon="list" :text="failed ? t('topup.loadFailed') : t('topup.admin.orders.events.empty')" />
    <ol v-else class="payment-event-items">
      <li v-for="e in events" :key="e.id" class="payment-event" data-testid="payment-order-event">
        <div class="payment-event-head">
          <SBadge :tone="eventTone(e.kind)" size="xs">{{ eventKindLabel(t, e.kind) }}</SBadge>
          <SBadge :tone="e.verified ? 'success' : 'danger'" size="xs">{{ e.verified ? t('topup.admin.orders.events.verified') : t('topup.admin.orders.events.unverified') }}</SBadge>
          <span class="payment-event-gateway">{{ gatewayName(t, e.gateway) }}</span>
          <SDateTime :value="e.created_at" mode="absolute" muted class="payment-event-time" />
        </div>
        <dl v-if="fields(e).length || e.event_id" class="payment-event-detail">
          <template v-for="f in fields(e)" :key="f.key">
            <dt>{{ t(`topup.admin.orders.events.fields.${f.key}`) }}</dt>
            <dd :class="{ 'payment-event-mono': f.key === 'trade_no' }">{{ f.value }}</dd>
          </template>
          <template v-if="e.event_id">
            <dt>ID</dt>
            <dd class="payment-event-mono">{{ e.event_id }}</dd>
          </template>
        </dl>
      </li>
    </ol>
    <p v-if="total > events.length" class="payment-event-more">{{ t('topup.admin.orders.events.more', { shown: events.length, total }) }}</p>
  </section>
</template>

<style scoped>
.payment-events-list {
  margin-top: 1.25rem;
}
.payment-event-items {
  display: flex;
  margin: 0;
  padding: 0;
  flex-direction: column;
  gap: 0.75rem;
  list-style: none;
}
.payment-event {
  padding: 0.625rem 0.75rem;
  border: 1px solid rgb(var(--line));
  border-radius: 0.5rem;
}
.payment-event-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8125rem;
}
.payment-event-time {
  margin-left: auto;
}
.payment-event-detail {
  display: grid;
  margin: 0.5rem 0 0;
  grid-template-columns: max-content 1fr;
  gap: 0.25rem 0.75rem;
  font-size: 0.75rem;
}
.payment-event-detail dt {
  color: rgb(var(--fg-muted));
}
.payment-event-detail dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.payment-event-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.payment-event-more {
  margin: 0.5rem 0 0;
  font-size: 0.75rem;
  color: rgb(var(--fg-muted));
}
</style>
