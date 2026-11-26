<script setup lang="ts">
// A gateway's callback address to copy, built from the core's notify_base;
// without it (the site address is not https) a line saying so instead.
import { computed } from 'vue'
import { SCopyButton, SInput } from '@sub2api/ui'
import { useTopup } from '../topup'
import { notifyUrl } from './settingsForm'

const props = defineProps<{ base: string; gateway: string; label: string }>()
const t = useTopup().t
const url = computed(() => notifyUrl(props.base, props.gateway))
</script>

<template>
  <div v-if="url" class="payment-url">
    <SInput :model-value="url" readonly mono :aria-label="label" />
    <SCopyButton :text="url" icon-only :label="t('topup.action.copy')" />
  </div>
  <span v-else class="payment-muted" data-testid="payment-notify-https">{{ t('topup.settings.gateways.needHttps') }}</span>
</template>
