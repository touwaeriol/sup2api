<script setup lang="ts">
import { SButton, SField, SInput, STextarea } from '@sub2api/ui'
import { categoryLabel } from './format'
import { useModHost } from './host'
import { JEV_DEFAULT_THRESHOLD, YES_NO_THRESHOLD } from './thresholdPresets'
import type { JevQuestions } from './types'

// A Jev question table (new-api jev-questions-editor): the preamble and one
// row per category with its label, threshold, question and the optional
// answers sent as criteria.true / criteria.false. Shared by the auditor
// editor and the default question set in the settings tab; the toolbar slot
// takes the caller's copy / restore buttons. hideThreshold leaves out the
// threshold column: the question-table LLM auditor answers yes or no and
// uses no threshold. Inside an SForm, prop is the table's form path: each
// cell shows the errors of its path (<prop>.preamble, <prop>.items.<i>.<field>).
const props = defineProps<{ hideThreshold?: boolean; prop?: string }>()
const model = defineModel<JevQuestions>({ required: true })
const t = useModHost().t

/** The form path of a field of the table, of row i when given (none without prop). */
const at = (field: string, i?: number) => (props.prop ? `${props.prop}.${i === undefined ? '' : `items.${i}.`}${field}` : undefined)
const addRow = () => model.value.items.push({ category: 'custom/', label: '', question: '', answer_yes: '', answer_no: '', threshold: props.hideThreshold ? YES_NO_THRESHOLD : JEV_DEFAULT_THRESHOLD })
</script>

<template>
  <div class="mod-form">
    <div class="mod-toolbar">
      <slot name="toolbar" />
      <SButton size="sm" icon="plus" @click="addRow">{{ t('auditors.addQuestion') }}</SButton>
    </div>
    <SField :label="t('auditors.preamble')" :prop="at('preamble')">
      <STextarea v-model="model.preamble" :rows="3" :maxlength="2000" />
    </SField>
    <div v-for="(q, i) in model.items" :key="i" class="mod-question">
      <div class="mod-form-row">
        <SField :label="t('auditors.category')" size="sm" :prop="at('category', i)"><SInput v-model="q.category" size="sm" :maxlength="64" /></SField>
        <SField :label="t('auditors.label')" size="sm" :prop="at('label', i)"><SInput v-model="q.label" size="sm" :maxlength="64" :placeholder="categoryLabel(q.category)" /></SField>
        <SField v-if="!hideThreshold" :label="t('auditors.threshold')" size="sm" :prop="at('threshold', i)"><SInput v-model="q.threshold" type="number" min="0.01" max="1" step="0.01" size="sm" /></SField>
        <SButton size="sm" variant="ghost" tone="danger" class="mod-form-row-unlabeled" @click="model.items.splice(i, 1)">{{ t('delete') }}</SButton>
      </div>
      <SField :prop="at('question', i)"><STextarea v-model="q.question" :rows="2" :maxlength="2000" :placeholder="t('auditors.question')" /></SField>
      <div class="mod-form-row">
        <SField :label="t('auditors.answerYes')" size="sm" :prop="at('answer_yes', i)"><SInput v-model="q.answer_yes" size="sm" :maxlength="1000" :placeholder="t('auditors.answerPlaceholder')" /></SField>
        <SField :label="t('auditors.answerNo')" size="sm" :prop="at('answer_no', i)"><SInput v-model="q.answer_no" size="sm" :maxlength="1000" :placeholder="t('auditors.answerPlaceholder')" /></SField>
      </div>
    </div>
  </div>
</template>
