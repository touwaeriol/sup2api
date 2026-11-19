<script setup lang="ts">
// 模型的协议限定 card of the account editor (DESIGN-2026-11-17-PROTOCOL-BINDING
// §5.1): choose models and the protocols they are served on, as a matrix, a
// list of entries or JSON. The findings (protocolBinding.bindingIssues) and
// the server's field errors show under whichever view is open.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SCard, SField, SHint, SSegmented, STextarea } from '@sub2api/ui'
import { modelProtocolRules } from './protocolBinding'
import { ruleErrors } from '../editorErrors'
import { useProtocolLabels } from './protocolLabels'
import ModelProtocolList from './ModelProtocolList.vue'
import ModelProtocolMatrix from './ModelProtocolMatrix.vue'
import ProtocolIssues from './ProtocolIssues.vue'
import type { ProtocolEditor, RulesView } from './useProtocolEdit'

const props = defineProps<{
  editor: ProtocolEditor
  /** The account's model list: the rows of the matrix. */
  models: string[]
  mapping?: Record<string, string>
  /** Models offered by the list view's inputs. */
  options: string[]
  /** Server field errors of the whole form (model_protocols...). */
  errors: Record<string, string>
  disabled?: boolean
}>()
const { t } = useI18n()
const labels = useProtocolLabels()
const { columns, rules, view, text, textProblem, issues } = props.editor

const views = computed(() => (['matrix', 'list', 'json'] as RulesView[]).map(v => ({ value: v, label: t(`accounts.protocols.rules.views.${v}`) })))
const textError = computed(() => (textProblem.value ? t(`accounts.protocols.rules.jsonProblems.${textProblem.value.problem}`, { model: textProblem.value.model ?? '' }) : ''))
/** The server's errors: by saved entry for the list, and worded with the entry's models for the other views. */
const server = computed(() => ruleErrors(props.errors))
const serverLines = computed(() => {
  const entries = modelProtocolRules(rules.value, columns.value)
  const entryLine = (i: number, message: string) =>
    entries[i] ? t('accounts.protocols.rules.entryError', { n: i + 1, models: entries[i].models.map(labels.modelName).join(', '), message }) : message
  return [...server.value.general, ...Object.entries(server.value.byEntry).flatMap(([i, messages]) => messages.map(m => entryLine(Number(i), m)))]
})
</script>

<template>
  <SCard :title="t('accounts.protocols.rules.title')" :subtitle="t('accounts.protocols.rules.subtitle')" data-testid="model-protocols">
    <template #actions>
      <SSegmented size="sm" :options="views" :model-value="view" :aria-label="t('accounts.protocols.rules.title')" data-testid="model-protocols-view" @update:model-value="editor.setView($event as RulesView)" />
    </template>
    <div class="space-y-3">
      <ModelProtocolMatrix v-if="view === 'matrix'" :editor="editor" :models="models" :mapping="mapping" :disabled="disabled" />
      <ModelProtocolList v-else-if="view === 'list'" :editor="editor" :options="options" :errors="server.byEntry" :disabled="disabled" />
      <SField v-else :error="textError">
        <STextarea v-model="text" mono :rows="10" :disabled="disabled" data-testid="model-protocols-json" @blur="editor.syncText()" />
        <template #hint>{{ t('accounts.protocols.rules.jsonHint') }}</template>
      </SField>
      <ProtocolIssues :issues="issues" />
      <template v-if="view !== 'list' || server.general.length">
        <SHint v-for="line in view === 'list' ? server.general : serverLines" :key="line" tone="danger" size="xs" data-testid="model-protocols-error">{{ line }}</SHint>
      </template>
    </div>
  </SCard>
</template>
