<script setup lang="ts">
import { computed, ref } from 'vue'
import { SLink } from '@sub2api/ui'
import ExprCodeEditor from './ExprCodeEditor.vue'
import PricePresets from './PricePresets.vue'
import { formatExpr } from './exprSyntax'
import { tokenExpr } from './priceExpr'
import { PRESETS } from './tokenPrice'
import type { PriceValidation } from './usePriceValidation'

// The expression editor: preset templates, a short reference of the names it
// reads, and a code box with line numbers, colours and the error position.
// The full guide opens in a drawer (PriceEditView).
// PROTOTYPE: new texts are Chinese literals until adopted.
const model = defineModel<string>({ required: true })
const props = defineProps<{ readonly?: boolean; error?: string; validation?: PriceValidation | null }>()
const emit = defineEmits<{ (e: 'guide'): void }>()
const code = ref<InstanceType<typeof ExprCodeEditor>>()

const fromTiers = (key: string) => formatExpr(tokenExpr({ tiers: PRESETS[key](), rules: [] }))
const EXTRA: Record<string, string> = {
  priority: 'tier("base", p*2.5 + c*15)\n* (param("service_tier") == "priority" ? 2 : 1)',
  night: 'tier("base", p*3 + c*15)\n* (hour("Asia/Shanghai") < 8 ? 0.5 : 1)',
  minimum: 'tier("base", max(p*3 + c*15, flat(0.001)))'
}
const GROUPS = [
  { label: '固定价格', items: [{ key: 'single', label: '单一价格' }, { key: 'claude', label: 'Claude（含缓存）' }, { key: 'image', label: '图片模型' }] },
  { label: '阶梯', items: [{ key: 'claudeLong', label: 'Claude 长上下文' }, { key: 'gptLong', label: 'GPT 长上下文' }, { key: 'three', label: '三档' }] },
  { label: '进阶', items: [{ key: 'priority', label: 'priority 整单 ×2' }, { key: 'night', label: '夜间五折' }, { key: 'minimum', label: '最低收 $0.001' }] }
]
const pick = (key: string) => (model.value = EXTRA[key] ?? fromTiers(key))

const located = computed(() => {
  for (const x of props.validation?.errors || []) {
    if (typeof x !== 'string' && typeof x.line === 'number') return { line: x.line as number, column: (x.column as number) || 1 }
  }
  return null
})
const format = () => (model.value = formatExpr(model.value))
defineExpose({ focusAt: (l: number, c: number) => code.value?.focusAt(l, c), insert: (s: string, c?: number) => code.value?.insert(s, c) })
</script>

<template>
  <div class="space-y-4" data-testid="price-expr-editor">
    <PricePresets v-if="!readonly" :groups="GROUPS" @pick="pick" />

    <div class="space-y-3 rounded-xl border border-line p-3 sm:p-4">
      <div class="rounded-lg border border-line bg-surface-2/60 px-3 py-2 font-mono text-[11.5px] leading-5 text-fg-muted" data-testid="expr-reference">
        <div><span class="font-sans text-fg-subtle">变量：</span>len, p, c, cr, cc, cc1h, img, img_o</div>
        <div><span class="font-sans text-fg-subtle">函数：</span>tier(name, 费用), flat(美元), param(path), header(name), u(name), has(文本, 片段), hour(tz), weekday(tz), max, min, ceil, floor, abs</div>
        <div class="font-sans">
          单价按美元 / 百万 token 写，<code class="font-mono">flat(0.01)</code> 是每次 0.01 美元；取不到的 token 数和用量字段按 0 算。
          <SLink as="button" class="text-[11.5px]" data-testid="expr-guide-open" @click="emit('guide')">完整说明</SLink>
        </div>
      </div>

      <ExprCodeEditor ref="code" v-model="model" :rows="10" :readonly="readonly" :error-line="located?.line" :error-column="located?.column" label="计费表达式" />
      <div v-if="!readonly" class="flex items-center gap-3 text-xs">
        <SLink as="button" class="text-xs" :disabled="!model.trim()" data-testid="expr-format" @click="format">格式化</SLink>
        <span v-if="error" class="text-danger-700 dark:text-danger-300">{{ error }}</span>
      </div>
    </div>
  </div>
</template>
