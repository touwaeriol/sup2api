<script setup lang="ts">
// Gateways tab: Epay and Stripe credentials. Secrets come back as "******"
// (left empty = kept); the callback addresses are the core's (notify_urls).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SChip, SInput, SSecretInput, SSettingRow } from '@sub2api/ui'
import { BUILTIN_GATEWAYS, type GatewayKey } from '../topup'
import BuiltinGateway from './BuiltinGateway.vue'
import { STRIPE_EVENTS, type MethodModel } from './settingsForm'
import type { TopupSettingsForm } from './useTopupSettings'

const props = defineProps<{ form: TopupSettingsForm; notifyUrls: Record<GatewayKey, string> }>()
const { t } = useI18n()

/** How many methods each built-in gateway serves, for its header. */
const usage = computed(() => {
  const methods = props.form.get<MethodModel[]>('methods') ?? []
  return Object.fromEntries(BUILTIN_GATEWAYS.map(g => [g, methods.filter(m => m.gateway === g).length])) as Record<GatewayKey, number>
})
</script>

<template>
  <BuiltinGateway
    gateway="epay"
    :form="form"
    :usage="usage.epay"
    :notify-url="notifyUrls.epay"
    :notify-label="t('topup.settings.gateways.epay.notify')"
  >
    <SSettingRow
      prop="epay.endpoint"
      :label="t('topup.settings.gateways.epay.endpoint')"
      :description="t('topup.settings.gateways.epay.endpointHelp')"
    >
      <SInput v-bind="form.bind('epay.endpoint')" type="url" placeholder="https://pay.example.com" class="w-full sm:w-[22rem]" />
    </SSettingRow>
    <SSettingRow prop="epay.merchant_id" :label="t('topup.settings.gateways.epay.merchant')">
      <SInput v-bind="form.bind('epay.merchant_id')" mono placeholder="1001" class="w-full sm:w-[22rem]" />
    </SSettingRow>
    <SSettingRow prop="epay.key" :label="t('topup.settings.gateways.epay.key')" :description="t('topup.settings.gateways.epay.keyHelp')">
      <SSecretInput v-bind="form.bind('epay.key')" class="w-full sm:w-[22rem]" />
    </SSettingRow>
    <template #notify>{{ t('topup.settings.gateways.epay.notifyHelp') }}</template>
  </BuiltinGateway>

  <BuiltinGateway
    gateway="stripe"
    :form="form"
    :usage="usage.stripe"
    :notify-url="notifyUrls.stripe"
    :notify-label="t('topup.settings.gateways.stripe.webhook')"
  >
    <SSettingRow
      prop="stripe.secret_key"
      :label="t('topup.settings.gateways.stripe.secretKey')"
      :description="t('topup.settings.gateways.stripe.secretKeyHelp')"
    >
      <SSecretInput v-bind="form.bind('stripe.secret_key')" placeholder="sk_live_…" class="w-full sm:w-[22rem]" />
    </SSettingRow>
    <SSettingRow
      prop="stripe.webhook_secret"
      :label="t('topup.settings.gateways.stripe.webhookSecret')"
      :description="t('topup.settings.gateways.stripe.webhookSecretHelp')"
    >
      <SSecretInput v-bind="form.bind('stripe.webhook_secret')" placeholder="whsec_…" class="w-full sm:w-[22rem]" />
    </SSettingRow>
    <template #notify>
      {{ t('topup.settings.gateways.stripe.webhookHelp') }}
      <span class="mt-1.5 flex flex-wrap gap-1.5">
        <SChip v-for="e in STRIPE_EVENTS" :key="e" size="xs"
          ><code class="font-mono text-xs text-fg">{{ e }}</code></SChip
        >
      </span>
    </template>
  </BuiltinGateway>
</template>
