<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SField, SFormModal, SHint, SIcon, SInput, SLink, STextarea } from '@sub2api/ui'
import { parseDomainLines, type DomainLineError, type PluginEgressMode } from './pluginEgress'

// Adds domains to the list of the current mode: one field per domain
// ("add another" adds a field), or one per line in bulk. Blank lines are
// skipped, duplicates and domains already listed dropped; a line that is not
// a domain keeps the dialog open with the error under it.
const props = defineProps<{ open: boolean; mode: PluginEgressMode; existing: readonly string[]; saving?: boolean }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'submit', domains: string[]): void }>()
const { t } = useI18n()

const rows = ref<string[]>([''])
const bulk = ref(false)
const bulkText = ref('')
const errors = ref<DomainLineError[]>([])
const empty = ref(false)

watch(
  () => props.open,
  open => {
    if (!open) return
    rows.value = ['']
    bulk.value = false
    bulkText.value = ''
    errors.value = []
    empty.value = false
  }
)

const title = computed(() => t(`pluginEgress.dialog.title.${props.mode}`))
const rowError = (i: number) => (errors.value.some(e => e.line === i + 1) ? t('pluginEgress.dialog.invalid') : '')
const lines = () => (bulk.value ? bulkText.value.split(/\r?\n/) : rows.value)

function addRow() {
  rows.value.push('')
}

function toggleBulk() {
  if (bulk.value) {
    const filled = bulkText.value.split(/\r?\n/).filter(l => l.trim())
    rows.value = filled.length ? filled : ['']
  } else {
    bulkText.value = rows.value.filter(l => l.trim()).join('\n')
  }
  bulk.value = !bulk.value
  errors.value = []
  empty.value = false
}

function submit() {
  const parsed = parseDomainLines(lines(), props.existing)
  errors.value = parsed.errors
  empty.value = !parsed.errors.length && !parsed.domains.length && lines().every(l => !l.trim())
  if (parsed.errors.length || empty.value) return
  if (!parsed.domains.length) {
    // Everything entered is listed already.
    emit('update:open', false)
    return
  }
  emit('submit', parsed.domains)
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="title"
    :loading="saving"
    :submit-text="t('pluginEgress.dialog.continue')"
    :cancel-text="t('common.cancel')"
    :enter-submit="!bulk"
    :submit-attrs="{ 'data-testid': 'plugin-egress-submit' }"
    data-testid="plugin-egress-dialog"
    @update:open="v => emit('update:open', v)"
    @submit="submit"
  >
    <div class="space-y-3">
      <template v-if="!bulk">
        <SField v-for="(_, i) in rows" :key="i" :error="rowError(i)">
          <SInput
            v-model="rows[i]"
            mono
            placeholder="website.com"
            :aria-label="t('pluginEgress.domain')"
            :error="!!rowError(i)"
            :data-testid="`plugin-egress-row-${i}`"
          />
        </SField>
      </template>
      <SField v-else :hint="t('pluginEgress.dialog.bulkHint')">
        <STextarea
          v-model="bulkText"
          mono
          :rows="8"
          placeholder="website.com&#10;api.example.com"
          :aria-label="t('pluginEgress.dialog.bulk')"
          :error="errors.length > 0"
          data-testid="plugin-egress-bulk"
        />
      </SField>
      <ul v-if="bulk && errors.length" class="space-y-0.5" data-testid="plugin-egress-bulk-errors">
        <li v-for="e in errors" :key="e.line">
          <SHint tone="danger">{{ t('pluginEgress.dialog.lineInvalid', { line: e.line, value: e.value }) }}</SHint>
        </li>
      </ul>
      <SHint v-if="empty" tone="danger" data-testid="plugin-egress-empty">{{ t('pluginEgress.dialog.empty') }}</SHint>
      <div class="flex items-center justify-between gap-3">
        <SLink v-if="!bulk" as="button" data-testid="plugin-egress-add-row" @click="addRow">
          <SIcon name="plus" class="mr-1 inline h-3.5 w-3.5" />{{ t('pluginEgress.dialog.addRow') }}
        </SLink>
        <span v-else />
        <SLink as="button" data-testid="plugin-egress-bulk-toggle" @click="toggleBulk">
          {{ bulk ? t('pluginEgress.dialog.single') : t('pluginEgress.dialog.bulk') }}
        </SLink>
      </div>
    </div>
  </SFormModal>
</template>
