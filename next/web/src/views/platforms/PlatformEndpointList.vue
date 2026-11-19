<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SCode, SHint } from '@sub2api/ui'
import PlatformBadge from '@/components/platform/PlatformBadge.vue'
import { endpointLine, type ListedEndpoint } from './listedEndpoint'

// The endpoints of one platform under its brand badge. `max` limits how many
// are listed; `known` is false for a platform the list does not have;
// `accounts` (by protocol) adds how many accounts serve each endpoint.
defineProps<{ id: string; endpoints: ListedEndpoint[]; known?: boolean; max?: number; accounts?: Partial<Record<string, number>> }>()
const { t } = useI18n()
</script>

<template>
  <div :data-platform="id">
    <PlatformBadge :id="id" />
    <ul v-if="endpoints.length" class="mt-1 space-y-0.5 pl-1">
      <li
        v-for="e in max ? endpoints.slice(0, max) : endpoints"
        :key="endpointLine(e)"
        class="flex flex-wrap items-center gap-1.5 text-xs"
      >
        <SCode inline class="min-w-0 break-words" :text="endpointLine(e)" />
        <SHint v-if="e.billing === 'free'" inline size="xs">({{ t('platforms.free') }})</SHint>
        <SHint v-if="accounts?.[e.protocol] !== undefined" inline size="xs" class="ml-auto" data-testid="endpoint-accounts">{{ t('platforms.servedAccounts', { n: accounts[e.protocol] }) }}</SHint>
      </li>
      <li v-if="max && endpoints.length > max">
        <SHint inline size="xs">{{ t('platforms.moreEndpoints', { n: endpoints.length - max }) }}</SHint>
      </li>
    </ul>
    <SHint v-else size="xs" class="mt-1 pl-1">{{ known ? t('platforms.noEndpoints') : t('platforms.unknownEndpoints') }}</SHint>
  </div>
</template>
