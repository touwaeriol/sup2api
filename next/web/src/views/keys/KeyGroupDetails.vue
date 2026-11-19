<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SBadge, SHint, SLink } from '@sub2api/ui'
import type { Group } from '@/api/types'
import GroupBadge from '@/components/platform/GroupBadge.vue'
import { rateText } from '@/components/pricing/pricingFormat'
import PlatformEndpointsPreview from '@/views/platforms/PlatformEndpointsPreview.vue'
import { hasOwnRate, myRate } from './myGroup'

// What a key of the group gets: the owner's rate there, models, platforms and
// their endpoints. The prices open in a new tab: the form around is unsaved.
defineProps<{ group: Group }>()
const { t } = useI18n()
</script>

<template>
  <div class="space-y-1.5 rounded-xl bg-surface-2 px-3 py-2 text-xs text-fg-muted" data-testid="key-group-details">
    <div class="flex flex-wrap items-center gap-1.5">
      <GroupBadge :name="group.name" :platforms="group.platforms" :rate="myRate(group)" />
      <SBadge v-if="group.exclusive" tone="accent" size="xs" data-testid="key-group-exclusive">{{ t('pricing.exclusive') }}</SBadge>
      <SLink class="ms-auto" :to="{ path: '/me/models', query: { group: group.id } }" target="_blank" data-testid="key-group-prices">{{ t('apikeys.groupPrices') }}</SLink>
    </div>
    <div v-if="hasOwnRate(group)" data-testid="key-group-own-rate">{{ t('apikeys.ownRate', { group: rateText(group.rate_multiplier), rate: rateText(myRate(group)) }) }}</div>
    <div>
      {{ t('apikeys.groupModels') }} · {{ t(group.model_filter_mode === 'blacklist' ? 'groups.blacklist' : 'groups.whitelist') }}:
      {{ group.model_allowlist?.length ? group.model_allowlist.join(', ') : t('common.unlimited') }}
    </div>
    <div v-if="group.description">{{ group.description }}</div>
    <template v-if="group.served_protocols">
      <SHint v-if="!group.served_protocols.length" tone="warning" size="xs">{{ t('platforms.keyNoPlatforms') }}</SHint>
      <div v-else>
        <div class="mb-1">{{ t('platforms.keyEndpoints') }}:</div>
        <PlatformEndpointsPreview :served="group.served_protocols" :max="3" />
      </div>
    </template>
  </div>
</template>
