<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SButton, SField, SInput, SModal, SSkeleton } from '@sub2api/ui'
import { errorMessage, paymentApi, usePaymentHost, type OrderPreview } from '../host'
import MethodMark from './MethodMark.vue'
import { balance, cents, discountLabel, localMoney } from './money'
import type { CheckoutMethod } from './useTopupQuote'

// Confirms a top-up with the server's price (preview): amount, bonus,
// original price, discount, what to pay and the method; a promo code is
// checked with "Apply" before it counts. "Confirm payment" hands the applied
// code and the price to the parent, which places the order.
const props = defineProps<{ open: boolean; amount: number; method: CheckoutMethod; currency: string; submitting: boolean }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'confirm', promoCode: string, preview: OrderPreview): void }>()
const host = usePaymentHost()
const t = host.t

const preview = ref<OrderPreview | null>(null)
const promo = ref('')
const applied = ref('')
const promoError = ref('')
const applying = ref(false)

async function fetchPreview(code: string): Promise<OrderPreview | null> {
  try {
    return await paymentApi.preview({ amount: props.amount, method: props.method.type, promo_code: code || undefined })
  } catch (e) {
    host.toast(errorMessage(e, t('wallet.confirm.previewFailed')), 'error')
    return null
  }
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    promo.value = applied.value = promoError.value = ''
    preview.value = null
    preview.value = await fetchPreview('')
  },
  { immediate: true }
)

async function apply() {
  const code = promo.value.trim()
  applying.value = true
  const res = await fetchPreview(code)
  applying.value = false
  if (!res) return
  preview.value = res
  promoError.value = res.promo_error ?? ''
  applied.value = res.promo_error ? '' : code
}

// A typed but unapplied code is applied first; the user then confirms the new price.
function confirm() {
  if (promo.value.trim() !== applied.value) return void apply()
  if (preview.value) emit('confirm', applied.value, preview.value)
}

const money = (v: number) => localMoney(v, props.currency)
const discounted = computed(() => !!preview.value && preview.value.discount_rate < 1)
const saved = computed(() => (preview.value ? cents(preview.value.original_pay_amount - preview.value.pay_amount) : 0))
</script>

<template>
  <SModal :open="open" :title="t('wallet.confirm.title')" width="sm" @update:open="emit('update:open', $event)">
    <dl class="pay-rows">
      <div class="pay-row">
        <dt>{{ t('wallet.confirm.amount') }}</dt>
        <dd class="pay-strong">{{ balance(amount) }}</dd>
      </div>
      <template v-if="preview && preview.bonus > 0">
        <div class="pay-row">
          <dt>{{ t('wallet.confirm.bonus') }}</dt>
          <dd>+{{ balance(preview.bonus) }}</dd>
        </div>
        <div class="pay-row">
          <dt>{{ t('wallet.confirm.credited') }}</dt>
          <dd>{{ balance(amount + preview.bonus) }}</dd>
        </div>
      </template>
      <template v-if="preview && discounted">
        <div class="pay-row">
          <dt>{{ t('wallet.confirm.original') }}</dt>
          <dd><s class="pay-was">{{ money(preview.original_pay_amount) }}</s></dd>
        </div>
        <div class="pay-row">
          <dt>{{ t('wallet.confirm.discount') }}</dt>
          <dd>{{ discountLabel(preview.discount_rate) }} · −{{ money(saved) }}</dd>
        </div>
      </template>
      <div class="pay-row">
        <dt>{{ t('wallet.confirm.method') }}</dt>
        <dd class="pay-method"><MethodMark :method="method" size="sm" />{{ method.name }}</dd>
      </div>
      <div class="pay-row pay-row--total">
        <dt>{{ t('wallet.confirm.pay') }}</dt>
        <dd v-if="preview" class="pay-total" data-testid="pay-confirm-total">{{ money(preview.pay_amount) }}</dd>
        <dd v-else><SSkeleton width="6rem" height="1.75rem" /></dd>
      </div>
    </dl>

    <SField :label="t('wallet.confirm.promo')" :error="promoError" :hint="applied ? t('wallet.confirm.applied', { amount: balance(preview?.bonus ?? 0) }) : ''">
      <div class="pay-promo">
        <SInput v-model="promo" mono :placeholder="t('wallet.confirm.promoPlaceholder')" data-testid="pay-promo" @keydown.enter.prevent="apply" />
        <SButton :loading="applying" :disabled="!promo.trim()" data-testid="pay-promo-apply" @click="apply">{{ t('wallet.confirm.apply') }}</SButton>
      </div>
    </SField>

    <template #footer>
      <SButton :disabled="submitting" @click="emit('update:open', false)">{{ t('action.cancel') }}</SButton>
      <SButton variant="primary" :loading="submitting" :disabled="!preview || applying" data-testid="pay-confirm" @click="confirm">{{ t('wallet.confirm.submit') }}</SButton>
    </template>
  </SModal>
</template>

<style scoped>
.pay-rows {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin: 0 0 1.25rem;
}
.pay-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  font-size: 0.875rem;
}
.pay-row dt {
  color: rgb(var(--fg-muted));
}
.pay-row dd {
  margin: 0;
  color: rgb(var(--fg));
  font-variant-numeric: tabular-nums;
}
.pay-row--total {
  padding-top: 0.75rem;
  border-top: 1px solid rgb(var(--line));
}
.pay-strong {
  font-weight: 600;
}
.pay-was {
  color: rgb(var(--fg-subtle));
}
.pay-method {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  font-weight: 500;
}
.pay-total {
  font-size: 1.5rem;
  font-weight: 600;
}
.pay-promo {
  display: flex;
  gap: 0.5rem;
}
.pay-promo > :first-child {
  min-width: 0;
  flex: 1;
}
</style>
