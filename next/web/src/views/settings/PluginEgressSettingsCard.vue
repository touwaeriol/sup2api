<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SCard, SRowAction, SSelect, SSettingRow, SSettingsGroup, SSpinner, STable, confirm, toast, type TableColumn } from '@sub2api/ui'
import { errorMessage, notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import PluginEgressAddDialog from './PluginEgressAddDialog.vue'
import { listField, PLUGIN_EGRESS_PATH, type PluginEgressMode, type PluginEgressPolicy } from './pluginEgress'

// Plugin network access (GET/PUT /settings/plugin-egress, CONTRACTS §113):
// a default for all domains and one list whose meaning follows it - the
// blocked domains while everything else is allowed, the allowed domains
// while everything else is blocked. Both lists are kept; every change is
// saved at once, as the browser's site settings this page follows do. The
// setting rules every request a plugin makes, the platform plugins' upstream
// requests included, so blocking all domains is confirmed first and warned
// about while it lasts.
const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('settings:manage'))

const policy = ref<PluginEgressPolicy | null>(null)
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const adding = ref(false)

const mode = computed<PluginEgressMode>(() => policy.value?.mode ?? 'allow_all')
const field = computed(() => listField(mode.value))
const domains = computed(() => policy.value?.[field.value] ?? [])
const rows = computed(() => domains.value.map(domain => ({ domain })))
const modeOptions = computed(() => (['allow_all', 'block_all'] as const).map(value => ({ value, label: t(`pluginEgress.modes.${value}`) })))
const columns = computed<TableColumn[]>(() => [
  { key: 'domain', label: t('pluginEgress.domain'), mobile: 'title' },
  ...(canManage.value ? [{ key: 'actions', label: '', align: 'right' as const }] : [])
])

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    policy.value = await api.get<PluginEgressPolicy>(PLUGIN_EGRESS_PATH)
  } catch (e) {
    loadError.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

/** Saves the fields given; true when saved. */
async function save(patch: Partial<PluginEgressPolicy>): Promise<boolean> {
  saving.value = true
  try {
    policy.value = await api.put<PluginEgressPolicy>(PLUGIN_EGRESS_PATH, patch)
    toast(t('common.saved'), 'success')
    return true
  } catch (e) {
    notifyError(e)
    return false
  } finally {
    saving.value = false
  }
}

async function setMode(v: unknown) {
  if (v === mode.value || (v !== 'allow_all' && v !== 'block_all')) return
  if (v === 'block_all') {
    const ok = await confirm({
      title: t('pluginEgress.blockAllConfirm.title'),
      message: t('pluginEgress.blockAllConfirm.message'),
      confirmText: t('pluginEgress.modes.block_all'),
      danger: true
    })
    if (!ok) return
  }
  void save({ mode: v })
}

async function add(list: string[]) {
  if (await save({ [field.value]: [...domains.value, ...list] })) adding.value = false
}

function remove(domain: string) {
  void save({ [field.value]: domains.value.filter(d => d !== domain) })
}

onMounted(load)
</script>

<template>
  <div class="space-y-4" data-testid="plugin-egress-settings">
    <SSettingsGroup :title="t('pluginEgress.title')" :description="t('pluginEgress.description')">
      <template v-if="loadError" #actions>
        <SButton size="sm" icon="refresh" :loading="loading" @click="load">{{ t('common.refresh') }}</SButton>
      </template>
      <SSpinner v-if="loading && !policy" block />
      <SAlert v-else-if="loadError" tone="danger" class="my-4">{{ loadError }}</SAlert>
      <template v-else-if="policy">
        <SSettingRow :label="t('pluginEgress.defaultLabel')" :description="t('pluginEgress.defaultHint')">
          <SSelect
            :model-value="mode"
            :options="modeOptions"
            :disabled="!canManage || saving"
            :aria-label="t('pluginEgress.defaultLabel')"
            class="w-full sm:w-56"
            data-testid="plugin-egress-mode"
            @update:model-value="setMode"
          />
        </SSettingRow>
        <SAlert tone="info" dense class="mb-4" data-testid="plugin-egress-info">{{ t(`pluginEgress.info.${mode}`) }}</SAlert>
        <SAlert v-if="mode === 'block_all'" tone="warning" :title="t('pluginEgress.blockAllWarning.title')" class="mb-4" data-testid="plugin-egress-block-all-warning">
          {{ t('pluginEgress.blockAllWarning.message') }}
        </SAlert>
      </template>
    </SSettingsGroup>

    <SCard v-if="policy" :title="t(`pluginEgress.list.${mode}`)" :subtitle="t(`pluginEgress.listHint.${mode}`)" :padded="false" data-testid="plugin-egress-list">
      <template v-if="canManage" #actions>
        <SButton size="sm" icon="plus" :disabled="saving" data-testid="plugin-egress-add" @click="adding = true">{{ t('pluginEgress.add') }}</SButton>
      </template>
      <STable :columns="columns" :rows="rows" row-key="domain" :empty-text="t('pluginEgress.empty')" dense>
        <template #cell-domain="{ row }">
          <span class="font-mono text-sm">{{ row.domain }}</span>
        </template>
        <template #cell-actions="{ row }">
          <SRowAction
            :label="t('common.remove')"
            icon="trash"
            tone="danger"
            :disabled="saving"
            :data-testid="`plugin-egress-remove-${row.domain}`"
            @click="remove(row.domain)"
          />
        </template>
      </STable>
    </SCard>

    <PluginEgressAddDialog v-model:open="adding" :mode="mode" :existing="domains" :saving="saving" @submit="add" />
  </div>
</template>
