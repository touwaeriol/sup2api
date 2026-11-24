<script setup lang="ts">
// The publisher of a store plugin as the store index lists it: id, name,
// trust level, status, homepage, GitHub logins (when the core sends them) and
// what a plugin's identity is (CONTRACTS §137).
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SDescItem, SDescriptions, SLink } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import { publisherLabel } from '../marketUtil'
import TrustBadge from './TrustBadge.vue'

defineProps<{ plugin: MarketPlugin }>()
const { t } = useI18n()
const statusTone = (s: string) => (s === 'active' ? 'success' : 'danger')
</script>

<template>
  <SCard :title="t('market.publisher.title')" data-testid="market-publisher-card">
    <SDescriptions dense label-width="6rem">
      <SDescItem :label="t('market.publisher.id')" :value="plugin.publisher" mono />
      <SDescItem :label="t('market.publisher.name')" :value="publisherLabel(plugin)" />
      <SDescItem :label="t('market.publisher.trust')"><TrustBadge v-if="plugin.trust" :trust="plugin.trust" /><span v-else>—</span></SDescItem>
      <SDescItem :label="t('market.publisher.status')">
        <SBadge :tone="statusTone(plugin.publisher_status)">{{ t(`market.publisherStatus.${plugin.publisher_status}`) }}</SBadge>
      </SDescItem>
      <SDescItem v-if="plugin.publisher_homepage" :label="t('market.publisher.homepage')">
        <SLink :href="plugin.publisher_homepage" external>{{ plugin.publisher_homepage.replace(/^https?:\/\//, '') }}</SLink>
      </SDescItem>
      <SDescItem v-if="plugin.publisher_github?.length" :label="t('market.publisher.github')">
        <span class="flex flex-wrap gap-2">
          <SLink v-for="g in plugin.publisher_github" :key="g" :href="`https://github.com/${g}`" external>@{{ g }}</SLink>
        </span>
      </SDescItem>
      <SDescItem :label="t('market.publisher.identity')" :value="t('market.publisher.identityHint')" />
    </SDescriptions>
  </SCard>
</template>
