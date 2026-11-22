<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SHint, SInput, SSegmented, type SelectOption } from '@sub2api/ui'
import type { PriceDef, PriceRule } from '@/api/pricing'
import PriceDefEditor from '@/views/prices/PriceDefEditor.vue'
import type { PriceDraft } from '@/views/prices/priceDraft'
import { applyRule, rateText, sameRate, timesRate } from './pricingFormat'
import { customRuleOf, priceDraftOfRule, ruleIssues, sameRule, startRule } from './ruleDrafts'
import { priceLabel } from './ruleText'

// The price of one model in a group, over its original price `base`: the
// original price, the original price × N, or a price of its own written as an
// original price is (visual or expression, PriceDefEditor). A line under it
// says what the model then costs and what that is at `rate`. Every edit emits
// a whole new rule, valid or not: one that is not says why, and the table
// that holds it refuses to save it. A model without an original price takes
// no rule.
type Kind = 'none' | PriceRule['kind']

const props = defineProps<{ modelValue: PriceRule | null; base: PriceDef | null; rate: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: PriceRule | null): void }>()
const { t } = useI18n()

const kind = computed<Kind>(() => props.modelValue?.kind ?? 'none')
/** Without a global price a rule cannot be saved: it may only be cleared. */
const locked = computed(() => props.disabled || !props.base)
const options = computed<SelectOption[]>(() => (['none', 'multiplier', 'custom'] as const).map((value) => ({ value, label: t(`pricing.rule.kind.${value}`) })))
const issues = computed(() => ruleIssues(props.modelValue).map((x) => t(x.key, x.params ?? {})))

/** The custom price as its editor holds it; it is edited in place, and each edit goes out as a rule. */
const price = ref<PriceDraft | null>(null)
/**
 * The rule emitted last. The table answers an edit that prices like the
 * saved rule with the saved rule itself: neither that nor the rule coming
 * back is a reason to start the editor over.
 */
let emitted: PriceRule | null = null
watch(
  () => props.modelValue,
  (rule) => {
    if (emitted && (rule === emitted || (rule && sameRule(rule, emitted)))) return
    emitted = null
    price.value = rule?.kind === 'custom' ? priceDraftOfRule(rule) : null
  },
  { immediate: true }
)
watch(
  price,
  (draft) => {
    const rule = draft && customRuleOf(draft)
    if (rule && !sameRule(rule, props.modelValue) && !sameRule(rule, emitted)) send(rule)
  },
  { deep: true }
)

/** The multiplier as typed: "1.0" stays so while the rule reads 1. */
const typed = ref('')
const multiplier = computed(() => (props.modelValue?.kind === 'multiplier' && !sameRate(typed.value, props.modelValue.multiplier) ? props.modelValue.multiplier : typed.value))
function typeMultiplier(v: unknown) {
  typed.value = String(v ?? '').trim()
  send({ kind: 'multiplier', multiplier: typed.value })
}

function send(rule: PriceRule | null) {
  emitted = rule
  emit('update:modelValue', rule)
}
function choose(next: Kind) {
  if (next === kind.value || !props.base) return
  const rule = next === 'none' ? null : startRule(next, props.base)
  price.value = rule?.kind === 'custom' ? priceDraftOfRule(rule) : null
  send(rule)
}

const listPrice = computed(() => applyRule(props.base, props.modelValue))
const preview = computed(() => {
  const list = listPrice.value
  const paid = list && timesRate(list, props.rate)
  return t('pricing.rule.preview', { price: priceLabel(t, list), rate: rateText(props.rate || null), paid: paid ? priceLabel(t, paid) : '—' })
})
</script>

<template>
  <div class="flex flex-col items-stretch gap-2.5 text-left" data-testid="price-rule-editor">
    <SSegmented
      class="self-start"
      :model-value="kind"
      :options="options"
      size="sm"
      :disabled="locked"
      :aria-label="t('pricing.rule.mode')"
      data-testid="price-rule-kind"
      @update:model-value="choose($event as Kind)"
    />
    <SAlert v-if="!base" tone="warning" dense data-testid="price-rule-no-base">{{ t('pricing.rule.noBase') }}</SAlert>

    <div v-if="modelValue?.kind === 'multiplier'" class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <SInput
        :model-value="multiplier"
        size="sm"
        mono
        inputmode="decimal"
        class="w-40"
        :error="issues.length > 0"
        :disabled="locked"
        :aria-label="t('pricing.rule.kind.multiplier')"
        data-testid="price-rule-multiplier"
        @update:model-value="typeMultiplier"
      >
        <template #prefix><span class="whitespace-nowrap text-xs text-fg-subtle">{{ t('pricing.rule.timesPrefix') }}</span></template>
      </SInput>
      <SHint inline size="xs">{{ t('pricing.rule.multiplierHint') }}</SHint>
    </div>

    <template v-if="price">
      <SHint size="xs">{{ t('pricing.rule.customHint') }}</SHint>
      <PriceDefEditor v-model="price" :readonly="locked" :issues="issues" />
    </template>

    <SHint v-if="issues.length" size="xs" tone="danger" data-testid="price-rule-issue">{{ issues[0] }}</SHint>
    <SHint v-else-if="base" size="xs" data-testid="price-rule-preview">{{ preview }}</SHint>
  </div>
</template>
