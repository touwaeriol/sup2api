<script setup lang="ts">
// Gateways tab: Epay and Stripe credentials. Secrets come back as "******"
// (left empty = kept); the callback addresses are this site's webhook route.
import { computed } from 'vue'
import { SBadge, SChip, SCopyButton, SInput, SSecretInput, SSettingRow, SSettingsGroup, SSwitch } from '@sub2api/ui'
import { useTopup, type GatewayKey, type PaymentMethod } from '../topup'
import { bindSwitch, STRIPE_EVENTS, notifyUrl, type SettingsForm } from './settingsForm'

const props = defineProps<{ form: SettingsForm }>()
const host = useTopup()
const t = host.t

const urls = { epay: notifyUrl('epay'), stripe: notifyUrl('stripe') }

/** How many methods each gateway serves, for its header. */
const usage = computed(() => {
  const n: Record<GatewayKey, number> = { epay: 0, stripe: 0 }
  for (const m of props.form.get<PaymentMethod[]>('methods') ?? []) n[m.gateway]++
  return n
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
      <div class="payment-url">
        <SInput :model-value="urls.epay" readonly mono :aria-label="t('topup.settings.gateways.epay.notify')" />
        <SCopyButton :text="urls.epay" icon-only :label="t('topup.action.copy')" />
      </div>
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
      <div class="payment-url">
        <SInput :model-value="urls.stripe" readonly mono :aria-label="t('topup.settings.gateways.stripe.webhook')" />
        <SCopyButton :text="urls.stripe" icon-only :label="t('topup.action.copy')" />
      </div>
    </SSettingRow>
  </SSettingsGroup>
</template>
