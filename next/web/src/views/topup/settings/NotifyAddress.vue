<script setup lang="ts">
// A gateway's callback address (the core gives it) to copy; without one
// (the site address is not https) a line saying so instead.
import { useI18n } from 'vue-i18n'
import { SCopyButton, SInput } from '@sub2api/ui'

defineProps<{ url: string; label: string }>()
const { t } = useI18n()
</script>

<template>
  <div v-if="url" class="flex w-full items-center gap-2">
    <SInput :model-value="url" readonly mono class="min-w-0 flex-1" :aria-label="label" />
    <SCopyButton :text="url" icon-only :label="t('common.copy')" />
  </div>
  <span v-else class="text-[0.8125rem] text-fg-muted" data-testid="payment-notify-https">{{ t('topup.settings.gateways.needHttps') }}</span>
</template>
