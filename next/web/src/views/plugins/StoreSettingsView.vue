<script setup lang="ts">
// The store settings (system settings): the one plugin store, the official
// registry repository, as a card with its repository and channels. The store
// is renamed, never added or deleted; its default channel cannot be disabled
// or deleted (CONTRACTS §137.6).
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SCard, SEmpty, SHint, SPageHeader, SSpinner } from '@sub2api/ui'
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

const store = ref<MarketStore | null>(null)
const channels = ref<MarketSource[]>([])
const loading = ref(true)
const renaming = ref(false)
/** The channel dialog: open, and the channel being renamed (null while adding one). */
const channelForm = ref({ open: false, channel: null as MarketSource | null })

async function load() {
  try {
    const list = await fetchStores()
    const official = list.find(s => s.official) ?? null
    channels.value = official ? await fetchChannels(official.id) : []
    store.value = official
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

const openChannel = (channel: MarketSource | null) => (channelForm.value = { open: true, channel })

onMounted(load)
</script>

<template>
  <div>
    <SPageHeader :title="t('market.settings.title')" :description="t('market.settings.description')" />

    <div v-if="loading" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!store">
      <SEmpty icon="market" :text="t('market.stores.empty')" />
    </SCard>

    <template v-else>
      <StoreSection
        :store="store"
        :channels="channels"
        :can-manage="canManage"
        @rename="renaming = true"
        @add-channel="openChannel(null)"
        @rename-channel="openChannel"
        @changed="load"
      />
      <SHint size="xs" class="mt-3">{{ t('market.settings.defaultHint') }}</SHint>
    </template>

    <StoreFormModal v-model:open="renaming" :store="store" @saved="load" />
    <ChannelFormModal v-model:open="channelForm.open" :store-id="store?.id ?? 0" :channel="channelForm.channel" @saved="load" />
  </div>
</template>
