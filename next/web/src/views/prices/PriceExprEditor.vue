<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SDropdown, SHint, SIcon } from '@sub2api/ui'
import ExprCodeEditor from './ExprCodeEditor.vue'
import { formatExpr } from './exprSyntax'
import type { PriceValidation } from './usePriceValidation'

// The expression mode: a code box with line numbers, colours and the error
// position, a toolbar (examples, format, guide), one-click inserts and a
// one-line status under the box. The full reference (ExprGuide) is placed by
// PriceEditView when the editor asks for it.
// PROTOTYPE: new texts are Chinese literals until adopted.
const model = defineModel<string>({ required: true })
const props = defineProps<{ readonly?: boolean; error?: string; validation?: PriceValidation | null; validating?: boolean }>()
const { t } = useI18n()

const code = ref<InstanceType<typeof ExprCodeEditor>>()
const emit = defineEmits<{ (e: 'guide'): void }>()

const EXAMPLES: Array<{ key: string; label: string; src: string }> = [
  { key: 'single', label: '单一价格', src: 'tier("base", p*3 + c*15 + cr*0.3)' },
  {
    key: 'tiers',
    label: '长上下文两档',
    src: 'len <= 200000\n  ? tier("standard", p*3 + c*15 + cr*0.3)\n  : tier("long_context", p*6 + c*22.5 + cr*0.6)'
  },
  { key: 'priority', label: 'priority 整单 ×2', src: 'tier("base", p*2.5 + c*15)\n* (param("service_tier") == "priority" ? 2 : 1)' },
  { key: 'night', label: '夜间五折', src: 'tier("base", p*3 + c*15)\n* (hour("Asia/Shanghai") < 8 ? 0.5 : 1)' },
  { key: 'minimum', label: '最低收 0.001 美元', src: 'tier("base", max(p*3 + c*15, flat(0.001)))' }
]
const exampleActions = EXAMPLES.map((x) => ({ key: x.key, label: x.label }))
function pickExample(key: string) {
  const x = EXAMPLES.find((e) => e.key === key)
  if (x) model.value = x.src
}

const QUICK: Array<{ text: string; caret?: number; label?: string }> = [
  { text: 'p' },
  { text: 'c' },
  { text: 'cr' },
  { text: 'cc' },
  { text: 'cc1h' },
  { text: 'img' },
  { text: 'img_o' },
  { text: 'len' },
  { text: 'tier("base", )', caret: 13, label: 'tier()' },
  { text: 'flat()', caret: 5, label: 'flat()' },
  { text: 'param("")', caret: 7, label: 'param()' },
  { text: 'header("")', caret: 8, label: 'header()' },
  { text: 'u("")', caret: 3, label: 'u()' },
  { text: 'has(, "")', caret: 4, label: 'has()' },
  { text: 'hour("Asia/Shanghai")', label: 'hour()' },
  { text: 'max(, )', caret: 4, label: 'max()' },
  { text: '\n* ( ? 2 : 1)', caret: 4, label: '× 加价规则' }
]

/** The first error that carries a position. */
const located = computed(() => {
  for (const x of props.validation?.errors || []) {
    if (typeof x !== 'string' && typeof x.line === 'number') return { line: x.line as number, column: (x.column as number) || 1, x }
  }
  return null
})
const status = computed(() => {
  const v = props.validation
  if (!model.value.trim()) return { tone: 'muted', text: '还没有内容。可以从“示例”开始，或打开“使用说明”。' }
  if (props.validating && !v) return { tone: 'muted', text: '检查中…' }
  if (!v) return null
  if (v.errors.length) {
    const l = located.value
    return { tone: 'danger', text: l ? `第 ${l.line} 行第 ${l.column} 列有错误` : '有错误，见右侧“检查”' }
  }
  return { tone: 'success', text: '语法正确，试跑通过' }
})

function format() {
  model.value = formatExpr(model.value)
}
function focusAt(line: number, column: number) {
  code.value?.focusAt(line, column)
}
function insert(text: string, caret?: number) {
  code.value?.insert(text, caret)
}
defineExpose({ focusAt, insert })
</script>

<template>
  <div data-testid="price-expr-editor">
    <div class="mb-2 flex flex-wrap items-center gap-2">
      <span class="text-sm font-medium text-fg">{{ t('prices.expression') }}</span>
      <div v-if="!readonly" class="ml-auto flex flex-wrap items-center gap-2">
        <SDropdown :actions="exampleActions" width="14rem" @select="pickExample">
          <template #trigger="{ toggle, attrs }">
            <SButton size="sm" v-bind="attrs" @click.stop="toggle">示例<SIcon name="chevron-down" class="ml-1 h-3.5 w-3.5" /></SButton>
          </template>
        </SDropdown>
        <SButton size="sm" :disabled="!model.trim()" data-testid="expr-format" @click="format">格式化</SButton>
        <SButton size="sm" variant="primary" icon="info" data-testid="expr-guide-open" @click="emit('guide')">使用说明</SButton>
      </div>
    </div>

    <ExprCodeEditor ref="code" v-model="model" :rows="12" :readonly="readonly" :error-line="located?.line" :error-column="located?.column" :label="t('prices.expression')" />

    <div class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
      <span
        v-if="status"
        class="inline-flex items-center gap-1"
        :class="{
          'text-success-700 dark:text-success-400': status.tone === 'success',
          'text-danger-600 dark:text-danger-400': status.tone === 'danger',
          'text-fg-muted': status.tone === 'muted'
        }"
        data-testid="expr-status"
      >
        <SIcon v-if="status.tone !== 'muted'" :name="status.tone === 'success' ? 'check' : 'x'" class="h-3.5 w-3.5" />{{ status.text }}
        <button v-if="located" type="button" class="ml-1 underline underline-offset-2" @click="focusAt(located.line, located.column)">定位</button>
      </span>
      <SHint inline size="xs" class="ml-auto">单价按美元 / 百万 token 写：<code class="font-mono">p*3</code> 即输入每百万 3 美元。</SHint>
    </div>
    <SHint v-if="error" tone="danger" size="xs" class="mt-1">{{ error }}</SHint>

    <div v-if="!readonly" class="mt-3 flex flex-wrap items-center gap-1.5" data-testid="expr-quick">
      <span class="mr-1 text-xs text-fg-muted">插入</span>
      <button
        v-for="q in QUICK"
        :key="q.text"
        type="button"
        class="rounded-md border border-line bg-surface px-1.5 py-0.5 font-mono text-xs text-fg hover:border-primary-400 hover:text-primary-700 dark:hover:text-primary-300"
        @click="code?.insert(q.text, q.caret)"
      >
        {{ q.label || q.text }}
      </button>
    </div>
  </div>
</template>
