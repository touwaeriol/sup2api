import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { i18n } from '@/i18n'
import PluginDetailView from './PluginDetailView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), confirm: vi.fn(async () => false) }))
vi.mock('@sub2api/host', async (original) => ({ ...await original<typeof import('@sub2api/host')>(), api: { get: mocks.get } }))
vi.mock('@sub2api/ui', async (original) => ({ ...await original<object>(), confirm: mocks.confirm }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ me: { roles: [], superuser: true, permissions: [] }, has: () => true }) }))
enableAutoUnmount(afterEach)

// The real permissions tab, whose matrix has one cell to edit: its leave guards are its own.
const READ = 'plugin.guard:rules:read'
const matrixOf = (key: string) => ({
  plugin_key: key,
  module: { module: `plugin.${key}`, label: key, source: 'plugin', plugin_key: key, status: 'active', permissions: [{ key: `plugin.${key}:rules:read`, label: 'Read', risk: 'low', status: 'active' }] },
  roles: [{ id: 2, key: 'admin', name: 'Admin', builtin: true, superuser: false, editable: true, permission_keys: [] }]
})

async function render(query = '', extra: Record<string, unknown> = {}, settings: Record<string, unknown> = { mode: 'none', values: {} }) {
  mocks.get.mockImplementation(async (path: string) => {
    if (path.endsWith('/settings')) return settings
    if (path === '/plugins/guard/database') return DATABASE
    const matrix = path.match(/^\/permissions\/plugins\/(\w+)$/)?.[1]
    if (matrix) return matrixOf(matrix)
    const key = path.match(/^\/plugins\/(\w+)$/)?.[1]
    return key ? { key, display_id: key, name: key, status: 'enabled', grants: [], versions: [], ...extra } : []
  })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/plugins/:key', component: PluginDetailView },
      { path: '/plugins', component: { template: '<div />' } },
      { path: '/p/:plugin/:page', component: { template: '<div />' } }
    ]
  })
  await router.push('/plugins/guard' + query)
  const w = mount({ template: '<router-view />' }, { global: { plugins: [i18n, router], stubs: { OverviewTab: true, NodesTab: true, SettingsTab: { props: ['pluginKey', 'settings'], template: '<div data-testid="settings-tab">{{ settings.component }}</div>' } } } })
  await flushPromises()
  return { w, router }
}
const DATABASE = {
  prefix: 'plg_guard__',
  role: 'plg713988_guard',
  access: [{ table: 'users', columns: ['id', 'email'], privileges: ['select'] }, { table: 'usage_logs', privileges: ['select', 'update'] }],
  migrations: [
    { id: '0001_init.sql', checksum: 'a', sql: 'CREATE TABLE plg_guard__rules (id bigint);', applied_at: '2026-11-18T08:00:00Z' },
    { id: '0002_stats.sql', checksum: 'b', sql: 'CREATE TABLE plg_guard__stats (day date);', applied_at: null }
  ]
}
/** Edits the permissions tab's matrix: it now holds unsaved edits. */
const edit = (w: Awaited<ReturnType<typeof render>>['w']) => w.get(`tr[data-permission="${READ}"] td[data-role="admin"] input`).setValue(true)
const permTab = (w: Awaited<ReturnType<typeof render>>['w']) => w.find('[data-testid="plugin-user-permissions"]')
const tabButton = (w: Awaited<ReturnType<typeof render>>['w'], label: string) => w.findAll('[role="tab"]').find((b) => b.text().startsWith(label))!

