<script setup lang="ts">
// The market page's store card: the store picker (configured stores, by
// name), the channel switcher of that store (its enabled channels, the
// default one marked), the link to the store settings, refreshing, and the
// store and channel being viewed with when its index was generated and
// fetched (when the core says). No repository or index address shows here
// (CONTRACTS §136).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCard, SHint, SIcon, SSegmented, SSelect } from '@sub2api/ui'
import type { MarketListing, MarketSource, MarketStore } from '@/api/marketTypes'
import { formatDateTime } from '@/utils/format'
import { STORE_SETTINGS_PATH } from '../consent/paths'

const props = defineProps<{
  stores: MarketStore[]
  store: MarketStore
  /** The store's enabled channels. */
  channels: MarketSource[]
  channel: MarketSource | null
  listing: MarketListing
  loading: boolean
  /** The viewer may open the store settings. */
  canConfigure: boolean
}>()
const emit = defineEmits<{ selectStore: [id: number]; selectChannel: [id: number]; refresh: [] }>()
const { t } = useI18n()

const storeOptions = computed(() => props.stores.map(s => ({ value: s.id, label: s.name })))
const channelOptions = computed(() => props.channels.map(c => ({ value: c.id, label: c.is_default ? `${c.name} · ${t('market.defaultChannel')}` : c.name })))
</script>

<template>
  <SCard class="mb-4" data-testid="market-store">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-3">
      <SIcon name="market" class="h-5 w-5 text-primary-600 dark:text-primary-400" />
      <div class="w-52">
        <SSelect
          :model-value="store.id"
          :options="storeOptions"
          searchable
          :aria-label="t('market.storePick')"
          data-testid="market-store-pick"
          @update:model-value="v => emit('selectStore', Number(v))"
        />
      </div>
      <SSegmented
        v-if="channel"
        :model-value="channel.id"
        :options="channelOptions"
        size="sm"
        :aria-label="t('market.channelSwitch')"
        data-testid="market-channels"
        @update:model-value="v => emit('selectChannel', Number(v))"
      />
      <div class="ml-auto flex items-center gap-2">
        <SButton v-if="canConfigure" icon="settings" :to="STORE_SETTINGS_PATH" data-testid="market-store-settings">{{ t('market.storeSettings') }}</SButton>
        <SButton :loading="loading" icon="refresh" data-testid="market-refresh" @click="emit('refresh')">{{ t('common.refresh') }}</SButton>
      </div>
    </div>
    <div v-if="channel" class="mt-3 flex flex-wrap gap-x-4 gap-y-1" data-testid="market-viewing">
      <SHint size="xs">{{ t('market.viewing', { store: store.name, channel: channel.name }) }}</SHint>
      <SHint v-if="listing.generated_at" size="xs">{{ t('market.generatedAt', { time: formatDateTime(listing.generated_at) }) }}</SHint>
      <SHint v-if="listing.fetched_at" size="xs">{{ t('market.fetchedAt', { time: formatDateTime(listing.fetched_at) }) }}</SHint>
    </div>
  </SCard>
</template>
