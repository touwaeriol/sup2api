import { createRouter, createWebHistory, type RouteLocationNormalized, type RouteLocationRaw, type RouteRecordRaw } from 'vue-router'
import { session } from '@sub2api/host'
import { useAuthStore } from '@/stores/auth'
import { ACCOUNT_PAGE_PERMS, PROXY_PAGE_PERMS } from '@/composables/useOwnership'
import AppLayout from '@/layouts/AppLayout.vue'
import type { SidebarViewKey } from '@/layouts/sidebarViews'
import { CCGATEWAY_PAGE, isPluginKey, pluginSettingsPath } from '@/views/plugins/pluginUtil'
import { settingsLanding } from '@/views/settings/settingsNav'
import { closeLayersOnBack } from './layerBack'

declare module 'vue-router' {
  interface RouteMeta {
    /** Required permission(s): any of them grants access. */
    perm?: string | string[]
    /** i18n key of the page title. */
    title?: string
    /**
     * Path of the page this one is reached from; the top bar shows it (and
     * its own parent, and so on) as breadcrumbs before the title.
     */
    parent?: string
    public?: boolean
    /** Where a caller without `perm` goes instead of /forbidden, if anywhere. */
    deniedRedirect?: (to: RouteLocationNormalized) => string | undefined
    /** Where an old address of this page now lives; asked on every navigation, a change of the query alone included. */
    movedTo?: (to: RouteLocationNormalized) => RouteLocationRaw | undefined
    /** The sidebar view this page and the pages under it open in (CONTRACTS §90). */
    sidebar?: SidebarViewKey
  }
}

/**
 * Plugin settings were tabs of the settings page (?tab=plugin:<key>) before
 * they had an address of their own. Anything but a plugin key stays: the
 * settings page says the linked page is not available.
 */
function movedSettingsTab(to: RouteLocationNormalized): RouteLocationRaw | undefined {
  const tab = to.query.tab
  if (typeof tab !== 'string' || !tab.startsWith('plugin:')) return undefined
  const key = tab.slice('plugin:'.length)
  return isPluginKey(key) ? { path: pluginSettingsPath(key), hash: to.hash } : undefined
}

