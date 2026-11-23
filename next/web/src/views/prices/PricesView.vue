<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import { SAlert, SBadge, SButton, SChip, SDropdown, SHint, SInput, SLink, SPageHeader, SPagination, SSelect, SSwitch, STable, type MenuAction, type TableColumn, type Tone } from '@sub2api/ui'
import type { Price } from '@/api/types'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import { canSimulatePrices } from '@/components/pricing/quoteSubjects'
import { deleteWithConfirm, useRowSwitch } from '@/composables/crud'
import { activeFilterCount, useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/format'
import { priceCells, priceExtras, usd } from '@/components/pricing/pricingFormat'
import { PRICE_MODES, type PriceMode } from './priceExpr'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const canManage = computed(() => auth.has('price:manage'))
const canSimulate = computed(() => canSimulatePrices(auth))

function queryStr(k: string): string {
  const v = route.query[k]
  return typeof v === 'string' ? v : ''
}
const querySource = () => (/^\d+$/.test(queryStr('sync_source_id')) ? queryStr('sync_source_id') : '')

// ?source= and ?sync_source_id= come from the sync sources page ("view these prices").
const { items, loading, error, page, pageSize, total, filters, reload } = useList<Price>('/prices', {
  mode: '',
  source: ['manual', 'sync'].includes(queryStr('source')) ? queryStr('source') : '',
  sync_source_id: querySource(),
  q: ''
})

watch(
  () => route.query.sync_source_id,
  () => {
    const s = querySource()
    if (s !== filters.sync_source_id) filters.sync_source_id = s
  }
)

const modeOptions = computed(() => [{ value: '', label: t('common.all') }, ...PRICE_MODES.map(m => ({ value: m, label: t(`prices.mode.${m}`) }))])

const sourceOptions = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'manual', label: t('prices.source.manual') },
  { value: 'sync', label: t('prices.source.sync') }
])

// Client-side filter as a fallback in case the server ignores a query parameter.
const rows = computed(() =>
  items.value.filter((p) => {
    if (filters.mode && p.mode !== filters.mode) return false
    if (filters.source && p.source !== filters.source) return false
    if (filters.sync_source_id && String(p.sync_source_id ?? '') !== String(filters.sync_source_id)) return false
    const q = String(filters.q || '').trim().toLowerCase()
    if (q && !`${p.model} ${p.note || ''}`.toLowerCase().includes(q)) return false
    return true
  })
)

/** Name of the source filtered by ?sync_source_id=, taken from the loaded rows. */
const filteredSourceName = computed(() => {
  if (!filters.sync_source_id) return ''
  const hit = items.value.find((p) => String(p.sync_source_id) === String(filters.sync_source_id) && p.sync_source_name)
  return hit?.sync_source_name || `#${filters.sync_source_id}`
})

function clearSourceFilter() {
  filters.sync_source_id = ''
  const q = { ...route.query }
  delete q.sync_source_id
  router.replace({ query: q })
}

const columns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('prices.model'), mobile: 'title' },
  { key: 'mode', label: t('prices.modeCol'), width: '100px', mobile: 'meta' },
  { key: 'summary', label: t('prices.summaryCol'), mobile: 'meta', mobileSpan: 2 },
  { key: 'source', label: t('common.source'), width: '170px', mobile: 'meta' },
  { key: 'enabled', label: t('common.enabled'), width: '90px', mobile: 'status' },
  { key: 'actions', label: t('common.actions'), align: 'right', width: '150px' }
])

/** "$3 / request", "input $3 · output $15 · cache read $0.3": the cells of a price (its rest, the last tier); '' for an expression. */
function cellsText(p: Price): string {
  const { perRequest, tokens } = priceCells(p)
  if (perRequest !== undefined) return t('prices.summary.perRequest', { price: usd(perRequest) })
  return tokens ? t('prices.summary.perToken', { p: usd(tokens.p ?? 0), c: usd(tokens.c ?? 0), cr: usd(tokens.cr) }) : ''
}

/** "N tiers · M surcharge rules" of an expression price with several tiers or rules, from the server's analysis; '' otherwise. */
function analysisText(p: Price): string {
  const an = p.analysis as { tiers?: unknown[]; rules?: unknown[] } | undefined
  const [tiers, rules] = [an?.tiers?.length ?? 0, an?.rules?.length ?? 0]
  return tiers > 1 || rules ? t('prices.summary.analysis', { tiers, rules }) : ''
}

const MODE_TONE: Record<PriceMode, Tone> = { expression: 'accent', visual: 'primary' }

function sourceLabel(p: Price): string {
  if (p.source === 'sync') return p.sync_source_name ? t('prices.sourceSync', { name: p.sync_source_name }) : t('prices.source.sync')
  return t('prices.source.manual')
}

const { busy: toggling, set: toggle } = useRowSwitch((p: Price, on) => api.patch(`/prices/${p.id}`, { enabled: on }))

function remove(p: Price) {
  return deleteWithConfirm(`/prices/${p.id}`, t('common.confirmDelete', { name: p.model }), reload)
}

