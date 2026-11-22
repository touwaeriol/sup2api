<script setup lang="ts">
// Mine > Models & prices: every model at its base price with the caller's
// groups that provide it, or what one of those groups provides at what the
// caller pays there, the list price times their rate (CONTRACTS §79.5).
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api, isApiError } from '@sub2api/host'
import { SAlert, SBadge, SCard, SChip, SInput, SPageHeader, SSelect, STable, type TableColumn } from '@sub2api/ui'
import type { MyPrices } from '@/api/pricing'
import type { Group } from '@/api/types'
import { fetchMyGroups } from '@/composables/lookups'
import { useLatestRequest } from '@/composables/useLatestRequest'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import { priceCells, priceText, rateText, round } from '@/components/pricing/pricingFormat'
import { myGroupLabel } from '@/views/keys/myGroup'
import { num } from '@/views/prices/priceExpr'
import { notifyError } from '@/utils/errors'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const groups = ref<Group[]>([])
const search = ref('')

/** The chosen group, kept in ?group= so that a link can open it; '' is every group, at the base price. */
const groupID = computed({
  get: () => (typeof route.query.group === 'string' && /^\d+$/.test(route.query.group) ? route.query.group : ''),
  set: (id: string) => void router.replace({ query: { ...route.query, group: id || undefined } })
})
/** The link named a group the caller cannot use (any more): every group is shown instead, with a note, until a group is chosen. */
const unavailable = ref(false)
watch(groupID, (id) => {
  if (id) unavailable.value = false
})

/**
 * GET /me/prices of the choice. A group the caller cannot use is a 404: the
 * choice goes back to every group, whose own request answers it; meanwhile
 * the rows shown stay, so that no error shows.
 */
async function loadPrices(id: string): Promise<MyPrices | null> {
  try {
    return await api.get<MyPrices>('/me/prices', id ? { group_id: id } : {})
  } catch (e) {
    if (!id || !isApiError(e) || e.status !== 404) throw e
    unavailable.value = true
    groupID.value = ''
    return prices.value
  }
}
// The answer stays while the next choice loads: what describes the rows below reads it, not the choice.
const { result: prices, loading, error } = useLatestRequest(groupID, loadPrices, 0)

const groupOptions = computed(() => [
  { value: '', label: t('me.models.allGroups') },
  ...groups.value.map((g) => ({ value: String(g.id), label: myGroupLabel(g, t('pricing.exclusive')) }))
])

onMounted(async () => {
  try {
    groups.value = await fetchMyGroups()
  } catch (e) {
    notifyError(e)
  }
})

/** Whether the rows are every model at its base price, not those of one group. */
const atBase = computed(() => prices.value?.group === null)
const rate = computed(() => num(prices.value?.rate_multiplier, 1))
const money = (v: number | null | undefined) => priceText(v, rate.value)
/** A price that cannot be broken down: billed by its rules, times the caller's rate in the group, which the cells beside it are multiplied by. */
const opaqueText = computed(() => (round(rate.value) === 1 ? t('me.models.custom') : t('me.models.customTimes', { rate: rateText(round(rate.value)) })))

/** What the prices of the rows are: the base ones, or the caller's in the group, and where their rate comes from unless it is the group's own. */
function priceNotes(p: MyPrices): string[] {
  if (!p.group) return [t('me.models.basePrice')]
  const rated = t('me.models.rated', { rate: rateText(p.rate_multiplier) })
  return p.rate_source && p.rate_source !== 'group' ? [rated, t(`me.models.rateSource.${p.rate_source}`)] : [rated]
}
const unit = computed(() => [t('me.models.unit'), ...(prices.value ? priceNotes(prices.value) : [])].join(' · '))

const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  const rows = prices.value?.prices ?? []
  return (q ? rows.filter((r) => r.model.toLowerCase().includes(q)) : rows).map((r) => ({ ...r, price: priceCells(r) }))
})

const columns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('me.models.model') },
  { key: 'input', label: t('me.models.input'), align: 'right', width: '120px' },
  { key: 'output', label: t('me.models.output'), align: 'right', width: '120px' },
  { key: 'cacheRead', label: t('me.models.cacheRead'), align: 'right', width: '120px' },
  { key: 'cacheWrite', label: t('me.models.cacheWrite'), align: 'right', width: '120px' },
  ...(atBase.value ? [{ key: 'groups', label: t('me.models.groups') }] : [])
])
</script>

<template>
  <div class="flex flex-col gap-4">
    <SPageHeader :title="t('me.models.title')" :description="t('me.models.description')" />
    <div class="flex flex-wrap items-center gap-2 rounded-xl border border-line bg-surface p-3 shadow-sm">
      <SSelect v-model="groupID" class="w-64" size="sm" :aria-label="t('me.models.group')" :options="groupOptions" data-testid="models-group" />
      <SInput v-model="search" class="w-64" size="sm" icon="search" clearable :placeholder="t('me.models.search')" :aria-label="t('me.models.search')" data-testid="models-search" />
      <span class="ms-auto text-xs text-fg-muted" data-testid="models-unit">{{ unit }}</span>
    </div>
    <SAlert v-if="error" tone="danger" dense>{{ error }}</SAlert>
    <SAlert v-if="unavailable" tone="warning" dense data-testid="models-group-unavailable">{{ t('me.models.groupUnavailable') }}</SAlert>
    <SAlert v-if="atBase" tone="primary" dense data-testid="models-base-note">{{ t('me.models.baseNote') }}</SAlert>
    <SCard :padded="false" data-testid="models-table">
      <STable :columns="columns" :rows="shown" :loading="loading" row-key="model" dense :empty-text="t(atBase ? 'me.models.emptyAll' : 'me.models.empty')">
        <template #cell-model="{ row }">
          <div class="flex flex-wrap items-center gap-2">
            <ModelBadge :model="row.model" />
            <SBadge v-if="row.price.tiers > 1" tone="gray">{{ t('me.models.tiered', { n: row.price.tiers }) }}</SBadge>
          </div>
        </template>
        <template #cell-input="{ row }">
          <span v-if="row.price.perRequest != null" class="whitespace-nowrap font-mono text-xs" data-testid="models-per-request">{{ t('me.models.perRequest', { price: money(row.price.perRequest) }) }}</span>
          <span v-else-if="row.price.opaque" class="text-xs text-fg-muted" data-testid="models-opaque">{{ opaqueText }}</span>
          <span v-else class="font-mono text-xs">{{ money(row.price.tokens?.p) }}</span>
        </template>
        <template #cell-output="{ row }"><span class="font-mono text-xs">{{ money(row.price.tokens?.c) }}</span></template>
        <template #cell-cacheRead="{ row }"><span class="font-mono text-xs">{{ money(row.price.tokens?.cr) }}</span></template>
        <template #cell-cacheWrite="{ row }"><span class="font-mono text-xs">{{ money(row.price.tokens?.cc) }}</span></template>
        <template #cell-groups="{ row }">
          <div v-if="row.groups?.length" class="flex flex-wrap gap-1">
            <SChip v-for="g in row.groups" :key="g.id" clickable size="xs" :title="t('me.models.viewGroup', { name: g.name })" data-testid="models-row-group" @click="groupID = String(g.id)">{{ g.name }}</SChip>
          </div>
          <span v-else class="text-xs text-fg-subtle">{{ t('me.models.noGroups') }}</span>
        </template>
      </STable>
    </SCard>
  </div>
</template>
