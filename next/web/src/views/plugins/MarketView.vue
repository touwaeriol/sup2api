<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { requestRaw } from '@sub2api/host'
import { SBadge, SButton, SCard, SChoiceCards, SEmpty, SField, SHint, SIcon, SInput, SLink, SModal, SPageHeader, SSelect, SSpinner, type ChoiceOption } from '@sub2api/ui'
import type { MarketPlugin, MarketSource, MarketVersion, PluginSummary } from '@/api/types'
import { lt } from '@/i18n'
import { notifyError } from '@/utils/errors'
import { fetchFirstPage } from '@/composables/lookups'
import { formatBytes } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'
import { marketInstallLocation } from './consent/paths'
import { compareVersions } from './pluginUtil'
import PluginAvatar from './parts/PluginAvatar.vue'
import TrustBadge from './parts/TrustBadge.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const sources = ref<MarketSource[]>([])
const sourceId = ref<number | null>(null)
const items = ref<MarketPlugin[]>([])
/** Version of the running core (top-level host_version of GET /market/plugins). */
const hostVersion = ref('')
const installed = ref<Record<string, PluginSummary>>({})
const q = ref('')
const loading = ref(false)
const loadingSources = ref(true)

const picker = ref<MarketPlugin | null>(null)
const pickVersion = ref('')

const sourceOptions = computed(() => sources.value.map((s) => ({ value: s.id, label: s.enabled ? s.name : `${s.name} (${t('common.disabled')})` })))

function latestOf(m: MarketPlugin): string {
  if (m.latest_version) return m.latest_version
  return [...(m.versions || [])].sort((a, b) => compareVersions(b.version, a.version))[0]?.version || ''
}

// Compatibility with the running core (CONTRACTS §14.3).
function incompatible(v: MarketVersion | undefined | null): boolean {
  return v?.compatible === false
}

function versionOf(m: MarketPlugin, version: string): MarketVersion | undefined {
  return (m.versions || []).find((v) => v.version === version)
}

/** Newest version installable on this core ('' when none is). */
function latestCompatibleOf(m: MarketPlugin): string {
  return [...(m.versions || [])].filter((v) => !incompatible(v)).sort((a, b) => compareVersions(b.version, a.version))[0]?.version || ''
}

/** Why a version cannot be installed, or '' when it can. */
function incompatibleReason(v: MarketVersion): string {
  if (!incompatible(v)) return ''
  return t('plugins.market.incompatibleReason', { range: v.host_compat || '—', version: hostVersion.value || '?' })
}

function installedOf(m: MarketPlugin): string {
  const p = installed.value[m.key]
  return p?.active_version || p?.desired_version || m.installed_version || ''
}

function stateOf(m: MarketPlugin): 'install' | 'upgrade' | 'installed' {
  const iv = installedOf(m)
  if (!iv && !installed.value[m.key]) return 'install'
  if (!iv) return 'installed'
  // Only offer upgrades this core can run.
  const target = m.versions?.length ? latestCompatibleOf(m) : latestOf(m)
  return target && compareVersions(target, iv) > 0 ? 'upgrade' : 'installed'
}

/** True when the listing has versions but none is compatible with this core. */
function noCompatible(m: MarketPlugin): boolean {
  return (m.versions?.length || 0) > 0 && !latestCompatibleOf(m)
}

const rows = computed(() => {
  const kw = q.value.trim().toLowerCase()
  if (!kw) return items.value
  return items.value.filter(
    (m) =>
      m.key.toLowerCase().includes(kw) ||
      lt(m.name).toLowerCase().includes(kw) ||
      lt(m.description).toLowerCase().includes(kw) ||
      (m.publisher || '').toLowerCase().includes(kw) ||
      (m.categories || []).some((c) => c.toLowerCase().includes(kw))
  )
})

const pickerVersions = computed(() => [...(picker.value?.versions || [])].sort((a, b) => compareVersions(b.version, a.version)))
/** Version cards of the picker: installed and incompatible versions cannot be chosen. */
const pickerChoices = computed<ChoiceOption[]>(() =>
  pickerVersions.value.map((v) => {
    const meta = [v.host_compat ? `${t('plugins.consent.hostCompat')} ${v.host_compat}` : '', v.size ? formatBytes(v.size) : ''].filter(Boolean).join(' · ')
    return {
      value: v.version,
      label: 'v' + v.version,
      description: incompatible(v) ? incompatibleReason(v) : meta || undefined,
      disabled: v.version === installedOf(picker.value!) || incompatible(v)
    }
  })
)

