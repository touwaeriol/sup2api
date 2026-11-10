import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, reactive } from 'vue'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { hasIcon } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { ViewGroup } from '@/layouts/sidebarViews'
import { CCGATEWAY_PAGE } from '@/views/plugins/pluginUtil'
import SettingsView from './SettingsView.vue'
import { emptyFailurePolicy } from './failurePolicy'
import { settingsLanding, useSettingsMenu, useSettingsNav } from './settingsNav'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), list: vi.fn() }))
const permissions = vi.hoisted(() => ({ manage: true, denied: [] as string[] }))
const route = reactive<{ path: string; meta: { parent?: string }; query: { tab?: string; sub?: string } }>({
  path: '/settings',
  meta: {},
  query: { tab: 'billing' }
})
/** The pages above a page (meta.parent), as the real routes declare them. */
const PARENTS: Record<string, string> = { '/prices/sources': '/prices' }
const router = { replace: vi.fn(), push: vi.fn(), resolve: (path: string) => ({ meta: { parent: PARENTS[path] } }) }
vi.mock('vue-router', async original => ({ ...(await original<object>()), useRoute: () => route, useRouter: () => router }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), api }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ has: (key: string) => !permissions.denied.includes(key) && (permissions.manage || key.endsWith(':read')) })
}))
enableAutoUnmount(afterEach)

let pinia: Pinia

const mountSettings = () => mount(SettingsView, { global: { plugins: [i18n, pinia] } })

/** What a composable returns inside a mounted component, as the sidebar and the settings page call it. */
function setupOf<T>(use: () => T): T {
  let result!: T
  mount(defineComponent({ setup: () => ((result = use()), () => null) }), { global: { plugins: [i18n, pinia] } })
  return result
}

/** Groups by header, as the keys of their links. */
const shape = (groups: ViewGroup[]) => groups.map(g => [g.label, g.links.map(l => l.key)])
const activeKeys = (groups: ViewGroup[]) => groups.flatMap(g => g.links.filter(l => l.active).map(l => l.key))
const pluginGroup = (groups: ViewGroup[]) => groups.find(g => g.key === 'pluginSettings')?.links ?? []

const plugin = (key: string, name: string, more: object = {}) => ({
  key,
  name: { zh: name },
  status: 'enabled',
  has_settings: true,
  ...more
})
/** Answers GET /plugins with whatever `installed` holds at the time. */
function servePlugins(installed: () => object[]) {
  api.list.mockImplementation(path => Promise.resolve({ items: path === '/plugins' ? installed() : [], total: 0 }))
}
const pluginRequests = () => api.list.mock.calls.filter(([path]) => path === '/plugins')

beforeEach(() => {
  vi.clearAllMocks()
  permissions.manage = true
  permissions.denied = []
  route.path = '/settings'
  route.meta = {}
  route.query = { tab: 'billing' }
  pinia = createPinia()
  setActivePinia(pinia)
  i18n.global.locale.value = 'zh'
  api.list.mockResolvedValue({ items: [], total: 0 })
  api.put.mockImplementation((_path, body) => Promise.resolve(body))
  api.get.mockImplementation(path => Promise.resolve({
    '/settings/billing': { pre_consume_tokens: 500, missing_price_policy: 'reject', min_balance: '0', big_cost_warning_usd: '10' },
    '/settings/gateway': { max_attempts: 3, platform_call_timeout_ms: 30000, default_hook_timeout_ms: 30000, platform_hotpath_timeout_ms: 30000 },
    '/settings/sticky': { enabled: true, default_ttl_seconds: 3600, keep_on_account_disabled: false },
    '/sticky-rules/stats': [],
    '/system/offload': { enabled: false, cpu_threshold_percent: 90, nodes: [] },
    '/system/update-source': { repository: '' },
    '/settings/failure-policy': { configured: true, policy: emptyFailurePolicy() }
  }[path as string] ?? {}))
})

