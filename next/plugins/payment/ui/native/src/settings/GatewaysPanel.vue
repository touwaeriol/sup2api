<script setup lang="ts">
// Gateways tab: Epay and Stripe credentials. Secrets come back as "******"
// (left empty = kept); the callback addresses are this site's webhook route.
import { computed } from 'vue'
import { SBadge, SChip, SCopyButton, SInput, SSecretInput, SSettingRow, SSettingsGroup, SSwitch } from '@sub2api/ui'
import { usePaymentHost, type GatewayKey, type PaymentMethod } from '../host'
import { bindSwitch, STRIPE_EVENTS, webhookUrl, type SettingsForm } from './settingsForm'

const props = defineProps<{ form: SettingsForm }>()
const host = usePaymentHost()
const t = host.t

const urls = { epay: webhookUrl(host.plugin.key, 'epay'), stripe: webhookUrl(host.plugin.key, 'stripe') }

/** How many methods each gateway serves, for its header. */
const usage = computed(() => {
  const n: Record<GatewayKey, number> = { epay: 0, stripe: 0 }
  for (const m of props.form.get<PaymentMethod[]>('methods') ?? []) n[m.gateway]++
  return n
})

const on = (g: GatewayKey) => !!props.form.get(`${g}.enabled`)
</script>

<template>
  <SSettingsGroup :title="t('gateway.epay')" :description="t('settings.gateways.epay.help')" data-testid="payment-gateway-epay">
    <template #actions>
      <SBadge :tone="on('epay') ? 'success' : 'gray'" size="xs">{{ on('epay') ? t('settings.gateways.on') : t('settings.gateways.off') }}</SBadge>
      <SBadge size="xs">{{ t('settings.gateways.usage', { count: usage.epay }) }}</SBadge>
    </template>
    <SSettingRow prop="epay.enabled" :label="t('settings.gateways.enabled')" :description="t('settings.gateways.epay.enabledHelp')">
      <SSwitch v-bind="bindSwitch(form, 'epay.enabled')" />
    </SSettingRow>
    <SSettingRow prop="epay.endpoint" :label="t('settings.gateways.epay.endpoint')" :description="t('settings.gateways.epay.endpointHelp')">
      <SInput v-bind="form.bind('epay.endpoint')" type="url" placeholder="https://pay.example.com" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow prop="epay.merchant_id" :label="t('settings.gateways.epay.merchant')">
      <SInput v-bind="form.bind('epay.merchant_id')" mono placeholder="1001" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow prop="epay.key" :label="t('settings.gateways.epay.key')" :description="t('settings.gateways.epay.keyHelp')">
      <SSecretInput v-bind="form.bind('epay.key')" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow :label="t('settings.gateways.epay.notify')" :description="t('settings.gateways.epay.notifyHelp')" stacked>
      <div class="payment-url">
        <SInput :model-value="urls.epay" readonly mono :aria-label="t('settings.gateways.epay.notify')" />
        <SCopyButton :text="urls.epay" icon-only :label="t('action.copy')" />
      </div>
    </SSettingRow>
  </SSettingsGroup>

  <SSettingsGroup :title="t('gateway.stripe')" :description="t('settings.gateways.stripe.help')" data-testid="payment-gateway-stripe">
    <template #actions>
      <SBadge :tone="on('stripe') ? 'success' : 'gray'" size="xs">{{ on('stripe') ? t('settings.gateways.on') : t('settings.gateways.off') }}</SBadge>
      <SBadge size="xs">{{ t('settings.gateways.usage', { count: usage.stripe }) }}</SBadge>
    </template>
    <SSettingRow prop="stripe.enabled" :label="t('settings.gateways.enabled')" :description="t('settings.gateways.stripe.enabledHelp')">
      <SSwitch v-bind="bindSwitch(form, 'stripe.enabled')" />
    </SSettingRow>
    <SSettingRow prop="stripe.secret_key" :label="t('settings.gateways.stripe.secretKey')" :description="t('settings.gateways.stripe.secretKeyHelp')">
      <SSecretInput v-bind="form.bind('stripe.secret_key')" placeholder="sk_live_…" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow prop="stripe.webhook_secret" :label="t('settings.gateways.stripe.webhookSecret')" :description="t('settings.gateways.stripe.webhookSecretHelp')">
      <SSecretInput v-bind="form.bind('stripe.webhook_secret')" placeholder="whsec_…" class="payment-control-wide" />
    </SSettingRow>
    <SSettingRow :label="t('settings.gateways.stripe.webhook')" stacked>
      <template #description>
        {{ t('settings.gateways.stripe.webhookHelp') }}
        <span class="payment-events">
          <SChip v-for="e in STRIPE_EVENTS" :key="e" size="xs"><code class="payment-code">{{ e }}</code></SChip>
        </span>
      </template>
      <div class="payment-url">
        <SInput :model-value="urls.stripe" readonly mono :aria-label="t('settings.gateways.stripe.webhook')" />
        <SCopyButton :text="urls.stripe" icon-only :label="t('action.copy')" />
      </div>
    </SSettingRow>
  </SSettingsGroup>
</template>
