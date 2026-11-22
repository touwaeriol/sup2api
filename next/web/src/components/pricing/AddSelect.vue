<script setup lang="ts">
import { SSelect, type SelectOption } from '@sub2api/ui'

// A small searchable select that adds what is picked (`add`) and never holds
// a value itself: the list a table adds rows from. Other attributes
// (data-testid, …) are SSelect's.
defineProps<{ options: SelectOption[]; label: string; noResults: string; footer?: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'add', value: NonNullable<SelectOption['value']>): void }>()

function pick(value: SelectOption['value'] | undefined) {
  if (value !== null && value !== undefined) emit('add', value)
}
</script>

<template>
  <!-- clearable drops the "no value" row the field would otherwise offer. -->
  <SSelect
    :model-value="null"
    :options="options"
    searchable
    clearable
    icon="plus"
    size="sm"
    class="w-44"
    :placeholder="label"
    :aria-label="label"
    :no-results="noResults"
    :footer="footer"
    :disabled="disabled"
    @update:model-value="pick"
  />
</template>
