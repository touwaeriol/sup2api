<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SSelect, type SelectOption } from '@sub2api/ui'
import type { Segment } from '@/api/pricing'
import { useSegmentsLookup } from './segmentLookup'

// Picks one named segment; the default segment says so. Personal segments are not offered: a user gets one
// through PersonalConvertDialog.
const props = defineProps<{ modelValue: number | null; disabled?: boolean; placeholder?: string; exclude?: number[] }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: number | null): void }>()
const { t } = useI18n()
const { segments } = useSegmentsLookup()

const label = (s: Segment): string => (s.is_default ? `${s.name} · ${t('segments.select.default')}` : s.name)

const options = computed<SelectOption[]>(() => segments.value.filter((s) => !props.exclude?.includes(s.id)).map((s) => ({ value: s.id, label: label(s) })))
</script>

<template>
  <SSelect
    :model-value="modelValue"
    :options="options"
    :placeholder="placeholder ?? t('segments.select.placeholder')"
    :disabled="disabled"
    data-testid="segment-select"
    @update:model-value="emit('update:modelValue', typeof $event === 'number' ? $event : null)"
  />
</template>
