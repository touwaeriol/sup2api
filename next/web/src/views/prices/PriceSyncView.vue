<script setup lang="ts">
import { computed, ref, shallowRef, triggerRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import {
  SBadge,
  SButton,
  SCard,
  SGrid,
  SHint,
  SIcon,
  SInput,
  SModal,
  SPageHeader,
  SPagination,
  SSectionTitle,
  SSpinner,
  SStatCard,
  STable,
  STabs,
  confirm,
  type TabItem,
  type TableColumn,
  type Tone
} from '@sub2api/ui'
import type { PriceSource, PriceSyncAction, PriceSyncItem, PriceSyncPreview, PriceSyncResult } from '@/api/types'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import { errorMessage, notifyError } from '@/utils/errors'
import { formatDateTime, formatNumber } from '@/utils/format'
import PriceDefSummary from './PriceDefSummary.vue'

type Tab = 'pending' | PriceSyncAction

const { t } = useI18n()
const skippedColumns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('prices.model') },
  { key: 'reason', label: t('prices.sync.reason') }
])
const route = useRoute()
const router = useRouter()

const sourceId = computed(() => {
  const n = Number(route.params.id)
  return Number.isFinite(n) && n > 0 ? n : 0
})

const source = ref<PriceSource | null>(null)
const preview = shallowRef<PriceSyncPreview | null>(null)
const fetching = ref(false)
const fetchError = ref('')

// Selection keyed by model. A shallow Set mutated in place + triggerRef keeps
// select-all over thousands of rows cheap.
const selected = shallowRef(new Set<string>())

const tab = ref<Tab>('pending')
const q = ref('')
const page = ref(1)
const pageSize = ref(100)

const sourceName = computed(() => source.value?.name || `#${sourceId.value}`)

async function loadSource() {
  try {
    const list = await api.get<PriceSource[]>('/price-sources')
    source.value = (Array.isArray(list) ? list : []).find(s => s.id === sourceId.value) || null
  } catch (e) {
    notifyError(e)
  }
}

async function fetchPreview() {
  if (!sourceId.value) return
  fetching.value = true
  fetchError.value = ''
  try {
    const r = await api.post<PriceSyncPreview>(`/price-sources/${sourceId.value}/preview`)
    const items = Array.isArray(r?.items) ? r.items : []
    preview.value = { ...r, items }
    // Default selection: new and updated synced prices; manual ones stay unticked.
    selected.value = new Set(items.filter(x => x.action === 'create' || x.action === 'update').map(x => x.model))
  } catch (e) {
    preview.value = null
    selected.value = new Set()
    fetchError.value = errorMessage(e)
  } finally {
    fetching.value = false
    // Refresh last_error / last_synced_at shown in the header.
    loadSource()
  }
}

watch(
  sourceId,
  () => {
    source.value = null
    preview.value = null
    tab.value = 'pending'
    q.value = ''
    loadSource()
    fetchPreview()
  },
  { immediate: true }
)

// ------------------------------------------------------------------ derived

const items = computed<PriceSyncItem[]>(() => preview.value?.items || [])

const counts = computed(() => {
  const c: Record<PriceSyncAction, number> = { create: 0, update: 0, manual: 0, unchanged: 0 }
  for (const x of items.value) if (x.action in c) c[x.action]++
  return c
})

const actionOf = computed(() => {
  const m = new Map<string, PriceSyncAction>()
  for (const x of items.value) m.set(x.model, x.action)
  return m
})

const tabs = computed<TabItem[]>(() => [
  { key: 'pending', label: t('prices.sync.tabs.pending'), badge: counts.value.create + counts.value.update + counts.value.manual },
  { key: 'create', label: t('prices.sync.tabs.create'), badge: counts.value.create },
  { key: 'update', label: t('prices.sync.tabs.update'), badge: counts.value.update },
  { key: 'manual', label: t('prices.sync.tabs.manual'), badge: counts.value.manual },
  { key: 'unchanged', label: t('prices.sync.tabs.unchanged'), badge: counts.value.unchanged }
])

