<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SDrawer, SSegmented, toast } from '@sub2api/ui'
import ExprGuide from './ExprGuide.vue'
import PriceExprEditor from './PriceExprEditor.vue'
import VisualPriceEditor from './VisualPriceEditor.vue'
import { sameExpr, visualOf } from './exprParse'
import { PRICE_MODES, type PriceMode } from './priceExpr'
import { pricePayload, type PriceDraft } from './priceDraft'
import type { PriceValidation } from './usePriceValidation'
import { editableVisualConfig } from './visualPrice'

// A price as one editor, the global price page's and a custom price's: the
// mode tabs over the visual editor or the expression editor, each keeping its
// own state while the other shows, and the expression guide in a drawer.
// Switching (design 2026-11-22 §0): into the expression mode with the
// expression the visual config generates, unless the price has a local
// problem (`issues`, of the price as sent: a blank value would be generated
// as 0); back into the visual mode on the visual config it left while the
// expression is still the one generated then (whitespace outside string
// literals aside), else on the config the expression was generated from
// (visualOf); an expression the generator did not write is dropped, with a
// toast, and the visual mode shows the config it had. A blank expression
// keeps the visual config. Edits the bound draft in place; another draft
// bound forgets the expression generated. Read-only, it switches and edits
// nothing, and the guide only reads.
const draft = defineModel<PriceDraft>({ required: true })
const props = defineProps<{
  readonly?: boolean
  /** The local problems of the price as sent (checkPrice), as shown. */
  issues: readonly string[]
  /** The expression's error from the last save. */
  error?: string
  validation?: PriceValidation | null
}>()
const { t } = useI18n()
const exprEditor = ref<InstanceType<typeof PriceExprEditor>>()

const modeOptions = computed(() => PRICE_MODES.map(m => ({ value: m, label: t(`prices.mode.${m}`) })))

/** The expression generated on the way into the expression mode; the visual config it was generated from stays on the draft. */
let leftExpr: string | null = null
watch(draft, () => (leftExpr = null))

/** Back into the visual mode on the config the expression was generated from; an expression the generator did not write is dropped. */
function readVisual(d: PriceDraft) {
  if (!d.expression.trim() || (leftExpr !== null && sameExpr(d.expression, leftExpr))) return
  const cfg = visualOf(d.expression)
  if (cfg) {
    d.config = editableVisualConfig(cfg)
  } else {
    d.expression = ''
    toast(t('prices.edit.notVisual'), 'warning')
  }
}

function switchMode(next: PriceMode) {
  const d = draft.value
  if (next === d.mode) return
  if (next === 'visual') readVisual(d)
  else if (props.issues.length) return toast(props.issues[0], 'error')
  else d.expression = leftExpr = pricePayload(d).expression
  d.mode = next
}

const guideOpen = ref(false)
/** A guide example replaces the whole expression. */
function guideReplace(src: string) {
  if (props.readonly) return
  draft.value.expression = src
  guideOpen.value = false
}

defineExpose({
  switchMode,
  /** Puts the caret of the expression editor at a line and column. */
  focusAt: (line: number, column: number) => exprEditor.value?.focusAt(line, column)
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
      <PriceExprEditor v-else ref="exprEditor" v-model="draft.expression" :readonly="readonly" :error="error" :validation="validation" @guide="guideOpen = true" />
    </KeepAlive>

    <SDrawer v-model:open="guideOpen" :title="t('prices.edit.guide')" width="lg">
      <ExprGuide :readonly="readonly" @insert="(s: string, c?: number) => exprEditor?.insert(s, c)" @replace="guideReplace" />
    </SDrawer>
  </div>
</template>
