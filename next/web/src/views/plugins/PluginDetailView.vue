<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, isApiError } from '@sub2api/host'
import { SAlert, SBadge, SButton, SCard, SChoiceCards, SDropdown, SEmpty, SHint, SFormModal, SPageHeader, SSpinner, STabs, confirm, type ChoiceOption, type MenuAction, type TabItem } from '@sub2api/ui'
import PluginStatusBadge from './parts/PluginStatusBadge.vue'
import type { Rollout } from '@/api/types'
import { lt } from '@/i18n'
import { errorMessage, notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import { refreshShell } from '@/stores/app'
import { consentPath } from './reviewCache'
import RoleGrantsReview from './parts/RoleGrantsReview.vue'
import { compareVersions, conflictLines, consoleSettings, isPendingConsent, nativeSettingsPath, refusalText, shownStatus, tabOf, type PluginDetail, type PluginSettings } from './pluginUtil'
import TrustBadge from './parts/TrustBadge.vue'
import OverviewTab from './detail/OverviewTab.vue'
import PermissionsTab from './detail/PermissionsTab.vue'
import NodesTab from './detail/NodesTab.vue'
import HooksTab from './detail/HooksTab.vue'
import JobsTab from './detail/JobsTab.vue'
import EventsTab from './detail/EventsTab.vue'
import EgressTab from './detail/EgressTab.vue'
import ResourcesTab from './detail/ResourcesTab.vue'
import SettingsTab from './detail/SettingsTab.vue'
import DatabaseTab from './detail/DatabaseTab.vue'
import UninstallModal from './detail/UninstallModal.vue'
import AuditLogTable from '@/components/AuditLogTable.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const key = computed(() => String(route.params.key))
const detail = ref<PluginDetail | null>(null)
const settings = ref<PluginSettings | null>(null)
/** The plugin page that edits the plugin's settings (§75.5): where ?tab=settings leads. */
const settingsPage = ref<string | null>(null)
const loading = ref(true)
const loadError = ref('')
const notFound = ref(false)
const acting = ref('')
const uninstallOpen = ref(false)
const upgradeOpen = ref(false)
const upgradeVersion = ref('')

const name = computed(() => (detail.value ? lt(detail.value.name) || detail.value.key : key.value))
const status = computed(() => detail.value?.status || '')
const canManage = computed(() => auth.has('plugin:manage'))
const inRollout = computed(() => status.value === 'enabling' || status.value === 'upgrading')

const approvedUpgrades = computed(() =>
  (detail.value?.versions || [])
    .filter((v) => v.consent_status === 'approved' && v.version !== detail.value?.active_version)
    .filter((v) => !detail.value?.active_version || compareVersions(v.version, detail.value.active_version) > 0)
    .sort((a, b) => compareVersions(b.version, a.version))
)
const upgradeChoices = computed<ChoiceOption[]>(() => approvedUpgrades.value.map((v) => ({ value: v.version, label: 'v' + v.version })))
const pendingVersions = computed(() => (detail.value?.versions || []).filter((v) => isPendingConsent(v.consent_status)))

const consented = computed(() => (typeof route.query.consented === 'string' ? route.query.consented : ''))
const consentedAction = computed<'enable' | 'upgrade' | null>(() => {
  if (!consented.value || !detail.value || !canManage.value) return null
  if (status.value === 'installed' || status.value === 'disabled') return 'enable'
  if (status.value === 'enabled' && consented.value !== detail.value.active_version) return 'upgrade'
  return null
})

/** The installed version declares a database (its review's or manifest's database). */
const hasDatabase = computed(() => !!detail.value?.manifest?.database)

const tabs = computed<TabItem[]>(() => {
  const d = detail.value
  const list: TabItem[] = [
    { key: 'overview', label: t('plugins.detail.tabs.overview') },
    // The host permissions in force: the user permissions below them are counted per role, not here.
    { key: 'permissions', label: t('plugins.detail.tabs.permissions'), badge: d?.grants?.filter((g) => g.status === 'active').length },
    { key: 'nodes', label: t('plugins.detail.tabs.nodes'), badge: d?.nodes?.length },
    { key: 'hooks', label: t('plugins.detail.tabs.hooks'), badge: d?.hooks?.length },
    { key: 'jobs', label: t('plugins.detail.tabs.jobs'), badge: d?.jobs?.length },
    { key: 'events', label: t('plugins.detail.tabs.events') }
  ]
  if (hasDatabase.value) list.push({ key: 'database', label: t('plugins.detail.tabs.database') })
  if (auth.has(['plugin:egress:read', 'plugin:manage'])) list.push({ key: 'egress', label: t('plugins.detail.tabs.egress') })
  list.push({ key: 'resources', label: t('plugins.detail.tabs.resources') })
  if (settings.value) list.push({ key: 'settings', label: t('plugins.detail.tabs.settings') })
  if (auth.has('audit:read')) list.push({ key: 'audit', label: t('plugins.detail.tabs.audit') })
  return list
})

/** The tab is the address's (?tab=): a click, a link and the history alike pass the tabs' own leave guards (PermissionsTab). */
const tab = computed(() => tabOf(route.query.tab))
function switchTab(v: string) {
  router.replace({ query: { ...route.query, tab: v } })
}
// An old link (?tab=grants) shows the tab it now opens in the address bar.
if (route.query.tab !== undefined && route.query.tab !== tab.value) switchTab(tab.value)

const moreActions = computed<MenuAction[]>(() => [
  { key: 'refresh', label: t('common.refresh') },
  { key: 'rollout', label: t('plugins.detail.viewRollout') },
  {
    key: 'uninstall',
    label: t('plugins.uninstall.action'),
    danger: true,
    hidden: !auth.has('plugin:uninstall') || !!detail.value?.builtin,
    disabled: status.value === 'enabled' || inRollout.value
  }
])

async function loadSettings() {
  try {
    const s = await api.get<PluginSettings>(`/plugins/${encodeURIComponent(key.value)}/settings`)
    settings.value = consoleSettings(s)
    settingsPage.value = nativeSettingsPath(key.value, s)
  } catch {
    settings.value = null
    settingsPage.value = null
  }
}

async function load() {
  loadError.value = ''
  notFound.value = false
  try {
    detail.value = await api.get<PluginDetail>(`/plugins/${encodeURIComponent(key.value)}`)
  } catch (e) {
    if (isApiError(e) && e.status === 404) notFound.value = true
    else loadError.value = errorMessage(e)
    detail.value = null
  } finally {
    loading.value = false
  }
  if (detail.value) {
    await loadSettings()
    // The plugins list links every plugin with settings here (has_settings).
    if (tab.value === 'settings' && settingsPage.value) router.replace(settingsPage.value)
    else if (!tabs.value.some((x) => x.key === tab.value)) switchTab('overview')
  }
}

async function startRollout(action: 'enable' | 'disable' | 'upgrade', version?: string) {
  if (action === 'disable') {
    const ok = await confirm({
      title: t('plugins.detail.disableTitle', { name: name.value }),
      message: t('plugins.detail.disableConfirm'),
      danger: true,
      confirmText: t('common.disable')
    })
    if (!ok) return
  }
  acting.value = action
  try {
    const body = action === 'upgrade' ? { version } : undefined
    await api.post<Rollout>(`/plugins/${encodeURIComponent(key.value)}/${action}`, body)
    upgradeOpen.value = false
    router.push(`/plugins/${encodeURIComponent(key.value)}/rollout`)
  } catch (e) {
    notifyError(e)
  } finally {
    acting.value = ''
  }
}

function openUpgrade() {
  upgradeVersion.value = approvedUpgrades.value[0]?.version || ''
  upgradeOpen.value = true
}

function onMore(k: string) {
  if (k === 'refresh') load()
  else if (k === 'rollout') router.push(`/plugins/${encodeURIComponent(key.value)}/rollout`)
  else if (k === 'uninstall') uninstallOpen.value = true
}

// The shell refresh is not awaited: a plugin module that never settles must not
// keep the page from leaving; the plugin list does not read the plugin store.
function onUninstalled() {
  void refreshShell()
  router.push('/plugins')
}

function dismissConsented() {
  const q = { ...route.query }
  delete q.consented
  router.replace({ query: q })
}

watch(key, () => {
  loading.value = true
  load()
})
onMounted(load)
</script>

<template>
  <div>
    <div v-if="loading" class="flex justify-center py-20"><SSpinner size="lg" /></div>

    <SCard v-else-if="!detail">
      <SEmpty icon="plugin" :text="notFound ? t('plugins.detail.notFound', { key }) : loadError || t('common.loadFailed')">
        <SButton size="sm" @click="load">{{ t('common.refresh') }}</SButton>
      </SEmpty>
    </SCard>

    <template v-else>
      <SPageHeader :title="name">
        <template #title-extra>
          <span class="font-mono text-sm text-gray-500">{{ detail.active_version ? 'v' + detail.active_version : '' }}</span>
          <span v-if="detail.desired_version && detail.desired_version !== detail.active_version" class="font-mono text-xs text-primary-600 dark:text-primary-400">
            → v{{ detail.desired_version }}
          </span>
          <PluginStatusBadge :status="shownStatus(detail)" />
          <TrustBadge :trust="detail.trust" />
          <SBadge v-if="detail.builtin" tone="info" :title="t('plugins.builtinHint')">{{ t('plugins.builtin') }}</SBadge>
        </template>
      </SPageHeader>

      <SHint v-if="detail.status_reason" tone="danger" class="-mt-3 mb-4">{{ detail.status_reason }}</SHint>
      <SAlert v-if="detail.refusal" tone="danger" icon="x" class="mb-4" data-testid="plugin-refusal">{{ refusalText(t, detail.key, detail.refusal) }}</SAlert>

      <!-- exclusive resources this node's generation could not give the plugin (audit 2026-10-09 P2-2) -->
      <SAlert v-if="detail.resource_conflicts?.length" tone="danger" :title="t('plugins.conflicts.title')" class="mb-4" data-testid="plugin-conflicts">
        <p class="text-xs">{{ t('plugins.conflicts.hint') }}</p>
        <ul class="mt-2 list-disc space-y-1 pl-5 font-mono text-xs">
          <li v-for="(line, i) in conflictLines(detail)" :key="i">{{ line }}</li>
        </ul>
      </SAlert>

      <!-- role grants a built-in upgrade held back (CONTRACTS P4C) -->
      <RoleGrantsReview v-if="detail.role_grants_to_review?.length" :plugin-key="detail.key" :grants="detail.role_grants_to_review" class="mb-4" @changed="load" />

      <!-- consent just recorded -->
      <SAlert v-if="consented" tone="success" dismissible class="mb-4" @dismiss="dismissConsented">
        {{ t('plugins.detail.consentedBanner', { version: consented }) }}
        <template v-if="consentedAction" #actions>
          <SButton v-if="consentedAction === 'enable'" size="sm" variant="primary" icon="play" :loading="acting === 'enable'" @click="startRollout('enable')">
            {{ t('plugins.detail.enableNow') }}
          </SButton>
          <SButton
            v-if="consentedAction === 'upgrade'"
            size="sm"
            variant="primary"
            icon="upload"
            :loading="acting === 'upgrade'"
            @click="startRollout('upgrade', consented)"
          >
            {{ t('plugins.detail.upgradeNow', { version: consented }) }}
          </SButton>
        </template>
      </SAlert>

      <!-- versions waiting for consent -->
      <SAlert v-for="v in pendingVersions" :key="v.version" tone="warning" icon="shield" class="mb-4">
        {{ t('plugins.detail.pendingConsent', { version: v.version }) }}
        <template v-if="auth.has('plugin:install')" #actions>
          <SButton :to="consentPath(detail.key, v.version)" variant="primary" size="sm">{{ t('plugins.list.review') }}</SButton>
        </template>
      </SAlert>

      <STabs :model-value="tab" :tabs="tabs" @update:model-value="switchTab">
        <template #end>
          <SButton v-if="inRollout" :to="`/plugins/${encodeURIComponent(detail.key)}/rollout`">
            <SSpinner size="sm" />{{ t('plugins.detail.viewRollout') }}
          </SButton>
          <template v-if="canManage">
            <SButton
              v-if="status === 'installed' || status === 'disabled'"
              variant="primary"
              icon="play"
              :loading="acting === 'enable'"
              @click="startRollout('enable')"
            >
              {{ t('common.enable') }}
            </SButton>
            <SButton v-if="status === 'enabled' && approvedUpgrades.length" variant="primary" icon="upload" @click="openUpgrade">
              {{ t('plugins.detail.upgrade') }}
            </SButton>
            <SButton v-if="status === 'enabled'" :loading="acting === 'disable'" icon="stop" @click="startRollout('disable')">
              {{ t('common.disable') }}
            </SButton>
          </template>
          <SDropdown :actions="moreActions" @select="onMore" />
        </template>

        <OverviewTab v-if="tab === 'overview'" :detail="detail" />
        <PermissionsTab v-else-if="tab === 'permissions'" :key="detail.key" :detail="detail" @changed="load" />
        <NodesTab v-else-if="tab === 'nodes'" :detail="detail" />
        <HooksTab v-else-if="tab === 'hooks'" :detail="detail" />
        <JobsTab v-else-if="tab === 'jobs'" :detail="detail" />
        <EventsTab v-else-if="tab === 'events'" :detail="detail" />
        <DatabaseTab v-else-if="tab === 'database' && hasDatabase" :detail="detail" />
        <EgressTab v-else-if="tab === 'egress'" :detail="detail" @changed="load" />
        <ResourcesTab v-else-if="tab === 'resources'" :detail="detail" @changed="load" />
        <SettingsTab v-else-if="tab === 'settings' && settings" :plugin-key="detail.key" :settings="settings" />
        <div v-else-if="tab === 'audit'" class="space-y-3">
          <SHint>{{ t('plugins.detail.auditHint') }}</SHint>
          <AuditLogTable :query="{ target_type: 'plugin', target_id: detail.key }" hide-target />
        </div>

      </STabs>

      <UninstallModal v-model:open="uninstallOpen" :plugin-key="detail.key" :name="name" :has-database="hasDatabase" @done="onUninstalled" />

      <SFormModal v-model:open="upgradeOpen" :title="t('plugins.detail.upgradeTitle', { name })" width="sm" :loading="acting === 'upgrade'" :submit-disabled="!upgradeVersion" :submit-text="t('plugins.detail.upgrade')" :enter-submit="false" @submit="startRollout('upgrade', upgradeVersion)">
        <SHint class="mb-3">{{ t('plugins.detail.upgradeHint', { version: detail.active_version || '—' }) }}</SHint>
        <SChoiceCards
          :model-value="upgradeVersion"
          :options="upgradeChoices"
          :cols="1"
          :aria-label="t('plugins.detail.upgradeTitle', { name })"
          @update:model-value="(v) => (upgradeVersion = String(v))"
        />
      </SFormModal>
    </template>
  </div>
</template>
