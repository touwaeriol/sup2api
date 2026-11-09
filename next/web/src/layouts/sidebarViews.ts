import { computed, ref, watchEffect, type ComputedRef, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { NavItem, NavSection } from '@/stores/app'
import { SETTINGS_PAGE, useSettingsMenu } from '@/views/settings/settingsNav'
import { useRouteLineage } from './useActiveMenu'

/**
 * A sidebar view (CONTRACTS §90): the same sidebar showing another menu. On a
 * page of a view the console menu gives way to the view's own, under a row
 * that leads back. A route joins a view with `meta.sidebar`, as do the pages
 * below it (meta.parent); a new view is one more row of SIDEBAR_VIEWS.
 */
export type SidebarViewKey = 'settings'

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
  /** The console-view entry that opens this view; undefined when the caller sees none of its pages. */
  entry(menus: NavSection[]): NavItem | undefined
  /** The view's own menu. Called once in the setup of the component that renders it. */
  useGroups(): ComputedRef<ViewGroup[]>
}

/** In the order their entries close the admin group. */
export const SIDEBAR_VIEWS: SidebarView[] = [
  {
    key: 'settings',
    entry: menus =>
      menus.some(s => s.items.some(i => i.path === SETTINGS_PAGE))
        ? { id: 'view:settings', labelKey: 'nav.views.settings', icon: 'settings', path: SETTINGS_PAGE }
        : undefined,
    useGroups: () => useSettingsMenu()
  }
]

/** The view the current page opens in: the one its route declares, else the nearest page above it; none on a console page. */
export function useSidebarView(): ComputedRef<SidebarView | undefined> {
  const router = useRouter()
  const lineage = useRouteLineage()
  return computed(() => {
    const key = lineage.value.map(p => router.resolve(p).meta.sidebar).find(Boolean)
    return SIDEBAR_VIEWS.find(v => v.key === key)
  })
}

const CONSOLE_HOME = '/dashboard'

/**
 * Where "Back to console" leads: the console page the user opened the view
 * from, else the dashboard. Remembered by the sidebar for as long as it is
 * mounted, so not across a sign-out.
 */
export function useConsoleReturn(view: Ref<SidebarView | undefined>): ComputedRef<string> {
  const route = useRoute()
  const last = ref<string>()
  watchEffect(() => {
    if (!view.value) last.value = route.fullPath
  })
  return computed(() => last.value ?? CONSOLE_HOME)
}
