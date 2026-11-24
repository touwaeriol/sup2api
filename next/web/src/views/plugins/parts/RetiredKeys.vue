<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SCard, SChoiceCards, SField, SHint, SIcon, SInput, SFormModal, STable, toast, type ChoiceOption, type TableColumn } from '@sub2api/ui'
import type { ReleaseRetiredKeyResult, RetiredKey } from '@/api/pluginKeys'
import { notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/format'
import { tablePrefix } from '../pluginUtil'
import PluginIdText from './PluginIdText.vue'

// Keys of uninstalled plugins stay with their publisher (CONTRACTS §55.1): only
// that publisher may install the key again until an administrator releases
// it here, optionally deleting what the plugin left behind.
const { t } = useI18n()
const auth = useAuthStore()

const items = ref<RetiredKey[]>([])
const releasing = ref<RetiredKey | null>(null)
/** What to delete before releasing the key: 'accounts' and / or 'purge' (the tables the plugin left, KV data). */
const purgeChoice = ref<ChoiceOption['value'][]>([])
const purge = computed(() => purgeChoice.value.includes('purge'))
const purgeAccounts = computed(() => purgeChoice.value.includes('accounts'))
const typed = ref('')
const busy = ref(false)
const matches = computed(() => !!releasing.value && typed.value.trim() === releasing.value.display_id)

const purgeOptions = computed<ChoiceOption[]>(() => {
  const k = releasing.value
  if (!k) return []
  return [
    { value: 'accounts', label: t('plugins.retired.purgeAccounts', { n: k.accounts }), icon: 'account', tone: 'danger' },
    { value: 'purge', label: k.schema ? t('plugins.retired.purge', { prefix: tablePrefix(k.key) }) : t('plugins.retired.purgeKv'), icon: 'trash', tone: 'danger' }
  ]
})

const columns = computed<TableColumn[]>(() => [
  { key: 'key', label: t('plugins.retired.key') },
  { key: 'publisher', label: t('plugins.publisher') },
  { key: 'left', label: t('plugins.retired.left') },
  { key: 'retired_at', label: t('plugins.retired.retiredAt') },
  { key: 'actions', label: '', align: 'right' }
])

async function load() {
  try {
    const r = await api.get<{ items: RetiredKey[] }>('/plugins/retired-keys')
    items.value = r.items || []
  } catch (e) {
    notifyError(e)
  }
}

function openRelease(k: RetiredKey) {
  releasing.value = k
  purgeChoice.value = []
  typed.value = ''
}

async function release() {
  const k = releasing.value
  if (!k || !matches.value) return
  busy.value = true
  try {
    const r = await api.post<ReleaseRetiredKeyResult>(`/plugins/retired-keys/${encodeURIComponent(k.key)}/release`, {
      purge: purge.value,
      purge_accounts: purgeAccounts.value
    })
    toast(t('plugins.retired.released', { key: k.display_id, n: r?.accounts_deleted ?? 0 }), 'success')
    releasing.value = null
    await load()
  } catch (e) {
    notifyError(e)
  } finally {
    busy.value = false
  }
}

onMounted(load)
defineExpose({ load })
</script>

<template>
  <SCard v-if="items.length" class="mt-6" :title="t('plugins.retired.title')" data-testid="retired-keys">
    <SHint class="mb-3">{{ t('plugins.retired.description') }}</SHint>
    <STable :columns="columns" :rows="items" row-key="key">
      <template #cell-key="{ row }">
        <PluginIdText :id="row.key" class="text-sm" />
        <SBadge v-if="row.installed" tone="info" class="ml-2">{{ t('plugins.retired.reinstalled') }}</SBadge>
      </template>
      <template #cell-publisher="{ row }">
        <span class="text-sm">{{ row.publisher || t('plugins.retired.unsigned') }}</span>
      </template>
      <template #cell-left="{ row }">
        <div class="flex flex-wrap gap-1">
          <SBadge v-if="row.accounts > 0" tone="warning">{{ t('plugins.retired.accounts', { n: row.accounts }) }}</SBadge>
          <SBadge v-if="row.schema" tone="warning">{{ t('plugins.retired.tables', { prefix: tablePrefix(row.key) }) }}</SBadge>
          <SHint v-if="!row.accounts && !row.schema" inline>—</SHint>
        </div>
      </template>
      <template #cell-retired_at="{ row }">
        <span class="text-sm">{{ formatDateTime(row.retired_at) }}</span>
      </template>
      <template #cell-actions="{ row }">
        <SButton v-if="auth.has('plugin:uninstall') && !row.installed" size="sm" icon="key" @click="openRelease(row)">
          {{ t('plugins.retired.release') }}
        </SButton>
      </template>
    </STable>

    <SFormModal
      :open="!!releasing"
      :title="releasing ? t('plugins.retired.releaseTitle', { key: releasing.display_id }) : ''"
      width="md"
      :loading="busy"
      :submit-disabled="!matches"
      submit-variant="danger"
      :submit-text="t('plugins.retired.release')"
      @update:open="(v) => !v && (releasing = null)"
      @submit="release"
    >
      <div v-if="releasing" class="space-y-4 text-sm">
        <p>{{ t('plugins.retired.releaseBody') }}</p>
        <SChoiceCards
          v-model="purgeChoice"
          :options="purgeOptions"
          :cols="1"
          multiple
          :aria-label="t('plugins.retired.releaseTitle', { key: releasing.display_id })"
        />
        <SHint v-if="!purge || !purgeAccounts" size="xs" tone="warning" class="flex items-center gap-1.5">
          <SIcon name="warning" class="h-4 w-4" />{{ t('plugins.retired.inheritWarn') }}
        </SHint>
        <SField :label="t('plugins.uninstall.typeKey', { key: releasing.display_id })">
          <SInput v-model="typed" mono :placeholder="releasing.display_id" autocomplete="off" />
        </SField>
      </div>
    </SFormModal>
  </SCard>
</template>
