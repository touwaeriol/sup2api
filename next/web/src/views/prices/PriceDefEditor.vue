<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SSegmented, toast } from '@sub2api/ui'
import PriceExprEditor from './PriceExprEditor.vue'
import VisualPriceEditor from './VisualPriceEditor.vue'
import { sameExpr, visualOf } from './exprParse'
import { PRICE_MODES, type PriceMode, type VisualConfig } from './priceExpr'
import { pricePayload, type PriceDraft } from './priceDraft'
import type { PriceValidation } from './usePriceValidation'
import { editableVisualConfig } from './visualPrice'

// A price as one editor, the global price page's and a custom price's: the
// mode tabs over the visual editor or the expression editor, each keeping
// its own state while the other shows. Switching (design 2026-11-22 §0):
// into the expression mode with the expression the visual config generates,
// unless the price has a local problem (`issues`, of the price as sent: a
// blank value would be generated as 0); back into the visual mode with the
// visual config it left while the expression is still the one generated
// then (whitespace outside string literals aside), else with the config the
// expression was generated from (visualOf); an expression the generator did
// not write is dropped, with a toast, and the visual mode shows the config it
// had. A blank expression keeps the visual config. Edits the bound draft in place; another draft bound forgets the
// config left. Read-only, it switches and edits nothing.
const draft = defineModel<PriceDraft>({ required: true })
const props = defineProps<{
  readonly?: boolean
  /** The local problems of the price as sent (checkPrice), as shown. */
  issues: readonly string[]
  /** The expression's error from the last save. */
  error?: string
  validation?: PriceValidation | null
}>()
const emit = defineEmits<{ (e: 'guide'): void }>()
const { t } = useI18n()
const exprEditor = ref<InstanceType<typeof PriceExprEditor>>()

const modeOptions = computed(() => PRICE_MODES.map(m => ({ value: m, label: t(`prices.mode.${m}`) })))

/** The expression generated on the way into the expression mode, and the visual config it was generated from (the same objects: the cards keep their state). */
let left: { expression: string; config: VisualConfig } | null = null
watch(draft, () => (left = null))

function switchMode(next: PriceMode) {
  const d = draft.value
  if (next === d.mode) return
  if (next === 'expression') {
    if (props.issues.length) return toast(props.issues[0], 'error')
    left = { expression: pricePayload(d).expression, config: d.config }
    d.expression = left.expression
  } else if (left && sameExpr(d.expression, left.expression)) {
    d.config = left.config
  } else if (d.expression.trim()) {
    const cfg = visualOf(d.expression)
    if (cfg) d.config = editableVisualConfig(cfg)
    else {
      if (left) d.config = left.config
      d.expression = ''
      toast(t('prices.edit.notVisual'), 'warning')
    }
  }
  d.mode = next
}

defineExpose({
  switchMode,
  /** Puts the caret of the expression editor at a line and column. */
  focusAt: (line: number, column: number) => exprEditor.value?.focusAt(line, column),
  /** Inserts text at the caret of the expression editor. */
  insert: (text: string, caret?: number) => exprEditor.value?.insert(text, caret)
})
</script>

<template>
  <div class="space-y-5" data-testid="price-def-editor">
    <div>
      <SSegmented
        :model-value="draft.mode"
        block
        :options="modeOptions"
        :disabled="readonly"
        :aria-label="t('prices.billingMode')"
        data-testid="price-mode"
        @update:model-value="switchMode($event as PriceMode)"
      />
      <p class="mt-1.5 text-xs text-fg-muted">{{ t(`prices.edit.modeHint.${draft.mode}`) }}</p>
    </div>

    <KeepAlive>
      <VisualPriceEditor v-if="draft.mode === 'visual'" v-model="draft.config" :disabled="readonly" />
      <PriceExprEditor v-else ref="exprEditor" v-model="draft.expression" :readonly="readonly" :error="error" :validation="validation" @guide="emit('guide')" />
    </KeepAlive>
  </div>
</template>
