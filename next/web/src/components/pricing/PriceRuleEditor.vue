<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SField, SHint, SInput, SSegmented, SStack, type SelectOption } from '@sub2api/ui'
import type { PriceDef, PriceRule } from '@/api/pricing'
import { applyRule, priceBrief } from './pricingFormat'
import { customFields, customPrices, customRule, shownFields, startRule, takesCustom, validRule } from './ruleDrafts'
import { priceLabel } from './ruleText'

// One price rule over the global price `base`: none (the price follows the
// level above, named by `inheritLabel`), a multiplier of the global price, or
// a price of its own in the global price's mode. A global price with tiers,
// rules or an expression takes a multiplier only; a model without a global
// price takes no rule at all.
// Every edit emits a whole new rule, valid or not: one that is not says so
// under its fields, and the table that holds it refuses to save it.
type Kind = 'inherit' | PriceRule['kind']

const props = defineProps<{
  modelValue: PriceRule | null
  base: PriceDef | null
  inheritLabel: string
  disabled?: boolean
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: PriceRule | null): void }>()
const { t } = useI18n()

const kind = computed<Kind>(() => props.modelValue?.kind ?? 'inherit')
const multiplierOnly = computed(() => !!props.base && !takesCustom(props.base))
const options = computed<SelectOption[]>(() => [
  { value: 'inherit', label: props.inheritLabel },
  { value: 'multiplier', label: t('pricing.rule.multiplier') },
  { value: 'custom', label: t('pricing.rule.custom'), disabled: multiplierOnly.value }
])
const valid = computed(() => validRule(props.modelValue))
/** The prices of a custom rule and those it shows; none while the rule is of another kind. */
const custom = computed(() => {
  const rule = props.modelValue
  return rule?.kind === 'custom' ? { rule, prices: customPrices(rule), keys: shownFields(rule, props.base), required: customFields(rule.mode).required } : null
})
const preview = computed(() => {
  const applied = applyRule(props.base, props.modelValue)
  return priceBrief(applied) ? t('pricing.rule.applied', { price: priceLabel(t, applied) }) : t('pricing.rule.appliedOpaque')
})
const customHint = computed(() => {
  const perRequest = custom.value?.rule.mode === 'per_request'
  if (valid.value) return perRequest ? t('pricing.rule.requestUnit') : t('pricing.rule.tokenUnit')
  return perRequest ? t('pricing.rule.requestInvalid') : t('pricing.rule.tokenInvalid')
})

/** Token prices are named as in the model price editor. */
const fieldLabel = (key: string) => (key === 'price' ? t('pricing.rule.requestPrice') : t(`prices.vars.${key}`))

function choose(next: Kind) {
  if (next === 'inherit') return emit('update:modelValue', null)
  if (props.base) emit('update:modelValue', startRule(next, props.base))
}
function setPrice(rule: Extract<PriceRule, { kind: 'custom' }>, key: string, price: number | null) {
  emit('update:modelValue', customRule(rule.mode, { ...customPrices(rule), [key]: price }))
}
</script>

<template>
  <div class="flex flex-col items-start gap-2 text-left" data-testid="price-rule-editor">
    <SSegmented
      :model-value="kind"
      :options="options"
      size="sm"
      :disabled="disabled || !base"
      :aria-label="t('pricing.rule.mode')"
      data-testid="price-rule-kind"
      @update:model-value="choose($event as Kind)"
    />
    <SAlert v-if="!base" tone="warning" dense data-testid="price-rule-no-base">{{ t('pricing.rule.noBase') }}</SAlert>
    <SHint v-else-if="multiplierOnly" size="xs" data-testid="price-rule-multiplier-only">{{ t('pricing.rule.multiplierOnly') }}</SHint>

    <div v-if="modelValue?.kind === 'multiplier'" class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <SInput
        :model-value="modelValue.multiplier"
        size="sm"
        mono
        inputmode="decimal"
        class="w-24"
        :error="!valid"
        :disabled="disabled"
        :aria-label="t('pricing.rule.multiplier')"
        data-testid="price-rule-multiplier"
        @update:model-value="emit('update:modelValue', { kind: 'multiplier', multiplier: String($event ?? '').trim() })"
      >
        <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
      </SInput>
      <SHint inline size="xs" :tone="valid ? 'muted' : 'danger'" data-testid="price-rule-preview">{{ valid ? preview : t('pricing.rule.multiplierInvalid') }}</SHint>
    </div>

    <template v-if="custom">
      <!-- One row where the editor has the width for it; the prices wrap where it has not. -->
      <SStack direction="row" :gap="2" wrap>
        <SField v-for="(key, i) in custom.keys" :key="key" size="sm" class="w-28" :label="fieldLabel(key)" :required="i < custom.required">
          <SInput
            :model-value="custom.prices[key]"
            type="number"
            min="0"
            step="any"
            mono
            :disabled="disabled"
            :data-testid="`price-rule-${key}`"
            @update:model-value="setPrice(custom.rule, key, $event as number | null)"
          />
        </SField>
      </SStack>
      <SHint size="xs" :tone="valid ? 'muted' : 'danger'" data-testid="price-rule-unit">{{ customHint }}</SHint>
    </template>
  </div>
</template>
