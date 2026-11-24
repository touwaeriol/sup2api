<script setup lang="ts">
// The domains the plugin may reach through the core (the net host
// permission's scope), the ones this version adds marked. Rendered only when
// the review names domains.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SCard } from '@sub2api/ui'
import type { PluginReview } from '@/api/types'
import { egressDomains } from './egress'

const props = defineProps<{ review: PluginReview }>()
const { t } = useI18n()
const domains = computed(() => egressDomains(props.review))
</script>

<template>
  <SCard v-if="domains.length" :title="t('pluginInstall.egress')" :subtitle="t('pluginInstall.egressHint')" class="mt-4" data-testid="consent-egress">
    <div class="flex flex-wrap gap-1.5">
      <SBadge v-for="d in domains" :key="d.host" :tone="d.added ? 'success' : 'gray'" :data-domain="d.host">
        <span class="font-mono">{{ d.added ? '+ ' : '' }}{{ d.host }}</span>
        <span v-if="d.added">（{{ t('pluginInstall.egressNew') }}）</span>
      </SBadge>
    </div>
  </SCard>
</template>
