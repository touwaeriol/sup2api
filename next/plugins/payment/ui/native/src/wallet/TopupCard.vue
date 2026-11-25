<script setup lang="ts">
import { computed, toRef } from 'vue'
import { SBadge, SButton, SCard, SEmpty, SField, SInput, SSkeleton } from '@sub2api/ui'
import type { CheckoutInfo } from '../host'
import { usePaymentHost } from '../host'
import AmountGrid from './AmountGrid.vue'
import MethodPicker from './MethodPicker.vue'
import { balance, cents, discountLabel, localMoney } from './money'
import { useTopupQuote, type CheckoutMethod } from './useTopupQuote'

// The top-up card (new-api's recharge form): preset amounts, a custom
// amount, the payment method and what to pay. "Pay now" hands the choice to
// the parent, which confirms and places the order.
const props = defineProps<{ info: CheckoutInfo | null; loading: boolean }>()
const emit = defineEmits<{ (e: 'pay', amount: number, method: CheckoutMethod): void; (e: 'history'): void }>()
const t = usePaymentHost().t
const q = useTopupQuote(toRef(props, 'info'))

const range = computed(() => (props.info ? t('wallet.range', { min: balance(q.min.value), max: balance(props.info.max_topup) }) : ''))
const errorText = computed(() => {
  const e = q.error.value
  if (e === 'min') return t('wallet.amountMin', { amount: balance(q.min.value) })
  if (e === 'max') return t('wallet.amountMax', { amount: balance(props.info?.max_topup ?? 0) })
  return ''
})
const canPay = computed(() => !q.error.value && !!q.selected.value && !q.blocked(q.selected.value))
const money = (v: number) => localMoney(v, props.info?.currency ?? 'CNY')

function setAmount(v: unknown) {
  q.amount.value = typeof v === 'number' && v > 0 ? cents(v) : null
}
function pay() {
  if (canPay.value) emit('pay', q.amount.value as number, q.selected.value as CheckoutMethod)
}
</script>

<template>
  <SCard :title="t('wallet.title')" :subtitle="t('wallet.subtitle')">
    <template #actions>
      <SButton size="sm" icon="list" data-testid="pay-history" @click="emit('history')">{{ t('wallet.history') }}</SButton>
    </template>

    <div v-if="loading" class="pay-sections" aria-busy="true">
      <SSkeleton height="1rem" width="6rem" />
      <SSkeleton height="8rem" />
      <SSkeleton height="2.5rem" />
    </div>
    <SEmpty v-else-if="!info || !q.available.value" icon="balance" :title="t('wallet.unavailable')" :text="t('wallet.unavailableHint')" data-testid="pay-unavailable" />
    <div v-else class="pay-sections">
      <section v-if="info.amount_options.length">
        <h4 class="pay-label">{{ t('wallet.amount') }}</h4>
        <AmountGrid :model-value="q.amount.value" :info="info" @update:model-value="setAmount" />
      </section>
      <section>
        <h4 class="pay-label">{{ t('wallet.custom') }}</h4>
        <SField :hint="range" :error="errorText">
          <SInput
            :model-value="q.amount.value"
            type="number"
            min="0"
            step="1"
            :placeholder="t('wallet.customPlaceholder')"
            :aria-label="t('wallet.custom')"
            data-testid="pay-amount"
            @update:model-value="setAmount"
          >
            <template #prefix><span class="pay-unit">$</span></template>
          </SInput>
        </SField>
      </section>
      <section>
        <h4 class="pay-label">{{ t('wallet.method') }}</h4>
        <MethodPicker v-model="q.method.value" :info="info" :amount="q.amount.value" />
      </section>
    </div>

    <template v-if="!loading && q.available.value" #footer>
      <div class="pay-total" aria-live="polite">
        <span class="pay-total-label">{{ t('wallet.payable') }}</span>
        <strong class="pay-total-value" data-testid="pay-total">{{ q.quote.value ? money(q.quote.value.pay) : '—' }}</strong>
        <template v-if="q.quote.value && q.quote.value.rate < 1">
          <s class="pay-total-was">{{ money(q.quote.value.original) }}</s>
          <SBadge tone="success" size="xs">{{ discountLabel(q.quote.value.rate) }}</SBadge>
        </template>
      </div>
      <SButton variant="primary" :disabled="!canPay" data-testid="pay-now" @click="pay">{{ t('wallet.payNow') }}</SButton>
    </template>
  </SCard>
</template>

<style scoped>
.pay-sections {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}
.pay-label {
  margin: 0 0 0.625rem;
  font-size: 0.75rem;
  font-weight: 500;
  letter-spacing: 0.04em;
  color: rgb(var(--fg-muted));
}
.pay-unit {
  font-size: 0.875rem;
  color: rgb(var(--fg-subtle));
}
.pay-total {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.5rem;
}
.pay-total-label {
  font-size: 0.8125rem;
  color: rgb(var(--fg-muted));
}
.pay-total-value {
  font-size: 1.375rem;
  font-weight: 600;
  color: rgb(var(--fg));
  font-variant-numeric: tabular-nums;
}
.pay-total-was {
  font-size: 0.8125rem;
  color: rgb(var(--fg-subtle));
}
</style>
