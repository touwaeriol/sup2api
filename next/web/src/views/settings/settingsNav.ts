import { computed, onMounted, shallowRef, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import type { PluginSummary } from '@/api/types'
import { fetchFirstPage } from '@/composables/lookups'
import { lt } from '@/i18n'
import type { ViewGroup, ViewLink } from '@/layouts/sidebarViews'
import { useRouteLineage } from '@/layouts/useActiveMenu'
import { useAuthStore } from '@/stores/auth'
import { consoleSettingsPath } from '@/views/plugins/pluginUtil'

/**
 * System settings (CONTRACTS §90): the configuration pages of the settings
 * page (?tab=<key>) and, in the sidebar's settings view, the pages with an
 * address of their own that belong with them: groups, model prices and the
 * settings of the enabled plugins.
 */

/** The settings page: every configuration page is one of its tabs. */
export const SETTINGS_PAGE = '/settings'

/** A page of the settings view. */
interface Page {
  /** The tab of the settings page that shows a configuration page; a page with an address of its own has none. */
  tab?: string
  /** Where it opens. */
  path: string
  perm: string
  /** i18n key. */
  label: string
  icon: string
}

/** A group of the settings view: its pages, or (`plugins`) the settings of the plugins. */
interface Group {
  key: string
  pages: Page[]
  plugins?: true
}

/** A configuration page. */
const page = (tab: string, perm: string, label: string, icon: string): Page => ({
  tab,
  path: `${SETTINGS_PAGE}?tab=${tab}`,
  perm,
  label,
  icon
})
/** A page with an address of its own; its route sits under the settings page (meta.parent), which opens it in the view. */
const ownPage = (path: string, perm: string, label: string, icon: string): Page => ({ path, perm, label, icon })

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
    key: 'pricing',
    pages: [
      ownPage('/groups', 'group:read', 'nav.items.groups', 'group'),
      ownPage('/prices', 'price:read', 'nav.items.prices', 'price')
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

const PAGES = LAYOUT.flatMap(g => g.pages)

/** The configuration pages the caller may open and the one the settings page shows. No requests: any number of callers agree. */
export function useSettingsNav() {
  const { t } = useI18n()
  const auth = useAuthStore()
  const route = useRoute()

  const pages = computed(() => PAGES.filter(p => p.tab && auth.has(p.perm)))

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
 * Where the settings entry leads a caller who may open no configuration page:
 * the first page of the view with an address of its own that is theirs.
 */
export function settingsLanding(): string | undefined {
  const auth = useAuthStore()
  return PAGES.find(p => !p.tab && auth.has(p.perm))?.path
}

/**
 * The enabled plugins. Kept between visits of the settings view: it draws the
 * last answer at once and refreshes behind it, so the groups below do not
 * jump every time it opens.
 */
const enabledPlugins = shallowRef<PluginSummary[]>([])
let lastRefresh = 0

function useEnabledPlugins(): Ref<PluginSummary[]> {
  const auth = useAuthStore()
  async function load(): Promise<PluginSummary[]> {
    // Not this caller's to list: nothing an earlier user was shown stays either.
    if (!auth.has('plugin:read')) return []
    try {
      const list = await fetchFirstPage<PluginSummary>('/plugins')
      return list.filter(p => p.status === 'enabled')
    } catch {
      // The group is an index; the plugin list page still reaches them.
      return []
    }
  }
  async function refresh() {
    const mine = ++lastRefresh
    const list = await load()
    // A late answer to an earlier visit, an earlier user's even, is dropped.
    if (mine === lastRefresh) enabledPlugins.value = list
  }
  onMounted(refresh)
  return enabledPlugins
}

/** The menu of the sidebar's settings view: the pages of every group the caller may open, the plugins' settings among them. */
export function useSettingsMenu(): ComputedRef<ViewGroup[]> {
  const { t } = useI18n()
  const route = useRoute()
  const auth = useAuthStore()
  const { activeTab } = useSettingsNav()
  const plugins = useEnabledPlugins()
  const lineage = useRouteLineage()

  const onSettingsPage = computed(() => route.path === SETTINGS_PAGE)
  /** The open page is the one at `path` or a page below it. */
  const within = (path: string) => lineage.value.includes(path)
  const pageLink = (p: Page): ViewLink => ({
    key: p.tab ?? p.path,
    item: { id: `settings:${p.tab ?? p.path}`, labelKey: p.label, icon: p.icon, path: p.path },
    active: p.tab ? onSettingsPage.value && activeTab.value === p.tab : within(p.path)
  })
  /** A plugin's entry, when its settings have a page this caller may open. */
  const pluginLinks = (p: PluginSummary): ViewLink[] => {
    const path = consoleSettingsPath(p, perm => auth.has(perm))
    if (!path) return []
    return [
      {
        key: `plugin:${p.key}`,
        item: { id: `plugin-settings:${p.key}`, label: lt(p.name) || p.key, icon: 'puzzle', path, pluginKey: p.key },
        active: within(path)
      }
    ]
  }
  const linksOf = (g: Group): ViewLink[] =>
    g.plugins ? plugins.value.flatMap(pluginLinks) : g.pages.filter(p => auth.has(p.perm)).map(pageLink)

  return computed(() =>
    LAYOUT.map(g => ({ key: g.key, label: t(`settings.groups.${g.key}`), links: linksOf(g) })).filter(g => g.links.length > 0)
  )
}
