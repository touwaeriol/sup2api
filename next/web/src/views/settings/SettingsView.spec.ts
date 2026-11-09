import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, reactive } from 'vue'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { hasIcon } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { ViewGroup } from '@/layouts/sidebarViews'
import { useAppStore, type NavSection } from '@/stores/app'
import SettingsView from './SettingsView.vue'
import { emptyFailurePolicy } from './failurePolicy'
import { useSettingsMenu, useSettingsNav } from './settingsNav'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), list: vi.fn() }))
const permissions = vi.hoisted(() => ({ manage: true, denied: [] as string[] }))
const route = reactive<{ path: string; meta: object; query: { tab?: string; sub?: string } }>({
  path: '/settings',
  meta: {},
  query: { tab: 'billing' }
})
const router = { replace: vi.fn(), push: vi.fn() }
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

const systemMenu = (): NavSection[] => [
  { key: 'system', items: [{ id: 'settings', labelKey: 'nav.items.settings', icon: 'settings', path: '/settings' }] }
]

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
  it('lists eight pages in three groups', () => {
    const { groups } = setupOf(useSettingsNav)
    expect(shape(groups.value)).toEqual([
      ['网关', ['gateway', 'failure', 'sticky', 'offload']],
      ['计费与通知', ['billing', 'notifications']],
      ['运行环境', ['updates', 'environment']]
    ])
    expect(groups.value[0].links[2]).toEqual({
      key: 'sticky',
      item: { id: 'settings:sticky', labelKey: 'settings.tabs.sticky', icon: 'sticky', path: '/settings?tab=sticky' },
      active: false
    })
  })

  it('gives every page an icon of its own that SIcon can draw', () => {
    const icons = setupOf(useSettingsNav).groups.value.flatMap(g => g.links.map(l => l.item.icon!))
    expect(icons.filter(hasIcon)).toHaveLength(8)
    expect(new Set(icons).size).toBe(8)
  })

  it('leaves out the pages the caller may not open, and the groups left empty', () => {
    permissions.denied = ['settings:read']
    const { groups, activeTab, missing } = setupOf(useSettingsNav)
    expect(shape(groups.value)).toEqual([['网关', ['sticky']]])
    // The linked page (billing) is not the caller's to see.
    expect(activeTab.value).toBe('sticky')
    expect(missing.value).toBe(true)
  })

  it('opens the page asked for, else the first one', () => {
    const { activeTab, activeLabel, missing } = setupOf(useSettingsNav)
    expect([activeTab.value, activeLabel.value, missing.value]).toEqual(['billing', '计费', false])
    route.query = {}
    expect([activeTab.value, activeLabel.value, missing.value]).toEqual(['gateway', '网关设置', false])
    route.query = { tab: 'plugin:mailer' }
    expect([activeTab.value, missing.value]).toEqual(['gateway', true])
  })

  it('lights up the open page only on the settings page', () => {
    const { groups } = setupOf(useSettingsNav)
    expect(activeKeys(groups.value)).toEqual(['billing'])
    route.path = '/plugins/mailer/settings'
    expect(activeKeys(groups.value)).toEqual([])
  })
})

describe('useSettingsMenu', () => {
  it('puts the settings of enabled plugins with a form between billing and runtime', async () => {
    servePlugins(() => [
      plugin('mailer', '邮件'),
      plugin('guard', '请求守卫', { status: 'disabled' }),
      // Native settings live on the plugin's own page: the server reports no form.
      plugin('moderation', '提示词审核', { has_settings: false }),
      // Has an entry of its own in the sidebar (/ccgateway).
      plugin('ccgateway', 'Claude Code')
    ])
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(shape(groups.value)).toEqual([
      ['网关', ['gateway', 'failure', 'sticky', 'offload']],
      ['计费与通知', ['billing', 'notifications']],
      ['插件设置', ['plugin:mailer']],
      ['运行环境', ['updates', 'environment']]
    ])
    expect(groups.value[2].links[0]).toEqual({
      key: 'plugin:mailer',
      item: { id: 'plugin-settings:mailer', label: '邮件', icon: 'puzzle', path: '/plugins/mailer/settings', pluginKey: 'mailer' },
      active: false
    })
  })

  it("lights up a plugin's entry on that plugin's settings page, and nothing else", async () => {
    servePlugins(() => [plugin('mailer', '邮件'), plugin('growth', '增长管理')])
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(activeKeys(groups.value)).toEqual(['billing'])
    route.path = '/plugins/mailer/settings'
    route.query = {}
    expect(activeKeys(groups.value)).toEqual(['plugin:mailer'])
  })

  it('follows plugins enabled while it is open, not the first menu load', async () => {
    let installed: object[] = []
    servePlugins(() => installed)
    const groups = setupOf(useSettingsMenu)
    const app = useAppStore()
    app.menus = systemMenu()
    await flushPromises()
    expect(pluginRequests()).toHaveLength(1)
    expect(shape(groups.value).map(([label]) => label)).toEqual(['网关', '计费与通知', '运行环境'])
    // Enabling a plugin refreshes the menu.
    installed = [plugin('mailer', '邮件')]
    app.menus = systemMenu()
    await flushPromises()
    expect(pluginRequests()).toHaveLength(2)
    expect(groups.value.map(g => g.key)).toEqual(['gateway', 'billing', 'pluginSettings', 'runtime'])
  })

  it("has no plugin group when the list fails or is not the caller's to read", async () => {
    api.list.mockRejectedValue(new Error('down'))
    const failed = setupOf(useSettingsMenu)
    await flushPromises()
    expect(failed.value.map(g => g.key)).toEqual(['gateway', 'billing', 'runtime'])

    api.list.mockClear()
    permissions.denied = ['plugin:read']
    const denied = setupOf(useSettingsMenu)
    await flushPromises()
    expect(pluginRequests()).toHaveLength(0)
    expect(denied.value.map(g => g.key)).toEqual(['gateway', 'billing', 'runtime'])
  })

  it('keeps the plugin group for a caller with no runtime page', async () => {
    permissions.denied = ['settings:read']
    servePlugins(() => [plugin('mailer', '邮件')])
    const groups = setupOf(useSettingsMenu)
    await flushPromises()
    expect(groups.value.map(g => g.key)).toEqual(['gateway', 'pluginSettings'])
  })
})
