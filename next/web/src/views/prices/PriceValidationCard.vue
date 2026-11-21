<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SCard, SHint, SIcon, SLink, SSpinner } from '@sub2api/ui'
import { formatMoney } from '@/utils/format'
import { issueText, type Issue } from './issues'
import type { PriceValidation } from './usePriceValidation'

// "Check" card of the price editor: three steps (syntax, usage fields, trial
// run), what the price reads, the cost of one million input tokens and the
// warnings. Local problems of the per-token and per-request forms come first.
// PROTOTYPE: new texts are Chinese literals until adopted.
const props = defineProps<{ localIssues: string[]; validation: PriceValidation | null; error: string; validating: boolean; mode?: string }>()
const emit = defineEmits<{ (e: 'locate', line: number, column: number): void }>()
const { t } = useI18n()

type Obj = Exclude<Issue, string>
const SYNTAX = new Set([
  'syntax_error',
  'empty_expression',
  'unsupported_version',
  'literal_argument_required',
  'invalid_timezone',
  'negative_multiplier',
  'duplicate_tier',
  'no_tier',
  'invalid_config',
  'compile',
  'empty'
])
const FIELDS = new Set(['unknown_usage_fact'])
const step = (x: Issue) => {
  const code = typeof x === 'string' ? '' : String(x.code || '')
  return FIELDS.has(code) ? 'fields' : SYNTAX.has(code) || !code ? 'syntax' : 'run'
}

type Step = { key: 'syntax' | 'fields' | 'run'; label: string; state: 'ok' | 'fail' | 'skip'; text: string; issues: Obj[] }
const steps = computed<Step[] | null>(() => {
  const v = props.validation
  if (!v || props.localIssues.length || props.error) return null
  const by = (k: string) => v.errors.filter((x) => step(x) === k).map((x) => (typeof x === 'string' ? { message: x } : x))
  const syntax = by('syntax')
  const fields = by('fields')
  const run = by('run')
  const facts = v.analysis?.facts || []
  const blocked = syntax.length > 0
  return [
    { key: 'syntax', label: '语法', state: blocked ? 'fail' : 'ok', text: blocked ? '' : '表达式能编译', issues: syntax },
    {
      key: 'fields',
      label: '用量字段',
      state: blocked ? 'skip' : fields.length ? 'fail' : 'ok',
      text: blocked ? '语法正确后再检查' : fields.length ? '' : facts.length ? `插件都声明了：${facts.join('、')}` : '没有读用量字段',
      issues: fields
    },
    {
      key: 'run',
      label: '试跑',
      state: blocked || fields.length ? 'skip' : run.length ? 'fail' : 'ok',
      text: blocked || fields.length ? '前面通过后再试跑' : run.length ? '' : `${v.samples || 0} 组样例用量都算出了非负金额`,
      issues: run
    }
  ]
})

const pos = (x: Obj) => (typeof x.line === 'number' ? { line: x.line as number, column: (x.column as number) || 1 } : null)
const detail = (x: Obj) => (x.detail === undefined || x.detail === null || x.detail === '' ? '' : String(x.detail))

const reads = computed(() => {
  const a = props.validation?.analysis
  if (!a || !steps.value || steps.value[0].state !== 'ok' || props.mode === 'per_request') return []
  const rows: Array<{ label: string; items: string[]; mono?: boolean }> = [
    { label: 'token 变量', items: a.vars.filter((x) => x !== 'len'), mono: true },
    { label: '上下文长度', items: a.vars.includes('len') ? (a.len_boundaries.length ? a.len_boundaries.map((b) => `len ≤ ${b.toLocaleString('zh-CN')}`) : ['len']) : [], mono: true },
    { label: '请求参数', items: a.params, mono: true },
    { label: '请求头', items: a.headers, mono: true },
    { label: '用量字段', items: a.facts, mono: true },
    { label: '时区', items: a.zones || [], mono: true },
    { label: '档位', items: a.tiers, mono: true },
    { label: '加价规则', items: a.rules.length ? [`${a.rules.length} 条`] : [] }
  ]
  return rows.filter((r) => r.items.length)
})

const warnings = computed(() => (props.localIssues.length || props.error ? [] : props.validation?.warnings || []))
const ok = computed(() => !!steps.value && steps.value.every((s) => s.state === 'ok'))
</script>

