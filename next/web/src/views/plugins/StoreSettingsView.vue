<script setup lang="ts">
// The store settings (system settings): every configured store, one card
// each with its repository and channels, and adding a third-party store
// (name, GitHub repository, the branch of its default channel). The official
// store cannot be deleted; a store's default channel cannot be disabled or
// deleted (CONTRACTS §136).
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCard, SEmpty, SHint, SPageHeader, SSpinner } from '@sub2api/ui'
import type { MarketSource, MarketStore } from '@/api/marketTypes'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { MARKET_MANAGE_PERM } from './marketChannels'
import { fetchChannels, fetchStores } from './useMarket'
import StoreSection from './store/StoreSection.vue'
import StoreFormModal from './store/StoreFormModal.vue'
import ChannelFormModal from './store/ChannelFormModal.vue'

const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has(MARKET_MANAGE_PERM))

const stores = ref<MarketStore[]>([])
/** Each store's channels, by store id. */
const channels = ref<Record<number, MarketSource[]>>({})
const loading = ref(true)
/** The store dialog: open, and the store being renamed (null while adding one). */
const storeForm = ref({ open: false, store: null as MarketStore | null })
/** The channel dialog: open, its store, and the channel being renamed (null while adding one). */
const channelForm = ref({ open: false, storeId: 0, channel: null as MarketSource | null })

async function load() {
  try {
    const list = await fetchStores()
    const lists = await Promise.all(list.map(s => fetchChannels(s.id)))
    channels.value = Object.fromEntries(list.map((s, i) => [s.id, lists[i]]))
    stores.value = list
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

const openStore = (store: MarketStore | null) => (storeForm.value = { open: true, store })
const openChannel = (storeId: number, channel: MarketSource | null) => (channelForm.value = { open: true, storeId, channel })

onMounted(load)
</script>

<template>
  <div>
    <SPageHeader :title="t('market.settings.title')" :description="t('market.settings.description')">
      <template v-if="canManage" #actions>
        <SButton variant="primary" icon="plus" data-testid="store-add" @click="openStore(null)">{{ t('market.stores.add') }}</SButton>
      </template>
    </SPageHeader>

    <div v-if="loading" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!stores.length">
      <SEmpty icon="market" :text="t('market.stores.empty')" />
    </SCard>

    <template v-else>
      <SHint size="xs" class="mb-3" data-testid="store-count">{{ t('market.stores.count', { n: stores.length }) }}</SHint>
      <div class="space-y-4">
        <StoreSection
          v-for="s in stores"
          :key="s.id"
          :store="s"
          :channels="channels[s.id] || []"
          :can-manage="canManage"
          @rename="openStore(s)"
          @add-channel="openChannel(s.id, null)"
          @rename-channel="c => openChannel(s.id, c)"
          @changed="load"
        />
      </div>
      <SHint size="xs" class="mt-3">{{ t('market.settings.defaultHint') }}</SHint>
    </template>

    <StoreFormModal v-model:open="storeForm.open" :store="storeForm.store" @saved="load" />
    <ChannelFormModal v-model:open="channelForm.open" :store-id="channelForm.storeId" :channel="channelForm.channel" @saved="load" />
  </div>
</template>
