<script setup lang="ts">
// Gateways tab, below the built-in ones: the gateways plugins provide
// (GET /topup/gateways). Their credentials are the plugin's settings, so
// each links there; the notify address is the core route like the
// built-in ones. None installed: a pointer to the plugin market.
import { onMounted, ref } from 'vue'
import { SBadge, SButton, SChip, SCopyButton, SEmpty, SInput, SSettingRow, SSettingsGroup } from '@sub2api/ui'
import { paymentApi, useTopup, type PluginGateway } from '../topup'
import { notifyUrl } from './settingsForm'

const host = useTopup()
const t = host.t
const gateways = ref<PluginGateway[]>([])
const loaded = ref(false)

onMounted(async () => {
  try {
    gateways.value = (await paymentApi.pluginGateways()) ?? []
  } catch {
    gateways.value = []
  } finally {
    loaded.value = true
  }
})
</script>

<template>
  <SSettingsGroup v-for="g in gateways" :key="g.id" :title="g.name" :description="t('topup.settings.gateways.plugin.help', { plugin: g.plugin_name })" data-testid="payment-gateway-plugin">
    <template #actions>
      <SBadge size="xs" tone="info">{{ t('topup.settings.gateways.plugin.badge') }}</SBadge>
      <SBadge :tone="g.ready ? 'success' : 'warning'" size="xs">{{ g.ready ? t('topup.settings.gateways.plugin.ready') : t('topup.settings.gateways.plugin.down') }}</SBadge>
      <SBadge size="xs">{{ t('topup.settings.gateways.usage', { count: g.in_use }) }}</SBadge>
    </template>
    <SSettingRow :label="t('topup.settings.gateways.plugin.methods')" :description="t('topup.settings.gateways.plugin.methodsHelp')">
      <span class="payment-events">
        <SChip v-for="m in g.methods" :key="m.type" size="xs">{{ m.name }} <code class="payment-code">{{ m.type }}</code></SChip>
      </span>
    </SSettingRow>
    <SSettingRow :label="t('topup.settings.gateways.plugin.settings')" :description="t('topup.settings.gateways.plugin.settingsHelp')">
      <SButton size="sm" icon="settings" :to="`/plugins/${g.plugin_key}`">{{ t('topup.settings.gateways.plugin.open') }}</SButton>
    </SSettingRow>
    <SSettingRow :label="t('topup.settings.gateways.plugin.notify')" :description="t('topup.settings.gateways.plugin.notifyHelp')" stacked>
      <div class="payment-url">
        <SInput :model-value="notifyUrl(g.id)" readonly mono :aria-label="t('topup.settings.gateways.plugin.notify')" />
        <SCopyButton :text="notifyUrl(g.id)" icon-only :label="t('topup.action.copy')" />
      </div>
    </SSettingRow>
  </SSettingsGroup>
  <SSettingsGroup v-if="loaded && !gateways.length" :title="t('topup.settings.gateways.plugin.title')">
    <SEmpty size="sm" icon="puzzle" :text="t('topup.settings.gateways.plugin.empty')">
      <template #actions>
        <SButton size="sm" icon="puzzle" to="/market">{{ t('topup.settings.gateways.plugin.market') }}</SButton>
      </template>
    </SEmpty>
  </SSettingsGroup>
</template>