<template>
  <SCard data-testid="price-check">
    <template #title>
      <span class="inline-flex items-center gap-2"
        >检查
        <span v-if="ok" class="rounded-full bg-success-50 px-2 py-px text-xs font-medium text-success-700 dark:bg-success-900/40 dark:text-success-300">可以保存</span>
        <span
          v-else-if="steps || localIssues.length || error"
          class="rounded-full bg-danger-50 px-2 py-px text-xs font-medium text-danger-700 dark:bg-danger-900/40 dark:text-danger-300"
          >不能保存</span
        >
      </span>
    </template>
    <template #actions><SSpinner v-if="validating" size="sm" /></template>

    <!-- local problems of the form modes -->
    <div v-if="localIssues.length" class="space-y-1">
      <SHint v-for="(x, i) in localIssues" :key="i" tone="danger" class="flex items-start gap-1">
        <SIcon name="x" class="mt-0.5 h-4 w-4 shrink-0" />{{ x }}
      </SHint>
    </div>
    <SHint v-else-if="error" tone="danger">{{ error }}</SHint>
    <SHint v-else-if="!steps" tone="muted">{{ t('prices.validate.pending') }}</SHint>

    <template v-else>
      <ol class="space-y-2.5">
        <li v-for="s in steps" :key="s.key" class="flex gap-2.5" :data-step="s.key" :data-state="s.state">
          <span
            class="mt-0.5 grid h-5 w-5 shrink-0 place-items-center rounded-full"
            :class="{
              'bg-success-100 text-success-700 dark:bg-success-900/50 dark:text-success-300': s.state === 'ok',
              'bg-danger-100 text-danger-700 dark:bg-danger-900/50 dark:text-danger-300': s.state === 'fail',
              'bg-surface-2 text-fg-subtle': s.state === 'skip'
            }"
          >
            <SIcon v-if="s.state !== 'skip'" :name="s.state === 'ok' ? 'check' : 'x'" class="h-3.5 w-3.5" />
            <span v-else class="h-1.5 w-1.5 rounded-full bg-current" />
          </span>
          <div class="min-w-0 flex-1 text-sm">
            <div class="flex flex-wrap items-baseline gap-x-2">
              <span class="font-medium" :class="s.state === 'skip' ? 'text-fg-subtle' : 'text-fg'">{{ s.label }}</span>
              <span v-if="s.text" class="text-xs text-fg-muted">{{ s.text }}</span>
            </div>
            <div v-for="(x, i) in s.issues" :key="i" class="mt-1 rounded-lg bg-danger-50 px-2.5 py-1.5 text-xs dark:bg-danger-950/40">
              <div class="flex flex-wrap items-baseline gap-x-2 text-danger-700 dark:text-danger-300">
                <span v-if="pos(x)" class="font-medium">第 {{ pos(x)!.line }} 行第 {{ pos(x)!.column }} 列</span>
                <span>{{ issueText(x) }}</span>
                <SLink v-if="pos(x)" as="button" class="ml-auto text-xs" @click="emit('locate', pos(x)!.line, pos(x)!.column)">定位</SLink>
              </div>
              <div v-if="detail(x)" class="mt-0.5 break-all font-mono text-danger-600/90 dark:text-danger-400/90">{{ detail(x) }}</div>
            </div>
          </div>
        </li>
      </ol>

      <div v-if="reads.length" class="mt-4 border-t border-line pt-3">
        <div class="mb-2 text-xs font-medium text-fg-muted">这个价格会读取</div>
        <dl class="grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1.5 text-xs">
          <template v-for="r in reads" :key="r.label">
            <dt class="pt-0.5 text-fg-muted">{{ r.label }}</dt>
            <dd class="flex flex-wrap gap-1">
              <span v-for="it in r.items" :key="it" class="rounded bg-surface-2 px-1.5 py-0.5 text-fg" :class="r.mono ? 'font-mono' : ''">{{ it }}</span>
            </dd>
          </template>
        </dl>
      </div>

      <div
        v-if="validation?.cost_per_million_input !== undefined && validation?.cost_per_million_input !== null && ok && mode !== 'per_request'"
        class="mt-3 flex items-baseline justify-between rounded-lg bg-surface-2 px-3 py-2 text-xs"
      >
        <span class="text-fg-muted">100 万输入 token 一次</span>
        <span class="font-mono text-sm font-semibold text-fg">{{ formatMoney(validation.cost_per_million_input) }}</span>
      </div>
    </template>

    <div v-if="warnings.length" class="mt-3 space-y-1">
      <SHint v-for="(x, i) in warnings" :key="i" tone="warning" class="flex items-start gap-1">
        <SIcon name="warning" class="mt-0.5 h-4 w-4 shrink-0" />{{ issueText(x) }}
      </SHint>
    </div>
  </SCard>
</template>
