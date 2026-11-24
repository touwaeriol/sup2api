<script setup lang="ts">
// Where an installed plugin's current version came from: built-in, the official store, a manual upload.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, type Tone } from '@sub2api/ui'
import type { PluginSummary } from '@/api/types'
import { pluginSource, type PluginSource } from '../marketUtil'

const props = defineProps<{ plugin: Pick<PluginSummary, 'signature_status' | 'builtin'> }>()
const { t } = useI18n()
const TONES: Record<PluginSource, Tone> = { builtin: 'info', market: 'primary', upload: 'gray' }
const source = computed(() => pluginSource(props.plugin))
</script>

<template>
  <SBadge :tone="TONES[source]" :title="plugin.builtin ? t('plugins.builtinHint') : undefined" :data-source="source">{{ t(`market.source.${source}`) }}</SBadge>
</template>
