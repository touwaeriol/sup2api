import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { channel, serveStore, store } from '@/testing/marketFixtures'

const mocks = vi.hoisted(() => ({
  firstPage: vi.fn(),
  api: { post: vi.fn(async () => ({ id: 3 })), patch: vi.fn(async () => ({})), del: vi.fn(async () => ({})) },
  confirm: vi.fn(async () => true),
  toast: vi.fn(),
  perms: null as string[] | null
}))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), api: mocks.api }))
vi.mock('@sub2api/ui', async original => ({ ...(await original<object>()), confirm: () => mocks.confirm(), toast: mocks.toast }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => !mocks.perms || mocks.perms.includes(p) }) }))
enableAutoUnmount(afterEach)

import StoreSettingsView from './StoreSettingsView.vue'

const STORES = [store(1)]
const CHANNELS = [
  channel(1),
  channel(2, { name: '测试', branch: 'develop', is_default: false }),
  channel(3, { name: '旧', branch: 'legacy', enabled: false, is_default: false })
]
const SFormModal = { props: ['open'], emits: ['submit'], template: '<form v-if="open" @submit.prevent="$emit(\'submit\')"><slot /></form>' }
const tt = (key: string, named: Record<string, unknown> = {}) => i18n.global.t(key, named)

async function render() {
  const w = mount(StoreSettingsView, { global: { plugins: [i18n], stubs: { SFormModal, teleport: true } } })
  await flushPromises()
  return w
}
type W = Awaited<ReturnType<typeof render>>
const input = (w: W, testid: string) => w.get(`[data-testid="${testid}"] input, input[data-testid="${testid}"]`)
const submit = async (w: W) => {
  await w.get('form').trigger('submit')
  await flushPromises()
}

describe('StoreSettingsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.perms = null
    i18n.global.locale.value = 'zh'
    mocks.firstPage.mockImplementation(serveStore({ stores: STORES, channels: CHANNELS }))
  })

  it('shows the official store with its repository and channels, without index addresses or a way to add or delete stores', async () => {
    const w = await render()
    const official = w.get('[data-testid="store-1"]')
    expect(official.get('[data-testid="store-kind-1"]').text()).toBe(tt('market.stores.official'))
    expect(official.get('[data-testid="store-repo-1"]').text()).toBe('touwaeriol/sup2api-plugin')
    expect(official.findAll('[data-testid^="channel-rename-"]')).toHaveLength(3)
    expect(official.text()).not.toContain('raw.githubusercontent.com')
    expect(official.get('[data-testid="store-note-1"]').text()).toBe(tt('market.stores.officialNote'))
    expect(w.find('[data-testid="store-add"]').exists()).toBe(false)
    expect(w.find('[data-testid="store-delete-1"]').exists()).toBe(false)
    expect(input(w, 'channel-enabled-2').attributes('aria-label')).toBe(tt('market.settings.enabledOf', { name: '测试' }))
  })

  it('locks the default channel', async () => {
    const w = await render()
    expect(input(w, 'channel-enabled-1').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="channel-delete-1"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="channel-delete-2"]').attributes('disabled')).toBeUndefined()
    // A disabled channel cannot become the default.
    expect(w.find('[data-testid="channel-set-default-2"]').exists()).toBe(true)
    expect(w.find('[data-testid="channel-set-default-3"]').exists()).toBe(false)
  })

  it('enables, disables and sets the default with PATCH, and deletes after the confirmation', async () => {
    const w = await render()
    await input(w, 'channel-enabled-2').setValue(false)
    await flushPromises()
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/sources/2', { enabled: false })
    await w.get('[data-testid="channel-set-default-2"]').trigger('click')
    await flushPromises()
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/sources/2', { is_default: true })
    await w.get('[data-testid="channel-delete-2"]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalled()
    expect(mocks.api.del).toHaveBeenCalledWith('/market/sources/2', undefined)
  })

  it('adds a channel only with a valid branch, and warns when its branch has no index yet', async () => {
    mocks.api.post.mockResolvedValueOnce({ id: 4, warning: 'no_index' } as never)
    const w = await render()
    await w.get('[data-testid="store-add-channel-1"]').trigger('click')
    await input(w, 'channel-name').setValue('灰度')
    await input(w, 'channel-branch').setValue('../main')
    await submit(w)
    expect(mocks.api.post).not.toHaveBeenCalled()
    expect(w.text()).toContain(tt('market.settings.branchInvalid'))
    await input(w, 'channel-branch').setValue(' canary ')
    await submit(w)
    expect(mocks.api.post).toHaveBeenCalledWith('/market/stores/1/sources', { name: '灰度', branch: 'canary' })
    expect(mocks.toast).toHaveBeenCalledWith(tt('market.settings.noIndex', { branch: 'canary' }), 'warning', 8000)
  })

  it('renames a channel without its branch', async () => {
    const w = await render()
    await w.get('[data-testid="channel-rename-2"]').trigger('click')
    expect(w.find('[data-testid="channel-branch"]').exists()).toBe(false)
    expect((input(w, 'channel-name').element as HTMLInputElement).value).toBe('测试')
    await input(w, 'channel-name').setValue('预发')
    await submit(w)
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/sources/2', { name: '预发' })
  })

  it('renames the store by name only', async () => {
    const w = await render()
    await w.get('[data-testid="store-rename-1"]').trigger('click')
    expect((input(w, 'store-name').element as HTMLInputElement).value).toBe('官方商店')
    await input(w, 'store-name').setValue('官方')
    await submit(w)
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/stores/1', { name: '官方' })
  })

  it('is read-only without the manage permission', async () => {
    mocks.perms = ['plugin:market:read']
    const w = await render()
    expect(w.find('[data-testid="store-rename-1"]').exists()).toBe(false)
    expect(w.find('[data-testid="channel-rename-2"]').exists()).toBe(false)
    expect(input(w, 'channel-enabled-2').attributes('disabled')).toBeDefined()
  })
})
