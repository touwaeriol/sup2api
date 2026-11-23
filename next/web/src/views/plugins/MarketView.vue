<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { requestRaw } from '@sub2api/host'
import { SButton, SCard, SEmpty, SField, SHint, SInput, SPageHeader, SSelect, SSpinner } from '@sub2api/ui'
import type { MarketPlugin, MarketSource, PluginSummary } from '@/api/types'
import { notifyError } from '@/utils/errors'
import { fetchFirstPage } from '@/composables/lookups'
import { useAuthStore } from '@/stores/auth'
import { marketInstallLocation } from './consent/paths'
import { compareVersions } from './pluginUtil'
import { matches, noCompatible, publisherBlocked, publisherOptions } from './marketUtil'
import MarketRow from './parts/MarketRow.vue'
import MarketVersionPicker from './parts/MarketVersionPicker.vue'

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
/** Publisher filter ('' = all). */
const publisher = ref('')
const loading = ref(false)
const loadingSources = ref(true)

/** The plugin whose version picker is open. */
const picker = ref<MarketPlugin | null>(null)

const sourceOptions = computed(() =>
  sources.value.map(s => ({ value: s.id, label: s.enabled ? s.name : `${s.name} (${t('common.disabled')})` }))
)
const publisherChoices = computed(() => [{ value: '', label: t('plugins.market.allPublishers') }, ...publisherOptions(items.value)])

function installedOf(m: MarketPlugin): string {
  const p = installed.value[m.key]
  return p?.active_version || p?.desired_version || m.installed_version || ''
}

function stateOf(m: MarketPlugin): 'install' | 'upgrade' | 'installed' {
  const iv = installedOf(m)
  if (!iv && !installed.value[m.key]) return 'install'
  if (!iv) return 'installed'
  // Only offer upgrades this core can run, never to a deprecated version (latest_version).
  return m.latest_version && compareVersions(m.latest_version, iv) > 0 ? 'upgrade' : 'installed'
}

/** Why m cannot be installed at all ('' when it can). */
function blockedReason(m: MarketPlugin): string {
  if (publisherBlocked(m)) return t('plugins.market.publisherBlocked')
  return noCompatible(m) ? t('plugins.market.noCompatible', { version: hostVersion.value || '?' }) : ''
}

const rows = computed(() => {
  const kw = q.value.trim().toLowerCase()
  return items.value.filter(m => (!publisher.value || m.publisher === publisher.value) && (!kw || matches(m, kw)))
})

async function loadSources() {
  loadingSources.value = true
  try {
    const items = await fetchFirstPage<MarketSource>('/market/sources')
    sources.value = items
    sourceId.value = (items.find(s => s.enabled) || items[0])?.id ?? null
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
    installed.value = Object.fromEntries(items.map(p => [p.key, p]))
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
    // Raw envelope: host_version sits next to data.
    const json = await requestRaw('GET', '/market/plugins', { query: { source_id: sourceId.value } })
    items.value = Array.isArray(json?.data) ? json.data : []
    hostVersion.value = String(json?.host_version ?? '')
  } catch (e) {
    items.value = []
    notifyError(e)
  } finally {
    loading.value = false
  }
}

/** Reviews the picked version first: nothing is downloaded or registered before the consent. */
function install(version: string) {
  const m = picker.value
  if (!m || sourceId.value === null) return
  picker.value = null
  router.push(marketInstallLocation(sourceId.value, m.key, version))
}

watch(sourceId, () => {
  publisher.value = ''
  loadPlugins()
})
onMounted(async () => {
  await Promise.all([loadSources(), loadInstalled()])
})
</script>

<template>
  <div>
    <SPageHeader :title="t('plugins.market.title')" :description="t('plugins.market.description')">
      <template #actions>
        <SButton :loading="loading" icon="refresh" @click="(loadPlugins(), loadInstalled())">{{ t('common.refresh') }}</SButton>
      </template>
      <template #filters>
        <SField :label="t('plugins.market.source')" class="w-56">
          <SSelect
            :model-value="sourceId"
            :options="sourceOptions"
            :disabled="loadingSources || !sources.length"
            @update:model-value="v => (sourceId = v === null ? null : Number(v))"
          />
        </SField>
        <SField :label="t('plugins.market.publisherFilter')" class="w-56">
          <SSelect
            :model-value="publisher"
            :options="publisherChoices"
            data-testid="market-publisher-filter"
            @update:model-value="v => (publisher = String(v ?? ''))"
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
      <SEmpty icon="market" :text="q || publisher ? t('plugins.market.noMatch') : t('plugins.market.empty')" />
    </SCard>

    <SCard v-else :padded="false" :class="loading ? 'opacity-60' : ''">
      <MarketRow
        v-for="m in rows"
        :key="m.key"
        :plugin="m"
        :installed-version="installedOf(m)"
        :has-detail="!!installed[m.key]"
        :state="stateOf(m)"
        :blocked="blockedReason(m)"
        :can-install="auth.has('plugin:install')"
        @pick="picker = m"
      />
    </SCard>

    <MarketVersionPicker
      :plugin="picker"
      :installed-version="picker ? installedOf(picker) : ''"
      :host-version="hostVersion"
      @close="picker = null"
      @install="install"
    />
  </div>
</template>
