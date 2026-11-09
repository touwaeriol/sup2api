<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SAlert, SPageHeader, STitleScope } from '@sub2api/ui'
import StickyView from '@/views/sticky/StickyView.vue'
import BillingSettingsCard from './BillingSettingsCard.vue'
import EnvironmentSettingsCard from './EnvironmentSettingsCard.vue'
import GatewaySettingsCard from './GatewaySettingsCard.vue'
import NotificationsSettingsCard from './NotificationsSettingsCard.vue'
import OffloadSettingsCard from './OffloadSettingsCard.vue'
import SchedulingPolicyView from './SchedulingPolicyView.vue'
import UpdateSourceCard from './UpdateSourceCard.vue'
import { useSettingsNav } from './settingsNav'

const { t } = useI18n()

// The page shown is ?tab=<key> (links such as /settings?tab=sticky keep
// working). The sidebar's settings view lists the pages (CONTRACTS §90).
const { activeTab: active, activeLabel, missing } = useSettingsNav()
</script>

<template>
  <div class="space-y-5">
    <SPageHeader :title="t('nav.items.settings')" :description="t('settings.description')" />
    <STitleScope :title="activeLabel" class="min-w-0" role="region" :aria-label="activeLabel" data-testid="settings-panel">
      <SAlert v-if="missing" tone="warning" class="mb-4" data-testid="settings-missing">{{ t('settings.missingTab') }}</SAlert>
      <BillingSettingsCard v-if="active === 'billing'" />
      <GatewaySettingsCard v-else-if="active === 'gateway'" />
      <EnvironmentSettingsCard v-else-if="active === 'environment'" />
      <NotificationsSettingsCard v-else-if="active === 'notifications'" />
      <SchedulingPolicyView v-else-if="active === 'failure'" />
      <StickyView v-else-if="active === 'sticky'" />
      <OffloadSettingsCard v-else-if="active === 'offload'" />
      <UpdateSourceCard v-else-if="active === 'updates'" />
    </STitleScope>
  </div>
</template>
