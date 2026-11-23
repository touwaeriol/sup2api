<script setup lang="ts">
// The publisher line of a market plugin: publisher name, homepage, repository and source code links (CONTRACTS §136).
import { useI18n } from 'vue-i18n'
import { SLink } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/types'
import { publisherLabel, repoURL } from '../marketUtil'

defineProps<{ plugin: MarketPlugin }>()
const { t } = useI18n()
</script>

<template>
  <span>{{ t('plugins.publisher') }} {{ publisherLabel(plugin) }}</span>
  <template v-if="plugin.publisher_homepage">
    · <SLink :href="plugin.publisher_homepage" external>{{ t('plugins.market.homepage') }}</SLink>
  </template>
  <template v-if="repoURL(plugin)">
    · <SLink :href="repoURL(plugin)" external data-testid="market-repo">{{ t('plugins.market.repo') }} {{ plugin.repo }}</SLink>
  </template>
  <template v-if="plugin.source_url">
    · <SLink :href="plugin.source_url" external data-testid="market-source">{{ t('plugins.market.sourceCode') }}</SLink>
  </template>
</template>