// The price simulator: opened as it was left from the header, on its model from a row.
const simulator = ref<{ open: boolean; preset?: { model: string } }>({ open: false })
const simulate = (model?: string) => (simulator.value = { open: true, preset: model ? { model } : undefined })

const rowActions = computed<MenuAction[]>(() => [
  { key: 'simulate', label: t('prices.simulateModel'), hidden: !canSimulate.value },
  { key: 'delete', label: t('common.delete'), danger: true, hidden: !canManage.value }
])
function runRowAction(key: string, p: Price) {
  if (key === 'simulate') simulate(p.model)
  if (key === 'delete') remove(p)
}
</script>

<template>
  <div>
    <SPageHeader :title="t('prices.title')" :description="t('prices.description')" :filters-active-count="activeFilterCount(filters)">
      <template #actions>
        <SButton v-if="canSimulate" data-testid="price-simulate" @click="simulate()">{{ t('prices.simulate') }}</SButton>
        <SButton v-if="canManage" data-testid="price-sync" @click="router.push('/prices/sources')">{{ t('prices.syncPrices') }}</SButton>
        <SButton v-else variant="ghost" data-testid="price-sources" @click="router.push('/prices/sources')">{{ t('prices.viewSources') }}</SButton>
        <SButton v-if="canManage" variant="primary" icon="plus" fab @click="router.push('/prices/new')">{{ t('prices.new') }}</SButton>
      </template>
      <template #filters-primary>
        <SInput v-model="filters.q" class="w-64" icon="search" clearable :aria-label="t('common.search')" :placeholder="t('prices.searchPlaceholder')" />
      </template>
      <template #filters>
        <SSelect v-model="filters.mode" class="w-40" :options="modeOptions" :aria-label="t('prices.modeCol')" />
        <SSelect v-model="filters.source" class="w-36" :options="sourceOptions" :aria-label="t('common.source')" data-testid="price-source-filter" />
        <SChip
          v-if="filters.sync_source_id"
          tone="info"
          closable
          :close-label="t('prices.clearSourceFilter')"
          data-testid="price-source-chip"
          @close="clearSourceFilter"
        >
          {{ t('prices.fromSource', { name: filteredSourceName }) }}
        </SChip>
      </template>
    </SPageHeader>

    <SAlert tone="primary" dense class="mb-3" data-testid="price-scope-note">
      {{ t('prices.scopeNote') }}
      <template #actions>
        <SLink v-if="auth.has('group:read')" to="/groups">{{ t('prices.scopeLinks.groups') }}</SLink>
        <SLink v-if="auth.has('segment:read')" to="/segments">{{ t('prices.scopeLinks.segments') }}</SLink>
      </template>
    </SAlert>
    <STable card :columns="columns" :rows="rows" :loading="loading" :error="error" @retry="reload">
      <template #cell-model="{ row }">
        <SLink :to="`/prices/${row.id}`" class="inline-flex max-w-full"><ModelBadge :model="row.model" plain /></SLink>
        <SHint v-if="row.note" size="xs" class="truncate">{{ row.note }}</SHint>
      </template>
      <template #cell-mode="{ row }">
        <SBadge :tone="MODE_TONE[row.mode] ?? 'gray'">{{ t(`prices.mode.${row.mode}`) }}</SBadge>
      </template>
      <template #cell-summary="{ row }">
        <span v-if="cellsText(row)" class="text-sm">
          {{ cellsText(row) }}
          <SHint v-if="priceExtras(t, row)" inline size="xs">{{ priceExtras(t, row) }}</SHint>
        </span>
        <span v-else-if="analysisText(row)" class="text-sm">{{ analysisText(row) }}</span>
        <span v-else class="block max-w-md truncate font-mono text-xs text-fg-muted" :title="row.expression">
          {{ t('prices.summary.custom') }} · {{ row.expression }}
        </span>
      </template>
      <template #cell-source="{ row }">
        <span :title="row.source === 'sync' && row.synced_at ? formatDateTime(row.synced_at) : undefined" data-testid="price-source">
          <SBadge :tone="row.source === 'sync' ? 'info' : 'primary'">{{ sourceLabel(row) }}</SBadge>
        </span>
      </template>
      <template #cell-enabled="{ row }">
        <SSwitch :model-value="row.enabled" :disabled="!canManage || toggling.has(row.id)" :aria-label="t('common.enabled') + ' · ' + row.model" @update:model-value="toggle(row, $event)" />
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end gap-1">
          <SButton size="sm" variant="ghost" @click="router.push(`/prices/${row.id}`)">{{ t(canManage ? 'common.edit' : 'common.view') }}</SButton>
          <SDropdown v-if="rowActions.some((a) => !a.hidden)" :actions="rowActions" data-testid="price-row-menu" @select="runRowAction($event, row)" />
        </div>
      </template>
      <template #footer>
        <SPagination v-model:page="page" v-model:page-size="pageSize" :total="total" />
      </template>
    </STable>

    <PriceSimulatorModal v-if="canSimulate" v-model:open="simulator.open" :preset="simulator.preset" />
  </div>
</template>
