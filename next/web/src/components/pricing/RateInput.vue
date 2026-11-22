<script setup lang="ts">
import { SInput } from '@sub2api/ui'

// A rate as typed: a small monospace field with "×" before it, which emits
// the text as typed. Other attributes (size, placeholder, clearable, error,
// disabled, aria-label, data-testid, class, …) are SInput's.
defineOptions({ inheritAttrs: false })
defineProps<{ modelValue: string | null }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
</script>

<template>
  <SInput :model-value="modelValue ?? ''" size="sm" mono inputmode="decimal" v-bind="$attrs" @update:model-value="emit('update:modelValue', String($event ?? ''))">
    <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
  </SInput>
</template>
