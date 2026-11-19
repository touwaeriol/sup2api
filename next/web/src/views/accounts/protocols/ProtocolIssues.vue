<script setup lang="ts">
// Findings of the protocol binding (protocolBinding.bindingIssues): errors
// block saving, warnings and notes explain what a choice does.
import { useI18n } from 'vue-i18n'
import { SAlert } from '@sub2api/ui'
import type { BindingIssue } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'

defineProps<{ issues: BindingIssue[] }>()
const { t } = useI18n()
const labels = useProtocolLabels()

const TONE = { error: 'danger', warning: 'warning', info: 'neutral' } as const
const text = (i: BindingIssue) =>
  t(`accounts.protocols.issues.${i.code}`, { model: i.model === undefined ? '' : labels.modelName(i.model), protocol: i.column ? labels.columnName(i.column) : (i.protocol ?? '') })
</script>

<template>
  <div v-if="issues.length" class="space-y-2" data-testid="protocol-issues">
    <SAlert v-for="(i, n) in issues" :key="n" :tone="TONE[i.level]" dense :data-issue="i.code">{{ text(i) }}</SAlert>
  </div>
</template>
