import { beforeEach, describe, expect, it, vi } from 'vitest'
import { router } from './index'

const auth = vi.hoisted(() => ({ denied: [] as string[] }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), session: { get: () => 'token' } }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ me: {}, has: (perm: string | string[]) => [perm].flat().some(p => !auth.denied.includes(p)) })
}))
// The pages the navigations below end on; the routes are what is under test.
const Page = vi.hoisted(() => ({ default: { render: () => null } }))
vi.mock('@/layouts/AppLayout.vue', () => Page)
vi.mock('@/views/settings/SettingsView.vue', () => Page)
vi.mock('@/views/plugins/PluginSettingsView.vue', () => Page)
vi.mock('@/views/ccgateway/CCGatewayView.vue', () => Page)
vi.mock('@/views/errors/ForbiddenView.vue', () => Page)

/** Where a link ends up. */
async function follow(link: string): Promise<string> {
  await router.push(link)
  return router.currentRoute.value.fullPath
}

const metaOf = (path: string) => router.resolve(path).meta

beforeEach(() => {
  auth.denied = []
})

describe('where pages belong (CONTRACTS §90)', () => {
  it('opens the settings page and the plugin settings below it in the settings view', () => {
    expect(metaOf('/settings').sidebar).toBe('settings')
    expect(metaOf('/plugins/mailer/settings')).toMatchObject({ parent: '/settings' })
  })

  it('puts roles below users and leaves the feature pages of the sidebar without a parent', () => {
    expect(metaOf('/roles').parent).toBe('/users')
    for (const path of [
      '/proxies',
      '/platforms',
      '/plugins',
      '/market',
      '/publishers',
      '/nodes',
      '/system/tasks',
      '/system/upgrades',
      '/system/audit',
      '/ccgateway'
    ]) {
      expect([path, metaOf(path).parent, metaOf(path).sidebar]).toEqual([path, undefined, undefined])
    }
    expect(metaOf('/plugins/mailer').parent).toBe('/plugins')
  })
})

describe('the CCGateway page', () => {
  it('has an address of its own, for settings:read', async () => {
    expect(metaOf('/ccgateway')).toMatchObject({ perm: 'settings:read', title: 'ccgateway.title' })
    expect(await follow('/ccgateway')).toBe('/ccgateway')
  })

  it('takes over its old addresses, keeping the anchor', async () => {
    expect(await follow('/system/ccgateway')).toBe('/ccgateway')
    expect(await follow('/plugins/ccgateway/settings#ccgateway-runtime')).toBe('/ccgateway#ccgateway-runtime')
    expect(await follow('/settings?tab=plugin:ccgateway')).toBe('/ccgateway')
  })
})

describe('old links to plugin settings as a tab of the settings page', () => {
  it("lead to the plugin's settings page", async () => {
    expect(await follow('/settings?tab=plugin:mailer')).toBe('/plugins/mailer/settings')
  })

  it('also from the settings page itself, where only the query changes', async () => {
    expect(await follow('/settings?tab=billing')).toBe('/settings?tab=billing')
    expect(await follow('/settings?tab=plugin:mailer')).toBe('/plugins/mailer/settings')
  })

  it("do not ask for the settings page's permissions: the core checks the plugin's", async () => {
    auth.denied = ['settings:read', 'sticky:read']
    expect(await follow('/settings?tab=plugin:mailer')).toBe('/plugins/mailer/settings')
    expect(await follow('/settings?tab=billing')).toMatch(/^\/forbidden/)
  })

  it('leave the configuration pages where they are', async () => {
    expect(await follow('/settings?tab=sticky')).toBe('/settings?tab=sticky')
    expect(await follow('/sticky')).toBe('/settings?tab=sticky')
  })
})
