<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SField, SHint, SInput } from '@sub2api/ui'
import { inheritedRate, rateText } from '@/components/pricing/pricingFormat'
import { validDiscount } from './segmentForm'

// The discount of a segment: one number on the group rate of every group.
// The example beside it follows what is typed.
const props = defineProps<{ modelValue: string; error?: string; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
const { t } = useI18n()

/** The group rate in the example. */
const EXAMPLE_RATE = '1.2'

const example = computed(() => {
  const discount = props.modelValue.trim()
  if (!validDiscount(discount)) return ''
  return t('segments.discount.example', { group: rateText(EXAMPLE_RATE), rate: rateText(inheritedRate({ group_default: EXAMPLE_RATE, discount })) })
})
</script>

<template>
  <SField :label="t('segments.discount.label')" required :hint="t('segments.discount.hint')" :error="error">
    <div class="flex flex-wrap items-center gap-3">
      <SInput :model-value="modelValue" class="w-36" mono inputmode="decimal" :disabled="disabled" data-testid="segment-discount" @update:model-value="emit('update:modelValue', String($event ?? ''))">
        <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
      </SInput>
      <SHint v-if="example" inline size="xs" data-testid="segment-discount-example">{{ example }}</SHint>
    </div>
  </SField>
</template>
