<script setup lang="ts">
// Protocol ids no column of the account type has (their endpoint is gone, or
// they were written by hand): warning chips that can only be removed, since
// the server refuses them.
import { useI18n } from 'vue-i18n'
import { SChip } from '@sub2api/ui'

defineProps<{ ids: string[]; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'remove', protocol: string): void }>()
const { t } = useI18n()
</script>

<template>
  <SChip v-for="id in ids" :key="id" tone="warning" closable :disabled="disabled" :close-label="t('common.remove')" :data-protocol="id" @close="emit('remove', id)">
    <span class="font-mono">{{ id }}</span>
  </SChip>
</template>