const filtered = computed(() => {
  const s = q.value.trim().toLowerCase()
  const want = tab.value
  return items.value.filter(x => {
    if (want === 'pending' ? x.action === 'unchanged' : x.action !== want) return false
    return !s || x.model.toLowerCase().includes(s)
  })
})

watch([tab, q, pageSize], () => (page.value = 1))

const pageRows = computed(() => filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))

const selectable = (x: PriceSyncItem) => x.action !== 'unchanged'

const filteredSelectable = computed(() => filtered.value.filter(selectable))

const filteredSomeSelected = computed(() => {
  const s = selected.value
  return filteredSelectable.value.some(x => s.has(x.model))
})

const selectedCount = computed(() => selected.value.size)
const selectedManual = computed(() => {
  let n = 0
  const a = actionOf.value
  for (const m of selected.value) if (a.get(m) === 'manual') n++
  return n
})

// ------------------------------------------------------------------ selection

function toggle(x: PriceSyncItem) {
  if (!selectable(x)) return
  const s = selected.value
  if (s.has(x.model)) s.delete(x.model)
  else s.add(x.model)
  triggerRef(selected)
}

/** Select / unselect all: only the rows of the current filter (tab + search). */
function selectAll() {
  const s = selected.value
  for (const x of filteredSelectable.value) s.add(x.model)
  triggerRef(selected)
}

function selectNone() {
  const s = selected.value
  for (const x of filtered.value) s.delete(x.model)
  triggerRef(selected)
}

// ------------------------------------------------------------------ view helpers

const ACTION_TONE: Record<PriceSyncAction, Tone> = { create: 'success', update: 'primary', manual: 'warning', unchanged: 'gray' }

const stats = computed(() => [
  { key: 'create' as const, value: counts.value.create, tone: 'success' as const, icon: 'plus' },
  { key: 'update' as const, value: counts.value.update, tone: 'primary' as const, icon: 'refresh' },
  { key: 'manual' as const, value: counts.value.manual, tone: 'warning' as const, icon: 'warning' },
  { key: 'unchanged' as const, value: counts.value.unchanged, tone: 'primary' as const, icon: 'check' },
  { key: 'skipped' as const, value: preview.value?.skipped ?? 0, tone: 'danger' as const, icon: 'x' }
])

// The preview table's selection: STable's checkboxes (current page, select-all
// in the header) and the toolbar buttons (every filtered row) share this set.
const previewColumns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('prices.model') },
  { key: 'action', label: t('common.status'), width: '10rem' },
  { key: 'incoming', label: t('prices.sync.incoming') },
  { key: 'current', label: t('prices.sync.current') }
])
const selectedKeys = computed<unknown[]>({
  get: () => [...selected.value],
  set: keys => {
    selected.value = new Set(keys as string[])
  }
})

function pickStat(k: string) {
  if (k !== 'skipped') tab.value = k as Tab
}

// ------------------------------------------------------------------ apply

const applying = ref(false)
const result = ref<PriceSyncResult | null>(null)
const resultOpen = ref(false)
const resultCounts = computed(() => {
  const r = result.value
  return r
    ? ([
        ['created', r.created],
        ['updated', r.updated],
        ['unchanged', r.unchanged],
        ['skipped', r.skipped.length]
      ] as const)
    : []
})

async function apply() {
  const models = items.value.filter(x => selected.value.has(x.model) && selectable(x)).map(x => x.model)
  if (!models.length) return
  const manual = selectedManual.value
  const ok = await confirm({
    title: t('prices.sync.applyConfirmTitle'),
    message: manual
      ? t('prices.sync.applyConfirmManual', { n: models.length, m: manual })
      : t('prices.sync.applyConfirm', { n: models.length }),
    danger: manual > 0
  })
  if (!ok) return
  applying.value = true
  try {
    const r = await api.post<PriceSyncResult>(`/price-sources/${sourceId.value}/apply`, { models })
    result.value = {
      created: r?.created ?? 0,
      updated: r?.updated ?? 0,
      unchanged: r?.unchanged ?? 0,
      skipped: Array.isArray(r?.skipped) ? r.skipped : []
    }
    resultOpen.value = true
    await fetchPreview()
  } catch (e) {
    notifyError(e)
  } finally {
    applying.value = false
  }
}
</script>

