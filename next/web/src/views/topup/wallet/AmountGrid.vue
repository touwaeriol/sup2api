<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SChoiceCards, type ChoiceOption } from '@sub2api/ui'
import { formatCurrency, formatMoney } from '@/utils/format'
import type { CheckoutInfo } from '../topup'
import { discountLabel } from '../format'
import { quoteOf, type Quote } from './useTopupQuote'

// Preset amounts: the balance credited, what it costs in the local currency
// and the discount tier it reaches.
const props = defineProps<{ modelValue: number | null; info: CheckoutInfo }>()
const emit = defineEmits<{ 'update:modelValue': [v: number] }>()
const { t } = useI18n()

const money = (v: number) => formatCurrency(v, props.info.currency)
const presets = computed(() =>
  props.info.amount_options.map(Number).map(a => {
    const quote = quoteOf(a, props.info)
    const option: ChoiceOption = {
      value: a,
      label: formatMoney(a, 2),
      description: t('topup.wallet.pay', { amount: money(quote.pay) }),
      disabled: a < Number(props.info.min_topup) || a > Number(props.info.max_topup)
    }
    return { option, quote }
  })
)
const options = computed(() => presets.value.map(p => p.option))
const discounted = (q: Quote) => q.rate < 1
</script>

<template>
  <SChoiceCards
    :model-value="modelValue ?? undefined"
    :options="options"
    :cols="3"
    :aria-label="t('topup.wallet.amount')"
    @update:model-value="emit('update:modelValue', Number($event))"
  >
    <template v-for="p in presets" :key="p.option.value" #[`option-${p.option.value}`]>
      <span v-if="discounted(p.quote)" class="mt-1 flex flex-wrap items-center gap-1.5 text-xs">
        <s class="text-fg-subtle">{{ money(p.quote.original) }}</s>
        <SBadge tone="success" size="xs">{{ discountLabel(p.quote.rate) }}</SBadge>
      </span>
    </template>
  </SChoiceCards>
</template>
