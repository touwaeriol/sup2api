<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SPageHeader } from '@sub2api/ui'
import UsageOverview from './UsageOverview.vue'
import type { UsageView } from './view'

// The overview menu items: all users' statistics, the caller's, or those of
// the accounts in the caller's reach (`account`, CONTRACTS §103.5).
const props = withDefaults(defineProps<{ view?: UsageView }>(), { view: 'self' })
const { t } = useI18n()
const PAGES: Record<UsageView, { summary: string; title: string; description: string }> = {
  all: { summary: '/usage/summary', title: 'usage.overviewTitle', description: 'usage.overviewDescription' },
  self: { summary: '/me/usage/summary', title: 'usage.myOverviewTitle', description: 'usage.myOverviewDescription' },
  account: { summary: '/account-usage/summary', title: 'usage.accountOverviewTitle', description: 'usage.accountOverviewDescription' }
}
const page = computed(() => PAGES[props.view])
</script>

<template>
  <div>
    <SPageHeader :title="t(page.title)" :description="t(page.description)" />
    <UsageOverview :summary-path="page.summary" :default-range="view === 'self' ? '7d' : 'today'" :view="view" />
  </div>
</template>
