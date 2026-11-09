<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SEmpty, SHint, SInput, SPageHeader, SSelect, STable, toast, type TableColumn, type Tone } from '@sub2api/ui'
import PluginStatusBadge from './parts/PluginStatusBadge.vue'
import type { PluginReview, PluginSummary } from '@/api/types'
import { lt } from '@/i18n'
import { notifyError } from '@/utils/errors'
import { fetchFirstPage } from '@/composables/lookups'
import { useAuthStore } from '@/stores/auth'
import { consentPath, putReview } from './reviewCache'
import PluginAvatar from './parts/PluginAvatar.vue'
import RetiredKeys from './parts/RetiredKeys.vue'
import TrustBadge from './parts/TrustBadge.vue'
import { conflictLines, consoleSettingsPath, shownStatus } from './pluginUtil'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const items = ref<PluginSummary[]>([])
const loading = ref(false)
const q = ref('')
const status = ref<string | null>(null)
const uploading = ref(false)
const fileInput = ref<HTMLInputElement>()

const STATUSES = ['awaiting_consent', 'installed', 'enabling', 'enabled', 'upgrading', 'disabled']
const statusOptions = computed(() => STATUSES.map((s) => ({ value: s, label: t(`plugins.status.${s}`) })))

const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('plugins.list.plugin'), mobile: 'title' },
  { key: 'status', label: t('common.status'), mobile: 'status' },
  { key: 'version', label: t('common.version'), mobile: 'meta' },
  { key: 'publisher', label: t('plugins.publisher'), mobile: 'meta', mobileSpan: 2 },
  { key: 'nodes', label: t('plugins.list.nodes'), mobile: 'meta' },
  { key: 'actions', label: '', align: 'right' }
])

const rows = computed(() => {
  const kw = q.value.trim().toLowerCase()
  return items.value.filter((p) => {
    if (status.value && p.status !== status.value) return false
    if (!kw) return true
    return p.key.toLowerCase().includes(kw) || lt(p.name).toLowerCase().includes(kw) || (p.publisher || '').toLowerCase().includes(kw)
  })
})

