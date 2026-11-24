<script setup lang="ts">
// The consent page's source check of a market install: the official store
// and channel it comes from, the publisher and its trust level, the release,
// size, the package's full sha256 (the core checks it against the index) and
// whether this core satisfies the version's host range (CONTRACTS §136).
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SDescItem, SDescriptions, SIcon, SLink } from '@sub2api/ui'
import type { MarketSource, MarketVersion } from '@/api/marketTypes'
import type { PluginReview } from '@/api/types'
import { formatBytes } from '@/utils/format'
import { releaseTag } from '../marketUtil'
import TrustBadge from '../parts/TrustBadge.vue'

defineProps<{ review: PluginReview; channel: MarketSource | null; version?: MarketVersion }>()
const { t } = useI18n()
</script>

<template>
  <SCard class="mt-4" data-testid="consent-source-check">
    <template #title>
      <h3 class="flex items-center gap-1.5 text-sm font-semibold">
        <SIcon name="shield" class="h-4 w-4 text-emerald-600 dark:text-emerald-400" />{{ t('market.origin.title') }}
      </h3>
    </template>
    <SDescriptions :columns="3" dense stacked>
      <SDescItem :label="t('market.origin.source')">
        {{ channel ? t('market.origin.sourceValue', { channel: channel.name, branch: channel.branch }) : t('market.storeTitle') }}
      </SDescItem>
      <SDescItem :label="t('market.origin.publisher')">
        <span class="inline-flex items-center gap-1.5">{{ review.publisher || '—' }}<TrustBadge :trust="review.trust" /></span>
      </SDescItem>
      <SDescItem :label="t('market.origin.release')">
        <SLink v-if="version?.release_url" :href="version.release_url" external>{{ releaseTag(version.release_url) }}</SLink>
        <span v-else>—</span>
      </SDescItem>
      <SDescItem :label="t('market.origin.size')" :value="version?.size ? formatBytes(version.size) : '—'" />
      <SDescItem :label="t('market.origin.hostCompat')">
        <span class="inline-flex items-center gap-1.5">
          <code class="font-mono text-xs">{{ review.host_compat || '—' }}</code>
          <SBadge :tone="review.host_compat_ok ? 'success' : 'danger'" data-testid="consent-host-compat">
            {{ review.host_compat_ok ? t('market.origin.satisfied') : t('market.origin.unsatisfied') }}
          </SBadge>
        </span>
      </SDescItem>
      <SDescItem v-if="review.package_sha256" :label="t('market.origin.sha256')" :value="review.package_sha256" mono copyable full />
    </SDescriptions>
  </SCard>
</template>
