<script setup lang="ts">
// A store plugin's page: its head with the latest version's upgrade or
// install, every version with its own action, and its publisher (CONTRACTS
// §136, §137). Reached from the market list by publisher and key.
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SEmpty, SPageHeader, SSpinner } from '@sub2api/ui'
import { lt } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useMarket } from './useMarket'
import { rememberedChannel } from './marketChannels'
import MarketPluginHeader from './parts/MarketPluginHeader.vue'
import MarketVersionsTable from './parts/MarketVersionsTable.vue'
import MarketPublisherCard from './parts/MarketPublisherCard.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
/** The channel the market list was viewing (?channel=), else the viewer's last one. */
const market = useMarket(() => Number(route.query.channel) || rememberedChannel())
const { items, loading, loadingStore } = market

const plugin = computed(() => items.value.find(m => m.publisher === route.params.publisher && m.key === route.params.key) ?? null)
const title = computed(() => (plugin.value ? lt(plugin.value.name) || plugin.value.key : t('nav.crumbs.marketPlugin')))

/** Reviews the version first: nothing is downloaded or registered before the consent. */
function install(version: string) {
  if (plugin.value && version) router.push(market.installLocation(plugin.value, version))
}

onMounted(market.load)
</script>

<template>
  <div>
    <SPageHeader :title="title">
      <template v-if="market.channel.value && plugin" #title-extra>
        <SBadge tone="gray" data-testid="market-detail-channel">{{ t('market.detail.channel', { name: market.channel.value.name }) }}</SBadge>
      </template>
    </SPageHeader>

    <div v-if="loadingStore || loading" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!plugin">
      <SEmpty icon="market" :text="market.channel.value ? t('market.detail.notFound') : t('market.storeUnavailable')" />
    </SCard>

    <template v-else>
      <MarketPluginHeader
        :plugin="plugin"
        :installed-version="market.installedOf(plugin)"
        :state="market.stateOf(plugin)"
        :blocked="market.blockedOf(plugin)"
        :can-install="auth.has('plugin:install')"
        :can-open-installed="auth.has('plugin:read')"
        @install="install"
      />
      <div class="flex flex-wrap items-start gap-4">
        <MarketVersionsTable
          class="min-w-0 flex-[999_1_40rem]"
          :plugin="plugin"
          :installed-version="market.installedOf(plugin)"
          :can-install="auth.has('plugin:install')"
          @install="install"
        />
        <MarketPublisherCard :plugin="plugin" class="flex-[1_1_18rem]" />
      </div>
    </template>
  </div>
</template>
