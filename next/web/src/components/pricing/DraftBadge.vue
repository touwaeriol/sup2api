<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge } from '@sub2api/ui'

// The state of a row a table keeps a draft of, as a small badge: refused by
// the server (`error`: its message on hover, or as the label with
// `showError`), to be removed, or changed and not saved yet, red while it
// cannot be saved (`invalid`, why on hover with `invalidTitle`). Nothing for
// a row as saved. With `testid` the badge is `<testid>-<state>`.
const props = defineProps<{ dirty?: boolean; invalid?: boolean; removed?: boolean; error?: string; showError?: boolean; invalidTitle?: string; testid?: string }>()
const { t } = useI18n()

const state = computed(() => (props.removed ? 'removed' : props.error ? 'refused' : props.dirty ? 'unsaved' : null))
const label = computed(() => (state.value === 'refused' && props.showError ? props.error : t(`pricing.draft.${state.value}`)))
const title = computed(() => props.error || (props.invalid ? props.invalidTitle : undefined))
</script>

<template>
  <SBadge v-if="state" :tone="state === 'unsaved' && !invalid ? 'warning' : 'danger'" size="xs" :title="title" :data-testid="testid && `${testid}-${state}`">{{ label }}</SBadge>
</template>
