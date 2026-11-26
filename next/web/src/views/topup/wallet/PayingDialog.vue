<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SButton, SDescItem, SDescriptions, SModal, SSpinner, toast, useNow } from '@sub2api/ui'
import { confirmAction } from '@/composables/crud'
import { formatCurrency, formatMoney } from '@/utils/format'
import { topupApi } from '../api'
import type { PayingOrder } from '../topup'
import { isPayUrl, isQrImage, openPayPage } from './payWindow'
import { useOrderPolling } from './useOrderPolling'

// Waits for an order to be paid: the payment page is open in another window
// and/or the gateway's QR code is shown here; the order is polled and the
// countdown runs to its expiry. Completion emits `credited` once.
const props = defineProps<{ open: boolean; order: PayingOrder | null; blocked: boolean }>()
const emit = defineEmits<{ 'update:open': [v: boolean]; credited: [] }>()
const { t } = useI18n()

const live = useOrderPolling(
  () => props.order?.out_trade_no ?? null,
  () => props.open
)
const now = useNow(() => (props.open ? 1000 : 0))
const remaining = computed(() => (props.order ? Math.max(0, Date.parse(props.order.expires_at) - now.value) : 0))

type View = 'waiting' | 'crediting' | 'success' | 'expired' | 'failed' | 'ended'
const VIEW_OF: Partial<Record<string, View>> = {
  completed: 'success',
  paid: 'crediting',
  failed: 'failed',
  expired: 'expired',
  cancelled: 'ended'
}
const view = computed<View>(() => {
  const s = live.status.value
  if (s === 'pending') return remaining.value > 0 ? 'waiting' : 'expired'
  return VIEW_OF[s] ?? 'ended'
})
watch(view, v => v === 'success' && emit('credited'))

function countdown(ms: number): string {
  const s = Math.floor(ms / 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  const hours = Math.floor(s / 3600)
  return `${hours ? `${hours}:` : ''}${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`
}

const cancelling = ref(false)
async function cancelOrder(no: string) {
  cancelling.value = true
  try {
    return await topupApi.cancelOrder(no)
  } finally {
    cancelling.value = false
  }
}
function cancel() {
  const no = props.order?.out_trade_no
  if (!no) return
  return confirmAction(
    { message: t('topup.wallet.paying.cancelConfirm'), danger: true },
    () => cancelOrder(no),
    o => {
      live.order.value = o
      toast(t('topup.wallet.paying.cancelled'), 'success')
      emit('update:open', false)
    }
  )
}

// The amounts are the order's as the server reads it back (a new order's
// create answer has none), never the confirm dialog's preview.
const shown = computed(() => live.order.value ?? props.order)
const credited = computed(() =>
  shown.value?.amount === undefined ? '—' : formatMoney(Number(shown.value.amount) + Number(shown.value.bonus ?? 0), 2)
)
const payText = computed(() => (shown.value?.currency ? formatCurrency(shown.value.pay_amount, shown.value.currency) : '—'))
const payUrl = computed(() => (isPayUrl(props.order?.pay_url) ? props.order.pay_url : ''))
const qrImage = computed(() => (isQrImage(props.order?.qr_image) ? props.order.qr_image : ''))
// Past its time but still payable (the server has not expired it yet, or a
// late payment may still credit it): no advice to order again.
const late = computed(() => live.late.value || (view.value === 'expired' && shown.value?.payable !== false))
const expiredText = computed(() => t(late.value ? 'topup.wallet.paying.expiredLate' : 'topup.wallet.paying.expired'))
const endedText = computed(() => t('topup.wallet.paying.ended', { status: t(`topup.status.${live.status.value}`) }))
const scanText = computed(() => t('topup.wallet.paying.scan', { method: props.order?.method_name ?? '' }))
// Whether a blocker stopped the payment page: as it went when the order was
// opened, then as the last "open again" went.
const pageBlocked = ref(false)
watch(
  () => [props.order, props.blocked] as const,
  ([, b]) => (pageBlocked.value = b),
  { immediate: true }
)
const reopen = () => (pageBlocked.value = openPayPage(payUrl.value) === 'blocked')
</script>

<template>
  <SModal
    :open="open"
    :title="view === 'success' ? t('topup.wallet.paying.successTitle') : t('topup.wallet.paying.title')"
    width="md"
    @update:open="emit('update:open', $event)"
  >
    <template v-if="order">
      <SAlert v-if="view === 'waiting' && payUrl" :tone="pageBlocked ? 'warning' : 'info'" dense class="mb-4" data-testid="pay-window">
        {{ pageBlocked ? t('topup.wallet.paying.blocked') : t('topup.wallet.paying.opened') }}
      </SAlert>
      <SAlert v-else-if="view === 'success'" tone="success" class="mb-4" data-testid="pay-success">{{
        t('topup.wallet.paying.success', { amount: credited })
      }}</SAlert>
      <SAlert
        v-else-if="view === 'expired' || view === 'ended'"
        :tone="view === 'expired' ? 'warning' : 'neutral'"
        dense
        class="mb-4"
        data-testid="pay-ended"
      >
        {{ view === 'expired' ? expiredText : endedText }}
        <template v-if="late"> {{ t('topup.wallet.paying.lateHint') }}</template>
      </SAlert>
      <SAlert v-else-if="view === 'failed'" tone="danger" dense class="mb-4">{{
        t('topup.wallet.paying.failed', { reason: live.order.value?.failed_reason || '—' })
      }}</SAlert>

      <div class="flex flex-wrap items-start gap-6 max-sm:flex-col max-sm:items-stretch">
        <figure v-if="view === 'waiting' && qrImage" class="m-0 flex flex-col items-center gap-2 text-xs text-fg-muted max-sm:self-center">
          <img
            :src="qrImage"
            :alt="scanText"
            width="176"
            height="176"
            class="block rounded-lg border border-line bg-white"
            data-testid="pay-qr"
          />
          <figcaption>{{ scanText }}</figcaption>
        </figure>
        <SDescriptions stacked class="min-w-48 flex-1">
          <SDescItem :label="t('topup.wallet.paying.orderNo')" :value="order.out_trade_no" mono copyable />
          <SDescItem :label="t('topup.wallet.paying.pay')">
            <span class="text-2xl font-semibold tabular-nums" data-testid="pay-amount-due">{{ payText }}</span>
          </SDescItem>
          <SDescItem v-if="view === 'waiting'" :label="t('topup.wallet.paying.remaining')">
            <span class="text-lg font-semibold tabular-nums" role="timer" data-testid="pay-countdown">{{ countdown(remaining) }}</span>
          </SDescItem>
          <SDescItem v-if="view === 'crediting'" :label="t('topup.wallet.orders.status')">
            <span class="inline-flex items-center gap-2"><SSpinner size="sm" decorative />{{ t('topup.wallet.paying.crediting') }}</span>
          </SDescItem>
        </SDescriptions>
      </div>
    </template>

    <template #footer>
      <template v-if="view === 'waiting'">
        <SButton variant="ghost" tone="danger" :loading="cancelling" data-testid="pay-cancel" @click="cancel">{{
          t('topup.wallet.paying.cancel')
        }}</SButton>
        <SButton v-if="payUrl" variant="primary" icon="external" data-testid="pay-reopen" @click="reopen">{{
          t('topup.wallet.paying.reopen')
        }}</SButton>
      </template>
      <SButton v-else :variant="view === 'success' ? 'primary' : 'secondary'" @click="emit('update:open', false)">
        {{ view === 'success' ? t('topup.wallet.paying.done') : t('common.close') }}
      </SButton>
    </template>
  </SModal>
</template>
