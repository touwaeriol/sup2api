<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SButton, SCopyButton, SModal, STextarea } from '@sub2api/ui'
import { usePaymentHost } from '../host'

// The plain codes of a batch just generated: shown once, to copy or save as
// a text file; closing drops them (the list keeps them masked).
const props = defineProps<{ open: boolean; codes: string[] }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const host = usePaymentHost()
const t = host.t

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
  <SModal :open="open" :title="t('admin.redeem.result.title', { n: codes.length })" width="lg" persistent @update:open="emit('update:open', $event)">
    <SAlert tone="warning" class="payment-result-warn">{{ t('admin.redeem.result.warn') }}</SAlert>
    <STextarea :model-value="text" :rows="Math.min(12, Math.max(4, codes.length))" mono readonly :aria-label="t('admin.redeem.fields.code')" />
    <template #footer>
      <SCopyButton :text="text" :label="t('admin.redeem.result.copyAll')" variant="secondary" size="md" />
      <SButton icon="download" @click="download">{{ t('admin.redeem.result.download') }}</SButton>
      <SButton variant="primary" @click="emit('update:open', false)">{{ t('admin.redeem.result.done') }}</SButton>
    </template>
  </SModal>
</template>

<style scoped>
.payment-result-warn {
  margin-bottom: 0.75rem;
}
</style>
