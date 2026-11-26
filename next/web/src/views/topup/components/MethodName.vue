<script setup lang="ts">
// A payment method's mark followed by its name, inline (table cells, detail
// rows); the default slot adds a note after the name.
import MethodIcon from './MethodIcon.vue'
import type { MethodLook } from './methodIcon'

withDefaults(defineProps<{ method: MethodLook; size?: 'sm' | 'md' }>(), { size: 'sm' })
</script>

<template>
  <span class="payment-method-label">
    <MethodIcon :method="method" :size="size" />
    <span class="payment-method-label-text">{{ method.name }}</span>
    <slot />
  </span>
</template>

<style scoped>
.payment-method-label {
  display: inline-flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 0.5rem;
  vertical-align: middle;
}
.payment-method-label-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
