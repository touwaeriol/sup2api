<script setup lang="ts">
// The marks after a tab's title in a tabbed form: a red dot when something
// in the tab is wrong, else an amber one when it holds unsaved changes. The
// dot's meaning is read out and shown on hover. Internal to TabBar and the
// side navigation of STabbedForm.
import { computed } from 'vue'
import { useUiT } from '../i18n'

const props = defineProps<{ dirty?: boolean; error?: boolean }>()
const t = useUiT()
const label = computed(() => (props.error ? t('ui.tabHasErrors') : props.dirty ? t('ui.tabUnsaved') : ''))
</script>

<template>
  <span v-if="label" class="inline-flex h-1.5 w-1.5 shrink-0 rounded-full" :class="error ? 'bg-danger-500' : 'bg-warning-500'" :title="label" :data-testid="error ? 'tab-mark-error' : 'tab-mark-unsaved'">
    <span class="sr-only">{{ label }}</span>
  </span>
</template>
