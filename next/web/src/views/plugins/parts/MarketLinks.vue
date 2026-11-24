<script setup lang="ts">
// The publisher line of a store plugin: publisher name, homepage, repository and source code links, separated by dots (CONTRACTS §136).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SLink } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import { publisherLabel, repoURL } from '../marketUtil'

const props = defineProps<{ plugin: MarketPlugin; withPublisher?: boolean }>()
const { t } = useI18n()

const links = computed(() => {
  const m = props.plugin
  const all = [
    { id: 'homepage', href: m.publisher_homepage, text: t('market.homepage') },
    { id: 'repo', href: repoURL(m), text: `${t('market.repo')} ${m.repo}` },
    { id: 'source', href: m.source_url, text: t('market.sourceCode') }
  ]
  return all.filter(l => !!l.href)
})
</script>

<template>
  <span v-if="withPublisher">{{ t('market.publisherLabel', { name: publisherLabel(plugin) }) }}</span>
  <template v-for="(l, i) in links" :key="l.id">
    <span v-if="withPublisher || i > 0" aria-hidden="true"> · </span>
    <SLink :href="l.href" external :data-testid="`market-${l.id}`">{{ l.text }}</SLink>
  </template>
</template>
