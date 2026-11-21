<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint, SInput } from '@sub2api/ui'
import type { EffectiveRate } from '@/api/pricing'
import { validOverride } from './accessRateDrafts'
import { inheritedRate } from './pricingFormat'

// The rate of a segment in a group, compact enough for a table cell. Empty
// (null) follows the group's rate times the segment's discount, and the
// placeholder says to what; a value replaces both. A value that is no rate
// says so underneath.
const props = defineProps<{
  modelValue: string | null
  rate: Pick<EffectiveRate, 'group_default' | 'discount'>
  disabled?: boolean
  ariaLabel?: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string | null): void }>()
const { t } = useI18n()

const valid = computed(() => validOverride(props.modelValue))
</script>

<template>
  <div class="inline-flex flex-col gap-0.5 text-left" data-testid="rate-override">
    <SInput
      :model-value="modelValue ?? ''"
      size="sm"
      mono
      inputmode="decimal"
      clearable
      class="w-36"
      :error="!valid"
      :disabled="disabled"
      :placeholder="t('pricing.rate.follow', { rate: inheritedRate(rate) })"
      :aria-label="ariaLabel || t('pricing.rate.label')"
      data-testid="rate-override-input"
      @update:model-value="emit('update:modelValue', String($event ?? '').trim() || null)"
    >
      <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
    </SInput>
    <SHint v-if="!valid" size="xs" tone="danger" class="leading-4" data-testid="rate-override-invalid">{{ t('pricing.rate.invalid') }}</SHint>
  </div>
</template>
