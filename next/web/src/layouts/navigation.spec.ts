import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { computed, defineComponent } from 'vue'
import { i18n } from '@/i18n'
import { useAppStore } from '@/stores/app'
import AppSidebar from './AppSidebar.vue'
import AppTopbar from './AppTopbar.vue'
import { SIDEBAR_VIEWS, type ViewGroup } from './sidebarViews'
import DeclarativeTable from '@/components/plugin/DeclarativeTable.vue'
import { mountPinned } from '@sub2api/ui/testing'

const api = vi.hoisted(() => ({ get: vi.fn(), list: vi.fn() }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), api }))
vi.mock('@/stores/plugins', () => ({ usePluginStore: () => ({ refresh: () => Promise.resolve() }) }))
const denied = vi.hoisted(() => new Set<string>())
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    me: { email: 'a@x', roles: [] },
    balance: null,
    has: (p?: string | string[]) => [p ?? []].flat().length === 0 || [p ?? []].flat().some(x => !denied.has(x))
  })
}))
enableAutoUnmount(afterEach)

const Blank = defineComponent({ template: '<div />' })
function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/dashboard', component: Blank, meta: { title: 'nav.items.dashboard' } },
      { path: '/accounts', component: Blank, meta: { title: 'nav.items.accounts' } },
      { path: '/users', component: Blank, meta: { title: 'nav.items.users' } },
      { path: '/ledger', component: Blank, meta: { title: 'nav.items.ledger', parent: '/users' } },
      { path: '/groups', component: Blank, meta: { title: 'nav.items.groups', sidebar: 'settings' } },
      { path: '/prices', component: Blank, meta: { title: 'nav.items.prices', sidebar: 'settings' } },
      { path: '/prices/sources', component: Blank, meta: { title: 'prices.sources.title', parent: '/prices' } },
      { path: '/prices/sources/:id/sync', component: Blank, meta: { title: 'prices.syncPrices', parent: '/prices/sources' } },
      {
        path: '/settings',
        component: Blank,
        meta: { perm: ['settings:read', 'sticky:read'], title: 'nav.items.settings', sidebar: 'settings' }
      },
      { path: '/plugins', component: Blank, meta: { perm: 'plugin:read', title: 'nav.items.plugins' } },
      { path: '/plugins/:key', component: Blank, meta: { title: 'nav.crumbs.pluginDetail', parent: '/plugins' } },
      { path: '/plugins/:key/settings', component: Blank, meta: { title: 'nav.crumbs.pluginSettings', parent: '/settings' } },
      { path: '/forbidden', component: Blank },
      { path: '/:pathMatch(.*)*', component: Blank }
    ]
  })
}

const item = (id: string, path: string) => ({ id, label: { zh: id }, icon: 'dashboard', path })
const settings = { ...item('settings', '/settings'), label: { zh: '系统设置' } }

beforeEach(() => {
  vi.clearAllMocks()
  denied.clear()
  localStorage.clear()
  setActivePinia(createPinia())
  i18n.global.locale.value = 'zh'
})

