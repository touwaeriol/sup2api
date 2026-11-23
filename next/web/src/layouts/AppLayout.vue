<script setup lang="ts">
import { onMounted, toRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { provideTitleContext } from '@sub2api/ui'
import { refreshShell, useAppStore } from '@/stores/app'
import AppSidebar from './AppSidebar.vue'
import AppTopbar from './AppTopbar.vue'

const app = useAppStore()
const route = useRoute()
const { t } = useI18n()

// The top bar shows what the page header of the page gives: its title, its
// description and, on a wide screen, what it puts beside the title. The header
// in the page is then only its filters and actions (AppTopbar's
// #s-page-title-extra). Until a page gives a title the top bar shows the route's.
provideTitleContext(() => app.pageHead?.title || (route.meta.title ? t(route.meta.title as string) : ''), { head: toRef(app, 'pageHead'), extra: '#s-page-title-extra' })

// The menus do not wait for the plugin modules: GET /me/menus already lists
// plugin menus, and the fallback is a fixed dashboard entry.
onMounted(() => void refreshShell())
</script>

<template>
  <div class="flex min-h-dvh">
    <AppSidebar />
    <div class="flex min-w-0 flex-1 flex-col">
      <AppTopbar />
      <!-- A floating action button (SButton fab) must not cover the end of the page: pagination, the last card's actions.
           While the bulk selection dock is up the fab is hidden and the dock keeps its own room (SBulkBar). -->
      <main
        class="w-full min-w-0 flex-1 px-3 py-3 md:px-5 md:py-4 [&:has([data-fab]):not(:has([data-bulk-dock]))]:pb-[calc(5.5rem+env(safe-area-inset-bottom))]"
      >
        <RouterView v-slot="{ Component, route: view }">
          <component :is="Component" :key="view.path" />
        </RouterView>
      </main>
    </div>
  </div>
</template>
