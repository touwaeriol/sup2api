<script setup lang="ts">
// Gateways tab: Epay and Stripe credentials. Secrets come back as "******"
// (left empty = kept); the callback addresses are this site's webhook route.
import { computed } from 'vue'
import { SBadge, SChip, SInput, SSecretInput, SSettingRow, SSettingsGroup, SSwitch } from '@sub2api/ui'
import { BUILTIN_GATEWAYS, useTopup, type GatewayKey, type PaymentMethod } from '../topup'
import NotifyAddress from './NotifyAddress.vue'
import { bindSwitch, STRIPE_EVENTS, type SettingsForm } from './settingsForm'

const props = defineProps<{ form: SettingsForm; notifyBase: string }>()
const host = useTopup()
const t = host.t

/** How many methods each built-in gateway serves, for its header. */
const usage = computed(() => {
  const methods = props.form.get<PaymentMethod[]>('methods') ?? []
  return Object.fromEntries(BUILTIN_GATEWAYS.map((g) => [g, methods.filter((m) => m.gateway === g).length])) as Record<GatewayKey, number>
})

const on = (g: GatewayKey) => !!props.form.get(`${g}.enabled`)
</script>

<template>
  <SSettingsGroup :title="t('topup.gateway.epay')" :description="t('topup.settings.gateways.epay.help')" data-testid="payment-gateway-epay">
    <template #actions>
      <SBadge :tone="on('epay') ? 'success' : 'gray'" size="xs">{{ on('epay') ? t('topup.settings.gateways.on') : t('topup.settings.gateways.off') }}</SBadge>
      <SBadge size="xs">{{ t('topup.settings.gateways.usage', { count: usage.epay }) }}</SBadge>
    </template>
    <SSettingRow prop="epay.enabled" :label="t('topup.settings.gateways.enabled')" :description="t('topup.settings.gateways.epay.enabledHelp')">
      <SSwitch v-bind="bindSwitch(form, 'epay.enabled')" />
    </SSettingRow>
    <SSettingRow prop="epay.endpoint" :label="t('topup.settings.gateways.epay.endpoint')" :description="t('topup.settings.gateways.epay.endpointHelp')">
      <SInput v-bind="form.bind('epay.endpoint')" type="url" placeholder="https://pay.example.com" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow prop="epay.merchant_id" :label="t('topup.settings.gateways.epay.merchant')">
      <SInput v-bind="form.bind('epay.merchant_id')" mono placeholder="1001" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow prop="epay.key" :label="t('topup.settings.gateways.epay.key')" :description="t('topup.settings.gateways.epay.keyHelp')">
      <SSecretInput v-bind="form.bind('epay.key')" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow :label="t('topup.settings.gateways.epay.notify')" :description="t('topup.settings.gateways.epay.notifyHelp')" stacked>
      <NotifyAddress :base="notifyBase" gateway="epay" :label="t('topup.settings.gateways.epay.notify')" />
    </SSettingRow>
  </SSettingsGroup>

  <SSettingsGroup :title="t('topup.gateway.stripe')" :description="t('topup.settings.gateways.stripe.help')" data-testid="payment-gateway-stripe">
    <template #actions>
      <SBadge :tone="on('stripe') ? 'success' : 'gray'" size="xs">{{ on('stripe') ? t('topup.settings.gateways.on') : t('topup.settings.gateways.off') }}</SBadge>
      <SBadge size="xs">{{ t('topup.settings.gateways.usage', { count: usage.stripe }) }}</SBadge>
    </template>
    <SSettingRow prop="stripe.enabled" :label="t('topup.settings.gateways.enabled')" :description="t('topup.settings.gateways.stripe.enabledHelp')">
      <SSwitch v-bind="bindSwitch(form, 'stripe.enabled')" />
    </SSettingRow>
    <SSettingRow prop="stripe.secret_key" :label="t('topup.settings.gateways.stripe.secretKey')" :description="t('topup.settings.gateways.stripe.secretKeyHelp')">
      <SSecretInput v-bind="form.bind('stripe.secret_key')" placeholder="sk_live_…" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow prop="stripe.webhook_secret" :label="t('topup.settings.gateways.stripe.webhookSecret')" :description="t('topup.settings.gateways.stripe.webhookSecretHelp')">
      <SSecretInput v-bind="form.bind('stripe.webhook_secret')" placeholder="whsec_…" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow :label="t('topup.settings.gateways.stripe.webhook')" stacked>
      <template #description>
        {{ t('topup.settings.gateways.stripe.webhookHelp') }}
        <span class="payment-events">
          <SChip v-for="e in STRIPE_EVENTS" :key="e" size="xs"><code class="payment-code">{{ e }}</code></SChip>
        </span>
      </template>
      <NotifyAddress :base="notifyBase" gateway="stripe" :label="t('topup.settings.gateways.stripe.webhook')" />
    </SSettingRow>
  </SSettingsGroup>
</template>
