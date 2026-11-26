<script setup lang="ts">
// Gateways tab, below the built-in ones: the gateways plugins provide
// (GET /topup/gateways, loaded once by the page's gateway catalog). Their
// credentials are the plugin's settings, so each links there; each comes
// with its callback address. None installed: a pointer to the plugin market.
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SChip, SEmpty, SSettingRow, SSettingsGroup } from '@sub2api/ui'
import type { GatewayCatalog } from './gatewayCatalog'
import NotifyAddress from './NotifyAddress.vue'

defineProps<{ gateways: GatewayCatalog }>()
const { t } = useI18n()
</script>

<template>
  <SSettingsGroup
    v-for="g in gateways.plugins.value"
    :key="g.id"
    :title="g.name"
    :description="t('topup.settings.gateways.plugin.help', { plugin: g.plugin_name })"
    data-testid="payment-gateway-plugin"
  >
    <template #actions>
      <SBadge size="xs" tone="info">{{ t('topup.settings.gateways.plugin.badge') }}</SBadge>
      <SBadge :tone="g.ready ? 'success' : 'warning'" size="xs">{{
        g.ready ? t('topup.settings.gateways.plugin.ready') : t('topup.settings.gateways.plugin.down')
      }}</SBadge>
      <SBadge size="xs">{{ t('topup.settings.gateways.usage', { count: g.in_use }) }}</SBadge>
    </template>
    <SSettingRow :label="t('topup.settings.gateways.plugin.methods')" :description="t('topup.settings.gateways.plugin.methodsHelp')">
      <span class="mt-1.5 flex flex-wrap gap-1.5">
        <SChip v-for="m in g.methods" :key="m.type" size="xs"
          >{{ m.name }} <code class="font-mono text-xs text-fg">{{ m.type }}</code></SChip
        >
      </span>
    </SSettingRow>
    <SSettingRow :label="t('topup.settings.gateways.plugin.settings')" :description="t('topup.settings.gateways.plugin.settingsHelp')">
      <SButton size="sm" icon="settings" :to="`/plugins/${g.plugin_key}`">{{ t('topup.settings.gateways.plugin.open') }}</SButton>
    </SSettingRow>
    <SSettingRow :label="t('topup.settings.gateways.plugin.notify')" :description="t('topup.settings.gateways.plugin.notifyHelp')" stacked>
      <NotifyAddress :url="g.notify_url" :label="t('topup.settings.gateways.plugin.notify')" />
    </SSettingRow>
  </SSettingsGroup>
  <SSettingsGroup v-if="gateways.loaded.value && !gateways.plugins.value.length" :title="t('topup.settings.gateways.plugin.title')">
    <SEmpty size="sm" icon="puzzle" :text="t('topup.settings.gateways.plugin.empty')">
      <template #actions>
        <SButton size="sm" icon="puzzle" to="/market">{{ t('topup.settings.gateways.plugin.market') }}</SButton>
      </template>
    </SEmpty>
  </SSettingsGroup>
</template>
