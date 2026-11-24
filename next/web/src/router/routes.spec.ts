import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { i18n } from '@/i18n'
import { useSettingsMenu } from '@/views/settings/settingsNav'
import { router } from './index'

const auth = vi.hoisted(() => ({ denied: [] as string[], me: {} as object | null, loadMe: vi.fn() }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), session: { get: () => 'token' } }))
vi.mock('@/stores/auth', async () => {
  const { deniedAuth } = await import('@/testing/authMock')
  return { useAuthStore: () => ({ me: auth.me, loadMe: auth.loadMe, ...deniedAuth(() => auth.denied) }) }
})
// The pages the navigations below end on; the routes are what is under test.
const Page = vi.hoisted(() => ({ default: { render: () => null } }))
vi.mock('@/layouts/AppLayout.vue', () => Page)
vi.mock('@/views/settings/SettingsView.vue', () => Page)
vi.mock('@/views/groups/GroupsView.vue', () => Page)
vi.mock('@/views/prices/PricesView.vue', () => Page)
vi.mock('@/views/accounts/AccountsView.vue', () => Page)
vi.mock('@/views/proxies/ProxiesView.vue', () => Page)
vi.mock('@/views/plugins/PluginSettingsView.vue', () => Page)
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
  auth.me = {}
})

describe('where pages belong (CONTRACTS §90)', () => {
  it('opens every page the settings menu lists, and the pages below them, in the settings view', () => {
    let menu!: ReturnType<typeof useSettingsMenu>
    mount(defineComponent({ setup: () => ((menu = useSettingsMenu()), () => null) }), { global: { plugins: [i18n, router] } })
    const listed = menu.value.flatMap(g => g.links.map(l => l.item.path))
    expect(listed).toEqual(expect.arrayContaining(['/settings?tab=sticky', '/prices']))
    for (const path of [...listed, '/prices/new', '/prices/sources/3/sync']) expect([path, viewOf(path)]).toEqual([path, 'settings'])
  })

  it('opens groups in the console view, beside the accounts (CONTRACTS §132)', () => {
    expect([metaOf('/groups').parent, viewOf('/groups')]).toEqual([undefined, undefined])
  })

  it('opens plugin settings in the console view, under the installed plugins', () => {
    const path = '/plugins/mailer/settings'
    expect([path, metaOf(path).parent, viewOf(path)]).toEqual([path, '/plugins', undefined])
  })

  it('declares the view once, on the settings page: the others join it as pages below', () => {
    const declaring = router.getRoutes().filter(r => r.meta.sidebar)
    expect(declaring.map(r => r.path)).toEqual(['/settings'])
  })

  it('leaves the feature pages of the sidebar without a parent', () => {
    for (const path of [
      '/segments',
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

  it('gives the balance ledger its own place: the finance menu entry (T80)', () => {
    expect(metaOf('/ledger')).toMatchObject({ perm: 'balance:all:read' })
    expect(metaOf('/ledger').parent).toBeUndefined()
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
    expect(await follow('/settings')).toBe('/prices')
  })

  it('is refused without any page of the view, as is a link to a configuration page', async () => {
    auth.denied = ['settings:read', 'sticky:read']
    expect(await follow('/settings?tab=billing')).toMatch(/^\/forbidden\?perm=settings:read/)
    auth.denied.push('group:read', 'price:read', 'plugin:market:read')
    expect(await follow('/settings')).toMatch(/^\/forbidden\?perm=settings:read/)
  })
})

describe('links to plugin settings as a tab', () => {
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

  it("of the detail page lead a holder of only the plugin's settings permission to the settings page, keeping the anchor", async () => {
    auth.denied = ['plugin:read']
    expect(await follow('/plugins/mailer?tab=settings#smtp')).toBe('/plugins/mailer/settings#smtp')
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

describe('the accounts and proxies pages', () => {
  beforeEach(() => follow('/forbidden'))

  it('open for the keys of their lists, not for the create keys alone', async () => {
    auth.denied = ['account:read', 'proxy:read']
    expect(await follow('/accounts')).toBe('/accounts')
    expect(await follow('/proxies')).toBe('/proxies')
    // account:create:own and proxy:manage:own are still held.
    auth.denied.push('account:read:own', 'proxy:read:own')
    expect(await follow('/accounts')).toMatch(/^\/forbidden\?perm=/)
    expect(await follow('/proxies')).toMatch(/^\/forbidden\?perm=/)
  })
})

describe('a caller whose account could not be loaded', () => {
  beforeEach(async () => {
    await follow('/forbidden')
    auth.me = null
    auth.loadMe.mockRejectedValue(new Error('network'))
    // Without the caller nothing is held, as the store's has() answers.
    auth.denied = ['group:read']
  })

  it('is offered a retry of the page, not told a permission is missing', async () => {
    await follow('/groups')
    const { path, query } = router.currentRoute.value
    expect([path, query]).toEqual(['/forbidden', { reason: 'load_failed', redirect: '/groups' }])
  })

  it('reaches the page once the account loads', async () => {
    auth.loadMe.mockImplementation(async () => {
      auth.me = {}
      auth.denied = []
    })
    expect(await follow('/groups')).toBe('/groups')
  })
})
