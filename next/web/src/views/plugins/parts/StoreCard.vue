<script setup lang="ts">
// The market page's store card: the official store's repository, the channel
// switcher (the enabled channels, the default one marked), the branch and
// read-only index URL of the channel being viewed, when its index was
// generated and fetched (when the core says) and refreshing it (CONTRACTS §136).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCard, SField, SHint, SIcon, SInput, SLink, SSegmented } from '@sub2api/ui'
import type { MarketListing, MarketSource } from '@/api/marketTypes'
import { formatDateTime } from '@/utils/format'
import { OFFICIAL_STORE_REPO } from '../marketChannels'

const props = defineProps<{ channels: MarketSource[]; channel: MarketSource; listing: MarketListing; loading: boolean }>()
const emit = defineEmits<{ select: [id: number]; refresh: [] }>()
const { t } = useI18n()

const options = computed(() => props.channels.map(c => ({ value: c.id, label: c.is_default ? `${c.name} · ${t('market.defaultChannel')}` : c.name })))
</script>

<template>
  <SCard class="mb-4" data-testid="market-store">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-3">
      <div class="flex items-center gap-2">
        <SIcon name="market" class="h-5 w-5 text-primary-600 dark:text-primary-400" />
        <span class="font-semibold">{{ t('market.storeTitle') }}</span>
        <SLink :href="`https://github.com/${OFFICIAL_STORE_REPO}`" external class="font-mono text-sm" data-testid="market-repo">{{ OFFICIAL_STORE_REPO }}</SLink>
      </div>
      <SSegmented
        :model-value="channel.id"
        :options="options"
        size="sm"
        :aria-label="t('market.channelSwitch')"
        data-testid="market-channels"
        @update:model-value="v => emit('select', Number(v))"
      />
      <SButton class="ml-auto" :loading="loading" icon="refresh" data-testid="market-refresh" @click="emit('refresh')">{{ t('common.refresh') }}</SButton>
    </div>
    <div class="mt-3 flex flex-wrap items-end gap-4">
      <SField :label="t('market.branch')" class="w-40">
        <SInput :model-value="channel.branch" readonly class="font-mono" data-testid="market-branch" />
      </SField>
      <SField :label="t('market.storeURL')" class="min-w-0 flex-[1_1_26rem]">
        <SInput :model-value="channel.url" readonly class="font-mono" data-testid="market-store-url" />
      </SField>
      <div v-if="listing.generated_at || listing.fetched_at" class="flex flex-col gap-0.5 pb-2" data-testid="market-index-times">
        <SHint v-if="listing.generated_at" size="xs">{{ t('market.generatedAt', { time: formatDateTime(listing.generated_at) }) }}</SHint>
        <SHint v-if="listing.fetched_at" size="xs">{{ t('market.fetchedAt', { time: formatDateTime(listing.fetched_at) }) }}</SHint>
      </div>
    </div>
  </SCard>
</template>
