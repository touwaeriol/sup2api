<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import {
  SDateTime,
  SBadge,
  SButton,
  SCode,
  SDropdown,
  SHint,
  SLink,
  SPageHeader,
  SSwitch,
  STable,
  type TableColumn,
  type Tone
} from '@sub2api/ui'
import type { PriceSource } from '@/api/types'
import { deleteWithConfirm, useRowSwitch } from '@/composables/crud'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import PriceSourceModal from './PriceSourceModal.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const canManage = computed(() => auth.has('price:manage'))

const sources = ref<PriceSource[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const r = await api.get<PriceSource[]>('/price-sources')
    sources.value = Array.isArray(r) ? r : []
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const columns = computed<TableColumn[]>(() => {
  const cols: TableColumn[] = [
    { key: 'name', label: t('common.name') },
    { key: 'kind', label: t('prices.sources.kindCol'), width: '130px' },
    { key: 'url', label: t('prices.sources.url') },
    { key: 'enabled', label: t('common.enabled'), width: '90px' },
    { key: 'last_synced_at', label: t('prices.sources.lastSynced'), width: '140px' },
    { key: 'last_error', label: t('prices.sources.lastError') },
    { key: 'price_count', label: t('prices.sources.priceCount'), align: 'right', width: '90px' }
  ]
  if (canManage.value) cols.push({ key: 'actions', label: t('common.actions'), align: 'right', width: '170px' })
  return cols
})

const KIND_TONE: Record<string, Tone> = { sup2api: 'accent', models_dev: 'success' }

function optionText(s: PriceSource): string {
  if (s.kind === 'sup2api')
    return s.options?.apply_multiplier === false ? t('prices.sources.multiplierOff') : t('prices.sources.multiplierOn')
  const p = Array.isArray(s.options?.providers) ? s.options.providers : []
  return p.join(', ')
}

// ------------------------------------------------------------------ edit

const modalOpen = ref(false)
const editing = ref<PriceSource | null>(null)

function openEditor(s: PriceSource | null) {
  editing.value = s
  modalOpen.value = true
}

const { busy: toggling, set: toggle } = useRowSwitch((s: PriceSource, on) => api.patch(`/price-sources/${s.id}`, { enabled: on }))

function remove(s: PriceSource) {
  return deleteWithConfirm(`/price-sources/${s.id}`, t('prices.sources.deleteConfirm', { name: s.name, n: s.price_count || 0 }), load)
}

function onAction(s: PriceSource, key: string) {
  if (key === 'edit') openEditor(s)
  else if (key === 'prices') viewPrices(s)
  else if (key === 'delete') remove(s)
}

function preview(s: PriceSource) {
  router.push(`/prices/sources/${s.id}/sync`)
}

function viewPrices(s: PriceSource) {
  router.push({ path: '/prices', query: { source: 'sync', sync_source_id: String(s.id) } })
}
</script>

<template>
  <div>
    <SPageHeader :title="t('prices.sources.title')" :description="t('prices.sources.description')">
      <template #actions>
        <SButton :loading="loading" icon="refresh" @click="load">{{ t('common.refresh') }}</SButton>
        <SButton v-if="canManage" variant="primary" data-testid="price-source-new" icon="plus" @click="openEditor(null)">{{
          t('prices.sources.create')
        }}</SButton>
      </template>
    </SPageHeader>

    <STable card :columns="columns" :rows="sources" :loading="loading" :empty-text="t('prices.sources.empty')">
      <template #cell-name="{ row }">
        <span class="whitespace-nowrap font-medium text-fg" data-testid="price-source-name">{{ row.name }}</span>
        <SHint v-if="optionText(row)" size="xs" class="max-w-[11rem] truncate" :title="optionText(row)">{{ optionText(row) }}</SHint>
      </template>
      <template #cell-kind="{ row }">
        <SBadge :tone="KIND_TONE[row.kind] ?? 'primary'">{{ t(`prices.sources.kind.${row.kind}`) }}</SBadge>
      </template>
      <template #cell-url="{ row }">
        <SCode inline class="block max-w-[13rem] truncate" :title="row.url" :text="row.url" />
        <SHint v-if="row.kind === 'sup2api'" inline size="xs">
          <template v-if="row.has_api_key">{{ t('prices.sources.hasKey') }} ••••</template>
          <SHint v-else inline size="xs" tone="danger">{{ t('prices.sources.apiKeyRequired') }}</SHint>
        </SHint>
      </template>
      <template #cell-enabled="{ row }">
        <SSwitch
          :model-value="row.enabled"
          :disabled="!canManage || toggling.has(row.id)"
          :aria-label="t('common.enabled') + ' · ' + row.name"
          @update:model-value="toggle(row, $event)"
        />
      </template>
      <template #cell-last_synced_at="{ row }">
        <SDateTime muted :value="row.last_synced_at" mode="relative" :empty="t('prices.sources.never')" class="text-xs" />
      </template>
      <template #cell-last_error="{ row }">
        <SHint
          v-if="row.last_error"
          tone="danger"
          size="xs"
          class="max-w-[11rem] truncate"
          :title="row.last_error"
          data-testid="price-source-error"
          >{{ row.last_error }}</SHint
        >
        <SHint v-else inline>—</SHint>
      </template>
      <template #cell-price_count="{ row }">
        <SLink v-if="row.price_count" as="button" class="text-sm" :title="t('prices.sources.viewPrices')" @click="viewPrices(row)">{{
          row.price_count
        }}</SLink>
        <SHint v-else inline>0</SHint>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <SButton size="sm" variant="ghost" data-testid="price-source-preview" @click="preview(row)">{{
            t('prices.sources.preview')
          }}</SButton>
          <SDropdown
            :actions="[
              { key: 'edit', label: t('common.edit') },
              { key: 'prices', label: t('prices.sources.viewPrices'), hidden: !row.price_count },
              { key: 'delete', label: t('common.delete'), danger: true }
            ]"
            @select="onAction(row, $event)"
          />
        </div>
      </template>
    </STable>

    <PriceSourceModal v-model:open="modalOpen" :source="editing" @saved="load" />
  </div>
</template>
