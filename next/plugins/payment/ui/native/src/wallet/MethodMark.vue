<script setup lang="ts">
import { computed } from 'vue'
import type { CheckoutMethod } from './useTopupQuote'

// A payment method's mark: its initial on the method's brand colour (set by
// the admin), or on the muted text colour when it has none.
const props = withDefaults(defineProps<{ method: Pick<CheckoutMethod, 'name' | 'color'>; size?: 'sm' | 'md' }>(), { size: 'md' })
const initial = computed(() => Array.from(props.method.name.trim())[0]?.toUpperCase() ?? '·')
</script>

<template>
  <span class="pay-mark" :class="`pay-mark--${size}`" :style="method.color ? { background: method.color } : undefined" aria-hidden="true">{{ initial }}</span>
</template>

<style scoped>
.pay-mark {
  display: inline-flex;
  width: 1.75rem;
  height: 1.75rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
  background: rgb(var(--fg-muted));
  color: #fff;
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1;
}
.pay-mark--sm {
  width: 1.25rem;
  height: 1.25rem;
  border-radius: 0.375rem;
  font-size: 0.6875rem;
}
</style>
