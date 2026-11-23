<script setup lang="ts">
// The consent page without a review: why, and the way on.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCard, SEmpty, SHint } from '@sub2api/ui'
import type { ConsentSource } from './consentSources'
import type { ReviewFailure } from './useConsentTarget'

const props = defineProps<{ failure: ReviewFailure | null; source: ConsentSource }>()
defineEmits<{ retry: []; back: [] }>()
const { t } = useI18n()

const text = computed(() => {
  if (props.failure?.kind === 'package_gone') return t('plugins.consent.packageGone', { version: props.source.version })
  return props.source.kind === 'registered' ? t('plugins.consent.notFound') : t('common.loadFailed')
})
</script>

<template>
  <SCard>
    <SEmpty v-if="failure?.kind === 'upload_missing'" icon="upload" :text="t('pluginInstall.uploadMissing')" data-testid="consent-unavailable">
      <SButton size="sm" variant="primary" @click="$emit('back')">{{ t('pluginInstall.backToList') }}</SButton>
    </SEmpty>
    <SEmpty v-else icon="warning" :text="text" data-testid="consent-unavailable">
      <SHint v-if="failure?.kind === 'error' && failure.message" size="xs" tone="danger">{{ failure.message }}</SHint>
      <SButton size="sm" @click="$emit('retry')">{{ t('common.refresh') }}</SButton>
      <!-- An install refused on review (a bad signature, an incompatible core) is left the way it came. -->
      <SButton v-if="source.kind !== 'registered'" size="sm" @click="$emit('back')">{{ t('common.back') }}</SButton>
    </SEmpty>
  </SCard>
</template>
