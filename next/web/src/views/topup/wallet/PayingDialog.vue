<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SAlert, SButton, SDescItem, SDescriptions, SModal, SSpinner, useNow } from '@sub2api/ui'
import { errorMessage, paymentApi, useTopup } from '../topup'
import { formatLocal, formatUsd } from '../format'
import { isPayUrl, openPayPage } from './payWindow'
import QrCode from './QrCode.vue'
import type { PayingOrder } from './types'
import { useOrderPolling } from './useOrderPolling'

// Waits for an order to be paid: the payment page is open in another window
// (and/or a QR code is shown here), the order is polled every 3 s and the
// countdown runs to its expiry. Completion emits `credited` once.
const props = defineProps<{ open: boolean; order: PayingOrder | null; blocked: boolean }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'credited'): void }>()
const host = useTopup()
const t = host.t

const no = () => props.order?.out_trade_no ?? null
const live = useOrderPolling(no, () => props.open)
const now = useNow(() => (props.open ? 1000 : 0))
const remaining = computed(() => (props.order ? Math.max(0, Date.parse(props.order.expires_at) - now.value) : 0))
const cancelling = ref(false)

type View = 'waiting' | 'crediting' | 'success' | 'expired' | 'failed' | 'ended'
const view = computed<View>(() => {
  const s = live.status.value
  if (s === 'completed') return 'success'
  if (s === 'paid') return 'crediting'
  if (s === 'failed') return 'failed'
  if (s === 'expired' || (s === 'pending' && remaining.value <= 0)) return 'expired'
  return s === 'cancelled' ? 'ended' : 'waiting'
})
watch(view, (v) => v === 'success' && emit('credited'))

function countdown(ms: number): string {
  const s = Math.floor(ms / 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  const hours = Math.floor(s / 3600)
  return `${hours ? `${hours}:` : ''}${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`
}

async function cancel() {
  const order = props.order
  if (!order || !(await host.confirm({ message: t('topup.wallet.paying.cancelConfirm'), danger: true }))) return
  cancelling.value = true
  try {
    live.order.value = await paymentApi.cancelOrder(order.out_trade_no)
    host.toast(t('topup.wallet.paying.cancelled'), 'success')
    emit('update:open', false)
  } catch (e) {
    host.toast(errorMessage(e, t('topup.saveFailed')), 'error')
  } finally {
    cancelling.value = false
  }
}

// The amounts are the order's as the server reads it back (a new order's
// create answer has none), never the confirm dialog's preview.
const shown = computed(() => live.order.value ?? props.order)
const credited = computed(() => (shown.value?.amount === undefined ? '—' : formatUsd(shown.value.amount + (shown.value.bonus ?? 0))))
const payText = computed(() => (shown.value?.currency ? formatLocal(shown.value.pay_amount, shown.value.currency) : '—'))
/** The order's payment page may be opened (https only). */
const payable = computed(() => isPayUrl(props.order?.pay_url))
const endedText = computed(() => t('topup.wallet.paying.ended', { status: t(`topup.status.${live.status.value}`) }))
</script>

<template>
  <SModal :open="open" :title="view === 'success' ? t('topup.wallet.paying.successTitle') : t('topup.wallet.paying.title')" width="md" @update:open="emit('update:open', $event)">
    <template v-if="order">
      <SAlert v-if="view === 'waiting' && order.pay_url && !payable" tone="danger" dense class="pay-gap" data-testid="pay-bad-url">{{ t('topup.wallet.paying.badUrl') }}</SAlert>
      <SAlert v-else-if="view === 'waiting' && order.pay_url" :tone="blocked ? 'warning' : 'info'" dense class="pay-gap">
        {{ blocked ? t('topup.wallet.paying.blocked') : t('topup.wallet.paying.opened') }}
      </SAlert>
      <SAlert v-else-if="view === 'success'" tone="success" class="pay-gap" data-testid="pay-success">{{ t('topup.wallet.paying.success', { amount: credited }) }}</SAlert>
      <SAlert v-else-if="view === 'expired'" tone="warning" dense class="pay-gap">{{ t('topup.wallet.paying.expired') }}</SAlert>
      <SAlert v-else-if="view === 'failed'" tone="danger" dense class="pay-gap">{{ t('topup.wallet.paying.failed', { reason: live.order.value?.failed_reason || '—' }) }}</SAlert>
      <SAlert v-else-if="view === 'ended'" tone="neutral" dense class="pay-gap">{{ endedText }}</SAlert>

      <div class="pay-body">
        <figure v-if="view === 'waiting' && order.qr_code" class="pay-qr-box">
          <QrCode :value="order.qr_code" :label="t('topup.wallet.paying.scan', { method: order.method_name })" />
          <figcaption>{{ t('topup.wallet.paying.scan', { method: order.method_name }) }}</figcaption>
        </figure>
        <SDescriptions stacked class="pay-facts">
          <SDescItem :label="t('topup.wallet.paying.orderNo')" :value="order.out_trade_no" mono copyable />
          <SDescItem :label="t('topup.wallet.paying.pay')">
            <span class="pay-amount" data-testid="pay-amount-due">{{ payText }}</span>
          </SDescItem>
          <SDescItem v-if="view === 'waiting'" :label="t('topup.wallet.paying.remaining')">
            <span class="pay-countdown" role="timer" data-testid="pay-countdown">{{ countdown(remaining) }}</span>
          </SDescItem>
          <SDescItem v-if="view === 'crediting'" :label="t('topup.wallet.orders.status')">
            <span class="pay-crediting"><SSpinner size="sm" decorative />{{ t('topup.wallet.paying.crediting') }}</span>
          </SDescItem>
        </SDescriptions>
      </div>
    </template>

    <template #footer>
      <template v-if="view === 'waiting'">
        <SButton variant="ghost" tone="danger" :loading="cancelling" data-testid="pay-cancel" @click="cancel">{{ t('topup.wallet.paying.cancel') }}</SButton>
        <SButton v-if="payable && order?.pay_url" variant="primary" icon="external" data-testid="pay-reopen" @click="openPayPage(order.pay_url)">{{ t('topup.wallet.paying.reopen') }}</SButton>
      </template>
      <SButton v-else :variant="view === 'success' ? 'primary' : 'secondary'" @click="emit('update:open', false)">
        {{ view === 'success' ? t('topup.wallet.paying.done') : t('topup.wallet.paying.close') }}
      </SButton>
    </template>
  </SModal>
</template>

<style scoped>
.pay-gap {
  margin-bottom: 1rem;
}
.pay-body {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: 1.5rem;
}
.pay-qr-box {
  display: flex;
  margin: 0;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.75rem;
  color: rgb(var(--fg-muted));
}
.pay-facts {
  min-width: 12rem;
  flex: 1;
}
.pay-amount {
  font-size: 1.5rem;
  font-weight: 600;
  line-height: 2rem;
  font-variant-numeric: tabular-nums;
}
.pay-countdown {
  font-size: 1.125rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.pay-crediting {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}
@media (max-width: 639px) {
  .pay-body {
    flex-direction: column;
    align-items: stretch;
  }
  .pay-qr-box {
    align-self: center;
  }
}
</style>
