<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SChoiceCards, SHint, SInput, type ChoiceOption } from '@sub2api/ui'
import ModelListEditor from '@/components/models/ModelListEditor.vue'
import { draftPatterns, mergeDraftPatterns } from './groupModelPolicy'

const props = defineProps<{ modelValue: string[]; mode: 'whitelist' | 'blacklist'; options: string[]; loading?: boolean; unrestrictedAccounts?: number }>()
const emit = defineEmits<{ (e: 'update:modelValue', value: string[]): void; (e: 'update:mode', value: 'whitelist' | 'blacklist'): void }>()
const { t } = useI18n()
const draft = ref('')
const modeOptions = computed<ChoiceOption[]>(() =>
  (['whitelist', 'blacklist'] as const).map((value) => ({ value, label: t(`groups.${value}`), description: t(`groups.${value}Hint`) }))
)
const suggestions = computed(() => [...new Set(props.options)])
function fillGroupModels() {
  const pending = draftPatterns(draft.value)
  commit()
  emit('update:modelValue', [...new Set([...props.modelValue, ...pending, ...suggestions.value])])
}
// commit adds the drafted patterns. A pattern of any length is accepted:
// the core no longer bounds a model name (CONTRACTS §112.16).
function commit() {
  if (draftPatterns(draft.value).length) emit('update:modelValue', mergeDraftPatterns(props.modelValue, draft.value))
  draft.value = ''
}
function paste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData('text')
  if (!text) return
  event.preventDefault()
  draft.value = [draft.value, text.replace(/[\s,]+/g, ',')].filter(Boolean).join(',')
}
defineExpose({ commit })
</script>

<template>
  <div class="space-y-3" data-testid="group-model-policy">
    <SChoiceCards :model-value="mode" :options="modeOptions" :aria-label="t('groups.modelRules')" @update:model-value="emit('update:mode', $event as 'whitelist' | 'blacklist')">
      <!-- Test hooks of the two options (kept from the button version). -->
      <template #option-whitelist><span data-testid="policy-whitelist" /></template>
      <template #option-blacklist><span data-testid="policy-blacklist" /></template>
    </SChoiceCards>
    <div class="flex flex-wrap items-center gap-2">
      <SButton size="sm" :loading="loading" :disabled="loading || !suggestions.length" data-testid="policy-fill-group" @click="fillGroupModels">{{ t(mode === 'whitelist' ? 'groups.fillWhitelist' : 'groups.fillBlacklist', { n: suggestions.length }) }}</SButton>
      <span class="text-xs text-fg-subtle">{{ t('groups.groupModelSource') }}</span>
    </div>
    <SHint v-if="unrestrictedAccounts" tone="warning" size="xs">{{ t('groups.unrestrictedModelsHint', { n: unrestrictedAccounts }) }}</SHint>
    <ModelListEditor show-options :model-value="modelValue" :options="suggestions" :mapping="{}" :empty-text="t('groups.emptyPolicy')" @update:model-value="emit('update:modelValue', $event)" />
    <div class="flex gap-2">
      <SInput v-model="draft" size="sm" class="min-w-0 flex-1" :aria-label="t('groups.allowlistPlaceholder')" :placeholder="t('groups.allowlistPlaceholder')" data-testid="policy-draft" @keydown.enter.prevent="commit" @paste="paste" @blur="commit" />
      <SButton size="sm" :disabled="!draft.trim()" @click="commit">{{ t('accounts.editorUi.addModels') }}</SButton>
    </div>
  </div>
</template>
