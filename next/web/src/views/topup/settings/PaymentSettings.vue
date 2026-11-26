<script setup lang="ts">
// System settings > Top-up settings (core, ?tab=topup): general / methods / gateways
// tabs over one settings form, saved together by SSettingsPage's bar
// (GET / PUT /topup/settings; secrets come back as "******"). Plugin
// gateways are listed on the gateways tab; their credentials are the
// plugin's own settings.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SSettingsPage, STabs } from '@sub2api/ui'
import GatewaysPanel from './GatewaysPanel.vue'
import { useGatewayCatalog } from './gatewayCatalog'
import PluginGateways from './PluginGateways.vue'
import GeneralPanel from './GeneralPanel.vue'
import MethodsEditor from './MethodsEditor.vue'
import { SETTINGS_TABS, settingsRules, tabOfPath, type SettingsTab } from './settingsForm'
import { useTopupSettings } from './useTopupSettings'

const { t } = useI18n()
const { settings, notifyUrls, currencies } = useTopupSettings(() => settingsRules(t))
const gateways = useGatewayCatalog(settings)
const tab = ref<SettingsTab>('general')

/** Errors shown per tab (rules and the server's field errors). */
const errorCount = computed(() => {
  const n: Record<SettingsTab, number> = { general: 0, methods: 0, gateways: 0 }
  for (const path of Object.keys(settings.errors)) n[tabOfPath(path)]++
  return n
})
const tabs = computed(() =>
  SETTINGS_TABS.map(key => ({ key, label: t(`topup.settings.tabs.${key}`), badge: errorCount.value[key] || undefined }))
)

// A failed save with nothing wrong on this tab opens the first tab that has errors.
watch(errorCount, n => {
  if (n[tab.value]) return
  const first = SETTINGS_TABS.find(k => n[k])
  if (first) tab.value = first
})
</script>

<template>
  <SSettingsPage :settings="settings">
    <STabs v-model="tab" :tabs="tabs" :aria-label="t('topup.settings.title')">
      <template #default="{ active }">
        <GeneralPanel v-if="active === 'general'" :form="settings" :currencies="currencies" />
        <MethodsEditor v-else-if="active === 'methods'" :form="settings" :gateways="gateways" @go="tab = $event" />
        <template v-else>
          <GatewaysPanel :form="settings" :notify-urls="notifyUrls" />
          <PluginGateways :gateways="gateways" />
        </template>
      </template>
    </STabs>
  </SSettingsPage>
</template>
