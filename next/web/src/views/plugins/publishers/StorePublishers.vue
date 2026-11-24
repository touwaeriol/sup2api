<script setup lang="ts">
// The store publishers tab: read-only, the publishers of the official store's
// index (its default channel) with their trust, status, GitHub logins (when the
// core sends them), store plugins and installed plugins; changed by a pull
// request to the store repository (CONTRACTS §136, §137).
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SEmpty, SHint, SLink, STable, type TableColumn } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import type { PluginSummary } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import TrustBadge from '../parts/TrustBadge.vue'
import { fetchInstalled, fetchListing } from '../useMarket'
import { OFFICIAL_STORE_REPO } from '../marketChannels'
import { storePublishers } from './storePublishers'

const emit = defineEmits<{ count: [n: number] }>()
const { t } = useI18n()
const auth = useAuthStore()

const items = ref<MarketPlugin[]>([])
const installed = ref<Record<string, PluginSummary>>({})
const loading = ref(true)
const rows = computed(() => storePublishers(items.value, Object.values(installed.value)))
const columns = computed<TableColumn[]>(() => [
  { key: 'id', label: t('market.publisher.id'), mobile: 'title' },
  { key: 'name', label: t('market.publisher.name'), mobile: 'meta' },
  { key: 'trust', label: t('market.publisher.trust'), mobile: 'meta' },
  { key: 'status', label: t('market.publisher.status'), mobile: 'status' },
  { key: 'github', label: t('market.publisher.github'), mobile: 'meta' },
  { key: 'plugins', label: t('publishers.store.plugins'), align: 'right', mobile: 'meta' },
  { key: 'installed', label: t('publishers.store.installed'), align: 'right', mobile: 'meta' }
])

/** The official store's default channel (GET /market/plugins without a channel) and the plugins installed here. */
async function load() {
  try {
    const [listing, here] = await Promise.all([fetchListing(), auth.has('plugin:read') ? fetchInstalled() : {}])
    items.value = listing.data
    installed.value = here
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await load()
  emit('count', rows.value.length)
})
</script>

<template>
  <div>
    <p class="mb-3 text-sm text-fg-muted" data-testid="store-publishers-hint">
      {{ t('publishers.store.hint') }}
      <SLink :href="`https://github.com/${OFFICIAL_STORE_REPO}`" external class="font-mono">{{ OFFICIAL_STORE_REPO }}</SLink>
    </p>
    <STable :columns="columns" :rows="rows" :loading="loading" row-key="id" data-testid="store-publishers">
      <template #empty><SEmpty icon="market" :text="t('publishers.store.empty')" /></template>
      <template #cell-id="{ row }"><span class="font-mono">{{ row.id }}</span></template>
      <template #cell-trust="{ row }"><TrustBadge v-if="row.trust" :trust="row.trust" /><SHint v-else inline>—</SHint></template>
      <template #cell-status="{ row }">
        <SBadge :tone="row.status === 'active' ? 'success' : 'danger'" dot>{{ t(`market.publisherStatus.${row.status}`) }}</SBadge>
      </template>
      <template #cell-github="{ row }">
        <span v-if="row.github.length" class="flex flex-wrap gap-2">
          <SLink v-for="g in row.github" :key="g" :href="`https://github.com/${g}`" external>@{{ g }}</SLink>
        </span>
        <SHint v-else inline>—</SHint>
      </template>
    </STable>
  </div>
</template>
