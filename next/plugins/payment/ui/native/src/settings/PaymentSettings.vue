<script setup lang="ts">
// The payment settings, a page of the plugin's "Top-up & payment" menu
// (manifest ui.settings.page, so the plugin detail has no settings tab):
// general / methods / gateways tabs over one
// usePluginSettings form, saved together by SSettingsPage's bar
// (GET / PUT /plugins/payment/settings; secrets come back as "******").
import { computed, ref, watch } from 'vue'
import { SSettingsPage, STabs, usePluginSettings } from '@sub2api/ui'
import { usePaymentHost } from '../host'
import GatewaysPanel from './GatewaysPanel.vue'
import GeneralPanel from './GeneralPanel.vue'
import MethodsEditor from './MethodsEditor.vue'
import { SETTINGS_DEFAULTS, SETTINGS_TABS, settingsRules, tabOfPath, type SettingsModel, type SettingsTab } from './settingsForm'
import './settings.css'

const host = usePaymentHost()
const t = host.t
const settings = usePluginSettings<SettingsModel>({ pluginKey: host.plugin.key, api: host.api, rules: () => settingsRules(t), defaults: SETTINGS_DEFAULTS })
const tab = ref<SettingsTab>('general')

/** Errors shown per tab (rules and the server's field errors). */
const errorCount = computed(() => {
  const n: Record<SettingsTab, number> = { general: 0, methods: 0, gateways: 0 }
  for (const path of Object.keys(settings.errors)) n[tabOfPath(path)]++
  return n
})
const tabs = computed(() => SETTINGS_TABS.map((key) => ({ key, label: t(`settings.tabs.${key}`), badge: errorCount.value[key] || undefined })))

// A failed save with nothing wrong on this tab opens the first tab that has errors.
watch(errorCount, (n) => {
  if (n[tab.value]) return
  const first = SETTINGS_TABS.find((k) => n[k])
  if (first) tab.value = first
})
</script>

<template>
  <SSettingsPage :settings="settings" :title="t('settings.title')" :description="t('settings.description')">
    <STabs v-model="tab" :tabs="tabs" :aria-label="t('settings.title')">
      <template #default="{ active }">
        <GeneralPanel v-if="active === 'general'" :form="settings" />
        <MethodsEditor v-else-if="active === 'methods'" :form="settings" @go="tab = $event" />
        <GatewaysPanel v-else :form="settings" />
      </template>
    </STabs>
  </SSettingsPage>
</template>
