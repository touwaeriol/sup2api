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

// The top bar shows the route title and the page description; a page header
// with the same title is only its filters and actions, and with no filters its
// actions move into the top bar on a wide screen (AppTopbar's #s-page-actions).
provideTitleContext(() => (route.meta.title ? t(route.meta.title as string) : ''), toRef(app, 'pageDescription'), '#s-page-actions')

// The menus do not wait for the plugin modules: GET /me/menus already lists
// plugin menus, and the fallback is a fixed dashboard entry.
onMounted(() => void refreshShell())
</script>

<template>
  <div class="flex min-h-dvh">
    <AppSidebar />
    <div class="flex min-w-0 flex-1 flex-col">
      <AppTopbar />
      <!-- A floating action button (SButton fab) must not cover the end of the page: pagination, the last card's actions. -->
      <main class="w-full min-w-0 flex-1 px-3 py-3 md:px-5 md:py-4 [&:has([data-fab])]:pb-[calc(5.5rem+env(safe-area-inset-bottom))]">
        <RouterView v-slot="{ Component, route: view }">
          <component :is="Component" :key="view.path" />
        </RouterView>
      </main>
    </div>
  </div>
</template>
