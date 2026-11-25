<script setup lang="ts">
import { computed } from 'vue'
import { SBadge } from '@sub2api/ui'
import type { CheckoutInfo } from '../host'
import { usePaymentHost } from '../host'
import ChoiceTiles from './ChoiceTiles.vue'
import { balance, discountLabel, localMoney } from './money'
import { quoteOf } from './useTopupQuote'

// Preset amounts: the balance credited, what it costs in the local currency
// and the discount tier it reaches.
const props = defineProps<{ modelValue: number | null; info: CheckoutInfo }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: number): void }>()
const t = usePaymentHost().t

const presets = computed(() =>
  props.info.amount_options.map((a) => ({
    value: a,
    disabled: a < props.info.min_topup || a > props.info.max_topup,
    quote: quoteOf(a, props.info)
  }))
)
const money = (v: number) => localMoney(v, props.info.currency)
</script>

<template>
  <ChoiceTiles :model-value="modelValue" :tiles="presets" :label="t('wallet.amount')" @update:model-value="emit('update:modelValue', $event)">
    <template #default="{ tile }">
      <span class="pay-amount">
        <span class="pay-amount-value">{{ balance(tile.value) }}</span>
        <span class="pay-amount-pay">
          {{ t('wallet.pay', { amount: money(tile.quote.pay) }) }}
          <s v-if="tile.quote.rate < 1" class="pay-amount-was">{{ money(tile.quote.original) }}</s>
        </span>
      </span>
      <SBadge v-if="tile.quote.rate < 1" class="pay-amount-badge" tone="success" size="xs">{{ discountLabel(tile.quote.rate) }}</SBadge>
    </template>
  </ChoiceTiles>
</template>

<style scoped>
.pay-amount {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.25rem;
}
.pay-amount-value {
  font-size: 1.125rem;
  font-weight: 600;
  line-height: 1.5rem;
  font-variant-numeric: tabular-nums;
}
.pay-amount-pay {
  font-size: 0.75rem;
  color: rgb(var(--fg-muted));
}
.pay-amount-was {
  margin-left: 0.25rem;
  color: rgb(var(--fg-subtle));
}
.pay-amount-badge {
  position: absolute;
  top: 0.5rem;
  right: 0.5rem;
}
</style>
