<script setup lang="ts">
// Badges of a market plugin: its publisher's trust level and status, built-in, deprecated (CONTRACTS §136).
import { useI18n } from 'vue-i18n'
import { SBadge } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/types'
import { lt } from '@/i18n'
import TrustBadge from './TrustBadge.vue'

defineProps<{ plugin: MarketPlugin }>()
const { t } = useI18n()
</script>

<template>
  <TrustBadge v-if="plugin.trust" :trust="plugin.trust" />
  <SBadge v-if="plugin.publisher_status === 'revoked'" tone="danger" data-testid="market-publisher-revoked">{{
    t('plugins.market.publisherRevoked')
  }}</SBadge>
  <SBadge v-else-if="plugin.publisher_status === 'missing'" tone="danger" data-testid="market-publisher-missing">{{
    t('plugins.market.publisherMissing')
  }}</SBadge>
  <SBadge v-if="plugin.builtin" tone="primary" data-testid="market-builtin">{{ t('plugins.market.builtin') }}</SBadge>
  <SBadge v-if="plugin.deprecated" tone="warning" :title="lt(plugin.deprecated.reason)" data-testid="market-deprecated">{{
    t('plugins.market.deprecated')
  }}</SBadge>
</template>
