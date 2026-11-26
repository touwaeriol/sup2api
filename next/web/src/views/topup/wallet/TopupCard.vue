<script setup lang="ts">
import { computed, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SCard, SEmpty, SField, SInput, SSkeleton } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import { formatCurrency, formatMoney } from '@/utils/format'
import type { CheckoutInfo, CheckoutMethod } from '../topup'
import { discountLabel } from '../format'
import AmountGrid from './AmountGrid.vue'
import MethodPicker from './MethodPicker.vue'
import { useTopupQuote } from './useTopupQuote'

// The top-up card: preset amounts, a custom amount, the payment method and
// what to pay. "Pay now" hands the choice to the parent, which confirms and
// places the order.
const props = defineProps<{ info: CheckoutInfo | null; loading: boolean }>()
const emit = defineEmits<{ pay: [amount: number, method: CheckoutMethod] }>()
const { t } = useI18n()
const auth = useAuthStore()
const q = useTopupQuote(toRef(props, 'info'))

const usd = (v: number | string) => formatMoney(v, 2)
const range = computed(() => (props.info ? t('topup.wallet.range', { min: usd(q.min.value), max: usd(props.info.max_topup) }) : ''))
const errorText = computed(() => {
  const e = q.error.value
  if (e === 'min') return t('topup.wallet.amountMin', { amount: usd(q.min.value) })
  if (e === 'max') return t('topup.wallet.amountMax', { amount: usd(props.info?.max_topup ?? 0) })
  return ''
})
// Pointing at redeem codes helps only those who may use them.
const unavailableHint = computed(() => t(auth.has('redeem:use') ? 'topup.wallet.unavailableHint' : 'topup.wallet.unavailableHintNoRedeem'))
const canPay = computed(() => !q.error.value && !!q.selected.value && !q.blocked(q.selected.value))
const money = (v: number) => (props.info ? formatCurrency(v, props.info.currency) : '—')

function pay() {
  if (canPay.value) emit('pay', q.amount.value as number, q.selected.value as CheckoutMethod)
}
</script>

<template>
  <SCard :title="t('topup.wallet.title')" :subtitle="t('topup.wallet.subtitle')">
    <template #actions>
      <SButton size="sm" icon="list" to="/me/topup-orders" data-testid="pay-history">{{ t('topup.wallet.history') }}</SButton>
    </template>

    <div v-if="loading" class="flex flex-col gap-5" aria-busy="true">
      <SSkeleton height="1rem" width="6rem" />
      <SSkeleton height="8rem" />
      <SSkeleton height="2.5rem" />
    </div>
    <SEmpty
      v-else-if="!info || !q.available.value"
      icon="balance"
      :title="t('topup.wallet.unavailable')"
      :text="unavailableHint"
      data-testid="pay-unavailable"
    />
    <div v-else class="flex flex-col gap-5">
      <section v-if="info.amount_options.length">
        <h4 class="mb-2.5 text-xs font-medium tracking-wide text-fg-muted">{{ t('topup.wallet.amount') }}</h4>
        <AmountGrid :model-value="q.amount.value" :info="info" @update:model-value="q.setAmount" />
      </section>
      <section>
        <h4 class="mb-2.5 text-xs font-medium tracking-wide text-fg-muted">{{ t('topup.wallet.custom') }}</h4>
        <SField :hint="range" :error="errorText">
          <SInput
            :model-value="q.amount.value"
            type="number"
            min="0"
            step="1"
            :placeholder="t('topup.wallet.customPlaceholder')"
            :aria-label="t('topup.wallet.custom')"
            data-testid="pay-amount"
            @update:model-value="q.setAmount"
          >
            <template #prefix><span class="text-sm text-fg-subtle">$</span></template>
          </SInput>
        </SField>
      </section>
      <section>
        <h4 class="mb-2.5 text-xs font-medium tracking-wide text-fg-muted">{{ t('topup.wallet.method') }}</h4>
        <MethodPicker v-model="q.method.value" :info="info" :amount="q.amount.value" />
      </section>
    </div>

    <template v-if="!loading && q.available.value" #footer>
      <div class="flex min-w-0 flex-1 flex-wrap items-baseline gap-2" aria-live="polite">
        <span class="text-[0.8125rem] text-fg-muted">{{ t('topup.wallet.payable') }}</span>
        <strong class="text-[1.375rem] font-semibold tabular-nums text-fg" data-testid="pay-total">{{
          q.quote.value ? money(q.quote.value.pay) : '—'
        }}</strong>
        <template v-if="q.quote.value && q.quote.value.rate < 1">
          <s class="text-[0.8125rem] text-fg-subtle">{{ money(q.quote.value.original) }}</s>
          <SBadge tone="success" size="xs">{{ discountLabel(q.quote.value.rate) }}</SBadge>
        </template>
      </div>
      <SButton variant="primary" :disabled="!canPay" data-testid="pay-now" @click="pay">{{ t('topup.wallet.payNow') }}</SButton>
    </template>
  </SCard>
</template>
