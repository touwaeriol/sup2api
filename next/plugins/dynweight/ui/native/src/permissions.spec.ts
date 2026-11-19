import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { PluginHost } from '@sub2api/host'
// Compiled here, at collection: the first load of @sub2api/ui compiles its
// whole source (seconds), which must not count against the timeout of the
// first test importing a component. The tests still get fresh modules.
import '@sub2api/ui'

// An own-level viewer (account:read:own with dynweight:read, no group:read
// or account:read) opens the account list: the plugin asks nothing the core
// refuses (no console 403s); a full-level viewer gets the names and the chip.
const list = vi.fn(async () => ({ items: [{ id: 2, name: 'g' }], total: 1 }))
const get = vi.fn(async () => ({ count: 1, account_ids: [7] }))
const pluginList = vi.fn(async () => ({ items: [{ account_id: 130 }], page: { page: 1, page_size: 50, total: 1 }, paged: true }))
let held: string[] = []

// Each test gets fresh modules: the catalog loads its groups once per module.
async function fresh() {
  vi.resetModules()
  const host = await import('./host')
  host.setHost({
    t: (key: string) => key,
    api: { list },
    pluginApi: { get, list: pluginList },
    can: (k: string) => held.includes(k),
    permissions: { has: (k: string) => held.includes(k), hasAction: (a: string) => held.some((k) => k === a || k.startsWith(a + ':')) }
  } as unknown as PluginHost)
  return host
}

beforeEach(() => {
  list.mockClear()
  get.mockClear()
  pluginList.mockClear()
})
afterEach(async () => (await import('./host')).setHost(null))
enableAutoUnmount(afterEach)

describe('catalog groups', () => {
  it('are not listed without group:read', async () => {
    held = []
    await fresh()
    const { useCatalog } = await import('./catalog')
    expect(useCatalog().groupName(2)).toBe('#2')
    await flushPromises()
    expect(list).not.toHaveBeenCalled()
  })

  it('are listed with group:read', async () => {
    held = ['group:read']
    await fresh()
    const { useCatalog } = await import('./catalog')
    const catalog = useCatalog()
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    expect(catalog.groupName(2)).toBe('g #2')
  })
})

describe('DegradedBadge', () => {
  const props = { ids: null, setIds: () => {} }

  it('asks nothing and stays hidden without account:read', async () => {
    held = ['account:read:own']
    await fresh()
    const { default: DegradedBadge } = await import('./DegradedBadge.vue')
    const w = mount(DegradedBadge, { props })
    await flushPromises()
    expect(get).not.toHaveBeenCalled()
    expect(w.html()).toBe('<!--v-if-->')
  })

  it('shows the count with account:read', async () => {
    held = ['account:read']
    await fresh()
    const { default: DegradedBadge } = await import('./DegradedBadge.vue')
    mount(DegradedBadge, { props })
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/degraded')
  })
})

// /status and /events without an account cover every account: the core asks
// the full account:read. A viewer of some accounts (a group scope) is not
// sent there until it names an account.
describe('keyListApi', () => {
  it('answers a query naming no account empty for a scoped viewer', async () => {
    held = ['account:read:group/19', 'plugin.dynweight:dynweight:read']
    const { keyListApi, seesAllAccounts } = await fresh()
    expect(seesAllAccounts()).toBe(false)
    const client = keyListApi()
    expect(await client.list('/status', { degraded: '1', account_id: '' })).toEqual({ items: [], page: { page: 1, page_size: 0, total: 0 }, paged: false })
    expect(pluginList).not.toHaveBeenCalled()
    await client.list('/status', { account_id: '130' })
    expect(pluginList).toHaveBeenCalledWith('/status', { account_id: '130' }, undefined)
  })

  it('is the plugin API itself with account:read', async () => {
    held = ['account:read']
    const host = await fresh()
    expect(host.keyListApi()).toBe(host.useDwHost().pluginApi)
  })
})

// POST /reset names accounts (RouteAccounts update): the reset entries need
// account:update at some scope besides dynweight:manage, and a reset naming
// no account (a whole group) the full account:update.
describe('canReset', () => {
  it('needs dynweight:manage and account:update at some scope', async () => {
    for (const [keys, want] of [
      [['dynweight:manage'], false],
      [['dynweight:manage', 'account:read'], false],
      [['account:update'], false],
      [['dynweight:manage', 'account:update:group/12'], true],
      [['dynweight:manage', 'account:update'], true]
    ] as const) {
      held = [...keys]
      const { canReset } = await fresh()
      expect(canReset(), keys.join(' ')).toBe(want)
    }
  })

  it('resets a whole group only with the full account:update', async () => {
    held = ['dynweight:manage', 'account:update:own']
    expect((await fresh()).resetsAllAccounts()).toBe(false)
    held = ['dynweight:manage', 'account:update']
    expect((await fresh()).resetsAllAccounts()).toBe(true)
  })
})
