<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SHint, SSegmented, type SelectOption } from '@sub2api/ui'
import type { CustomPrice, PriceDef } from '@/api/pricing'
import PriceDefEditor from '@/views/prices/PriceDefEditor.vue'
import type { PriceDraft } from '@/views/prices/priceDraft'
import { listPriceOf, rateText } from './pricingFormat'
import { customPriceOf, priceDraftOfCustom, priceIssues, samePrice, startPrice } from './pricingRows'
import { paidLabel, priceLabel } from './priceLabels'

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
/** Without a global price a model price cannot be saved: it may only be cleared. */
const locked = computed(() => props.disabled || !props.base)
const options = computed<SelectOption[]>(() => (['none', 'custom'] as const).map((value) => ({ value, label: t(`pricing.editor.kind.${value}`) })))
const issues = computed(() => priceIssues(props.modelValue).map((x) => t(x.key, x.params ?? {})))

/** The custom price as its editor holds it; it is edited in place, and each edit goes out as a model price. */
const price = ref<PriceDraft | null>(null)
/**
 * The model price emitted last. The table answers an edit that prices like the
 * saved one with the saved one itself: neither that nor the price coming
 * back is a reason to start the editor over.
 */
let emitted: CustomPrice | null = null
watch(
  () => props.modelValue,
  (custom) => {
    if (emitted && (custom === emitted || (custom && samePrice(custom, emitted)))) return
    emitted = null
    price.value = custom ? priceDraftOfCustom(custom) : null
  },
  { immediate: true }
)
watch(
  price,
  (draft) => {
    const custom = draft && customPriceOf(draft)
    if (custom && !samePrice(custom, props.modelValue) && !samePrice(custom, emitted)) send(custom)
  },
  { deep: true }
)

function send(custom: CustomPrice | null) {
  emitted = custom
  emit('update:modelValue', custom)
}
function choose(next: Kind) {
  if (next === kind.value || !props.base) return
  const custom = next === 'none' ? null : startPrice(props.base)
  price.value = custom && priceDraftOfCustom(custom)
  send(custom)
}

const preview = computed(() => {
  const list = listPriceOf(props.base, props.modelValue)
  return t('pricing.editor.preview', { price: priceLabel(t, list), rate: rateText(props.rate || null), paid: paidLabel(t, list, props.rate) })
})
</script>

<template>
  <div class="flex flex-col items-stretch gap-2.5 text-left" data-testid="model-price-editor-editor">
    <SSegmented
      class="self-start"
      :model-value="kind"
      :options="options"
      size="sm"
      :disabled="locked"
      :aria-label="t('pricing.editor.mode')"
      data-testid="model-price-editor-kind"
      @update:model-value="choose($event as Kind)"
    />
    <SAlert v-if="!base" tone="warning" dense data-testid="model-price-editor-no-base">{{ t('pricing.editor.noBase') }}</SAlert>

    <template v-if="price">
      <SHint size="xs">{{ t('pricing.editor.customHint') }}</SHint>
      <PriceDefEditor v-model="price" :readonly="locked" :issues="issues" />
    </template>

    <SHint v-if="issues.length" size="xs" tone="danger" data-testid="model-price-editor-issue">{{ issues[0] }}</SHint>
    <SHint v-else-if="base" size="xs" data-testid="model-price-editor-preview">{{ preview }}</SHint>
  </div>
</template>
