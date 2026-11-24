<script setup lang="ts">
// Badges of a store plugin: its publisher's trust level and status, built-in, an update, deprecated as a whole (CONTRACTS §136).
import { useI18n } from 'vue-i18n'
import { SBadge } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import { lt } from '@/i18n'
import TrustBadge from './TrustBadge.vue'

defineProps<{ plugin: MarketPlugin; upgrade?: boolean }>()
const { t } = useI18n()
</script>

<template>
  <TrustBadge v-if="plugin.trust" :trust="plugin.trust" />
  <SBadge v-if="plugin.publisher_status === 'revoked'" tone="danger" data-testid="market-publisher-revoked">{{ t('market.publisherRevoked') }}</SBadge>
  <SBadge v-else-if="plugin.publisher_status === 'missing'" tone="danger" data-testid="market-publisher-missing">{{ t('market.publisherMissing') }}</SBadge>
  <SBadge v-if="plugin.builtin" tone="info" data-testid="market-builtin">{{ t('market.builtin') }}</SBadge>
  <SBadge v-if="upgrade" tone="warning" data-testid="market-has-update">{{ t('market.hasUpdate') }}</SBadge>
  <SBadge v-if="plugin.deprecated" tone="warning" :title="lt(plugin.deprecated.reason)" data-testid="market-deprecated">{{ t('market.pluginDeprecated') }}</SBadge>
</template>
