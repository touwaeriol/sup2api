<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SButton, SCopyButton, SModal, STextarea } from '@sub2api/ui'

// The plain codes of a batch just generated: shown once, to copy or save as
// a text file; closing drops them (the list keeps them masked).
const props = defineProps<{ open: boolean; codes: string[] }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const { t } = useI18n()

const text = computed(() => props.codes.join('\n'))

/** redeem-codes-20261125-153000.txt, in local time. */
function fileName(d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `redeem-codes-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}.txt`
}

function download() {
  const url = URL.createObjectURL(new Blob([text.value + '\n'], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = fileName()
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <SModal :open="open" :title="t('redeem.result.title', { n: codes.length })" width="lg" persistent @update:open="emit('update:open', $event)">
    <SAlert tone="warning" class="mb-3">{{ t('redeem.result.warn') }}</SAlert>
    <STextarea :model-value="text" :rows="Math.min(12, Math.max(4, codes.length))" mono readonly :aria-label="t('redeem.cols.code')" />
    <template #footer>
      <SCopyButton :text="text" :label="t('redeem.result.copyAll')" variant="secondary" size="md" />
      <SButton icon="download" @click="download">{{ t('redeem.result.download') }}</SButton>
      <SButton variant="primary" @click="emit('update:open', false)">{{ t('redeem.result.done') }}</SButton>
    </template>
  </SModal>
</template>
