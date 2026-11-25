<script setup lang="ts">
import { computed } from 'vue'
import type { CheckoutInfo } from '../host'
import { usePaymentHost } from '../host'
import ChoiceTiles from './ChoiceTiles.vue'
import MethodMark from './MethodMark.vue'
import { balance } from './money'
import { belowMin, methodMin } from './useTopupQuote'

// Payment methods; one whose minimum the amount does not reach is disabled
// and says from what amount it can be used.
const props = defineProps<{ modelValue: string; info: CheckoutInfo; amount: number | null }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
const t = usePaymentHost().t

const tiles = computed(() =>
  props.info.methods.map((m) => {
    const min = methodMin(props.info, m)
    const disabled = belowMin(props.amount, props.info, m)
    return { value: m.type, method: m, disabled, min, title: disabled ? t('wallet.methodMinTitle', { name: m.name, amount: balance(min) }) : undefined }
  })
)
</script>

<template>
  <ChoiceTiles :model-value="modelValue" :tiles="tiles" :label="t('wallet.method')" @update:model-value="emit('update:modelValue', $event)">
    <template #default="{ tile }">
      <MethodMark :method="tile.method" />
      <span class="pay-method">
        <span class="pay-method-name">{{ tile.method.name }}</span>
        <span v-if="tile.disabled" class="pay-method-min">{{ t('wallet.methodMin', { amount: balance(tile.min) }) }}</span>
      </span>
    </template>
  </ChoiceTiles>
</template>

<style scoped>
.pay-method {
  display: flex;
  min-width: 0;
  flex-direction: column;
}
.pay-method-name {
  overflow: hidden;
  font-size: 0.875rem;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pay-method-min {
  font-size: 0.6875rem;
  line-height: 1rem;
  color: rgb(var(--fg-muted));
}
</style>
