<script setup lang="ts">
// The plugin market: the official store and the channel being viewed (the
// viewer's last one, else the default), its plugins filtered by name,
// publisher and state, and each plugin's action: install, upgrade to the
// channel's latest version, or why it cannot (CONTRACTS §136, §137).
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { SButton, SCard, SEmpty, SFilterBar, SHint, SInput, SPageHeader, SSelect, SSpinner } from '@sub2api/ui'
import type { MarketPlugin } from '@/api/marketTypes'
import { useAuthStore } from '@/stores/auth'
import { OFFICIAL_PUBLISHER } from './pluginUtil'
import { matches, publisherOptions, stateMatches, type MarketStatusFilter } from './marketUtil'
import { useMarket } from './useMarket'
import { MARKET_MANAGE_PERM, rememberedChannel } from './marketChannels'
import { STORE_SETTINGS_PATH } from './consent/paths'
import StoreCard from './parts/StoreCard.vue'
import MarketRow from './parts/MarketRow.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const market = useMarket(rememberedChannel)
const { channel, listing, items, hostVersion, loading, loadingStore, enabledChannels } = market

const q = ref('')
/** Publisher filter ('' = all). */
const publisher = ref('')
const status = ref<MarketStatusFilter>('')

const publisherChoices = computed(() => [
  { value: '', label: t('market.allPublishers') },
  ...publisherOptions(items.value).map(o => (o.value === OFFICIAL_PUBLISHER ? { ...o, label: t('market.officialPublisher', { name: o.label }) } : o))
])
const statusChoices = computed(() =>
  (['', 'upgrade', 'installed', 'notInstalled'] as const).map(value => ({ value, label: t(`market.status.${value || 'all'}`) }))
)
const activeFilters = computed(() => [publisher.value, status.value].filter(Boolean).length)

const rows = computed(() => {
  const kw = q.value.trim().toLowerCase()
  return items.value.filter(
    m => (!publisher.value || m.publisher === publisher.value) && stateMatches(market.stateOf(m), status.value) && (!kw || matches(m, kw))
  )
})
const updatable = computed(() => items.value.filter(m => market.stateOf(m) === 'upgrade' && !market.blockedOf(m)).length)

/** Reviews the version first: nothing is downloaded or registered before the consent. */
function install(m: MarketPlugin, version: string) {
  if (version) router.push(market.installLocation(m, version))
}

const refresh = () => Promise.all([market.loadListing(), market.loadInstalled()])
onMounted(market.load)
</script>

<template>
  <div>
    <SPageHeader :title="t('market.title')" :description="t('market.description')" />

    <div v-if="loadingStore" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!channel">
      <SEmpty icon="market" :text="t('market.storeUnavailable')" />
    </SCard>

    <template v-else>
      <StoreCard :channels="enabledChannels" :channel="channel" :listing="listing" :loading="loading" @select="market.selectChannel" @refresh="refresh" />

      <SFilterBar :card="false" :active-count="activeFilters" class="mb-4">
        <template #primary>
          <SInput v-model="q" class="w-72" :placeholder="t('market.searchPlaceholder')" :aria-label="t('common.search')" />
        </template>
        <div class="w-52">
          <SSelect v-model="publisher" :options="publisherChoices" searchable :aria-label="t('market.publisherFilter')" data-testid="market-publisher-filter" />
        </div>
        <div class="w-44">
          <SSelect v-model="status" :options="statusChoices" :aria-label="t('market.statusFilter')" data-testid="market-status-filter" />
        </div>
        <template #end>
          <SHint v-if="hostVersion" size="xs" data-testid="market-summary">
            {{ t('market.hostVersion', { version: hostVersion }) }} · {{ t('market.updatable', { n: updatable }) }}
          </SHint>
          <SButton v-if="auth.has(MARKET_MANAGE_PERM)" size="sm" variant="ghost" icon="settings" :to="STORE_SETTINGS_PATH" data-testid="market-store-settings">
            {{ t('market.storeSettings') }}
          </SButton>
        </template>
      </SFilterBar>

      <div v-if="loading && !items.length" class="flex justify-center py-20"><SSpinner size="lg" /></div>

      <SCard v-else-if="!rows.length">
        <SEmpty icon="market" :text="q || publisher || status ? t('market.noMatch') : t('market.empty')" />
      </SCard>

      <SCard v-else :padded="false" :class="loading ? 'opacity-60' : ''">
        <SHint size="xs" class="border-b border-line px-5 py-2.5" data-testid="market-total">{{ t('market.total', { n: rows.length }) }}</SHint>
        <MarketRow
          v-for="m in rows"
          :key="m.id"
          :plugin="m"
          :channel="channel.id"
          :installed-version="market.installedOf(m)"
          :state="market.stateOf(m)"
          :blocked="market.blockedOf(m)"
          :can-install="auth.has('plugin:install')"
          @install="v => install(m, v)"
        />
      </SCard>
    </template>
  </div>
</template>
