<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SButton, SField, SInput, SModal, SSkeleton } from '@sub2api/ui'
import { useLatestRequest } from '@/composables/useLatestRequest'
import { formatCurrency, formatMoney } from '@/utils/format'
import { topupApi } from '../api'
import type { CheckoutMethod, OrderRequest } from '../topup'
import { discountLabel } from '../format'
import MethodName from '../components/MethodName.vue'
import { roundTo } from '../rounding'

// Confirms a top-up with the server's price (preview): amount, bonus,
// original price, discount, what to pay and the method; a promo code is
// checked with "Apply" before it counts. "Confirm payment" hands the applied
// code to the parent, which places the order. Only the answer to the latest
// question counts (an earlier one arriving late is dropped).
const props = defineProps<{
  open: boolean
  amount: number
  method: CheckoutMethod
  currency: string
  exponent: number
  submitting: boolean
}>()
const emit = defineEmits<{ 'update:open': [v: boolean]; confirm: [promoCode: string] }>()
const { t } = useI18n()

const promo = ref('')
/** The code the shown price was asked with. */
const tried = ref('')
watch(
  () => props.open,
  open => open && (promo.value = tried.value = '')
)

const request = computed<OrderRequest | null>(() =>
  props.open ? { amount: String(props.amount), method: props.method.type, promo_code: tried.value || undefined } : null
)
const { result: preview, loading, error, reload } = useLatestRequest(request, topupApi.preview, 0)
const promoError = computed(() => (loading.value ? '' : (preview.value?.promo_error ?? '')))
const applied = computed(() => (loading.value || promoError.value ? '' : tried.value))

const apply = () => (tried.value = promo.value.trim())
const unapplied = computed(() => promo.value.trim() !== tried.value)
// A typed but unapplied code is applied first; the user then confirms the new price.
const confirmable = computed(() => unapplied.value || (!!preview.value && !loading.value && !promoError.value))
function confirm() {
  if (unapplied.value) return apply()
  if (confirmable.value) emit('confirm', applied.value)
}

const money = (v: string | number) => formatCurrency(v, props.currency)
const usd = (v: string | number) => formatMoney(v, 2)
const bonus = computed(() => Number(preview.value?.bonus ?? 0))
const discounted = computed(() => !!preview.value && Number(preview.value.discount_rate) < 1)
const saved = computed(() =>
  preview.value ? roundTo(Number(preview.value.original_pay_amount) - Number(preview.value.pay_amount), props.exponent) : 0
)
const rows = computed(() => [
  ...(bonus.value > 0
    ? [
        { key: 'bonus', value: `+${usd(bonus.value)}` },
        { key: 'credited', value: usd(props.amount + bonus.value) }
      ]
    : []),
  ...(discounted.value && preview.value
    ? [
        { key: 'original', value: money(preview.value.original_pay_amount), was: true },
        { key: 'discount', value: `${discountLabel(preview.value.discount_rate)} · −${money(saved.value)}` }
      ]
    : [])
])
</script>

<template>
  <SModal :open="open" :title="t('topup.wallet.confirm.title')" width="sm" @update:open="emit('update:open', $event)">
    <SAlert v-if="error" tone="danger" dense class="mb-4">{{ error }}</SAlert>
    <dl class="mb-5 flex flex-col gap-3 text-sm">
      <div class="flex items-center justify-between gap-4">
        <dt class="text-fg-muted">{{ t('topup.wallet.confirm.amount') }}</dt>
        <dd class="font-semibold tabular-nums text-fg">{{ usd(amount) }}</dd>
      </div>
      <div v-for="r in rows" :key="r.key" class="flex items-center justify-between gap-4">
        <dt class="text-fg-muted">{{ t(`topup.wallet.confirm.${r.key}`) }}</dt>
        <dd class="tabular-nums" :class="r.was ? 'text-fg-subtle line-through' : 'text-fg'">{{ r.value }}</dd>
      </div>
      <div class="flex items-center justify-between gap-4">
        <dt class="text-fg-muted">{{ t('topup.wallet.confirm.method') }}</dt>
        <dd class="min-w-0 font-medium text-fg"><MethodName :method="method" /></dd>
      </div>
      <div class="flex items-center justify-between gap-4 border-t border-line pt-3">
        <dt class="text-fg-muted">{{ t('topup.wallet.confirm.pay') }}</dt>
        <dd v-if="preview" class="text-2xl font-semibold tabular-nums text-fg" data-testid="pay-confirm-total">
          {{ money(preview.pay_amount) }}
        </dd>
        <dd v-else><SSkeleton width="6rem" height="1.75rem" /></dd>
      </div>
    </dl>

    <SField
      :label="t('topup.wallet.confirm.promo')"
      :error="promoError"
      :hint="applied ? t('topup.wallet.confirm.applied', { amount: usd(bonus) }) : ''"
    >
      <div class="flex gap-2">
        <SInput
          v-model="promo"
          mono
          class="min-w-0 flex-1"
          :placeholder="t('topup.wallet.confirm.promoPlaceholder')"
          data-testid="pay-promo"
          @keydown.enter.prevent="apply"
        />
        <SButton :loading="loading && !!tried" :disabled="!promo.trim()" data-testid="pay-promo-apply" @click="apply">{{
          t('topup.wallet.confirm.apply')
        }}</SButton>
      </div>
    </SField>

    <template #footer>
      <SButton :disabled="submitting" @click="emit('update:open', false)">{{ t('common.cancel') }}</SButton>
      <SButton v-if="error && !loading" variant="primary" icon="refresh" data-testid="pay-confirm-retry" @click="reload">{{
        t('topup.wallet.confirm.retry')
      }}</SButton>
      <SButton v-else variant="primary" :loading="submitting" :disabled="!confirmable" data-testid="pay-confirm" @click="confirm">{{
        t('topup.wallet.confirm.submit')
      }}</SButton>
    </template>
  </SModal>
</template>