describe('sidebar', () => {
  async function mountSidebar(sections: unknown[] | Error, at = '/dashboard') {
    if (sections instanceof Error) api.get.mockRejectedValue(sections)
    else api.get.mockResolvedValue(sections)
    const router = makeRouter()
    await router.push(at)
    const app = useAppStore()
    await app.loadMenus()
    const wrapper = mount(AppSidebar, { global: { plugins: [i18n, router] } })
    return { wrapper, app, router }
  }
  type Sidebar = ReturnType<typeof mount>
  const labels = (w: Sidebar) => w.findAll('nav a').map(a => a.text())
  const submenus = (w: Sidebar) => w.findAll('nav button[aria-expanded]').map(b => b.text())
  const current = (w: Sidebar) =>
    w
      .findAll('nav a')
      .filter(a => a.attributes('aria-current') === 'page')
      .map(a => a.text())

  it('shows a user the console and personal groups, a public plugin page untitled after them', async () => {
    const { wrapper } = await mountSidebar([
      { section: 'overview', items: [item('dashboard', '/dashboard'), item('keys', '/me/api-keys')] },
      { section: 'plugins', items: [item('info', '/p/guard/info')] },
      { section: 'me', items: [item('wallet', '/me/wallet')] }
    ])
    expect(labels(wrapper)).toEqual(['dashboard', 'keys', 'wallet', 'info'])
    expect(wrapper.get('[data-testid="nav-group-overview"]').text()).toContain('控制台')
    expect(wrapper.get('[data-testid="nav-group-me"]').text()).toContain('个人中心')
    expect(wrapper.get('[data-testid="nav-group-admin"]').text()).toBe('info')
  })

  const overview = { section: 'overview', items: [item('dashboard', '/dashboard')] }
  const operatorMenu = [
    overview,
    { section: 'gateway', items: [item('accounts', '/accounts'), item('proxies', '/proxies')] },
    { section: 'security', items: [item('guard', '/p/guard/dashboard')] },
    { section: 'finance', items: [item('growth', '/p/growth/dashboard')] },
    { section: 'users', items: [item('users', '/users'), item('roles', '/roles')] },
    { section: 'plugins', items: [item('plugins', '/plugins'), item('market', '/market'), item('inbox', '/p/mailer/inbox')] },
    { section: 'vol:ark', label: { zh: '火山方舟' }, items: [item('assets', '/p/vol/assets'), item('sets', '/p/vol/sets')] },
    { section: 'system', items: [item('nodes', '/nodes'), item('tasks', '/system/tasks'), settings] },
    { section: 'me', items: [item('wallet', '/me/wallet')] }
  ]
  const flat = ['dashboard', 'wallet', 'accounts', 'proxies', 'guard', 'growth']

  it('lists gateway, security and finance flat, folds the other admin sections and ends with system settings', async () => {
    const { wrapper, app } = await mountSidebar(operatorMenu)
    expect(wrapper.get('[data-testid="nav-group-admin"]').text()).toContain('管理员')
    expect(labels(wrapper)).toEqual([...flat, '系统设置'])
    expect(submenus(wrapper)).toEqual(['用户', '插件', '火山方舟', '系统'])
    expect(wrapper.findAll('nav a').at(-1)!.attributes('href')).toBe('/settings')
    // The settings page is reached by its own entry, not from the system submenu.
    await wrapper.get('[data-testid="nav-submenu-system"]').trigger('click')
    expect(labels(wrapper)).toEqual([...flat, 'nodes', 'tasks', '系统设置'])
    expect(wrapper.get('[data-testid="nav-submenu-system"]').attributes('aria-expanded')).toBe('true')
    expect(JSON.parse(localStorage.getItem('s2a.navOpen') || '[]')).toEqual(['system'])
    // A little space sets the blocks apart: gateway, security with finance, the submenus, the settings entry.
    const spacers = wrapper.findAll('[data-testid="nav-group-admin"] > [aria-hidden="true"]')
    expect(spacers.map(d => d.element.nextElementSibling?.textContent?.trim())).toEqual(['guard', '用户', '系统设置'])
    app.sidebarCollapsed = true
    await flushPromises()
    // Collapsed: no titles or submenus, every page an icon, a rule between every two groups.
    expect(wrapper.text()).not.toContain('管理员')
    expect(submenus(wrapper)).toEqual([])
    expect(wrapper.findAll('nav a').map(a => a.attributes('aria-label'))).toEqual([
      ...flat,
      ...['users', 'roles', 'plugins', 'market', 'inbox', 'assets', 'sets', 'nodes', 'tasks'],
      '系统设置'
    ])
    expect(wrapper.findAll('nav hr')).toHaveLength(2)
  })

  it('shows a section left with one page as that page, and no settings entry without the settings page', async () => {
    const { wrapper } = await mountSidebar([
      overview,
      { section: 'users', items: [item('users', '/users')] },
      { section: 'plugins', items: [item('market', '/market')] },
      { section: 'system', items: [item('nodes', '/nodes'), settings] }
    ])
    expect(labels(wrapper)).toEqual(['dashboard', 'users', 'market', 'nodes', '系统设置'])
    expect(submenus(wrapper)).toEqual([])
    const vendor = await mountSidebar([overview, { section: 'system', items: [item('nodes', '/nodes'), item('tasks', '/system/tasks')] }])
    expect(labels(vendor.wrapper)).toEqual(['dashboard'])
    expect(submenus(vendor.wrapper)).toEqual(['系统'])
  })

  it('opens the submenu of the current page and remembers the ones opened before', async () => {
    localStorage.setItem('s2a.navOpen', JSON.stringify(['other:x']))
    const { wrapper } = await mountSidebar(operatorMenu, '/p/vol/sets')
    expect(labels(wrapper)).toContain('sets')
    await wrapper.get('[data-testid="nav-submenu-vol:ark"]').trigger('click')
    expect(labels(wrapper)).not.toContain('sets')
    expect(JSON.parse(localStorage.getItem('s2a.navOpen') || '[]')).toEqual(['other:x'])
  })

  it('lights up the installed plugins on a page reached from them, inside the opened plugins submenu', async () => {
    const { wrapper } = await mountSidebar(operatorMenu, '/plugins/mailer')
    expect(wrapper.get('[data-testid="nav-submenu-plugins"]').attributes('aria-expanded')).toBe('true')
    expect(labels(wrapper)).toEqual([...flat, 'plugins', 'market', 'inbox', '系统设置'])
    expect(current(wrapper)).toEqual(['plugins'])
  })

  it('lights up the menu entry of the page, else of its nearest parent page, else of a path it starts with', async () => {
    expect(current((await mountSidebar(operatorMenu, '/accounts')).wrapper)).toEqual(['accounts'])
    // The balance ledger has no entry: it is opened from the users list.
    const ledger = await mountSidebar(operatorMenu, '/ledger?user_id=3')
    expect(current(ledger.wrapper)).toEqual(['users'])
    expect(ledger.wrapper.get('[data-testid="nav-submenu-users"]').attributes('aria-expanded')).toBe('true')
    expect(current((await mountSidebar(operatorMenu, '/accounts/5')).wrapper)).toEqual(['accounts'])
  })

  it('keeps the labels in the mobile drawer when collapsed on desktop', async () => {
    const { wrapper, app } = await mountSidebar([{ section: 'overview', items: [item('dashboard', '/dashboard')] }])
    app.sidebarCollapsed = true
    await flushPromises()
    expect(labels(wrapper)).toEqual([''])
    app.mobileNavOpen = true
    await flushPromises()
    expect(labels(wrapper)).toEqual(['dashboard'])
  })

  it('closes the mobile drawer on any navigation', async () => {
    const { wrapper, app } = await mountSidebar([{ section: 'overview', items: [item('dashboard', '/dashboard')] }])
    app.mobileNavOpen = true
    await wrapper.vm.$router.push('/prices')
    await flushPromises()
    expect(app.mobileNavOpen).toBe(false)
  })

  it('shows only the dashboard and a retry when the menu cannot load', async () => {
    const { wrapper, app } = await mountSidebar(new Error('down'))
    expect(app.menusError).toBe(true)
    expect(labels(wrapper)).toEqual(['仪表盘'])
    api.get.mockResolvedValue([
      { section: 'overview', items: [item('dashboard', '/dashboard')] },
      { section: 'gateway', items: [item('accounts', '/accounts')] }
    ])
    await wrapper.get('[data-testid="menu-retry"]').trigger('click')
    await flushPromises()
    expect(app.menusError).toBe(false)
    expect(wrapper.find('[data-testid="menu-retry"]').exists()).toBe(false)
    expect(labels(wrapper)).toEqual(['dashboard', 'accounts'])
  })

  describe('settings view', () => {
    const link = (key: string, path: string, active = false) => ({ key, item: item(key, path), active })
    const groups: ViewGroup[] = [
      { key: 'gateway', label: '网关', links: [link('gateway', '/settings?tab=gateway', true), link('sticky', '/settings?tab=sticky')] },
      { key: 'pricing', label: '分组与定价', links: [link('groups', '/groups')] },
      { key: 'pluginSettings', label: '插件设置', links: [link('mailer', '/plugins/mailer/settings')] }
    ]
    const view = (w: Sidebar) => w.find('[data-testid="nav-view-settings"]')
    const back = (w: Sidebar) => w.get<HTMLElement>('[data-testid="nav-back"]')
    // The view's menu belongs to the settings pages; the sidebar draws what it is given.
    let useGroups: MockInstance
    beforeEach(() => {
      useGroups = vi.spyOn(SIDEBAR_VIEWS[0], 'useGroups').mockReturnValue(computed(() => groups))
    })

    it('swaps the console menu for the menu of system settings, under a way back', async () => {
      const { wrapper } = await mountSidebar(operatorMenu, '/settings')
      expect(wrapper.find('[data-testid="nav-group-admin"]').exists()).toBe(false)
      expect(labels(wrapper)).toEqual(['返回控制台', 'gateway', 'sticky', 'groups', 'mailer'])
      expect(wrapper.findAll('nav a').map(a => a.attributes('href'))).toEqual([
        '/dashboard',
        '/settings?tab=gateway',
        '/settings?tab=sticky',
        '/groups',
        '/plugins/mailer/settings'
      ])
      expect(view(wrapper).text()).toContain('网关')
      expect(view(wrapper).text()).toContain('分组与定价')
      expect(view(wrapper).text()).toContain('插件设置')
      expect(current(wrapper)).toEqual(['gateway'])
    })

    it('is chosen by the route: the pages of the view and the pages under them, not a page of the system section', async () => {
      const { wrapper, router } = await mountSidebar(operatorMenu, '/plugins/mailer/settings')
      expect(view(wrapper).exists()).toBe(true)
      for (const path of ['/settings?tab=sticky', '/groups', '/prices/sources/3/sync']) {
        await router.push(path)
        await flushPromises()
        expect([path, view(wrapper).exists()]).toEqual([path, true])
      }
      // One menu serves the whole stay in the view.
      expect(useGroups).toHaveBeenCalledTimes(1)
      await router.push('/nodes')
      await flushPromises()
      expect(view(wrapper).exists()).toBe(false)
      expect(current(wrapper)).toEqual(['nodes'])
    })

    it('is not for a caller whose menu lacks its entry, once the menu has loaded', async () => {
      let menus!: (v: unknown) => void
      api.get.mockReturnValue(new Promise(r => (menus = r)))
      const router = makeRouter()
      await router.push('/plugins/mailer/settings')
      const loading = useAppStore().loadMenus()
      const wrapper = mount(AppSidebar, { global: { plugins: [i18n, router] } })
      // Until then the route decides, so the settings view does not flash the console menu first.
      expect(view(wrapper).exists()).toBe(true)
      menus([overview, { section: 'plugins', items: [item('plugins', '/plugins'), item('market', '/market')] }])
      await loading
      await flushPromises()
      expect(view(wrapper).exists()).toBe(false)
      expect(labels(wrapper)).toEqual(['dashboard', 'plugins', 'market'])
    })

    it('leads back to the console page the view was opened from, not to an error page', async () => {
      const { wrapper, router } = await mountSidebar(operatorMenu, '/accounts?page=2')
      await router.push('/forbidden?perm=x')
      await router.push('/no/such/page')
      await router.push('/settings')
      await router.push('/plugins/mailer/settings')
      await flushPromises()
      expect(back(wrapper).attributes('href')).toBe('/accounts?page=2')
      await back(wrapper).trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.fullPath).toBe('/accounts?page=2')
      expect(current(wrapper)).toEqual(['accounts'])
    })

    it('hands the scroll position and the keyboard focus over to the menu swapped in', async () => {
      api.get.mockResolvedValue(operatorMenu)
      const router = makeRouter()
      await router.push('/accounts')
      await useAppStore().loadMenus()
      const wrapper = mount(AppSidebar, { attachTo: document.body, global: { plugins: [i18n, router], stubs: { transition: false } } })
      const nav = wrapper.get('nav')
      const scrollTo = vi.spyOn(nav.element, 'scrollTo')
      expect(nav.attributes('aria-label')).toBe('控制台菜单')
      // Opened from the page, not from the menu: the focus stays where it is.
      await router.push('/settings')
      await vi.waitFor(() => expect(view(wrapper).exists()).toBe(true))
      expect(nav.attributes('aria-label')).toBe('系统设置')
      expect(scrollTo).toHaveBeenLastCalledWith({ top: 0 })
      expect(document.activeElement).toBe(document.body)
      // Left by its own "back": the focus moves on to the console menu.
      back(wrapper).element.focus()
      await back(wrapper).trigger('click')
      await vi.waitFor(() => expect(document.activeElement).toBe(wrapper.get('nav a[href="/dashboard"]').element))
      expect(scrollTo).toHaveBeenCalledTimes(2)
    })

    it('shows icons alone when collapsed, a rule before every group', async () => {
      const { wrapper, app } = await mountSidebar(operatorMenu, '/settings')
      app.sidebarCollapsed = true
      await flushPromises()
      expect(view(wrapper).text()).toBe('')
      expect(wrapper.findAll('nav a').map(a => a.attributes('aria-label'))).toEqual(['返回控制台', 'gateway', 'sticky', 'groups', 'mailer'])
      expect(back(wrapper).attributes('title')).toBe('返回控制台')
      expect(wrapper.findAll('nav hr')).toHaveLength(3)
    })
  })
})

