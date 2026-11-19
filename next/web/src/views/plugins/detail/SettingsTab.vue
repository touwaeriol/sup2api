<script setup lang="ts">
import PluginComponent from '@/components/plugin/PluginComponent.vue'
import type { PluginSettings } from '../pluginUtil'

// Native settings without a page of their own: the component the plugin
// registered for them (ui.settings.component), which reads, checks and saves
// them itself (SSettingsPage over usePluginSettings, with its own save bar).
defineProps<{ pluginKey: string; settings: PluginSettings }>()
</script>

<template>
  <PluginComponent v-slot="{ component }" :plugin-key="pluginKey" :name="settings.component">
    <component :is="component" />
  </PluginComponent>
</template>
