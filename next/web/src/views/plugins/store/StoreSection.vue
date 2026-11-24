<script setup lang="ts">
// The store of the store settings: its name, its repository (shown only
// here), its actions (rename, add a channel) and its channels: enable or
// disable, set the default, rename, delete. The default channel cannot be
// disabled or deleted (CONTRACTS §137.6).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SHint, SSwitch, STable, toast, type TableColumn } from '@sub2api/ui'
import type { MarketSource, MarketSourcePatch, MarketStore } from '@/api/marketTypes'
import { notifyError } from '@/utils/errors'
import { deleteWithConfirm } from '@/composables/crud'

const props = defineProps<{ store: MarketStore; channels: MarketSource[]; canManage: boolean }>()
const emit = defineEmits<{ rename: []; addChannel: []; renameChannel: [c: MarketSource]; changed: [] }>()
const { t } = useI18n()

const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('market.settings.name'), mobile: 'title' },
  { key: 'branch', label: t('market.settings.branch'), mobile: 'meta' },
  { key: 'enabled', label: t('market.settings.enabled'), mobile: 'meta' },
  { key: 'is_default', label: t('market.settings.default'), mobile: 'status' },
  ...(props.canManage ? [{ key: 'actions', label: '', align: 'right' as const }] : [])
])

async function patch(c: MarketSource, body: MarketSourcePatch) {
  try {
    await api.patch(`/market/sources/${c.id}`, body)
    toast(t('common.updated'), 'success')
  } catch (e) {
    notifyError(e)
  }
  emit('changed')
}

const removeChannel = (c: MarketSource) => deleteWithConfirm(`/market/sources/${c.id}`, t('market.settings.deleteConfirm', { name: c.name }), () => emit('changed'))
</script>

<template>
  <STable card :columns="columns" :rows="channels" row-key="id" :data-testid="`store-${store.id}`">
    <template #toolbar>
      <div class="flex w-full flex-wrap items-center gap-x-3 gap-y-2">
        <h2 class="text-base font-semibold">{{ store.name }}</h2>
        <SBadge v-if="store.official" tone="accent" :data-testid="`store-kind-${store.id}`">{{ t('market.stores.official') }}</SBadge>
        <span class="font-mono text-xs text-fg-muted" :data-testid="`store-repo-${store.id}`">{{ store.repo }}</span>
        <div v-if="canManage" class="ml-auto flex flex-wrap gap-1">
          <SButton size="sm" variant="ghost" :data-testid="`store-rename-${store.id}`" @click="emit('rename')">{{ t('market.settings.rename') }}</SButton>
          <SButton size="sm" variant="ghost" icon="plus" :data-testid="`store-add-channel-${store.id}`" @click="emit('addChannel')">{{ t('market.settings.add') }}</SButton>
        </div>
      </div>
    </template>
    <template #cell-name="{ row }"><span class="font-medium">{{ row.name }}</span></template>
    <template #cell-branch="{ row }"><span class="font-mono text-sm">{{ row.branch }}</span></template>
    <template #cell-enabled="{ row }">
      <SSwitch
        :model-value="row.enabled"
        :disabled="!canManage || row.is_default"
        :title="row.is_default ? t('market.settings.defaultLocked') : undefined"
        :aria-label="t('market.settings.enabledOf', { name: row.name })"
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
        <SButton size="sm" variant="ghost" :data-testid="`channel-rename-${row.id}`" @click="emit('renameChannel', row)">{{ t('market.settings.rename') }}</SButton>
        <SButton
          size="sm"
          variant="ghost"
          tone="danger"
          :disabled="row.is_default"
          :title="row.is_default ? t('market.settings.defaultLocked') : undefined"
          :data-testid="`channel-delete-${row.id}`"
          @click="removeChannel(row)"
        >
          {{ t('common.delete') }}
        </SButton>
      </div>
    </template>
    <template #footer>
      <SHint size="xs" class="py-2.5" :data-testid="`store-note-${store.id}`">{{ t('market.stores.officialNote') }}</SHint>
    </template>
  </STable>
</template>