describe('the settings page', () => {
  it('names the open page once: the panel carries its title, no heading repeats it', async () => {
    const titles = {
      billing: '计费',
      gateway: '网关设置',
      failure: '调度策略',
      sticky: '粘性会话',
      offload: 'CPU 保护',
      updates: '在线更新'
    }
    const wrapper = mountSettings()
    for (const [tab, title] of Object.entries(titles)) {
      route.query.tab = tab
      await flushPromises()
      const panel = wrapper.get('[data-testid="settings-panel"]')
      expect(panel.attributes('aria-label')).toBe(title)
      expect(panel.findAll('h1,h2,h3,h4,h5,h6').map(el => el.text())).not.toContain(title)
      if (tab === 'failure') expect(panel.findAll('h5').map(el => el.text())).toEqual(['自动重试', '自动禁用'])
    }
  })

  it('shows the first page without a tab, and says so when the linked one is not available', async () => {
    route.query = {}
    const wrapper = mountSettings()
    await flushPromises()
    expect(wrapper.get('[data-testid="settings-panel"]').attributes('aria-label')).toBe('网关设置')
    expect(wrapper.find('[data-testid="settings-missing"]').exists()).toBe(false)
    route.query = { tab: 'gone' }
    await flushPromises()
    expect(wrapper.get('[data-testid="settings-panel"]').attributes('aria-label')).toBe('网关设置')
    expect(wrapper.find('[data-testid="settings-missing"]').exists()).toBe(true)
  })

  it('makes no request for plugins: their settings open at their own address', async () => {
    mountSettings()
    await flushPromises()
    expect(pluginRequests()).toHaveLength(0)
  })

  it('retains saving and removes the empty billing header for read-only users', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="billing-settings"] input[type="number"]').setValue('501')
    await wrapper.get('[data-testid="settings-save"]').trigger('click')
    await flushPromises()
    expect(api.put).toHaveBeenCalledWith('/settings/billing', expect.objectContaining({ pre_consume_tokens: 501 }))
    wrapper.unmount()
    permissions.manage = false
    const readonly = mountSettings()
    await flushPromises()
    expect(readonly.find('[data-testid="billing-settings"] header').exists()).toBe(false)
    expect(readonly.find('[data-testid="settings-save"]').exists()).toBe(false)
  })
})

describe('useSettingsNav', () => {
  it('knows the eight configuration pages', () => {
    const { pages } = setupOf(useSettingsNav)
    expect(pages.value.map(p => p.tab)).toEqual([
      'gateway',
      'failure',
      'sticky',
      'offload',
      'billing',
      'notifications',
      'updates',
      'environment'
    ])
  })

  it('leaves out the pages the caller may not open', () => {
    permissions.denied = ['settings:read']
    const { pages, activeTab, missing } = setupOf(useSettingsNav)
    expect(pages.value.map(p => p.tab)).toEqual(['sticky'])
    // The linked page (billing) is not the caller's to see.
    expect(activeTab.value).toBe('sticky')
    expect(missing.value).toBe(true)
  })

  it('opens the page asked for, else the first one', () => {
    const { activeTab, activeLabel, missing } = setupOf(useSettingsNav)
    expect([activeTab.value, activeLabel.value, missing.value]).toEqual(['billing', '计费', false])
    route.query = {}
    expect([activeTab.value, activeLabel.value, missing.value]).toEqual(['gateway', '网关设置', false])
    route.query = { tab: 'plugin:..' }
    expect([activeTab.value, missing.value]).toEqual(['gateway', true])
  })
})