async function loadSources() {
  loadingSources.value = true
  try {
    const items = await fetchFirstPage<MarketSource>('/market/sources')
    sources.value = items
    sourceId.value = (items.find((s) => s.enabled) || items[0])?.id ?? null
  } catch (e) {
    notifyError(e)
  } finally {
    loadingSources.value = false
  }
}

async function loadInstalled() {
  if (!auth.has('plugin:read')) return
  try {
    const items = await fetchFirstPage<PluginSummary>('/plugins')
    installed.value = Object.fromEntries(items.map((p) => [p.key, p]))
  } catch {
    installed.value = {}
  }
}

async function loadPlugins() {
  if (sourceId.value === null) {
    items.value = []
    return
  }
  loading.value = true
  try {
    // Raw envelope: host_version sits next to data (or inside it on some servers).
    const json = await requestRaw('GET', '/market/plugins', { query: { source_id: sourceId.value, page_size: 200 } })
    const data = json?.data
    items.value = Array.isArray(data) ? data : Array.isArray(data?.items) ? data.items : Array.isArray(data?.plugins) ? data.plugins : []
    hostVersion.value = String(json?.host_version ?? data?.host_version ?? '')
  } catch (e) {
    items.value = []
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function openPicker(m: MarketPlugin) {
  picker.value = m
  const latest = latestOf(m)
  pickVersion.value = incompatible(versionOf(m, latest)) ? latestCompatibleOf(m) : latest
}

/** The picked version cannot be installed (already installed or incompatible). */
const pickBlocked = computed(() => {
  const m = picker.value
  if (!m || !pickVersion.value) return true
  return pickVersion.value === installedOf(m) || incompatible(versionOf(m, pickVersion.value))
})

/** Reviews the picked version first: nothing is downloaded or registered before the consent. */
function install() {
  const m = picker.value
  if (!m || sourceId.value === null || pickBlocked.value) return
  picker.value = null
  router.push(marketInstallLocation(sourceId.value, m.key, pickVersion.value))
}

watch(sourceId, loadPlugins)
onMounted(async () => {
  await Promise.all([loadSources(), loadInstalled()])
})
</script>

<template>
  <div>
    <SPageHeader :title="t('plugins.market.title')" :description="t('plugins.market.description')">
      <template #actions>
        <SButton :loading="loading" icon="refresh" @click="loadPlugins(), loadInstalled()">{{ t('common.refresh') }}</SButton>
      </template>
      <template #filters>
        <SField :label="t('plugins.market.source')" class="w-56">
          <SSelect
            :model-value="sourceId"
            :options="sourceOptions"
            :disabled="loadingSources || !sources.length"
            @update:model-value="(v) => (sourceId = v === null ? null : Number(v))"
          />
        </SField>
        <SField :label="t('common.search')" class="w-72">
          <SInput v-model="q" :placeholder="t('plugins.market.searchPlaceholder')" />
        </SField>
        <SHint v-if="hostVersion" size="xs" class="self-end pb-2" data-testid="market-host-version">
          {{ t('plugins.market.hostVersion', { version: hostVersion }) }}
        </SHint>
      </template>
    </SPageHeader>

    <div v-if="loadingSources || (loading && !items.length)" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!sources.length">
      <SEmpty icon="market" :text="t('plugins.market.noSources')" />
    </SCard>

    <SCard v-else-if="!rows.length">
      <SEmpty icon="market" :text="q ? t('plugins.market.noMatch') : t('plugins.market.empty')" />
    </SCard>

    <SCard v-else :padded="false" :class="loading ? 'opacity-60' : ''">
      <div v-for="m in rows" :key="m.key" class="flex flex-wrap items-center gap-4 border-b border-gray-100 px-5 py-4 last:border-b-0 dark:border-dark-700">
        <PluginAvatar :name="lt(m.name)" :plugin-key="m.key" :icon="(m as any).icon" size="lg" />
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-semibold text-gray-900 dark:text-white">{{ lt(m.name) || m.key }}</span>
            <SHint inline size="xs" class="font-mono">{{ m.key }}</SHint>
            <span v-if="latestOf(m)" class="font-mono text-sm">v{{ latestOf(m) }}</span>
            <SBadge
              v-if="incompatible(versionOf(m, latestOf(m)))"
              tone="danger"
              :title="incompatibleReason(versionOf(m, latestOf(m))!)"
              data-testid="market-incompatible"
            >
              {{ t('plugins.market.incompatible') }}
            </SBadge>
            <TrustBadge :trust="m.trust" />
            <SBadge v-for="c in m.categories || []" :key="c" tone="gray">{{ c }}</SBadge>
          </div>
          <p v-if="m.description" class="mt-1 line-clamp-2 text-sm text-gray-600 dark:text-gray-300">{{ lt(m.description) }}</p>
          <SHint size="xs" class="mt-1">
            {{ t('plugins.publisher') }} {{ m.publisher || '—' }}
            <template v-if="installedOf(m)">
              · {{ t('plugins.market.installedVersion', { version: installedOf(m) }) }}
              <SLink v-if="installed[m.key]" :to="`/plugins/${encodeURIComponent(m.key)}`" class="ml-1">{{ t('common.detail') }}</SLink>
            </template>
          </SHint>
          <SHint v-if="noCompatible(m)" size="xs" tone="danger" class="mt-1 flex items-center gap-1">
            <SIcon name="warning" class="h-3.5 w-3.5" />{{ t('plugins.market.noCompatible', { version: hostVersion || '?' }) }}
          </SHint>
          <SHint v-else-if="stateOf(m) !== 'install' && latestCompatibleOf(m) && latestCompatibleOf(m) !== latestOf(m)" size="xs" class="mt-1">
            {{ t('plugins.market.newerIncompatible', { version: latestOf(m) }) }}
          </SHint>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <template v-if="stateOf(m) === 'installed'">
            <SBadge tone="success"><SIcon name="check" class="h-3.5 w-3.5" />{{ t('plugins.market.installed') }}</SBadge>
            <SButton v-if="auth.has('plugin:install') && (m.versions?.length || 0) > 1" size="sm" variant="ghost" @click="openPicker(m)">
              {{ t('plugins.market.versions') }}
            </SButton>
          </template>
          <SButton
            v-else-if="auth.has('plugin:install')"
            :variant="stateOf(m) === 'upgrade' ? 'warning' : 'primary'"
            size="sm"
            :icon="stateOf(m) === 'upgrade' ? 'upload' : 'download'"
            :disabled="noCompatible(m)"
            :title="noCompatible(m) ? t('plugins.market.noCompatible', { version: hostVersion || '?' }) : undefined"
            @click="openPicker(m)"
          >
            {{ stateOf(m) === 'upgrade' ? t('plugins.market.upgrade') : t('plugins.market.install') }}
          </SButton>
        </div>
      </div>
    </SCard>

    <SModal :open="!!picker" :title="picker ? t('plugins.market.pickTitle', { name: lt(picker.name) || picker.key }) : ''" width="md" @update:open="(v) => !v && (picker = null)">
      <template v-if="picker">
        <SHint class="mb-3">{{ t('plugins.market.pickHint') }}</SHint>
        <div class="max-h-80 overflow-y-auto">
          <SChoiceCards
            :model-value="pickVersion"
            :options="pickerChoices"
            :cols="1"
            :aria-label="t('plugins.market.pickTitle', { name: lt(picker.name) || picker.key })"
            @update:model-value="(v) => (pickVersion = String(v))"
          >
            <template v-for="v in pickerVersions" :key="v.version" #[`option-${v.version}`]>
              <span class="mt-1.5 flex flex-wrap items-center gap-1.5" :data-version="v.version">
                <SBadge v-if="v.version === latestOf(picker)" tone="primary">{{ t('plugins.market.latest') }}</SBadge>
                <SBadge v-if="v.version === installedOf(picker)" tone="success">{{ t('plugins.market.installed') }}</SBadge>
                <SBadge v-if="v.compatible === true" tone="success" data-testid="version-compatible">{{ t('plugins.market.compatible') }}</SBadge>
                <SBadge v-else-if="incompatible(v)" tone="danger" data-testid="version-incompatible">{{ t('plugins.market.incompatible') }}</SBadge>
              </span>
            </template>
          </SChoiceCards>
        </div>
        <SHint v-if="hostVersion" size="xs" class="mt-3">{{ t('plugins.market.hostVersion', { version: hostVersion }) }}</SHint>
      </template>
      <template #footer>
        <SButton @click="picker = null">{{ t('common.cancel') }}</SButton>
        <SButton variant="primary" :disabled="pickBlocked" icon="shield" data-testid="market-continue" @click="install">
          {{ t('plugins.market.continue') }}
        </SButton>
      </template>
    </SModal>
  </div>
</template>
