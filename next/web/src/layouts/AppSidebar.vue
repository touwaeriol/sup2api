<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { SIcon, SSkeleton } from '@sub2api/ui'
import { useAppStore, type NavSection } from '@/stores/app'
import { lt } from '@/i18n'
import VersionBadge from '@/components/VersionBadge.vue'
import SidebarGroup from './SidebarGroup.vue'
import SidebarLink from './SidebarLink.vue'
import SidebarSubmenu from './SidebarSubmenu.vue'
import SidebarViewMenu from './SidebarViewMenu.vue'
import { groupLinks, groupMenus, showGroupTitle, useOpenSections, type NavGroup, type NavLink } from './sidebarGroups'
import { useConsoleReturn, useMenuHandover, useSidebarView, useViewEntries } from './sidebarViews'
import { useNavItemLabel } from './navItem'
import { useActiveMenuPath } from './useActiveMenu'

const app = useAppStore()
const route = useRoute()
const { t } = useI18n()
const labelOf = useNavItemLabel()

function sectionLabel(s: NavSection): string {
  return s.label ? lt(s.label) : t(`nav.sections.${s.key}`)
}
function groupTitle(g: NavGroup): string | undefined {
  return showGroupTitle(g, app.menus) ? t(`nav.sections.${g.key}`) : undefined
}

const activePath = useActiveMenuPath()
function active(l: NavLink): boolean {
  return l.item.path === activePath.value
}

// The drawer closes on any navigation, not only on a menu click (links in pages, Back).
watch(
  () => route.fullPath,
  () => (app.mobileNavOpen = false)
)

/** The desktop collapse does not apply to the mobile drawer, which has no expand button. */
const collapsed = computed(() => app.sidebarCollapsed && !app.mobileNavOpen)

// A page of a sidebar view (system settings) swaps the console menu for the view's own.
const view = useSidebarView()
const backTo = useConsoleReturn(view)
const nav = ref<HTMLElement>()
const handover = useMenuHandover(nav)
/** Names the landmark after the menu it shows: a view's by the entry that opens it. */
const navLabel = computed(() => {
  const entry = view.value?.entry(app.menus)
  return entry ? labelOf(entry) : t('nav.label')
})

const viewEntries = useViewEntries()
const groups = computed(() => groupMenus(app.menus, viewEntries.value))
const activeSubmenu = computed(
  () => groups.value.flatMap(g => g.entries).find(e => e.section?.items.some(i => i.path === activePath.value))?.section?.key
)
const { isOpen, toggle } = useOpenSections(activeSubmenu)
</script>

<template>
  <div v-if="app.mobileNavOpen" class="fixed inset-0 z-30 bg-black/40 lg:hidden" @click="app.mobileNavOpen = false" />
  <aside
    class="fixed inset-y-0 left-0 z-40 flex flex-col border-r border-line bg-surface pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] transition-all duration-200 lg:sticky lg:top-0 lg:h-screen lg:translate-x-0"
    :class="[collapsed ? 'w-[72px]' : 'w-60', app.mobileNavOpen ? 'translate-x-0' : '-translate-x-full']"
  >
    <div class="flex min-h-12 shrink-0 items-center gap-2 border-b border-line px-4 py-2" :class="collapsed ? 'flex-col px-1' : ''">
      <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-primary-500 to-primary-700 text-sm font-bold text-white">
        S
      </div>
      <div class="min-w-0" :class="collapsed ? 'max-w-full' : ''">
        <span v-if="!collapsed" class="block text-base font-semibold leading-tight tracking-tight text-fg">sub2api</span>
        <VersionBadge :compact="collapsed" />
      </div>
    </div>
    <nav ref="nav" class="flex-1 overflow-y-auto px-3 py-3" :aria-label="navLabel">
      <Transition
        mode="out-in"
        enter-active-class="transition duration-150 ease-out motion-reduce:transition-none"
        enter-from-class="translate-x-1 opacity-0"
        leave-active-class="transition duration-75 ease-in motion-reduce:transition-none"
        leave-to-class="opacity-0"
        v-bind="handover"
      >
        <SidebarViewMenu v-if="view" :key="view.key" :view="view" :back-to="backTo" :collapsed="collapsed" />
        <div v-else key="console">
          <div v-if="!app.menusLoaded" class="space-y-2 px-2">
            <SSkeleton v-for="i in 8" :key="i" height="2rem" />
          </div>
          <SidebarGroup
            v-for="(g, index) in groups"
            :key="g.key"
            :title="groupTitle(g)"
            :collapsed="collapsed"
            :divided="index > 0"
            :data-testid="`nav-group-${g.key}`"
          >
            <template v-if="collapsed">
              <SidebarLink v-for="l in groupLinks(g)" :key="l.key" :item="l.item" :active="active(l)" collapsed />
            </template>
            <template v-else>
              <template v-for="e in g.entries" :key="e.key">
                <div v-if="e.gap" class="h-2" aria-hidden="true" />
                <SidebarSubmenu
                  v-if="e.section"
                  :section="e.section"
                  :label="sectionLabel(e.section)"
                  :open="isOpen(e.section.key)"
                  :active-path="activePath"
                  @toggle="toggle(e.section.key)"
                />
                <SidebarLink v-else :item="e.item" :active="active(e)" />
              </template>
            </template>
          </SidebarGroup>
        </div>
      </Transition>
      <button
        v-if="app.menusError"
        type="button"
        class="flex w-full items-center gap-3 rounded-xl px-3 py-2 text-sm text-danger-600 hover:bg-surface-2"
        :title="collapsed ? t('nav.menuRetry') : undefined"
        data-testid="menu-retry"
        @click="app.loadMenus()"
      >
        <SIcon name="refresh" class="h-5 w-5 shrink-0" />
        <span v-if="!collapsed" class="truncate text-left">{{ t('nav.menuRetry') }}</span>
      </button>
    </nav>
    <button
      type="button"
      class="hidden h-10 shrink-0 items-center justify-center border-t border-line text-fg-subtle hover:text-fg lg:flex"
      :title="app.sidebarCollapsed ? t('nav.expand') : t('nav.collapse')"
      :aria-label="app.sidebarCollapsed ? t('nav.expand') : t('nav.collapse')"
      :aria-expanded="!app.sidebarCollapsed"
      @click="app.sidebarCollapsed = !app.sidebarCollapsed"
    >
      <SIcon name="chevron-right" class="h-4 w-4 transition-transform" :class="app.sidebarCollapsed ? '' : 'rotate-180'" />
    </button>
  </aside>
</template>
