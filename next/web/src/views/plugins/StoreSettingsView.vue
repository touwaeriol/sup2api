<script setup lang="ts">
// The store settings (system settings): the official store's repository,
// read-only, and its channels: add one (name and branch), rename, enable or
// disable, set the default, delete. The default channel cannot be disabled
// or deleted (CONTRACTS §136).
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SCard, SEmpty, SField, SHint, SInput, SPageHeader, SSwitch, STable, toast, type TableColumn } from '@sub2api/ui'
import type { MarketSource, MarketSourcePatch } from '@/api/marketTypes'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { deleteWithConfirm } from '@/composables/crud'
import { MARKET_MANAGE_PERM, OFFICIAL_STORE_REPO } from './marketChannels'
import { fetchChannels } from './useMarket'
import ChannelFormModal from './store/ChannelFormModal.vue'

const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has(MARKET_MANAGE_PERM))

const channels = ref<MarketSource[]>([])
const loading = ref(false)
const formOpen = ref(false)
/** The channel being renamed; null while adding one. */
const editing = ref<MarketSource | null>(null)

const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('market.settings.name'), mobile: 'title' },
  { key: 'branch', label: t('market.settings.branch'), mobile: 'meta' },
  { key: 'url', label: t('market.settings.url'), mobile: 'meta', mobileSpan: 2 },
  { key: 'enabled', label: t('market.settings.enabled'), mobile: 'meta' },
  { key: 'is_default', label: t('market.settings.default'), mobile: 'status' },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : [])
])

async function load() {
  loading.value = true
  try {
    channels.value = await fetchChannels()
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

async function patch(c: MarketSource, body: MarketSourcePatch) {
  try {
    await api.patch(`/market/sources/${c.id}`, body)
    toast(t('common.updated'), 'success')
  } catch (e) {
    notifyError(e)
  }
  await load()
}

function openForm(c: MarketSource | null) {
  editing.value = c
  formOpen.value = true
}

const remove = (c: MarketSource) => deleteWithConfirm(`/market/sources/${c.id}`, t('market.settings.deleteConfirm', { name: c.name }), load)

onMounted(load)
</script>

<template>
  <div>
    <SPageHeader :title="t('market.settings.title')" :description="t('market.settings.description')">
      <template v-if="canManage" #actions>
        <SButton variant="primary" icon="plus" data-testid="channel-add" @click="openForm(null)">{{ t('market.settings.add') }}</SButton>
      </template>
    </SPageHeader>

    <SCard class="mb-4">
      <SField :label="t('market.settings.repo')" :hint="t('market.settings.repoHint')" class="max-w-xl">
        <SInput :model-value="OFFICIAL_STORE_REPO" readonly class="font-mono" data-testid="store-repo" />
      </SField>
    </SCard>

    <STable :columns="columns" :rows="channels" :loading="loading" row-key="id" data-testid="channel-table">
      <template #empty><SEmpty icon="market" :text="t('market.settings.empty')" /></template>
      <template #cell-name="{ row }">
        <span class="font-medium">{{ row.name }}</span>
      </template>
      <template #cell-branch="{ row }"><span class="font-mono text-sm">{{ row.branch }}</span></template>
      <template #cell-url="{ row }">
        <SHint inline size="xs" class="break-all font-mono">{{ row.url }}</SHint>
      </template>
      <template #cell-enabled="{ row }">
        <SSwitch
          :model-value="row.enabled"
          :disabled="!canManage || row.is_default"
          :title="row.is_default ? t('market.settings.defaultLocked') : undefined"
          :aria-label="t('market.settings.enabled')"
          :data-testid="`channel-enabled-${row.id}`"
          @update:model-value="v => patch(row, { enabled: v })"
        />
      </template>
      <template #cell-is_default="{ row }">
        <SBadge v-if="row.is_default" tone="success" :data-testid="`channel-default-${row.id}`">{{ t('market.settings.default') }}</SBadge>
        <SButton v-else-if="canManage && row.enabled" size="sm" variant="ghost" :data-testid="`channel-set-default-${row.id}`" @click="patch(row, { is_default: true })">
          {{ t('market.settings.setDefault') }}
        </SButton>
        <SHint v-else inline>—</SHint>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end gap-2">
          <SButton size="sm" variant="ghost" :data-testid="`channel-rename-${row.id}`" @click="openForm(row)">{{ t('market.settings.rename') }}</SButton>
          <SButton
            size="sm"
            variant="ghost"
            tone="danger"
            :disabled="row.is_default"
            :title="row.is_default ? t('market.settings.defaultLocked') : undefined"
            :data-testid="`channel-delete-${row.id}`"
            @click="remove(row)"
          >
            {{ t('common.delete') }}
          </SButton>
        </div>
      </template>
    </STable>
    <SHint size="xs" class="mt-3">{{ t('market.settings.defaultHint') }}</SHint>

    <ChannelFormModal v-model:open="formOpen" :channel="editing" @saved="load" />
  </div>
</template>
