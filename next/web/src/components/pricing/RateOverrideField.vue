<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint } from '@sub2api/ui'
import RateInput from './RateInput.vue'
import { rateText, validRate } from './pricingFormat'

// A rate that may be left to follow another, compact enough for a table
// cell: a user group's rate in an account group, or a model rate. Empty
// (null) follows `follow`, and the placeholder says so; a value replaces it.
// A value that is no rate says so underneath. `readonly` shows the rate, or
// `followText` while it follows.
const props = defineProps<{ modelValue: string | null; follow: string; disabled?: boolean; ariaLabel?: string; readonly?: boolean; followText?: string }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string | null): void }>()
const { t } = useI18n()

const valid = computed(() => props.modelValue === null || validRate(props.modelValue))
</script>

<template>
  <template v-if="readonly">
    <span v-if="modelValue !== null" class="font-mono" data-testid="rate-override-value">{{ rateText(modelValue) }}</span>
    <SHint v-else inline size="xs" data-testid="rate-override-value">{{ followText }}</SHint>
  </template>
  <div v-else class="inline-flex flex-col gap-0.5 text-left" data-testid="rate-override">
    <RateInput
      :model-value="modelValue"
      clearable
      class="w-32"
      :error="!valid"
      :disabled="disabled"
      :placeholder="t('pricing.rate.follow', { rate: validRate(follow) ? Number(follow) : '—' })"
      :aria-label="ariaLabel || t('pricing.rate.label')"
      data-testid="rate-override-input"
      @update:model-value="emit('update:modelValue', $event.trim() || null)"
    />
    <SHint v-if="!valid" size="xs" tone="danger" class="leading-4" data-testid="rate-override-invalid">{{ t('pricing.rate.invalid') }}</SHint>
  </div>
</template>