describe('plugin detail view', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'en'
  })

  it('?tab=grants opens the permissions tab', async () => {
    const { w, router } = await render('?tab=grants')
    expect(permTab(w).exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBe('permissions')
  })

  it('switching tabs with unsaved matrix asks first', async () => {
    const { w, router } = await render('?tab=permissions')
    await edit(w)

    // Staying keeps the tab and its edits.
    await tabButton(w, 'Nodes').trigger('click')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
    expect(permTab(w).exists()).toBe(true)

    // So does leaving the page.
    await router.push('/plugins')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(2)
    expect(router.currentRoute.value.path).toBe('/plugins/guard')

    mocks.confirm.mockResolvedValueOnce(true)
    await tabButton(w, 'Nodes').trigger('click')
    await flushPromises()
    expect(permTab(w).exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('nodes')
  })

  it('a ?tab= change from a link or the history asks first too', async () => {
    const { w, router } = await render('?tab=permissions')
    await edit(w)
    await router.push('/plugins/guard?tab=nodes')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.query.tab).toBe('permissions')
    expect(permTab(w).exists()).toBe(true)

    // Other query changes keep the tab without asking.
    await router.push('/plugins/guard?tab=permissions&consented=0.2.0')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
  })

  it('opening another plugin on the same page asks first, and drops the edits once given up', async () => {
    const { w, router } = await render('?tab=permissions')
    await edit(w)
    await router.push('/plugins/other?tab=permissions')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.path).toBe('/plugins/guard')

    mocks.confirm.mockResolvedValueOnce(true)
    await router.push('/plugins/other?tab=permissions')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/plugins/other')
    // Given up: leaving again does not ask.
    await router.push('/plugins')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(2)
    expect(router.currentRoute.value.path).toBe('/plugins')
  })

  it('names the first core of the plugin protocol when the nodes refuse the plugin', async () => {
    const refusal = { code: 'outdated_contract', version: '0.1.0', host_version: '0.9.4', protocol_version: 2, first_core_version: '0.9.0', message: 'runtime text' }
    const { w } = await render('', { refusal })
    expect(w.get('[data-testid="plugin-refusal"]').text()).toBe(
      'Plugin guard@0.1.0 was built for an older plugin protocol and cannot be loaded by this core (0.9.4, plugin protocol 2). Install a version built for core 0.9.0 or later.'
    )
  })

  // has_settings: native ui.settings (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY §6.3).
  it('shows native settings without a page on the settings tab', async () => {
    const { w } = await render('?tab=settings', {}, { mode: 'native', component: 'MailerSettings', values: {} })
    expect(tabButton(w, 'Settings')).toBeDefined()
    expect(w.get('[data-testid="settings-tab"]').text()).toBe('MailerSettings')
  })

  it('sends ?tab=settings to the plugin page that edits the settings', async () => {
    const { w, router } = await render('?tab=settings', {}, { mode: 'native', page: 'dashboard', values: {} })
    expect(router.currentRoute.value.fullPath).toBe('/p/guard/dashboard?tab=settings')
    expect(w.find('[data-testid="settings-tab"]').exists()).toBe(false)
  })

  it('shows the database tab only for a plugin with a database', async () => {
    const { w, router } = await render('?tab=database')
    expect(w.findAll('[role="tab"]').some((b) => b.text().startsWith('Database'))).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('overview')
    expect(mocks.get).not.toHaveBeenCalledWith('/plugins/guard/database')
  })

  it('shows the table prefix, database identity, tables outside its own and the scripts with when each ran', async () => {
    const { w } = await render('?tab=database', { manifest: { database: { migrations: ['0001_init.sql', '0002_stats.sql'] } } })
    const tab = w.get('[data-testid="plugin-database"]')
    expect(tab.text()).toContain('plg_guard__')
    expect(tab.text()).toContain('plg713988_guard')
    expect(tab.get('[data-table="users"]').text()).toContain('Columns: id, email')
    expect(tab.get('[data-table="usage_logs"]').text()).toContain('all columns')
    expect(tab.get('[data-testid="database-access-warn"]').text()).toBe('Writing core tables directly bypasses the core\'s caches, permission versions and audit.')
    expect(tab.get('[data-migration="0001_init.sql"]').text()).toContain('Ran ')
    expect(tab.get('[data-migration="0002_stats.sql"]').text()).toContain('Not run')
    await tab.get('[data-migration="0002_stats.sql"] button').trigger('click')
    expect(tab.get('[data-migration="0002_stats.sql"] [data-testid="migration-sql"]').text()).toBe('CREATE TABLE plg_guard__stats (day date);')
  })

  it('has no settings tab for a plugin without settings', async () => {
    const { w, router } = await render('?tab=settings')
    expect(w.findAll('[role="tab"]').some((b) => b.text().startsWith('Settings'))).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('overview')
  })
})
