<script setup lang="ts">
// The codex plugin settings (manifest ui.settings.component), shown in the
// plugin's settings tab: the client headers of the accounts that set none of
// their own (GET / PUT /plugins/codex/settings through usePluginSettings).
// The plugin checks them again (ValidateSettings), as an account's own.
import { SInput, SSettingRow, SSettingsGroup, SSettingsPage, usePluginSettings } from '@sub2api/ui'
import { useCodexHost } from './host'
import { SETTINGS_DEFAULTS, SETTINGS_RULES, type CodexSettingsValue } from './settings'

const host = useCodexHost()
const t = host.t
const settings = usePluginSettings<CodexSettingsValue>({ pluginKey: host.plugin.key, api: host.api, rules: SETTINGS_RULES, defaults: SETTINGS_DEFAULTS })
</script>

<template>
  <SSettingsPage :settings="settings">
    <SSettingsGroup>
      <SSettingRow prop="user_agent" :label="t('settings.userAgent')" :description="t('settings.userAgentHint')">
        <SInput v-bind="settings.bind('user_agent')" mono autocomplete="off" spellcheck="false" placeholder="sup2api-codex/<version>" data-testid="codex-settings-user-agent" />
      </SSettingRow>
      <SSettingRow prop="originator" :label="t('settings.originator')" :description="t('settings.originatorHint')">
        <SInput v-bind="settings.bind('originator')" mono autocomplete="off" spellcheck="false" placeholder="sup2api" data-testid="codex-settings-originator" />
      </SSettingRow>
    </SSettingsGroup>
  </SSettingsPage>
</template>