const children: RouteRecordRaw[] = [
  { path: '', redirect: '/dashboard' },
  { path: 'dashboard', component: () => import('@/views/dashboard/DashboardView.vue'), meta: { title: 'nav.items.dashboard' } },

  { path: 'users', component: () => import('@/views/users/UsersView.vue'), meta: { perm: 'user:read', title: 'nav.items.users' } },
  { path: 'roles', component: () => import('@/views/roles/RolesView.vue'), meta: { perm: 'role:read', title: 'nav.items.roles' } },
  { path: 'api-keys', redirect: '/me/api-keys' },
  { path: 'me/api-keys', component: () => import('@/views/keys/MyApiKeysView.vue'), meta: { perm: 'apikey:self:manage', title: 'nav.items.myApiKeys' } },
  { path: 'groups', component: () => import('@/views/groups/GroupsView.vue'), meta: { perm: 'group:read', title: 'nav.items.groups', sidebar: 'settings' } },
  { path: 'proxies', component: () => import('@/views/proxies/ProxiesView.vue'), meta: { perm: PROXY_PAGE_PERMS, title: 'nav.items.proxies' } },
  { path: 'accounts', component: () => import('@/views/accounts/AccountsView.vue'), meta: { perm: ACCOUNT_PAGE_PERMS, title: 'nav.items.accounts' } },
  // The old platforms page.
  { path: 'platforms', redirect: '/accounts' },

  { path: 'prices', component: () => import('@/views/prices/PricesView.vue'), meta: { perm: 'price:read', title: 'nav.items.prices', sidebar: 'settings' } },
  { path: 'prices/new', component: () => import('@/views/prices/PriceEditView.vue'), meta: { perm: 'price:manage', title: 'prices.newTitle', parent: '/prices' } },
  { path: 'prices/sources', component: () => import('@/views/prices/PriceSourcesView.vue'), meta: { perm: 'price:read', title: 'prices.sources.title', parent: '/prices' } },
  { path: 'prices/sources/:id/sync', component: () => import('@/views/prices/PriceSyncView.vue'), meta: { perm: 'price:manage', title: 'prices.syncPrices', parent: '/prices/sources' } },
  { path: 'prices/:id', component: () => import('@/views/prices/PriceEditView.vue'), meta: { perm: 'price:read', title: 'prices.editTitleShort', parent: '/prices' } },
  { path: 'usage/overview', component: () => import('@/views/usage/UsageOverviewView.vue'), props: { admin: true }, meta: { perm: 'usage:all:read', title: 'nav.items.usageOverview' } },
  { path: 'usage', component: () => import('@/views/usage/UsageView.vue'), meta: { perm: 'usage:all:read', title: 'nav.items.usage' } },
  { path: 'me/models', component: () => import('@/views/me/MyModelsView.vue'), meta: { perm: 'apikey:self:manage', title: 'nav.items.myModels' } },
  { path: 'me/wallet', component: () => import('@/views/me/WalletView.vue'), meta: { perm: 'balance:self:read', title: 'nav.items.myWallet' } },
  { path: 'me/profile', component: () => import('@/views/me/ProfileView.vue'), meta: { title: 'nav.items.myProfile' } },
  { path: 'me/usage/overview', component: () => import('@/views/usage/UsageOverviewView.vue'), meta: { perm: 'usage:self:read', title: 'nav.items.myUsageOverview' } },
  { path: 'me/usage', component: () => import('@/views/usage/MyUsageView.vue'), meta: { perm: 'usage:self:read', title: 'nav.items.myUsage' } },
  // The old usage statistics page.
  { path: 'me/stats', redirect: '/me/usage/overview' },
  // Opened from a row of the users list.
  { path: 'ledger', component: () => import('@/views/ledger/LedgerView.vue'), meta: { perm: 'balance:all:read', title: 'nav.items.ledger', parent: '/users' } },
  { path: 'me/ledger', redirect: '/me/wallet' },
  { path: 'me/balance', redirect: '/me/wallet' },
  { path: 'sticky', redirect: { path: '/settings', query: { tab: 'sticky' } } },
  {
    path: 'settings',
    component: () => import('@/views/settings/SettingsView.vue'),
    meta: {
      perm: ['settings:read', 'sticky:read'],
      title: 'nav.items.settings',
      sidebar: 'settings',
      movedTo: movedSettingsTab,
      // The entry of the settings view is also for a caller with none of the configuration pages; a link to one of them stays refused.
      deniedRedirect: to => (to.query.tab ? undefined : settingsLanding())
    }
  },

  { path: CCGATEWAY_PAGE, component: () => import('@/views/ccgateway/CCGatewayView.vue'), meta: { perm: 'settings:read', title: 'ccgateway.title', sidebar: 'settings' } },
  { path: 'plugins', component: () => import('@/views/plugins/PluginsView.vue'), meta: { perm: 'plugin:read', title: 'nav.items.plugins' } },
  {
    path: 'plugins/:key/versions/:version/consent',
    component: () => import('@/views/plugins/ConsentView.vue'),
    meta: { perm: 'plugin:install', title: 'nav.crumbs.pluginConsent', parent: '/plugins' }
  },
  { path: 'plugins/:key/rollout', component: () => import('@/views/plugins/RolloutView.vue'), meta: { perm: 'plugin:read', title: 'nav.crumbs.pluginRollout', parent: '/plugins' } },
  {
    path: 'plugins/:key',
    component: () => import('@/views/plugins/PluginDetailView.vue'),
    meta: {
      perm: 'plugin:read',
      title: 'nav.crumbs.pluginDetail',
      parent: '/plugins',
      // Plugins link to ?tab=settings; holders of the plugin's settings permission get the settings page.
      deniedRedirect: to => (to.query.tab === 'settings' ? pluginSettingsPath(String(to.params.key)) : undefined)
    }
  },
  // ccgateway's settings are a core page of its own.
  { path: 'plugins/ccgateway/settings', redirect: CCGATEWAY_PAGE },
  // Access is the plugin's settings permission or plugin:read, checked by the core.
  {
    path: 'plugins/:key/settings',
    component: () => import('@/views/plugins/PluginSettingsView.vue'),
    meta: { title: 'nav.crumbs.pluginSettings', parent: '/settings' }
  },
  { path: 'market', component: () => import('@/views/plugins/MarketView.vue'), meta: { perm: 'plugin:market:read', title: 'nav.items.market' } },
  { path: 'publishers', component: () => import('@/views/plugins/PublishersView.vue'), meta: { perm: 'publisher:read', title: 'nav.items.publishers' } },
  { path: 'nodes', component: () => import('@/views/nodes/NodesView.vue'), meta: { perm: 'node:read', title: 'nav.items.nodes' } },
  { path: 'system/tasks', component: () => import('@/views/tasks/TasksView.vue'), meta: { perm: 'task:read', title: 'nav.items.tasks' } },
  { path: 'system/upgrades', component: () => import('@/views/upgrades/UpgradesView.vue'), meta: { perm: 'system:update:read', title: 'upgrades.title' } },
  { path: 'system/audit', component: () => import('@/views/upgrades/AuditView.vue'), meta: { perm: 'audit:read', title: 'observe.audit' } },

  { path: 'p/:plugin/:page', component: () => import('@/views/plugin-host/PluginPageView.vue') },
  { path: 'forbidden', component: () => import('@/views/errors/ForbiddenView.vue') },
  { path: ':pathMatch(.*)*', component: () => import('@/views/errors/NotFoundView.vue') }
]

const routes: RouteRecordRaw[] = [
  { path: '/login', component: () => import('@/views/auth/LoginView.vue'), meta: { public: true } },
  { path: '/', component: AppLayout, children }
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 })
})
closeLayersOnBack(router)

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (to.meta.public) {
    if (to.path === '/login' && session.get()) return '/dashboard'
    return true
  }
  if (!session.get()) return { path: '/login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  if (!auth.me) {
    try {
      await auth.loadMe()
    } catch {
      if (!session.get()) return { path: '/login', query: { redirect: to.fullPath } }
    }
  }
  const moved = to.meta.movedTo?.(to)
  if (moved) return moved
  if (to.meta.perm && !auth.has(to.meta.perm)) {
    const redirect = to.meta.deniedRedirect?.(to)
    if (redirect) return redirect
    const perm = Array.isArray(to.meta.perm) ? to.meta.perm.join(' / ') : to.meta.perm
    return { path: '/forbidden', query: { perm } }
  }
  return true
})