describe('top bar', () => {
  it('shows the parents of a nested page as breadcrumbs', async () => {
    const router = makeRouter()
    await router.push('/prices/sources/3/sync')
    const wrapper = mount(AppTopbar, { global: { plugins: [i18n, router], stubs: { ChangePasswordDialog: true } } })
    const crumbs = wrapper.get('[data-testid="breadcrumbs"]').findAll('a')
    expect(crumbs.map(a => [a.text(), a.attributes('href')])).toEqual([
      ['模型价格', '/prices'],
      ['价格同步源', '/prices/sources']
    ])
    expect(wrapper.get('h1').text()).toBe('同步价格')
    await router.push('/prices')
    await flushPromises()
    expect(wrapper.find('[data-testid="breadcrumbs"]').exists()).toBe(false)
  })

  it('does not link a parent page the caller cannot open', async () => {
    denied.add('settings:read').add('sticky:read')
    const router = makeRouter()
    await router.push('/plugins/mailer/settings')
    const wrapper = mount(AppTopbar, { global: { plugins: [i18n, router], stubs: { ChangePasswordDialog: true } } })
    const nav = wrapper.get('[data-testid="breadcrumbs"]')
    expect(nav.findAll('a')).toHaveLength(0)
    expect(nav.text()).toContain('系统设置')
  })

  it('keeps the menu button, title and account menu on a phone, language and theme inside the menu', async () => {
    const router = makeRouter()
    await router.push('/dashboard')
    const wrapper = mountPinned(AppTopbar, { global: { plugins: [i18n, router], stubs: { ChangePasswordDialog: true } } })
    expect(wrapper.findAll('header > button, header > a').map(b => b.attributes('aria-label'))).toEqual(['打开导航菜单'])
    await wrapper.get('[aria-label="账号菜单"]').trigger('click')
    await flushPromises()
    const items = Array.from(document.querySelectorAll<HTMLElement>('[role^="menuitem"]'))
    expect(items.map(i => i.textContent?.trim())).toEqual(['个人设置', '修改密码', '语言 · 中文', '深色模式', '退出登录'])
    items[2].click()
    await flushPromises()
    expect(i18n.global.locale.value).toBe('en')
  })

  it('every parent of the real routes is a page with a title', async () => {
    const { router } = await import('@/router')
    for (const r of router.getRoutes()) {
      if (!r.meta.parent) continue
      expect(router.resolve(r.meta.parent).meta.title, `${r.path} → ${r.meta.parent}`).toBeTruthy()
    }
  })
})

describe('declarative table', () => {
  const plugin = (create?: { page: string }) => ({
    key: 'volcengine',
    pages: {
      assets: { type: 'table', source: 'GET /assets', columns: [], create },
      new_asset: { type: 'form', title: { zh: '新建素材' } }
    }
  })

  it('opens the create form from a "New" button when the core sends the link', async () => {
    api.list.mockResolvedValue({ items: [], page: { total: 0 }, paged: false })
    const p = plugin({ page: 'new_asset' })
    const wrapper = mount(DeclarativeTable, {
      props: { plugin: p as never, page: p.pages.assets as never },
      global: { plugins: [i18n, makeRouter()] }
    })
    await flushPromises()
    const btn = wrapper.get('[data-testid="table-create"]')
    expect(btn.text()).toBe('新建素材')
    expect(btn.attributes('href')).toBe('/p/volcengine/new_asset')
  })

  it('has no button without the link', async () => {
    api.list.mockResolvedValue({ items: [], page: { total: 0 }, paged: false })
    const p = plugin()
    const wrapper = mount(DeclarativeTable, {
      props: { plugin: p as never, page: p.pages.assets as never },
      global: { plugins: [i18n, makeRouter()] }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="table-create"]').exists()).toBe(false)
  })
})
