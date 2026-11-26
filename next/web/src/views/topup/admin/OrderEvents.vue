<script setup lang="ts">
// The gateway events of one order (GET /topup/orders/:id/events), newest
// first: what the core made of each notification or query, whether it was
// verified, and the fields it kept, for reconciling a disputed payment.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SDateTime, SEmpty, SSectionTitle, SSkeleton } from '@sub2api/ui'
import { useLatestRequest } from '@/composables/useLatestRequest'
import { topupApi } from '../api'
import { gatewayName, type PaymentEvent } from '../topup'
import { EVENT_FIELDS, eventField, eventKindLabel, eventTone } from './events'

const props = defineProps<{ orderId: number }>()
const { t, te } = useI18n()
const PAGE_SIZE = 50

// Switching orders drops the answer for the previous one.
const page = useLatestRequest(
  () => props.orderId,
  id => topupApi.orderEvents(id, PAGE_SIZE),
  0
)
const events = computed(() => page.result.value?.items ?? [])
const total = computed(() => page.result.value?.page.total ?? 0)

const fields = (e: PaymentEvent) => EVENT_FIELDS.map(key => ({ key, value: eventField(e, key) })).filter(f => f.value)
</script>

<template>
  <section class="mt-5" data-testid="payment-order-events">
    <SSectionTitle tag="h4" :title="t('topup.admin.orders.events.title')" />
    <SSkeleton v-if="page.loading.value && !events.length" :lines="2" />
    <SEmpty
      v-else-if="page.error.value || !events.length"
      size="sm"
      icon="list"
      :text="page.error.value || t('topup.admin.orders.events.empty')"
    />
    <ol v-else class="m-0 flex list-none flex-col gap-3 p-0">
      <li v-for="e in events" :key="e.id" class="rounded-lg border border-line px-3 py-2.5" data-testid="payment-order-event">
        <div class="flex flex-wrap items-center gap-2 text-[0.8125rem]">
          <SBadge :tone="eventTone(e.kind)" size="xs">{{ eventKindLabel(t, te, e.kind) }}</SBadge>
          <SBadge :tone="e.verified ? 'success' : 'danger'" size="xs">{{
            e.verified ? t('topup.admin.orders.events.verified') : t('topup.admin.orders.events.unverified')
          }}</SBadge>
          <span>{{ gatewayName(t, e.gateway) }}</span>
          <SDateTime :value="e.created_at" mode="absolute" muted class="ml-auto" />
        </div>
        <dl v-if="fields(e).length || e.event_id" class="mt-2 mb-0 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs">
          <template v-for="f in fields(e)" :key="f.key">
            <dt class="text-fg-muted">{{ t(`topup.admin.orders.events.fields.${f.key}`) }}</dt>
            <dd class="m-0 [overflow-wrap:anywhere]" :class="{ 'font-mono': f.key === 'trade_no' }">{{ f.value }}</dd>
          </template>
          <template v-if="e.event_id">
            <dt class="text-fg-muted">ID</dt>
            <dd class="m-0 font-mono [overflow-wrap:anywhere]">{{ e.event_id }}</dd>
          </template>
        </dl>
      </li>
    </ol>
    <p v-if="total > events.length" class="mt-2 mb-0 text-xs text-fg-muted">
      {{ t('topup.admin.orders.events.more', { shown: events.length, total }) }}
    </p>
  </section>
</template>
