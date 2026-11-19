<script setup lang="ts">
// Date and time input over an RFC 3339 value: <SInput type="datetime-local">
// shows it in local time; a picked time is emitted as RFC 3339 (UTC), a
// cleared box as undefined, so the key is not sent. Remaining attrs
// (placeholder, disabled, size, data-testid, ...) go to the SInput. For
// showing a time read-only use SDateTime.
import SInput from './SInput.vue'
import { fromLocalInput, toLocalInput } from './timeRange'

defineProps<{ modelValue?: string | null }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string | undefined): void }>()
</script>

<template>
  <SInput
    type="datetime-local"
    :model-value="toLocalInput(modelValue)"
    @update:model-value="emit('update:modelValue', fromLocalInput(String($event ?? '')) || undefined)"
  />
</template>
