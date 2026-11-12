<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { SBadge, SButton, SPageHeader, SSegmented, STabs } from '@sub2api/ui'
import OverviewTab from './OverviewTab.vue'
import EventsTab from './EventsTab.vue'
import WatchTab from './WatchTab.vue'
import BlocksTab from './BlocksTab.vue'
import FlowsTab from './FlowsTab.vue'
import BindingsTab from './BindingsTab.vue'
import AuditorsTab from './AuditorsTab.vue'
import ChannelsTab from './ChannelsTab.vue'
import TestTab from './TestTab.vue'
import SettingsTab from './SettingsTab.vue'
import { useCatalog } from './catalog'
import { canManage, canManageSettings, useModHost } from './host'
import { useStatus } from './status'
import { modeTone } from './tones'
import { hostShowsNav, isSettingsPage, useSection, visibleGroups, type Access, type SectionKey } from './nav'

// Prompt moderation page (CONTRACTS §72.15): the sections of nav.ts, in the
// console's sidebar from UI 3.14 (manifest ui.pages.dashboard.nav) or as tabs
// here before it. The settings are several short pages, one per nav item of
// access "settings" (the ui:section page of forms/settings.ui.json), for those
// who may edit them (plugin:manage or moderation:manage, §75.1). The console
// still sends /plugins/moderation/settings here with ?tab=settings, which
// opens the first of them (nav.ts RENAMED).
const host = useModHost()
const t = host.t

const manage = computed(() => canManage())
const manageSettings = computed(() => canManageSettings())
const may = (a: Access) => a === 'read' || (a === 'manage' ? manage.value : manageSettings.value)
const groups = computed(() => visibleGroups(may))
const keys = computed<SectionKey[]>(() => groups.value.flatMap((g) => g.items.map((i) => i.key)))
const settingsPages = computed(() => keys.value.filter(isSettingsPage))

// The section is ?tab= (useSection). From UI 3.14 the console's sidebar lists
// the sections; an older console gets them as tabs here, the settings pages
// as one Settings tab with a switch over them (hostShowsNav).
const inSidebar = hostShowsNav(host)
const tab = useSection(host.router, keys, inSidebar)
const SETTINGS_TAB = 'settings'
/** The tabs wrapping the sections on an older console; a plain block when the sidebar lists them. */
const sectionsProps = computed(() => {
  if (inSidebar) return {}
  const own = keys.value.filter((k) => !isSettingsPage(k)).map((k) => ({ key: k, label: t(`nav.items.${k}`) }))
  const tabs = settingsPages.value.length ? [...own, { key: SETTINGS_TAB, label: t('tabs.settings') }] : own
  return {
    tabs,
    modelValue: isSettingsPage(tab.value) ? SETTINGS_TAB : tab.value,
    'onUpdate:modelValue': (v: string) => (tab.value = v === SETTINGS_TAB ? lastSettingsPage() : (v as SectionKey))
  }
})
/** The settings page last shown, else the first: the Settings tab of an older console opens it. */
const lastSettingsPage = () => [...visited.value].reverse().find((k) => settingsPages.value.includes(k)) ?? settingsPages.value[0]
const settingsOptions = computed(() => settingsPages.value.map((k) => ({ value: k, label: t(`nav.items.${k}`) })))
const visited = ref(new Set<SectionKey>([tab.value]))
watch(tab, (v) => {
  if (!visited.value.has(v)) visited.value = new Set([...visited.value, v])
})
const shown = (k: SectionKey) => visited.value.has(k) && keys.value.includes(k)

// Mode badge and the error-rate alert threshold, from the status the
// overview shows too (the overview loads it itself).
const status = useStatus()
const runtime = computed(() => status.data.value?.runtime ?? null)
onMounted(() => tab.value !== 'overview' && status.reload())

// Sections with reload(), refreshed by the header button (the overview reloads the status itself); a settings page also says whether it has unsaved edits.
type SectionRef = { reload(): unknown; dirty?: boolean }
const refs: Partial<Record<SectionKey, SectionRef>> = {}
const setRef = (k: SectionKey) => (el: unknown) => {
  if (el) refs[k] = el as SectionRef
  else delete refs[k]
}