describe('useSettingsMenu', () => {
  it('lists the eight configuration pages, groups and model prices in four groups', async () => {
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(shape(groups.value)).toEqual([
      ['网关', ['gateway', 'failure', 'sticky', 'offload']],
      ['分组与定价', ['/groups', '/prices']],
      ['计费与通知', ['billing', 'notifications']],
      ['运行环境', ['updates', 'environment']]
    ])
    expect(groups.value[0].links[2]).toEqual({
      key: 'sticky',
      item: { id: 'settings:sticky', labelKey: 'settings.tabs.sticky', icon: 'sticky', path: '/settings?tab=sticky' },
      active: false
    })
    // A page with an address of its own keeps it.
    expect(groups.value[1].links[1]).toEqual({
      key: '/prices',
      item: { id: 'settings:/prices', labelKey: 'nav.items.prices', icon: 'price', path: '/prices' },
      active: false
    })
  })

  it('gives every page an icon of its own that SIcon can draw', async () => {
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    const icons = groups.value.flatMap(g => g.links.map(l => l.item.icon!))
    expect(icons.filter(hasIcon)).toHaveLength(10)
    expect(new Set(icons).size).toBe(10)
  })

  it('puts the settings of the enabled plugins that have a page between billing and runtime', async () => {
    servePlugins(() => [
      plugin('mailer', '邮件'),
      plugin('guard', '请求守卫', { status: 'disabled' }),
      // Native settings live on the plugin's own page: the server reports no form.
      plugin('moderation', '提示词审核', { has_settings: false }),
      // Its settings are a core page, whatever its manifest says.
      plugin('ccgateway', 'Claude Code', { has_settings: false })
    ])
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(shape(groups.value)).toEqual([
      ['网关', ['gateway', 'failure', 'sticky', 'offload']],
      ['分组与定价', ['/groups', '/prices']],
      ['计费与通知', ['billing', 'notifications']],
      ['插件设置', ['plugin:mailer', 'plugin:ccgateway']],
      ['运行环境', ['updates', 'environment']]
    ])
    expect(pluginGroup(groups.value)).toEqual([
      {
        key: 'plugin:mailer',
        item: { id: 'plugin-settings:mailer', label: '邮件', icon: 'puzzle', path: '/plugins/mailer/settings', pluginKey: 'mailer' },
        active: false
      },
      {
        key: 'plugin:ccgateway',
        item: { id: 'plugin-settings:ccgateway', label: 'Claude Code', icon: 'puzzle', path: CCGATEWAY_PAGE, pluginKey: 'ccgateway' },
        active: false
      }
    ])
  })

  it('leaves out the pages the caller may not open and the groups left empty', async () => {
    permissions.denied = ['settings:read']
    // The form of a plugin is the core's to guard; the CCGateway page needs settings:read.
    servePlugins(() => [plugin('mailer', '邮件'), plugin('ccgateway', 'Claude Code')])
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(shape(groups.value)).toEqual([
      ['网关', ['sticky']],
      ['分组与定价', ['/groups', '/prices']],
      ['插件设置', ['plugin:mailer']]
    ])
  })

  it('shows a caller with no configuration page, as the built-in operator, the pages that are theirs', async () => {
    permissions.denied = ['settings:read', 'sticky:read', 'plugin:read']
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(shape(groups.value)).toEqual([['分组与定价', ['/groups', '/prices']]])
    // The settings entry opens the first of them.
    expect(settingsLanding()).toBe('/groups')
    permissions.denied.push('group:read')
    expect(settingsLanding()).toBe('/prices')
  })

  it('lights up the open configuration page on the settings page only', async () => {
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(activeKeys(groups.value)).toEqual(['billing'])
    route.path = '/nodes'
    expect(activeKeys(groups.value)).toEqual([])
  })

  it('lights up a page with an address of its own on that page and on the pages below it', async () => {
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    route.query = {}
    route.path = '/groups'
    expect(activeKeys(groups.value)).toEqual(['/groups'])
    route.path = '/prices/sources/3/sync'
    route.meta = { parent: '/prices/sources' }
    expect(activeKeys(groups.value)).toEqual(['/prices'])
  })

  it("lights up a plugin's entry on that plugin's settings page, and nothing else", async () => {
    servePlugins(() => [plugin('mailer', '邮件'), plugin('growth', '增长管理'), plugin('ccgateway', 'Claude Code')])
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(activeKeys(groups.value)).toEqual(['billing'])
    route.path = '/plugins/mailer/settings'
    route.meta = { parent: '/settings' }
    route.query = {}
    expect(activeKeys(groups.value)).toEqual(['plugin:mailer'])
    route.path = CCGATEWAY_PAGE
    route.meta = {}
    expect(activeKeys(groups.value)).toEqual(['plugin:ccgateway'])
  })

  it('draws the plugins of the last visit at once and refreshes them behind', async () => {
    servePlugins(() => [plugin('mailer', '邮件')])
    setupOf(useSettingsMenu)
    await flushPromises()

    let answer!: (list: object) => void
    api.list.mockImplementation(() => new Promise(resolve => (answer = resolve)))
    const again = setupOf(useSettingsMenu)
    expect(again.value.map(g => g.key)).toEqual(['gateway', 'pricing', 'billing', 'pluginSettings', 'runtime'])
    answer({ items: [plugin('mailer', '邮件'), plugin('growth', '增长管理')], total: 2 })
    await flushPromises()
    expect(pluginGroup(again.value).map(l => l.key)).toEqual(['plugin:mailer', 'plugin:growth'])
    expect(pluginRequests()).toHaveLength(2)
  })

  it('drops a late answer to an earlier visit', async () => {
    const answers: Array<(list: object) => void> = []
    api.list.mockImplementation(() => new Promise(resolve => answers.push(resolve)))
    setupOf(useSettingsMenu)
    const groups = setupOf(useSettingsMenu)
    answers[1]({ items: [plugin('growth', '增长管理')], total: 1 })
    answers[0]({ items: [plugin('mailer', '邮件')], total: 1 })
    await flushPromises()
    expect(pluginGroup(groups.value).map(l => l.key)).toEqual(['plugin:growth'])
  })

  it('has no plugin group when the list fails', async () => {
    api.list.mockRejectedValue(new Error('down'))
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(groups.value.map(g => g.key)).toEqual(['gateway', 'pricing', 'billing', 'runtime'])
  })

  it('forgets the plugins shown to an earlier user who could list them', async () => {
    servePlugins(() => [plugin('mailer', '邮件')])
    setupOf(useSettingsMenu)
    await flushPromises()

    api.list.mockClear()
    permissions.denied = ['plugin:read']
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(pluginRequests()).toHaveLength(0)
    expect(groups.value.map(g => g.key)).toEqual(['gateway', 'pricing', 'billing', 'runtime'])
  })
})
