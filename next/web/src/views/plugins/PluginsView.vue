<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { SBadge, SButton, SEmpty, SHint, SInput, SLink, SPageHeader, SSelect, STable, toast, type TableColumn, type Tone } from '@sub2api/ui'
import PluginStatusBadge from './parts/PluginStatusBadge.vue'
import type { PluginSummary } from '@/api/types'
import { lt } from '@/i18n'
import { notifyError } from '@/utils/errors'
import { fetchFirstPage } from '@/composables/lookups'
import { useAuthStore } from '@/stores/auth'
import { UPLOAD_INSTALL_PATH, consentPath } from './consent/paths'
import { setPendingUpload } from './consent/pendingUpload'
import PluginAvatar from './parts/PluginAvatar.vue'
import RetiredKeys from './parts/RetiredKeys.vue'
import TrustBadge from './parts/TrustBadge.vue'
import PluginIdText from './parts/PluginIdText.vue'
import PluginSourceBadge from './parts/PluginSourceBadge.vue'
import { conflictLines, consoleSettingsPath, displayPluginId, refusalText, shownStatus } from './pluginUtil'
import { usePluginUpgrades } from './usePluginUpgrades'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const items = ref<PluginSummary[]>([])
const loading = ref(false)
const q = ref('')
const status = ref<string | null>(null)
const fileInput = ref<HTMLInputElement>()
/** The store's upgrades of the installed plugins (default channel). */
const upgrades = usePluginUpgrades(items)

const STATUSES = ['awaiting_consent', 'installed', 'enabling', 'enabled', 'upgrading', 'disabled']
const statusOptions = computed(() => STATUSES.map((s) => ({ value: s, label: t(`plugins.status.${s}`) })))

const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('plugins.list.plugin'), mobile: 'title' },
  { key: 'status', label: t('common.status'), mobile: 'status' },
  { key: 'version', label: t('common.version'), mobile: 'meta' },
  { key: 'publisher', label: t('plugins.publisher'), mobile: 'meta', mobileSpan: 2 },
  { key: 'source', label: t('market.source.title'), mobile: 'meta' },
  { key: 'nodes', label: t('plugins.list.nodes'), mobile: 'meta' },
  { key: 'actions', label: '', align: 'right' }
])

const rows = computed(() => {
  const kw = q.value.trim().toLowerCase()
  return items.value.filter((p) => {
    if (status.value && p.status !== status.value) return false
    if (!kw) return true
    return p.key.toLowerCase().includes(kw) || displayPluginId(p.key).includes(kw) || lt(p.name).toLowerCase().includes(kw) || (p.publisher || '').toLowerCase().includes(kw)
  })
})
/** The text of each refused plugin's refusal, by key: shown and as the tooltip. */
const refusals = computed(() => Object.fromEntries(items.value.flatMap((p) => (p.refusal ? [[p.key, refusalText(t, p.key, p.refusal)]] : []))))

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
  return p.status === 'awaiting_consent' ? undefined : consoleSettingsPath(p)
}

function review(p: PluginSummary) {
  const v = p.desired_version || p.active_version
  if (v) router.push(consentPath(p.key, v))
}

function pickFile() {
  fileInput.value?.click()
}

/** The chosen package is reviewed first: it is sent to be installed only with the consent. */
function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!/\.s2plugin$/i.test(file.name)) {
    toast(t('plugins.list.badFile'), 'error')
    return
  }
  setPendingUpload(file)
  router.push(UPLOAD_INSTALL_PATH)
}

onMounted(() => Promise.all([load(), upgrades.load()]))
</script>

<template>
  <div>
    <SPageHeader :title="t('nav.items.plugins')" :description="t('plugins.list.description')">
      <template #actions>
        <SButton :loading="loading" icon="refresh" @click="load">{{ t('common.refresh') }}</SButton>
        <SButton v-if="auth.has('plugin:market:read')" to="/market" icon="market" data-testid="plugins-market">
          {{ t('market.title') }}
          <SBadge v-if="upgrades.count.value" tone="warning" class="ml-1" data-testid="plugins-updatable">{{ t('market.updatable', { n: upgrades.count.value }) }}</SBadge>
        </SButton>
        <SButton v-if="auth.has('plugin:install')" variant="primary" icon="upload" @click="pickFile">
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
          <PluginAvatar :name="lt(row.name)" :plugin-key="row.key" :icon="row.icon" :asset-base="row.asset_base" />
          <div class="min-w-0">
            <div class="truncate font-medium text-gray-900 dark:text-white">{{ lt(row.name) || displayPluginId(row.key) }}</div>
            <SHint size="xs" class="truncate"><PluginIdText :id="row.key" /></SHint>
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
          <SHint v-if="row.role_grants_to_review?.length" inline size="xs" tone="warning" data-testid="plugin-role-grants">
            {{ t('plugins.roleGrants.short', { n: row.role_grants_to_review.length }, row.role_grants_to_review.length) }}
          </SHint>
          <SHint v-if="row.refusal" inline size="xs" tone="danger" class="max-w-[16rem] truncate" :title="refusals[row.key]" data-testid="plugin-refusal">
            {{ refusals[row.key] }}
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
        <SLink v-if="upgrades.upgrades.value[row.key]" class="ml-1.5" :to="upgrades.location(row)" data-testid="plugin-upgrade-badge" @click.stop>
          <SBadge tone="warning">{{ t('market.upgradeBadge', { version: upgrades.upgrades.value[row.key].latest_version }) }}</SBadge>
        </SLink>
      </template>
      <template #cell-publisher="{ row }">
        <div class="flex items-center gap-2">
          <span class="text-sm">{{ row.publisher || '—' }}</span>
          <TrustBadge :trust="row.trust" />
        </div>
      </template>
      <template #cell-source="{ row }"><PluginSourceBadge :plugin="row" /></template>
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
          <SButton v-if="upgrades.upgrades.value[row.key] && auth.has('plugin:install')" size="sm" variant="warning" icon="upload" :to="upgrades.location(row)" data-testid="plugin-upgrade">
            {{ t('market.upgrade') }}
          </SButton>
          <SButton size="sm" variant="ghost" @click="open(row)">{{ t('common.detail') }}</SButton>
          <SButton v-if="settingsPath(row)" size="sm" :to="settingsPath(row)" icon="settings">
            {{ t('plugins.detail.tabs.settings') }}
          </SButton>
        </div>
      </template>
    </STable>

    <div class="mt-3 space-y-1" data-testid="plugins-footnote">
      <SHint size="xs">{{ t('market.sourceNote') }}</SHint>
      <SHint size="xs">{{ t('market.identityNote') }}</SHint>
    </div>

    <RetiredKeys />
  </div>
</template>
