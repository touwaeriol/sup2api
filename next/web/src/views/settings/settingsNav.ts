import { computed, onMounted, ref, watch, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import type { PluginSummary } from '@/api/types'
import { fetchFirstPage } from '@/composables/lookups'
import { lt } from '@/i18n'
import type { ViewGroup, ViewLink } from '@/layouts/sidebarViews'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { hasSettingsForm } from '@/views/plugins/pluginUtil'

/**
 * System settings (CONTRACTS §90): the configuration pages of the settings
 * page (?tab=<key>) and, in the sidebar's settings view, the settings forms of
 * the enabled plugins, which open at their own address.
 */

/** The settings page: every configuration page is one of its tabs. */
export const SETTINGS_PAGE = '/settings'

interface Page {
  tab: string
  perm: string
  /** i18n key. */
  label: string
  icon: string
}

const page = (tab: string, perm: string, label: string, icon: string): Page => ({ tab, perm, label, icon })

/** The configuration pages, in the order shown. */
const LAYOUT: Array<{ key: string; pages: Page[] }> = [
  {
    key: 'gateway',
    pages: [
      page('gateway', 'settings:read', 'settings.tabs.gateway', 'settings'),
      page('failure', 'settings:read', 'failurePolicy.title', 'filter'),
      page('sticky', 'sticky:read', 'settings.tabs.sticky', 'sticky'),
      page('offload', 'settings:read', 'settings.tabs.offload', 'cpu')
    ]
  },
  {
    key: 'billing',
    pages: [
      page('billing', 'settings:read', 'settings.tabs.billing', 'balance'),
      page('notifications', 'settings:read', 'settings.tabs.notifications', 'inbox')
    ]
  },
  {
    key: 'runtime',
    pages: [
      page('updates', 'settings:read', 'coreUpdates.sourceTitle', 'download'),
      page('environment', 'settings:read', 'settings.tabs.environment', 'code')
    ]
  }
]

/** The groups of the sidebar's settings view: plugin settings sit between the configuration groups. */
const MENU_ORDER = ['gateway', 'billing', 'pluginSettings', 'runtime']

/** The configuration pages the caller may open and the one the settings page shows. No requests: any number of callers agree. */
export function useSettingsNav() {
  const { t } = useI18n()
  const auth = useAuthStore()
  const route = useRoute()

  const visible = computed(() =>
    LAYOUT.map(g => ({ key: g.key, pages: g.pages.filter(p => auth.has(p.perm)) })).filter(g => g.pages.length > 0)
  )
  const pages = computed(() => visible.value.flatMap(g => g.pages))

  const askedTab = computed(() => (typeof route.query.tab === 'string' ? route.query.tab : ''))
  const known = computed(() => pages.value.some(p => p.tab === askedTab.value))
  /** A linked page the user cannot see: the settings page says so instead of silently showing another one. */
  const missing = computed(() => !!askedTab.value && !known.value)
  /** The page asked for when the user may see it, else the first visible one. */
  const activeTab = computed(() => (known.value ? askedTab.value : pages.value[0]?.tab))
  const activeLabel = computed(() => {
    const active = pages.value.find(p => p.tab === activeTab.value)
    return active ? t(active.label) : ''
  })

  const onSettingsPage = computed(() => route.path === SETTINGS_PAGE)
  const linkOf = (p: Page): ViewLink => ({
    key: p.tab,
    item: { id: `settings:${p.tab}`, labelKey: p.label, icon: p.icon, path: `${SETTINGS_PAGE}?tab=${p.tab}` },
    active: onSettingsPage.value && activeTab.value === p.tab
  })
  const groups = computed<ViewGroup[]>(() =>
    visible.value.map(g => ({ key: g.key, label: t(`settings.groups.${g.key}`), links: g.pages.map(linkOf) }))
  )

  return { groups, activeTab, activeLabel, missing }
}

/** Enabled plugins whose settings are a form the console renders, kept in step with the menu. */
function useSettingsPlugins(): Ref<PluginSummary[]> {
  const auth = useAuthStore()
  const app = useAppStore()
  const plugins = ref<PluginSummary[]>([])
  async function load() {
    if (!auth.has('plugin:read')) return
    try {
      const list = await fetchFirstPage<PluginSummary>('/plugins')
      plugins.value = list.filter(p => p.status === 'enabled' && hasSettingsForm(p))
    } catch {
      // The group is an index; the plugin list page still reaches them.
      plugins.value = []
    }
  }
  onMounted(load)
  // Installing, enabling or removing a plugin refreshes the menu (refreshShell): follow it. The first menu load is not such a change.
  watch(
    () => app.menus,
    (_, before) => {
      if (before.length > 0) void load()
    }
  )
  return plugins
}

/** The menu of the sidebar's settings view: the configuration pages and the plugins' settings forms. */
export function useSettingsMenu(): ComputedRef<ViewGroup[]> {
  const { t } = useI18n()
  const route = useRoute()
  const { groups } = useSettingsNav()
  const plugins = useSettingsPlugins()

  const linkOf = (p: PluginSummary): ViewLink => {
    const path = `/plugins/${encodeURIComponent(p.key)}/settings`
    return {
      key: `plugin:${p.key}`,
      item: { id: `plugin-settings:${p.key}`, label: lt(p.name) || p.key, icon: 'puzzle', path, pluginKey: p.key },
      active: route.path === path
    }
  }
  const pluginGroup = computed<ViewGroup>(() => ({
    key: 'pluginSettings',
    label: t('settings.groups.pluginSettings'),
    links: plugins.value.map(linkOf)
  }))

  return computed(() => {
    const all = [...groups.value, pluginGroup.value].filter(g => g.links.length > 0)
    return MENU_ORDER.flatMap(key => all.filter(g => g.key === key))
  })
}
