<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SChoiceCards, type ChoiceOption } from '@sub2api/ui'
import { formatMoney } from '@/utils/format'
import type { CheckoutInfo } from '../topup'
import MethodName from '../components/MethodName.vue'
import { belowMin, methodMin } from './useTopupQuote'

// Payment methods, each with its mark; one whose minimum the amount does
// not reach is disabled and says from what amount it can be used. The mark
// and name are the option's content (SChoiceCards draws UI icons only).
const props = defineProps<{ modelValue: string; info: CheckoutInfo; amount: number | null }>()
const emit = defineEmits<{ 'update:modelValue': [v: string] }>()
const { t } = useI18n()

const options = computed<ChoiceOption[]>(() =>
  props.info.methods.map(m => ({ value: m.type, label: '', disabled: belowMin(props.amount, props.info, m) }))
)
const minText = (i: number) => t('topup.wallet.methodMin', { amount: formatMoney(methodMin(props.info, props.info.methods[i]), 2) })
</script>

<template>
  <SChoiceCards
    :model-value="modelValue"
    :options="options"
    :cols="3"
    :aria-label="t('topup.wallet.method')"
    @update:model-value="emit('update:modelValue', String($event))"
  >
    <template v-for="(m, i) in info.methods" :key="m.type" #[`option-${m.type}`]>
      <MethodName :method="m" size="md" class="-mt-0.5 text-sm font-medium text-fg" />
      <span v-if="options[i].disabled" class="mt-1 block text-xs text-fg-muted">{{ minText(i) }}</span>
    </template>
  </SChoiceCards>
</template>
