<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SHint, SSegmented, type SelectOption } from '@sub2api/ui'
import type { CustomPrice, PriceDef } from '@/api/pricing'
import PriceDefEditor from '@/views/prices/PriceDefEditor.vue'
import type { PriceDraft } from '@/views/prices/priceDraft'
import { applyRule, rateText, timesRate } from './pricingFormat'
import { customRuleOf, priceDraftOfRule, ruleIssues, sameRule, startRule } from './ruleDrafts'
import { priceLabel } from './ruleText'

// The model price of one model in a group, over its original price `base`:
// the original price, or a price of its own written as an original price is
// (visual or expression, PriceDefEditor). A line under it says what the model
// then costs and what that is at `rate`. Every edit emits a whole new price,
// valid or not: one that is not says why, and the table that holds it refuses
// to send it. A model without an original price takes no price.
type Kind = 'none' | 'custom'

const props = defineProps<{ modelValue: CustomPrice | null; base: PriceDef | null; rate: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: CustomPrice | null): void }>()
const { t } = useI18n()

const kind = computed<Kind>(() => (props.modelValue ? 'custom' : 'none'))
/** Without a global price a rule cannot be saved: it may only be cleared. */
const locked = computed(() => props.disabled || !props.base)
const options = computed<SelectOption[]>(() => (['none', 'custom'] as const).map((value) => ({ value, label: t(`pricing.rule.kind.${value}`) })))
const issues = computed(() => ruleIssues(props.modelValue).map((x) => t(x.key, x.params ?? {})))

/** The custom price as its editor holds it; it is edited in place, and each edit goes out as a rule. */
const price = ref<PriceDraft | null>(null)
/**
 * The rule emitted last. The table answers an edit that prices like the
 * saved rule with the saved rule itself: neither that nor the rule coming
 * back is a reason to start the editor over.
 */
let emitted: CustomPrice | null = null
watch(
  () => props.modelValue,
  (rule) => {
    if (emitted && (rule === emitted || (rule && sameRule(rule, emitted)))) return
    emitted = null
    price.value = rule ? priceDraftOfRule(rule) : null
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

function send(rule: CustomPrice | null) {
  emitted = rule
  emit('update:modelValue', rule)
}
function choose(next: Kind) {
  if (next === kind.value || !props.base) return
  const rule = next === 'none' ? null : startRule(props.base)
  price.value = rule && priceDraftOfRule(rule)
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

    <template v-if="price">
      <SHint size="xs">{{ t('pricing.rule.customHint') }}</SHint>
      <PriceDefEditor v-model="price" :readonly="locked" :issues="issues" />
    </template>

    <SHint v-if="issues.length" size="xs" tone="danger" data-testid="price-rule-issue">{{ issues[0] }}</SHint>
    <SHint v-else-if="base" size="xs" data-testid="price-rule-preview">{{ preview }}</SHint>
  </div>
</template>
