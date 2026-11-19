<script setup lang="ts">
// The mailer's settings (manifest ui.settings.component), shown in the
// plugin's settings tab: GET / PUT /plugins/mailer/settings through
// usePluginSettings. The password is a stored secret: kept as "******",
// replaced when typed, cleared ('') with its button for a server without
// login.
import { computed } from 'vue'
import { SInput, SSecretInput, SSelect, SSettingRow, SSettingsGroup, SSettingsPage, usePluginSettings } from '@sub2api/ui'
import { useMailerHost } from './host'
import { DEFAULTS, PLACEHOLDERS, RULES, SECURITY, type MailerSettingsValue } from './settings'

const host = useMailerHost()
const t = host.t
const settings = usePluginSettings<MailerSettingsValue>({ pluginKey: host.plugin.key, api: host.api, rules: RULES, defaults: DEFAULTS })
const securityOptions = computed(() => SECURITY.map((value) => ({ value, label: t(`security.${value}`) })))
</script>

<template>
  <SSettingsPage :settings="settings">
    <SSettingsGroup>
      <SSettingRow prop="host" :label="t('host.label')" :description="t('host.help')">
        <SInput v-bind="settings.bind('host')" :placeholder="PLACEHOLDERS.host" mono data-testid="mailer-host" />
      </SSettingRow>
      <SSettingRow prop="port" :label="t('port.label')" :description="t('port.help')">
        <!-- A cleared number box sends no key: the plugin's default applies. -->
        <SInput type="number" v-bind="settings.bindNumber('port')" :min="1" :max="65535" data-testid="mailer-port" />
      </SSettingRow>
      <SSettingRow prop="security" :label="t('security.label')">
        <SSelect v-bind="settings.bind('security')" :options="securityOptions" data-testid="mailer-security" />
      </SSettingRow>
      <SSettingRow prop="username" :label="t('username.label')" :description="t('username.help')">
        <SInput v-bind="settings.bind('username')" autocomplete="off" data-testid="mailer-username" />
      </SSettingRow>
      <SSettingRow prop="password" :label="t('password.label')" :description="t('password.help')">
        <SSecretInput v-bind="settings.bind('password')" clearable data-testid="mailer-password" />
      </SSettingRow>
      <SSettingRow prop="from_address" :label="t('fromAddress.label')">
        <SInput v-bind="settings.bind('from_address')" :placeholder="PLACEHOLDERS.from_address" data-testid="mailer-from-address" />
      </SSettingRow>
      <SSettingRow prop="from_name" :label="t('fromName.label')">
        <SInput v-bind="settings.bind('from_name')" data-testid="mailer-from-name" />
      </SSettingRow>
      <SSettingRow prop="timeout_seconds" :label="t('timeout.label')" :description="t('timeout.help')">
        <SInput type="number" v-bind="settings.bindNumber('timeout_seconds')" :min="5" :max="25" :suffix="t('timeout.unit')" data-testid="mailer-timeout" />
      </SSettingRow>
    </SSettingsGroup>
  </SSettingsPage>
</template>
