import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { i18n } from '@/i18n'
import { CCGATEWAY_PAGE } from '@/views/plugins/pluginUtil'
import { useSettingsMenu } from '@/views/settings/settingsNav'
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
vi.mock('@/views/groups/GroupsView.vue', () => Page)
vi.mock('@/views/prices/PricesView.vue', () => Page)
vi.mock('@/views/accounts/AccountsView.vue', () => Page)
vi.mock('@/views/plugins/PluginSettingsView.vue', () => Page)
vi.mock('@/views/ccgateway/CCGatewayView.vue', () => Page)
vi.mock('@/views/errors/ForbiddenView.vue', () => Page)

/** Where a link ends up. */
async function follow(link: string): Promise<string> {
  await router.push(link)
  return router.currentRoute.value.fullPath
}

const metaOf = (path: string) => router.resolve(path).meta

/** The sidebar view a page opens in: the one its route declares, else that of the page above it. */
function viewOf(path: string): string | undefined {
  const { sidebar, parent } = metaOf(path)
  return sidebar ?? (parent ? viewOf(parent) : undefined)
}

beforeEach(() => {
  auth.denied = []
})

describe('where pages belong (CONTRACTS §90)', () => {
  it('opens every page the settings menu lists, and the pages below them, in the settings view', () => {
    let menu!: ReturnType<typeof useSettingsMenu>
    mount(defineComponent({ setup: () => ((menu = useSettingsMenu()), () => null) }), { global: { plugins: [i18n, router] } })
    const listed = menu.value.flatMap(g => g.links.map(l => l.item.path))
    expect(listed).toEqual(expect.arrayContaining(['/settings?tab=sticky', '/groups', '/prices']))
    for (const path of [...listed, '/prices/new', '/prices/sources/3/sync']) expect([path, viewOf(path)]).toEqual([path, 'settings'])
  })

  it('opens plugin settings in the console view, under the installed plugins', () => {
    for (const path of [CCGATEWAY_PAGE, '/plugins/mailer/settings']) {
      expect([path, metaOf(path).parent, viewOf(path)]).toEqual([path, '/plugins', undefined])
    }
  })

  it('declares the view once, on the settings page: the others join it as pages below', () => {
    const declaring = router.getRoutes().filter(r => r.meta.sidebar)
    expect(declaring.map(r => r.path)).toEqual(['/settings'])
  })

  it('leaves the feature pages of the sidebar without a parent', () => {
    for (const path of [
      '/roles',
      '/proxies',
      '/plugins',
      '/market',
      '/publishers',
      '/nodes',
      '/system/tasks',
      '/system/upgrades',
      '/system/audit'
    ]) {
      expect([path, metaOf(path).parent, metaOf(path).sidebar]).toEqual([path, undefined, undefined])
    }
    expect(metaOf('/plugins/mailer').parent).toBe('/plugins')
  })

  it('puts the balance ledger under the users list it is opened from', () => {
    expect(metaOf('/ledger')).toMatchObject({ perm: 'balance:all:read', parent: '/users' })
    expect(metaOf('/ledger').sidebar).toBeUndefined()
  })

  it('sends the old platforms page to the accounts', async () => {
    expect(await follow('/platforms')).toBe('/accounts')
  })
})

describe('the settings entry', () => {
  // From another page: a link to the open page is no navigation.
  beforeEach(() => follow('/forbidden'))

  it('opens the settings page for a caller with a configuration page', async () => {
    auth.denied = ['settings:read']
    expect(await follow('/settings')).toBe('/settings')
  })

  it('leads a caller without one to the first page of the view that is theirs', async () => {
    auth.denied = ['settings:read', 'sticky:read']
    expect(await follow('/settings')).toBe('/groups')
    auth.denied.push('group:read')
    expect(await follow('/settings')).toBe('/prices')
  })

  it('is refused without any page of the view, as is a link to a configuration page', async () => {
    auth.denied = ['settings:read', 'sticky:read']
    expect(await follow('/settings?tab=billing')).toMatch(/^\/forbidden\?perm=settings:read/)
    auth.denied.push('group:read', 'price:read')
    expect(await follow('/settings')).toMatch(/^\/forbidden\?perm=settings:read/)
  })
})

describe('the CCGateway page', () => {
  it('has an address of its own, for settings:read', async () => {
    expect(metaOf(CCGATEWAY_PAGE)).toMatchObject({ perm: 'settings:read', title: 'ccgateway.title' })
    expect(await follow(CCGATEWAY_PAGE)).toBe(CCGATEWAY_PAGE)
  })

  it('is not for a caller without settings:read', async () => {
    auth.denied = ['settings:read']
    // From another page: a link to the open page is no navigation.
    await follow('/forbidden')
    expect(await follow(CCGATEWAY_PAGE)).toBe('/forbidden?perm=settings:read')
  })

  it("takes over the plugin's settings addresses, keeping the anchor", async () => {
    expect(await follow('/plugins/ccgateway/settings#ccgateway-runtime')).toBe(`${CCGATEWAY_PAGE}#ccgateway-runtime`)
    expect(await follow('/settings?tab=plugin:ccgateway#ccgateway-runtime')).toBe(`${CCGATEWAY_PAGE}#ccgateway-runtime`)
  })
})

describe('old links to plugin settings as a tab of the settings page', () => {
  it("lead to the plugin's settings page, keeping the anchor", async () => {
    expect(await follow('/settings?tab=plugin:mailer')).toBe('/plugins/mailer/settings')
    expect(await follow('/settings?tab=plugin:mailer#smtp')).toBe('/plugins/mailer/settings#smtp')
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

  it('stay on the settings page, which reports the page missing, when the key is not a plugin key', async () => {
    for (const link of ['/settings?tab=plugin:..', '/settings?tab=plugin:', '/settings?tab=plugin:a/b', '/settings?tab=plugin:Mailer']) {
      expect(await follow(link)).toBe(link)
    }
  })

  it('leave the configuration pages where they are', async () => {
    expect(await follow('/settings?tab=sticky')).toBe('/settings?tab=sticky')
    expect(await follow('/sticky')).toBe('/settings?tab=sticky')
  })
})
