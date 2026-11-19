<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SDescItem, SDescriptions } from '@sub2api/ui'
import { formatNumber } from '@/utils/format'
import type { UsageTokenFacts } from './usage'

const props = defineProps<{ tokens: UsageTokenFacts }>()
const { t } = useI18n()
const base = { input_tokens: 'p', output_tokens: 'c', cache_read_tokens: 'cr', cache_creation_tokens: 'cc', cache_creation_1h_tokens: 'cc1h' } as const
// The image share of input and output (CONTRACTS §118), shown only when an image endpoint reported it.
const image = { image_input_tokens: 'img', image_output_tokens: 'img_o' } as const
const fields = computed<Partial<Record<keyof UsageTokenFacts, string>>>(() => ({
  ...base,
  ...Object.fromEntries(Object.entries(image).filter(([field]) => Number(props.tokens[field as keyof UsageTokenFacts]) > 0))
}))
</script>

<template>
  <SDescriptions dense divided label-width="min(14rem, 62%)">
    <template
      v-for="(label, field) in fields"
      :key="field"
    >
      <SDescItem :label="label === 'cc' ? t('usage.tokens.cacheDefaultBucket') : label === 'cc1h' ? t('usage.tokens.cache1hBucket') : t(`prices.vars.${label}`)">{{ tokens[field] == null ? '—' : formatNumber(tokens[field]!) }}</SDescItem>
    </template>
  </SDescriptions>
</template>
