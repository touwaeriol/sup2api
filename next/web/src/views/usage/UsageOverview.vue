<script setup lang="ts">
// The usage statistics of the `view`: all users' (an operator's), the
// caller's, or those of the accounts in the caller's reach (`account`,
// CONTRACTS §103.5: by account instead of by group or key, costs at the list
// price): the search bar (time range, granularity, filters), the four totals
// and the charts.
// Every part follows the same filters, which a link may preset (?user_id=,
// ?from=, …). Refresh sits with Reset / Search in the search bar and reloads
// every part. The requests themselves are on the usage records page.
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { api } from '@sub2api/host'
import { SButton, SDateRange, SSegmented, type RangeKey } from '@sub2api/ui'
import type { UsageSummaryRow } from '@/api/types'
import UsageSummaryCards from '@/components/UsageSummaryCards.vue'
import UsageCharts from '@/components/stats/UsageCharts.vue'
import { autoGranularity, hourAllowed, type Granularity } from '@/components/stats/stats'
import { useAuthStore } from '@/stores/auth'
import UsageFilters from './UsageFilters.vue'
import { baseUsageFilters, entityPatch, queryUsageFilters } from './usageFilters'
import { useUsageSummary } from './useUsageSummary'
import { LIST_PRICE_LABEL, type UsageView } from './view'

const props = withDefaults(
  defineProps<{
    summaryPath: string
    defaultRange: RangeKey
    view: UsageView
  }>(),
  {}
)
const { t } = useI18n()
const route = useRoute()
const auth = useAuthStore()

const showAccount = computed(() => props.view === 'account' || (props.view === 'all' && auth.has('account:read')))
/** The account view names its costs as the list price; the others keep each part's own label. */
const costLabel = computed(() => (props.view === 'account' ? t(LIST_PRICE_LABEL) : undefined))

const fromQuery = queryUsageFilters(route.query, props.view, showAccount.value)
const range = ref<RangeKey>(fromQuery.from || fromQuery.to ? 'custom' : props.defaultRange)
const filters = reactive<Record<string, string>>({ ...baseUsageFilters(props.defaultRange), ...fromQuery })

// Granularity: automatic (hours up to a day) until the user picks one; a new
// range goes back to automatic. Hours over 31 days are refused by the server.
const picked = ref<Granularity | null>(null)
watch(() => [filters.from, filters.to], () => (picked.value = null))
const canHour = computed(() => hourAllowed(String(filters.from || ''), String(filters.to || '')))
const granularity = computed<Granularity>(() => {
  const g = picked.value ?? autoGranularity(String(filters.from || ''), String(filters.to || ''))
  return g === 'hour' && !canHour.value ? 'day' : g
})
const granularityOptions = computed(() => [
  { value: 'day', label: t('stats.day') },
  { value: 'hour', label: t('stats.hour'), disabled: !canHour.value }
])

const totals = useUsageSummary(props.summaryPath, filters, 'none')
// The request types under the search, for the type select (see UsageRecords).
const protocols = useUsageSummary(props.summaryPath, filters, 'protocol', true, () => ({ protocol: '' }))
const protocolOptions = computed(() => protocols.rows.value.map((r) => r.key).filter((k) => k !== ''))
// The models under the search, for the model picker (see UsageRecords).
const models = useUsageSummary(props.summaryPath, filters, 'model', true, () => ({ model: '', model_contains: '' }))
const modelOptions = computed(() => models.rows.value.map((r) => r.key).filter((k) => k !== ''))

// The caller's API key names for the "by API key" breakdown (keyed by id).
const keyNames = reactive(new Map<string, string>())
if (props.view === 'self' && auth.has('apikey:self:manage')) {
  api
    .list<{ id: number; name: string }>('/me/api-keys', { page_size: 200 })
    .then((res) => res.items.forEach((k) => keyNames.set(String(k.id), k.name)))
    .catch(() => {})
}
// The account view names an account as its rows do: a deleted account, or one
// read under the usage scope only (no account:read), keeps its name.
const secondary = computed(() => ({
  all: { groupBy: 'group', title: t('stats.groupDist'), keyLabel: t('stats.group') },
  self: { groupBy: 'api_key', title: t('stats.keyDist'), keyLabel: t('stats.apiKey'), labelOf: (r: UsageSummaryRow) => keyNames.get(r.key) || `#${r.key}` },
  account: { groupBy: 'account', title: t('stats.accountDist'), keyLabel: t('common.account'), labelOf: (r: UsageSummaryRow) => r.account_name || `#${r.key}` }
})[props.view])
/** The filter a picked slice of the second breakdown sets. */
function secondaryFilter(k: string): Record<string, string> {
  if (props.view === 'account') return entityPatch('account', { id: k })
  return props.view === 'all' ? { group_name: k } : entityPatch('api_key', { id: k })
}

/** Where each view keeps the charts' settings. */
const CHART_GROUPS: Record<UsageView, string> = { all: 'usage-admin', self: 'usage-mine', account: 'usage-account' }
const revisionCharts = ref(0)
function refresh() {
  totals.reload()
  protocols.reload()
  models.reload()
  revisionCharts.value++
}

function filterBy(patch: Record<string, string>) {
  Object.assign(filters, patch)
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <UsageFilters class="!mb-0" :model-value="filters" :view="view" :accounts="showAccount" :protocol-options="protocolOptions" :model-options="modelOptions" @update:model-value="Object.assign(filters, $event)">
      <template #range>
        <div class="flex flex-wrap items-center gap-2">
          <SDateRange v-model:range="range" v-model:from="filters.from" v-model:to="filters.to" size="sm" :label="false" />
          <SSegmented
            :model-value="granularity"
            :options="granularityOptions"
            size="sm"
            :aria-label="t('stats.granularity')"
            :title="canHour ? undefined : t('stats.hourTooLong')"
            data-testid="usage-granularity"
            @update:model-value="picked = $event as Granularity"
          />
        </div>
      </template>
      <template #tools>
        <SButton size="sm" icon="refresh" :loading="totals.loading.value" data-testid="usage-refresh" @click="refresh">{{ t('common.refresh') }}</SButton>
      </template>
    </UsageFilters>

    <UsageSummaryCards :totals="totals.rows.value[0] || null" :loading="totals.loading.value" :cost-label="costLabel" />

    <UsageCharts
      :summary-path="summaryPath"
      :filters="filters"
      :granularity="granularity"
      :secondary="secondary"
      :revision="revisionCharts"
      :group="CHART_GROUPS[view]"
      :cost-label="costLabel"
      :model-sources="view === 'all'"
      @select-model="filterBy(entityPatch('model', view === 'account' ? { id: $event } : { text: $event }))"
      @select-secondary="(k: string) => filterBy(secondaryFilter(k))"
    />
  </div>
</template>
