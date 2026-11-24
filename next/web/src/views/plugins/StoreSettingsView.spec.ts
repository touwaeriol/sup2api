import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { channel } from '@/testing/marketFixtures'

const mocks = vi.hoisted(() => ({
  firstPage: vi.fn(),
  api: { post: vi.fn(async () => ({ id: 3 })), patch: vi.fn(async () => ({})), del: vi.fn(async () => ({})) },
  confirm: vi.fn(async () => true),
  perms: null as string[] | null
}))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), api: mocks.api }))
vi.mock('@sub2api/ui', async original => ({ ...(await original<object>()), confirm: () => mocks.confirm() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => !mocks.perms || mocks.perms.includes(p) }) }))
enableAutoUnmount(afterEach)

import StoreSettingsView from './StoreSettingsView.vue'

const CHANNELS = [channel(1), channel(2, { name: '测试', branch: 'develop' }), channel(3, { name: '旧', branch: 'legacy', enabled: false })]
const SFormModal = { props: ['open'], emits: ['submit'], template: '<form v-if="open" data-testid="channel-form" @submit.prevent="$emit(\'submit\')"><slot /></form>' }

async function render() {
  const w = mount(StoreSettingsView, { global: { plugins: [i18n], stubs: { SFormModal, teleport: true } } })
  await flushPromises()
  return w
}
type W = Awaited<ReturnType<typeof render>>
const input = (w: W, testid: string) => w.get(`[data-testid="${testid}"] input, input[data-testid="${testid}"]`)

describe('StoreSettingsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.perms = null
    i18n.global.locale.value = 'zh'
    mocks.firstPage.mockResolvedValue(CHANNELS)
  })

  it('shows the repository read-only and locks the default channel', async () => {
    const w = await render()
    expect((input(w, 'store-repo').element as HTMLInputElement).value).toBe('touwaeriol/sup2api-plugin')
    expect(input(w, 'store-repo').attributes('readonly')).toBeDefined()
    expect(w.find('[data-testid="channel-default-1"]').exists()).toBe(true)
    expect(input(w, 'channel-enabled-1').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="channel-delete-1"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="channel-delete-2"]').attributes('disabled')).toBeUndefined()
    // A disabled channel cannot become the default.
    expect(w.find('[data-testid="channel-set-default-2"]').exists()).toBe(true)
    expect(w.find('[data-testid="channel-set-default-3"]').exists()).toBe(false)
  })

  it('enables, disables and sets the default with PATCH', async () => {
    const w = await render()
    await input(w, 'channel-enabled-2').setValue(false)
    await flushPromises()
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/sources/2', { enabled: false })
    await w.get('[data-testid="channel-set-default-2"]').trigger('click')
    await flushPromises()
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/sources/2', { is_default: true })
    expect(mocks.firstPage).toHaveBeenCalledTimes(3)
  })

  it('deletes a channel after the confirmation', async () => {
    const w = await render()
    await w.get('[data-testid="channel-delete-2"]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalled()
    expect(mocks.api.del).toHaveBeenCalledWith('/market/sources/2', undefined)
  })

  it('adds a channel only with a valid branch', async () => {
    const w = await render()
    await w.get('[data-testid="channel-add"]').trigger('click')
    await input(w, 'channel-name').setValue('灰度')
    await input(w, 'channel-branch').setValue('../main')
    await w.get('[data-testid="channel-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.api.post).not.toHaveBeenCalled()
    expect(w.text()).toContain(i18n.global.t('market.settings.branchInvalid'))
    await input(w, 'channel-branch').setValue(' canary ')
    await w.get('[data-testid="channel-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.api.post).toHaveBeenCalledWith('/market/sources', { name: '灰度', branch: 'canary' })
  })

  it('renames a channel without its branch', async () => {
    const w = await render()
    await w.get('[data-testid="channel-rename-2"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="channel-branch"]').exists()).toBe(false)
    expect((input(w, 'channel-name').element as HTMLInputElement).value).toBe('测试')
    await input(w, 'channel-name').setValue('预发')
    await w.get('[data-testid="channel-form"]').trigger('submit')
    await flushPromises()
    expect(mocks.api.patch).toHaveBeenCalledWith('/market/sources/2', { name: '预发' })
  })

  it('is read-only without the manage permission', async () => {
    mocks.perms = ['plugin:market:read']
    const w = await render()
    expect(w.find('[data-testid="channel-add"]').exists()).toBe(false)
    expect(w.find('[data-testid="channel-rename-2"]').exists()).toBe(false)
    expect(input(w, 'channel-enabled-2').attributes('disabled')).toBeDefined()
  })
})
