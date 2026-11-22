<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SLink } from '@sub2api/ui'
import ExprCodeEditor from './ExprCodeEditor.vue'
import GuideText from './GuideText.vue'
import PricePresets from './PricePresets.vue'
import { formatExpr } from './exprSyntax'
import { issuePos } from './issues'
import { visualExpr } from './priceExpr'
import { EXPR_EXAMPLES, EXPR_PRESET_GROUP, PRESETS, TIER_PRESET_GROUPS } from './presets'
import type { PriceValidation } from './usePriceValidation'

// The expression editor: preset templates, a short reference of the names it
// reads, and a code box with line numbers, colours and the error position.
// The full guide opens in a drawer (PriceDefEditor).
const model = defineModel<string>({ required: true })
const props = defineProps<{ readonly?: boolean; error?: string; validation?: PriceValidation | null }>()
const emit = defineEmits<{ (e: 'guide'): void }>()
const code = ref<InstanceType<typeof ExprCodeEditor>>()
const { t } = useI18n()

// A tier template as the expression it generates; an advanced one is an example of the guide.
const GROUPS = [...TIER_PRESET_GROUPS, EXPR_PRESET_GROUP]
const pick = (key: string) => (model.value = PRESETS[key] ? formatExpr(visualExpr({ tiers: PRESETS[key](), rules: [] })) : EXPR_EXAMPLES[key])

/** Where the first error that has a position points. */
const located = computed(() => (props.validation?.errors || []).map(issuePos).find(Boolean) ?? null)
const format = () => (model.value = formatExpr(model.value))
defineExpose({ focusAt: (l: number, c: number) => code.value?.focusAt(l, c), insert: (s: string, c?: number) => code.value?.insert(s, c) })
</script>

<template>
  <div class="space-y-4" data-testid="price-expr-editor">
    <PricePresets v-if="!readonly" :groups="GROUPS" @pick="pick" />

    <div class="space-y-3 rounded-xl border border-line p-3 sm:p-4">
      <div class="rounded-lg border border-line bg-surface-2/60 px-3 py-2 font-mono text-[11.5px] leading-5 text-fg-muted" data-testid="expr-reference">
        <div><span class="font-sans text-fg-subtle">{{ t('prices.exprEditor.vars') }}</span>len, p, c, cr, cc, cc1h, img, img_o, ai, ao</div>
        <div><span class="font-sans text-fg-subtle">{{ t('prices.exprEditor.fns') }}</span>{{ t('prices.exprEditor.fnList') }}</div>
        <div class="font-sans">
          <GuideText :text="t('prices.exprEditor.note')" />
          <SLink as="button" class="text-[11.5px]" data-testid="expr-guide-open" @click="emit('guide')">{{ t('prices.exprEditor.guide') }}</SLink>
        </div>
      </div>

      <ExprCodeEditor ref="code" v-model="model" :rows="10" :readonly="readonly" :error-line="located?.line" :error-column="located?.column" :label="t('prices.exprEditor.label')" />
      <div v-if="!readonly" class="flex items-center gap-3 text-xs">
        <SLink as="button" class="text-xs" :disabled="!model.trim()" data-testid="expr-format" @click="format">{{ t('prices.exprEditor.format') }}</SLink>
        <span v-if="error" class="text-danger-700 dark:text-danger-300">{{ error }}</span>
      </div>
    </div>
  </div>
</template>
