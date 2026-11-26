<script setup lang="ts">
// One built-in gateway on the Gateways tab: its header (on or off, how many
// methods it serves), the on switch, its credential rows (the default slot)
// and the callback address the core gives, with what to tell the gateway
// about it (the `notify` slot).
import { useI18n } from 'vue-i18n'
import { SBadge, SSettingRow, SSettingsGroup, SSwitch } from '@sub2api/ui'
import type { GatewayKey } from '../topup'
import NotifyAddress from './NotifyAddress.vue'
import type { TopupSettingsForm } from './useTopupSettings'

const props = defineProps<{ gateway: GatewayKey; form: TopupSettingsForm; usage: number; notifyUrl: string; notifyLabel: string }>()
const { t } = useI18n()
const on = () => !!props.form.get(`${props.gateway}.enabled`)
</script>

<template>
  <SSettingsGroup
    :title="t(`topup.gateway.${gateway}`)"
    :description="t(`topup.settings.gateways.${gateway}.help`)"
    :data-testid="`payment-gateway-${gateway}`"
  >
    <template #actions>
      <SBadge :tone="on() ? 'success' : 'gray'" size="xs">{{
        on() ? t('topup.settings.gateways.on') : t('topup.settings.gateways.off')
      }}</SBadge>
      <SBadge size="xs">{{ t('topup.settings.gateways.usage', { count: usage }) }}</SBadge>
    </template>
    <SSettingRow
      :prop="`${gateway}.enabled`"
      :label="t('topup.settings.gateways.enabled')"
      :description="t(`topup.settings.gateways.${gateway}.enabledHelp`)"
    >
      <SSwitch v-bind="form.bind(`${gateway}.enabled`)" />
    </SSettingRow>
    <slot />
    <SSettingRow :label="notifyLabel" stacked>
      <template #description><slot name="notify" /></template>
      <NotifyAddress :url="notifyUrl" :label="notifyLabel" />
    </SSettingRow>
  </SSettingsGroup>
</template>
