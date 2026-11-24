<script setup lang="ts">
// The high and critical host permissions an upgrade newly asks for,
// highlighted, with the acknowledgement the approval waits for. Rendered
// only when there are any.
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SCheckbox, SHint } from '@sub2api/ui'
import RiskDot from '../parts/RiskDot.vue'
import { useConsentLabels, type HPRow } from './hostPermission'

defineProps<{ rows: HPRow[]; modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [v: boolean] }>()
const { t } = useI18n()
const { hpLabel } = useConsentLabels()
</script>

<template>
  <SCard v-if="rows.length" class="mt-4 !border-danger-300 dark:!border-danger-800" data-testid="consent-new-risk">
    <template #title>
      <h3 class="flex items-center gap-2 text-sm font-semibold">
        {{ t('pluginInstall.newRiskTitle') }}
        <SBadge tone="danger">{{ t('pluginInstall.newRisk', { n: rows.length }, rows.length) }}</SBadge>
      </h3>
    </template>
    <SHint size="xs" class="mb-2">{{ t('pluginInstall.newRiskHint') }}</SHint>
    <ul class="space-y-1.5">
      <li v-for="r in rows" :key="r.id" class="flex flex-wrap items-center gap-2 rounded-lg bg-danger-50 px-3 py-2 text-sm dark:bg-danger-900/20" :data-new-risk="r.id">
        <RiskDot :risk="r.risk" />
        <SBadge tone="danger">{{ t('plugins.consent.diffAdded') }}</SBadge>
        <code class="font-mono text-xs">{{ r.id }}</code>
        <span>{{ hpLabel(r.id) }}</span>
        <span class="ml-auto text-xs font-semibold text-danger-700 dark:text-danger-300">{{ t(`plugins.risk.${r.risk}`) }}</span>
      </li>
    </ul>
    <SCheckbox
      class="mt-3"
      :model-value="modelValue"
      :label="t('pluginInstall.ack')"
      data-testid="consent-ack"
      @update:model-value="v => emit('update:modelValue', v)"
    />
  </SCard>
</template>