async function load() {
  loading.value = true
  try {
    items.value = await fetchFirstPage<PluginSummary>('/plugins')
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

/** node_summary {total, states:{state: n}} -> [{state, count}]. */
function nodeSummary(p: PluginSummary): Array<{ state: string; count: number }> {
  const states = p.node_summary?.states || {}
  return Object.entries(states)
    .map(([state, count]) => ({ state, count: Number(count) || 0 }))
    .filter((x) => x.count > 0)
}

function nodeTone(s: string): Tone {
  return s === 'running' || s === 'active' || s === 'ready' || s === 'healthy' ? 'success' : s === 'failed' || s === 'crashed' || s === 'unavailable' ? 'danger' : 'gray'
}

function open(p: PluginSummary) {
  router.push(`/plugins/${encodeURIComponent(p.key)}`)
}

function settingsPath(p: PluginSummary): string | undefined {
  return p.status === 'awaiting_consent' ? undefined : consoleSettingsPath(p, perm => auth.has(perm))
}

function review(p: PluginSummary) {
  const v = p.desired_version || p.active_version
  if (v) router.push(consentPath(p.key, v))
}

function pickFile() {
  fileInput.value?.click()
}

async function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!/\.s2plugin$/i.test(file.name)) {
    toast(t('plugins.list.badFile'), 'error')
    return
  }
  uploading.value = true
  try {
    const fd = new FormData()
    fd.append('file', file)
    const r = await api.upload<PluginReview>('/plugins/upload', fd)
    putReview(r)
    toast(t('plugins.list.uploaded', { name: lt(r.name) || r.plugin_key, version: r.version }), 'success')
    router.push(consentPath(r.plugin_key, r.version))
  } catch (err) {
    notifyError(err)
  } finally {
    uploading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <SPageHeader :title="t('nav.items.plugins')" :description="t('plugins.list.description')">
      <template #actions>
        <SButton :loading="loading" icon="refresh" @click="load">{{ t('common.refresh') }}</SButton>
        <SButton v-if="auth.has('plugin:market:read')" to="/market" icon="market">
          {{ t('plugins.market.title') }}
        </SButton>
        <SButton v-if="auth.has('plugin:install')" variant="primary" :loading="uploading" icon="upload" @click="pickFile">
          {{ t('plugins.list.upload') }}
        </SButton>
        <input ref="fileInput" type="file" accept=".s2plugin" class="hidden" @change="onFile" />
      </template>
      <template #filters>
        <SInput v-model="q" class="w-64" :placeholder="t('plugins.list.searchPlaceholder')" :aria-label="t('common.search')" />
        <div class="w-48">
          <SSelect v-model="status" :options="statusOptions" :placeholder="t('plugins.list.allStatuses')" :aria-label="t('common.status')" />
        </div>
      </template>
    </SPageHeader>

    <STable :columns="columns" :rows="rows" :loading="loading" row-key="key" class="cursor-pointer" @row-click="open">
      <template #empty>
        <SEmpty icon="plugin" :text="t('plugins.list.empty')" />
      </template>
      <template #cell-name="{ row }">
        <div class="flex items-center gap-3">
          <PluginAvatar :name="lt(row.name)" :plugin-key="row.key" :icon="(row as any).icon" />
          <div class="min-w-0">
            <div class="truncate font-medium text-gray-900 dark:text-white">{{ lt(row.name) || row.key }}</div>
            <SHint size="xs" class="truncate font-mono">{{ row.key }}</SHint>
          </div>
        </div>
      </template>
      <template #cell-status="{ row }">
        <div class="flex flex-col items-start gap-0.5">
          <PluginStatusBadge :status="shownStatus(row)" />
          <SHint
            v-if="row.resource_conflicts?.length"
            inline
            size="xs"
            tone="danger"
            class="max-w-[16rem] truncate"
            :title="conflictLines(row).join('\n')"
            data-testid="plugin-conflicts"
          >
            {{ t('plugins.conflicts.short', { n: row.resource_conflicts.length }) }}
          </SHint>
          <SHint v-if="row.status_reason" inline size="xs" tone="danger" class="max-w-[16rem] truncate" :title="row.status_reason">{{ row.status_reason }}</SHint>
        </div>
      </template>
      <template #cell-version="{ row }">
        <span class="font-mono text-sm">{{ row.active_version ? 'v' + row.active_version : '—' }}</span>
        <span
          v-if="row.desired_version && row.desired_version !== row.active_version"
          class="ml-1 font-mono text-xs text-primary-600 dark:text-primary-400"
          :title="t('plugins.desiredVersion')"
          >→ v{{ row.desired_version }}</span
        >
      </template>
      <template #cell-publisher="{ row }">
        <div class="flex items-center gap-2">
          <span class="text-sm">{{ row.publisher || '—' }}</span>
          <TrustBadge :trust="row.trust" />
          <SBadge v-if="row.builtin" tone="info" :title="t('plugins.builtinHint')">{{ t('plugins.builtin') }}</SBadge>
        </div>
      </template>
      <template #cell-nodes="{ row }">
        <div class="flex flex-wrap gap-1">
          <SBadge v-for="n in nodeSummary(row)" :key="n.state" :tone="nodeTone(n.state)">{{ n.state }} × {{ n.count }}</SBadge>
          <SHint v-if="nodeSummary(row).length === 0" inline>—</SHint>
        </div>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex justify-end gap-2" @click.stop>
          <SButton v-if="row.status === 'awaiting_consent' && auth.has('plugin:install')" size="sm" variant="primary" icon="shield" @click="review(row)">
            {{ t('plugins.list.review') }}
          </SButton>
          <SButton size="sm" variant="ghost" @click="open(row)">{{ t('common.detail') }}</SButton>
          <SButton v-if="settingsPath(row)" size="sm" :to="settingsPath(row)" icon="settings">
            {{ t('plugins.detail.tabs.settings') }}
          </SButton>
        </div>
      </template>
    </STable>

    <RetiredKeys />
  </div>
</template>
