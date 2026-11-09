import { computed, onMounted, shallowRef, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import type { PluginSummary } from '@/api/types'
import { fetchFirstPage } from '@/composables/lookups'
import { lt } from '@/i18n'
import type { ViewGroup, ViewLink } from '@/layouts/sidebarViews'
import { useAuthStore } from '@/stores/auth'
import { hasSettingsForm, pluginSettingsPath } from '@/views/plugins/pluginUtil'

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

/** A group of the settings view: configuration pages, or (`plugins`) the settings forms of the plugins. */
interface Group {
  key: string
  pages: Page[]
  plugins?: true
}

const page = (tab: string, perm: string, label: string, icon: string): Page => ({ tab, perm, label, icon })

/** The groups and their pages, in the order shown. */
const LAYOUT: Group[] = [
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
  { key: 'pluginSettings', pages: [], plugins: true },
  {
    key: 'runtime',
    pages: [
      page('updates', 'settings:read', 'coreUpdates.sourceTitle', 'download'),
      page('environment', 'settings:read', 'settings.tabs.environment', 'code')
    ]
  }
]

/** The configuration pages the caller may open and the one the settings page shows. No requests: any number of callers agree. */
export function useSettingsNav() {
  const { t } = useI18n()
  const auth = useAuthStore()
  const route = useRoute()

  const pages = computed(() => LAYOUT.flatMap(g => g.pages).filter(p => auth.has(p.perm)))

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

  return { pages, activeTab, activeLabel, missing }
}

/**
 * Enabled plugins whose settings are a form the console renders. Kept between
 * visits of the settings view: it draws the last answer at once and refreshes
 * behind it, so the groups below do not jump every time it opens.
 */
const settingsPlugins = shallowRef<PluginSummary[]>([])
let lastRefresh = 0

function useSettingsPlugins(): Ref<PluginSummary[]> {
  const auth = useAuthStore()
  async function load(): Promise<PluginSummary[]> {
    // Not this caller's to list: nothing an earlier user was shown stays either.
    if (!auth.has('plugin:read')) return []
    try {
      const list = await fetchFirstPage<PluginSummary>('/plugins')
      return list.filter(p => p.status === 'enabled' && hasSettingsForm(p))
    } catch {
      // The group is an index; the plugin list page still reaches them.
      return []
    }
  }
  async function refresh() {
    const mine = ++lastRefresh
    const list = await load()
    // A late answer to an earlier visit, an earlier user's even, is dropped.
    if (mine === lastRefresh) settingsPlugins.value = list
  }
  onMounted(refresh)
  return settingsPlugins
}

/** The menu of the sidebar's settings view: the configuration pages and the plugins' settings forms. */
export function useSettingsMenu(): ComputedRef<ViewGroup[]> {
  const { t } = useI18n()
  const route = useRoute()
  const { pages, activeTab } = useSettingsNav()
  const plugins = useSettingsPlugins()

  const onSettingsPage = computed(() => route.path === SETTINGS_PAGE)
  const pageLink = (p: Page): ViewLink => ({
    key: p.tab,
    item: { id: `settings:${p.tab}`, labelKey: p.label, icon: p.icon, path: `${SETTINGS_PAGE}?tab=${p.tab}` },
    active: onSettingsPage.value && activeTab.value === p.tab
  })
  const pluginLink = (p: PluginSummary): ViewLink => {
    const path = pluginSettingsPath(p.key)
    return {
      key: `plugin:${p.key}`,
      item: { id: `plugin-settings:${p.key}`, label: lt(p.name) || p.key, icon: 'puzzle', path, pluginKey: p.key },
      active: route.path === path
    }
  }
  const linksOf = (g: Group): ViewLink[] =>
    g.plugins ? plugins.value.map(pluginLink) : g.pages.filter(p => pages.value.includes(p)).map(pageLink)

  return computed(() =>
    LAYOUT.map(g => ({ key: g.key, label: t(`settings.groups.${g.key}`), links: linksOf(g) })).filter(g => g.links.length > 0)
  )
}
