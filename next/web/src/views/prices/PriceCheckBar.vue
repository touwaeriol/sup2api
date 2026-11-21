<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SIcon, SLink, SSpinner } from '@sub2api/ui'
import { formatMoney } from '@/utils/format'
import { issuePos, issueText, type Issue } from './issues'
import type { PriceValidation } from './usePriceValidation'

// The check result as one line under the editor: passed (with the cost of
// one million input tokens), or each problem on a line of its own with its
// position (a link to it in the expression editor).
const props = defineProps<{ localIssues: string[]; validation: PriceValidation | null; error: string; validating: boolean; mode: string }>()
const emit = defineEmits<{ (e: 'locate', line: number, column: number): void }>()
const { t } = useI18n()

type Obj = Exclude<Issue, string>
type Line = { text: string; detail?: string; line?: number; column?: number }
const problems = computed<Line[]>(() => {
  if (props.localIssues.length) return props.localIssues.map((text) => ({ text }))
  if (props.error) return [{ text: props.error }]
  return (props.validation?.errors || []).map((x) => {
    const o: Obj = typeof x === 'string' ? { message: x } : x
    const detail = o.detail === undefined || o.detail === null || o.detail === '' ? undefined : String(o.detail)
    return { text: issueText(o), detail, ...issuePos(o) }
  })
})
const warnings = computed(() => (problems.value.length ? [] : (props.validation?.warnings || []).map(issueText)))
const ok = computed(() => !problems.value.length && !!props.validation)
const cost = computed(() => {
  const c = props.validation?.cost_per_million_input
  return ok.value && props.mode !== 'per_request' && c !== undefined && c !== null ? formatMoney(c) : ''
})
</script>

<template>
  <div class="rounded-lg border px-3 py-2 text-sm" aria-live="polite" :class="problems.length ? 'border-danger-200 bg-danger-50/60 dark:border-danger-900 dark:bg-danger-950/30' : 'border-line bg-surface-2'" data-testid="price-check">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <SSpinner v-if="validating" size="sm" />
      <span v-else-if="ok" class="inline-flex items-center gap-1 text-success-700 dark:text-success-400"><SIcon name="check" class="h-4 w-4" />{{ t('prices.check.ok') }}</span>
      <span v-else-if="problems.length" class="inline-flex items-center gap-1 font-medium text-danger-700 dark:text-danger-300"
        ><SIcon name="x" class="h-4 w-4" />{{ t('prices.check.problems', { n: problems.length }) }}</span
      >
      <span v-else class="text-fg-muted">{{ t('prices.check.checking') }}</span>
      <span v-if="cost" class="text-xs text-fg-muted">{{ t('prices.check.costPerMillion', { cost }) }}</span>
    </div>
    <ul v-if="problems.length" class="mt-1.5 space-y-1">
      <li v-for="(p, i) in problems" :key="i" class="flex flex-wrap items-baseline gap-x-2 text-xs text-danger-700 dark:text-danger-300">
        <span v-if="p.line" class="font-medium">{{ t('prices.check.position', { line: p.line, column: p.column }) }}</span>
        <span>{{ p.text }}</span>
        <span v-if="p.detail" class="font-mono text-danger-600/90 dark:text-danger-400/90">{{ p.detail }}</span>
        <SLink v-if="p.line && mode === 'expression'" as="button" class="text-xs" @click="emit('locate', p.line!, p.column!)">{{ t('prices.check.locate') }}</SLink>
      </li>
    </ul>
    <ul v-if="warnings.length" class="mt-1.5 space-y-1">
      <li v-for="(w, i) in warnings" :key="i" class="flex items-start gap-1 text-xs text-warning-700 dark:text-warning-400"><SIcon name="warning" class="mt-px h-3.5 w-3.5 shrink-0" />{{ w }}</li>
    </ul>
  </div>
</template>
