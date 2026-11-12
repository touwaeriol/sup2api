import { computed, type ComputedRef } from 'vue'
import { useRoute, type RouteLocationNormalizedLoaded } from 'vue-router'
import { lt } from '@/i18n'
import { usePluginStore } from '@/stores/plugins'
import type { UIPageNav } from '@/api/types'
import type { SidebarView, ViewGroup } from './sidebarViews'

/**
 * A plugin page with sections (manifest ui.pages.<page>.nav, UI 3.14) opens
 * in a sidebar view of its own, like system settings (CONTRACTS §90): the
 * sidebar lists the page's sections and each one opens ?tab=<key> on the
 * page. The plugin only declares the sections; the page reads ?tab= and
 * shows that section. An older console ignores the declaration and the
 * plugin keeps its in-page tabs.
 */

/** The query parameter naming the section shown, as on the settings page. */
export const NAV_QUERY = 'tab'

const PAGE_ROUTE = /^\/p\/([^/]+)\/([^/]+)$/

export const pluginPagePath = (plugin: string, page: string) => `/p/${plugin}/${page}`

/** The section shown: the one asked for when the page has it, else the first. */
export function activeSection(nav: UIPageNav, asked: unknown): string | undefined {
  const keys = nav.groups.flatMap(g => g.items.map(i => i.key))
  return typeof asked === 'string' && keys.includes(asked) ? asked : keys[0]
}

function useGroups(plugin: string, page: string): ComputedRef<ViewGroup[]> {
  const route = useRoute()
  const plugins = usePluginStore()
  const path = pluginPagePath(plugin, page)
  return computed(() => {
    const nav = plugins.page(plugin, page)?.page.nav
    if (!nav) return []
    const active = route.path === path ? activeSection(nav, route.query[NAV_QUERY]) : undefined
    return nav.groups.map(g => ({
      key: g.key,
      label: lt(g.label),
      links: g.items.map(i => ({
        key: i.key,
        item: {
          id: `${plugin}:${i.key}`,
          label: i.label,
          icon: i.icon,
          path: `${path}?${NAV_QUERY}=${encodeURIComponent(i.key)}`,
          pluginKey: plugin
        },
        active: i.key === active
      }))
    }))
  })
}

// One view per page, so the sidebar keeps the menu it set up while the page stays open.
const views = new Map<string, SidebarView>()

function viewOf(plugin: string, page: string): SidebarView {
  const key = `plugin:${plugin}/${page}` as const
  const path = pluginPagePath(plugin, page)
  const view = views.get(key) ?? {
    key,
    entry: menus => menus.flatMap(s => s.items).find(i => i.path === path),
    useGroups: () => useGroups(plugin, page)
  }
  views.set(key, view)
  return view
}

/** The view of the plugin page shown, when that page declares sections; none on any other page. */
export function usePluginPageView(): ComputedRef<SidebarView | undefined> {
  const route = useRoute()
  const plugins = usePluginStore()
  return computed(() => pluginPageView(route, plugins.page))
}

function pluginPageView(route: RouteLocationNormalizedLoaded, pageOf: ReturnType<typeof usePluginStore>['page']): SidebarView | undefined {
  const m = PAGE_ROUTE.exec(route.path)
  const [plugin, page] = m ? [decoded(m[1]), decoded(m[2])] : []
  return plugin && page && pageOf(plugin, page)?.page.nav?.groups.length ? viewOf(plugin, page) : undefined
}

/** A path segment decoded; undefined for a malformed one (the router shows that address as unknown). */
function decoded(s: string): string | undefined {
  try {
    return decodeURIComponent(s)
  } catch {
    return undefined
  }
}
