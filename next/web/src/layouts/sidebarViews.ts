import { computed, ref, watchEffect, type ComputedRef, type Ref } from 'vue'
import { useRoute, useRouter, type RouteLocationNormalizedLoaded } from 'vue-router'
import { useAppStore, type NavItem, type NavSection } from '@/stores/app'
import { SETTINGS_PAGE, useSettingsMenu } from '@/views/settings/settingsNav'
import { usePluginPageView } from './pluginNav'
import { useRouteLineage } from './useActiveMenu'

/**
 * A sidebar view (CONTRACTS §90): the same sidebar showing another menu. On a
 * page of a view the console menu gives way to the view's own, under a row
 * that leads back. A route joins a view with `meta.sidebar`, as do the pages
 * below it (meta.parent); a new view is one more row of SIDEBAR_VIEWS. A
 * plugin page that declares sections opens in a view of its own (pluginNav.ts).
 */
export type SidebarViewKey = 'settings' | `plugin:${string}`

/** A row of a view's menu. `item.path` may carry a query (e.g. /settings?tab=sticky). */
export interface ViewLink {
  key: string
  item: NavItem
  active: boolean
}

export interface ViewGroup {
  key: string
  label: string
  links: ViewLink[]
}

export interface SidebarView {
  key: SidebarViewKey
  /** The menu entry that opens this view, shown last in the admin group; undefined when the caller's menu has none. */
  entry(menus: NavSection[]): NavItem | undefined
  /** The view's own menu. Called once in the setup of the component that renders it. */
  useGroups(): ComputedRef<ViewGroup[]>
}

/** In the order their entries close the admin group. */
export const SIDEBAR_VIEWS: SidebarView[] = [
  {
    key: 'settings',
    entry: menus => menus.flatMap(s => s.items).find(i => i.path === SETTINGS_PAGE),
    useGroups: () => useSettingsMenu()
  }
]

/** The entries of the views the caller may open. */
export function useViewEntries(): ComputedRef<NavItem[]> {
  const app = useAppStore()
  return computed(() => SIDEBAR_VIEWS.flatMap(v => v.entry(app.menus) ?? []))
}

/**
 * The view the current page opens in: the one its route declares, else the
 * nearest page above it; none on a console page. A caller whose menu lacks the
 * view's entry keeps the console menu (an older core lists groups and model
 * prices in it and gives their readers no settings entry); until the menu
 * loads the route decides. A plugin page that declares sections always gets
 * its view: the page drops its own tabs on this console (UI 3.14), so its
 * sections must stay reachable also when it was opened by a link rather than
 * from a menu entry.
 */
export function useSidebarView(): ComputedRef<SidebarView | undefined> {
  const app = useAppStore()
  const router = useRouter()
  const lineage = useRouteLineage()
  const pluginView = usePluginPageView()
  return computed(() => {
    const key = lineage.value.map(p => router.resolve(p).meta.sidebar).find(Boolean)
    const view = SIDEBAR_VIEWS.find(v => v.key === key)
    if (!view) return pluginView.value
    return !app.menusLoaded || view.entry(app.menus) ? view : undefined
  })
}

const CONSOLE_HOME = '/dashboard'

/** Where a refused or an unknown address ends up (router/index.ts): nothing to come back to. */
const isErrorPage = (route: RouteLocationNormalizedLoaded) => route.path === '/forbidden' || 'pathMatch' in route.params

/**
 * Where "Back to console" leads: the console page the user opened the view
 * from, else the dashboard. Remembered by the sidebar for as long as it is
 * mounted, so not across a sign-out.
 */
export function useConsoleReturn(view: Ref<SidebarView | undefined>): ComputedRef<string> {
  const route = useRoute()
  const last = ref<string>()
  watchEffect(() => {
    if (!view.value && !isErrorPage(route)) last.value = route.fullPath
  })
  return computed(() => last.value ?? CONSOLE_HOME)
}

/**
 * Hands the menu area over when the sidebar swaps its menu (hooks of the
 * transition): the new menu starts at its top, not where the old one was
 * scrolled to, and takes the keyboard focus the old one held, which would
 * otherwise fall back to the page.
 */
export function useMenuHandover(nav: Ref<HTMLElement | undefined>) {
  let focused = false
  return {
    onBeforeLeave: () => {
      focused = !!nav.value?.contains(document.activeElement)
    },
    onBeforeEnter: () => nav.value?.scrollTo({ top: 0 }),
    onAfterEnter: () => {
      if (focused) nav.value?.querySelector<HTMLElement>('a, button')?.focus()
    }
  }
}
