<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint, SInput } from '@sub2api/ui'
import { validRate } from './pricingFormat'

// A rate that may be left to follow another, compact enough for a table
// cell: a user group's rate in an account group, or a model rate. Empty
// (null) follows `follow`, and the placeholder says so; a value replaces it.
// A value that is no rate says so underneath.
const props = defineProps<{ modelValue: string | null; follow: string; disabled?: boolean; ariaLabel?: string }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string | null): void }>()
const { t } = useI18n()

const valid = computed(() => props.modelValue === null || validRate(props.modelValue))
</script>

<template>
  <div class="inline-flex flex-col gap-0.5 text-left" data-testid="rate-override">
    <SInput
      :model-value="modelValue ?? ''"
      size="sm"
      mono
      inputmode="decimal"
      clearable
      class="w-32"
      :error="!valid"
      :disabled="disabled"
      :placeholder="t('pricing.rate.follow', { rate: validRate(follow) ? Number(follow) : '—' })"
      :aria-label="ariaLabel || t('pricing.rate.label')"
      data-testid="rate-override-input"
      @update:model-value="emit('update:modelValue', String($event ?? '').trim() || null)"
    >
      <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
    </SInput>
    <SHint v-if="!valid" size="xs" tone="danger" class="leading-4" data-testid="rate-override-invalid">{{ t('pricing.rate.invalid') }}</SHint>
  </div>
</template>