<template>
  <div>
    <SPageHeader :title="t('prices.sync.title', { name: sourceName })">
      <template #title-extra>
        <SBadge v-if="source" tone="gray">{{ t(`prices.sources.kind.${source.kind}`) }}</SBadge>
      </template>
    </SPageHeader>

    <SCard v-if="fetching && !preview" data-testid="price-sync-fetching">
      <div class="flex flex-col items-center gap-3 py-12 text-sm text-fg-muted">
        <SSpinner size="lg" />
        {{ t('prices.sync.fetching', { name: sourceName }) }}
      </div>
    </SCard>

    <SCard v-else-if="fetchError && !preview" data-testid="price-sync-error">
      <div class="flex flex-col items-center gap-3 py-8 text-center">
        <SIcon name="x" class="h-8 w-8 text-danger-500 dark:text-danger-400" />
        <p class="font-medium text-fg">{{ t('prices.sync.fetchFailed') }}</p>
        <SHint tone="danger" class="max-w-xl break-words">{{ fetchError }}</SHint>
        <SButton variant="primary" :loading="fetching" @click="fetchPreview">{{ t('prices.sync.retry') }}</SButton>
      </div>
    </SCard>

    <template v-else-if="preview">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
        <SHint size="xs" data-testid="price-sync-fetched">
          {{ t('prices.sync.fetchedAt', { time: formatDateTime(preview.fetched_at), total: formatNumber(preview.total) }) }}
        </SHint>
        <SButton :loading="fetching" data-testid="price-sync-refetch" icon="refresh" @click="fetchPreview">{{ t('prices.sync.refetch') }}</SButton>
      </div>

      <!-- stats -->
      <SGrid variant="stats" :cols="3" :lg-cols="5" :gap="3" class="mb-4" data-testid="price-sync-stats">
        <SStatCard
          v-for="s in stats"
          :key="s.key"
          compact
          :label="t(`prices.sync.stats.${s.key}`)"
          :value="formatNumber(s.value)"
          :icon="s.icon"
          :tone="s.tone"
          :selectable="s.key !== 'skipped'"
          :selected="s.key !== 'skipped' ? tab === s.key : undefined"
          :title="s.key === 'skipped' ? t('prices.sync.skippedHint') : undefined"
          :data-stat="s.key"
          @click="pickStat(s.key)"
        />
      </SGrid>

      <STabs v-model="tab" :tabs="tabs" data-testid="price-sync-tabs">
        <STable
          v-model:selected="selectedKeys"
          card
          dense
          selectable
          clickable
          :columns="previewColumns"
          :rows="pageRows"
          row-key="model"
          :row-selectable="selectable"
          :row-class="(x: PriceSyncItem) => (selectable(x) ? '' : 'opacity-70')"
          :row-attrs="(x: PriceSyncItem) => ({ 'data-model': x.model, 'data-action': x.action })"
          :empty-text="t('prices.sync.empty')"
          data-testid="price-sync-table"
          @row-click="toggle"
        >
          <template #toolbar>
            <SInput
              v-model="q"
              class="w-64"
              icon="search"
              clearable
              :aria-label="t('common.search')"
              :placeholder="t('prices.sync.searchPlaceholder')"
              data-testid="price-sync-search"
            />
            <SButton size="sm" :disabled="!filteredSelectable.length" data-testid="price-sync-select-all" @click="selectAll">
              {{ t('prices.sync.selectAll', { n: formatNumber(filteredSelectable.length) }) }}
            </SButton>
            <SButton size="sm" :disabled="!filteredSomeSelected" data-testid="price-sync-select-none" @click="selectNone">{{
              t('prices.sync.selectNone')
            }}</SButton>
            <SHint inline size="xs">{{ t('prices.sync.selectScope') }}</SHint>
            <div class="ml-auto flex flex-wrap items-center gap-3">
              <span class="text-sm" data-testid="price-sync-selected">
                {{ t('prices.sync.selected', { n: formatNumber(selectedCount) }) }}
                <SHint v-if="selectedManual" inline tone="warning">· {{ t('prices.sync.selectedManual', { n: selectedManual }) }}</SHint>
              </span>
              <SButton variant="primary" :disabled="!selectedCount" :loading="applying" data-testid="price-sync-apply" @click="apply">
                {{ t('prices.sync.apply', { n: formatNumber(selectedCount) }) }}
              </SButton>
            </div>
          </template>
          <template #cell-model="{ row }"><ModelBadge :model="row.model" plain /></template>
          <template #cell-action="{ row }">
            <SBadge
              :tone="ACTION_TONE[row.action as PriceSyncAction]"
              :title="row.action === 'unchanged' ? t('prices.sync.unchangedHint') : undefined"
              >{{ t(`prices.sync.action.${row.action}`) }}</SBadge
            >
            <SHint v-if="row.action === 'manual'" tone="warning" size="xs" class="mt-0.5 flex items-center gap-1">
              <SIcon name="warning" class="h-3.5 w-3.5 shrink-0" />{{ t('prices.sync.manualWarn') }}
            </SHint>
          </template>
          <template #cell-incoming="{ row }">
            <PriceDefSummary :def="row.incoming" :compare="row.action === 'unchanged' ? null : row.current" />
          </template>
          <template #cell-current="{ row }">
            <SHint v-if="!row.current" inline size="xs">{{ t('prices.sync.noCurrent') }}</SHint>
            <template v-else>
              <PriceDefSummary :def="row.current" muted />
              <div class="mt-0.5 flex gap-1">
                <SHint inline size="xs">{{
                  row.current.source === 'sync' ? t('prices.sync.currentSync') : t('prices.sync.currentManual')
                }}</SHint>
                <SHint v-if="!row.current.enabled" inline size="xs">· {{ t('prices.sync.currentDisabled') }}</SHint>
              </div>
            </template>
          </template>
          <template #footer>
            <SPagination v-model:page="page" v-model:page-size="pageSize" :total="filtered.length" :page-sizes="[50, 100, 200]" />
          </template>
        </STable>
      </STabs>
    </template>

    <SModal v-model:open="resultOpen" :title="t('prices.sync.resultTitle')" width="lg">
      <div v-if="result" class="space-y-4" data-testid="price-sync-result">
        <SGrid :cols-base="2" :cols="4" :gap="3">
          <div v-for="[k, n] in resultCounts" :key="k" class="rounded-lg bg-surface-2 px-3 py-2">
            <SHint size="xs">{{ t(`prices.sync.result.${k}`) }}</SHint>
            <p
              class="text-xl font-semibold tabular-nums"
              :class="k === 'skipped' && n ? 'text-warning-600 dark:text-warning-400' : ''"
              :data-result="k"
            >
              {{ n }}
            </p>
          </div>
        </SGrid>
        <div v-if="result.skipped.length">
          <SSectionTitle tag="p">{{ t('prices.sync.skippedList') }}</SSectionTitle>
          <div class="max-h-60 overflow-y-auto">
            <STable :columns="skippedColumns" :rows="result.skipped" row-key="model" dense data-testid="price-sync-skipped">
              <template #cell-model="{ row }"><ModelBadge :model="row.model" plain /></template>
              <template #cell-reason="{ row }"
                ><span class="text-xs">{{ row.reason }}</span></template
              >
            </STable>
          </div>
        </div>
      </div>
      <template #footer>
        <SButton @click="resultOpen = false">{{ t('common.close') }}</SButton>
        <SButton variant="primary" @click="router.push({ path: '/prices', query: { source: 'sync', sync_source_id: String(sourceId) } })">
          {{ t('prices.sync.viewPrices') }}
        </SButton>
      </template>
    </SModal>
  </div>
</template>
