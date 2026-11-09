<script setup lang="ts">
import { computed } from 'vue'
import type { NavItem } from '@/stores/app'
import SidebarGroup from './SidebarGroup.vue'
import SidebarLink from './SidebarLink.vue'
import type { SidebarView } from './sidebarViews'

/**
 * The menu of a sidebar view (CONTRACTS §90) under a row that leads back to
 * the console. The parent keys it by the view, so each view sets up its own
 * menu once.
 */
const props = defineProps<{ view: SidebarView; backTo: string; collapsed?: boolean }>()

const groups = props.view.useGroups()
const back = computed<NavItem>(() => ({ id: 'back', labelKey: 'nav.backToConsole', icon: 'arrow-left', path: props.backTo }))
</script>

<template>
  <div :data-testid="`nav-view-${view.key}`">
    <!-- The way back stays in sight while a long menu scrolls: it sticks over the nav's top padding (-top-3), where nothing shows above it. -->
    <SidebarGroup class="sticky -top-3 z-10 -mt-3 bg-surface pt-3" :collapsed="collapsed">
      <SidebarLink :item="back" :active="false" :collapsed="collapsed" data-testid="nav-back" />
    </SidebarGroup>
    <SidebarGroup v-for="g in groups" :key="g.key" :title="g.label" :collapsed="collapsed" divided>
      <SidebarLink v-for="l in g.links" :key="l.key" :item="l.item" :active="l.active" :collapsed="collapsed" />
    </SidebarGroup>
  </div>
</template>