// Leaving a settings page with unsaved edits, for another section or another page, asks first.
const offGuard = host.router.beforeEach((to, from) => {
  const leaving = to.path !== from.path || to.query.tab !== from.query.tab
  if (!leaving || !refs[tab.value]?.dirty) return true
  return host.confirm({ title: t('settingsTab.leaveTitle'), message: t('settingsTab.leaveMessage'), confirmText: t('flows.leave'), cancelText: t('flows.stay'), danger: true })
})
onUnmounted(offGuard)
const catalog = useCatalog()
const refreshing = ref(false)
async function refresh() {
  refreshing.value = true
  try {
    await Promise.all([refs[tab.value]?.reload(), tab.value !== 'overview' && Promise.all([catalog.refresh(), status.reload()])])
  } finally {
    refreshing.value = false
  }
}

/** A section link; sections the user cannot see (playground, settings) are ignored (useSection). */
const goTab = (k: SectionKey) => (tab.value = k)

// "Show records of this user" from the overview and the watch list.
const userPreset = ref<{ user_id: number; n: number } | null>(null)
let presetN = 0
function showUser(userId: number) {
  userPreset.value = { user_id: userId, n: ++presetN }
  tab.value = 'events'
}

// "Open this user's watch detail" from the overview.
/** A user's watch list drawer (?tab=watch&user=, WatchTab). */
const openWatch = (userId: number) => host.router.push({ query: { tab: 'watch', user: String(userId) } })
</script>

<template>
  <div>
    <!-- The mode badges go with the title: in the top bar on a wide screen, in the page on a phone. -->
    <SPageHeader :title="inSidebar ? t(`nav.items.${tab}`) : t('title')" :description="inSidebar ? t(`nav.desc.${tab}`) : t('subtitle')">
      <template v-if="runtime" #title-extra>
        <SBadge :tone="modeTone(runtime.mode)" dot>{{ t(`mode.${runtime.mode || 'off'}`) }}</SBadge>
        <SBadge v-if="!runtime.configured" tone="warning">{{ t('notConfiguredBadge') }}</SBadge>
      </template>
      <template #actions>
        <SButton icon="refresh" :loading="refreshing" @click="refresh">{{ t('refresh') }}</SButton>
      </template>
    </SPageHeader>

    <component :is="inSidebar ? 'div' : STabs" v-bind="sectionsProps" data-testid="mod-sections">
      <OverviewTab
        v-if="shown('overview')"
        v-show="tab === 'overview'"
        :ref="setRef('overview')"
        :active="tab === 'overview'"
        @open-watch="openWatch"
        @go-tab="goTab"
      />
      <EventsTab v-if="shown('events')" v-show="tab === 'events'" :ref="setRef('events')" :preset="userPreset" />
      <TestTab v-if="shown('test')" v-show="tab === 'test'" @go-tab="goTab" />
      <FlowsTab v-if="shown('flows')" v-show="tab === 'flows'" :ref="setRef('flows')" @go-tab="goTab" />
      <BindingsTab v-if="shown('bindings')" v-show="tab === 'bindings'" :ref="setRef('bindings')" @go-tab="goTab" />
      <WatchTab v-if="shown('watch')" v-show="tab === 'watch'" :ref="setRef('watch')" @show-user="showUser" />
      <BlocksTab v-if="shown('blocks')" v-show="tab === 'blocks'" :ref="setRef('blocks')" />
      <AuditorsTab v-if="shown('auditors')" v-show="tab === 'auditors'" :ref="setRef('auditors')" />
      <ChannelsTab v-if="shown('channels')" v-show="tab === 'channels'" :ref="setRef('channels')" :alert-percent="status.data.value?.error_rate_alert_percent ?? 0" />
      <SSegmented v-if="!inSidebar && isSettingsPage(tab)" v-model="tab" :options="settingsOptions" size="sm" class="mod-settings-switch" />
      <template v-for="k in settingsPages" :key="k">
        <SettingsTab v-if="shown(k)" v-show="tab === k" :ref="setRef(k)" :page="k" />
      </template>
    </component>
  </div>
</template>
