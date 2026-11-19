import { computed, createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { i18n } from './i18n'
import { router } from './router'
import { setupHost } from './host'
import { vPermission } from './directives/permission'
import { TABLE_COLUMN_SCOPE_KEY } from '@sub2api/ui'
import { useAuthStore } from './stores/auth'
import './style.css'
import './components/platform/brand.css'

const app = createApp(App)
app.use(createPinia())
app.use(i18n)
setupHost(router)
app.use(router)
app.directive('permission', vPermission)
app.provide(TABLE_COLUMN_SCOPE_KEY, computed(() => useAuthStore().me?.id ?? null))
app.mount('#app')
