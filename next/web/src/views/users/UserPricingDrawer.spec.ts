import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RouterLinkStub, enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, type Component, type SetupContext } from 'vue'
import { i18n } from '@/i18n'
import type { User } from '@/api/types'
import type { Segment } from '@/api/pricing'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import UserPricingDrawer from './UserPricingDrawer.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), toast: vi.fn(), confirm: vi.fn(), denied: new Set<string>() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get, patch: mocks.patch } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a), confirm: (...a: unknown[]) => mocks.confirm(...a) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !mocks.denied.has(key) }) }))
enableAutoUnmount(afterEach)

/** What ran, in order: the discount's request and the save of the groups. */
const calls: string[] = []
const groups = { dirty: false, saved: true }
const groupsEditor: Component = {
  props: ['segmentId', 'editable', 'disabled', 'discount'],
  setup(props: { editable?: boolean; discount?: string }, { expose }: SetupContext) {
    expose({ save: async () => (calls.push(`groups at ${props.discount}`), groups.saved), get dirty() { return groups.dirty } })
    return () => h('div', { 'data-testid': 'groups', 'data-editable': String(!!props.editable), 'data-discount': props.discount })
  }
}
/** The membership panel: a field for the discount it edits. */
const panel: Component = {
  props: ['discount', 'user', 'segment', 'editable'],
  emits: ['update:discount', 'changed'],
  setup: (props: { discount?: string }, { emit }: SetupContext) => () =>
    h('input', { 'data-testid': 'discount', value: props.discount, onInput: (e: Event) => emit('update:discount', (e.target as HTMLInputElement).value) })
}

const segment = (over: Partial<Segment> = {}): Segment => ({ id: 2, kind: 'named', name: 'VIP', description: '', discount: '0.9', is_default: false, member_count: 3, granted_groups: [], rate_overrides: [], ...over })
const solo = segment({ id: 31, kind: 'personal', name: 'amy@x', user_id: 7, discount: '1' })
const amy = { id: 7, email: 'amy@x', display_name: 'Amy', status: 'active', segment: { id: 1, kind: 'named', name: '默认' } } as User

async function render(of: Segment) {
  mocks.get.mockImplementation(async (path: string) => (path === '/users/7' ? { ...amy, segment: of } : of))
  const w = mount(UserPricingDrawer, {
    props: { open: true, user: amy },
    global: { plugins: [i18n], stubs: { Teleport: true, SegmentGroupsEditor: groupsEditor, UserSegmentPanel: panel, PriceSimulatorModal: true, RouterLink: RouterLinkStub } }
  })
  await flushPromises()
  await new Promise((r) => setTimeout(r))
  await flushPromises()
  const click = async (selector: string) => {
    await w.get(selector).trigger('click')
    await flushPromises()
  }
  return { w, click }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  calls.length = 0
  Object.assign(groups, { dirty: false, saved: true })
  mocks.denied.clear()
  mocks.toast.mockReset()
  mocks.confirm.mockReset().mockResolvedValue(false)
  mocks.patch.mockReset().mockImplementation(async (path: string, body: { discount: string }) => (calls.push(`PATCH ${path}`), { ...solo, ...body }))
})

describe('groups and pricing of a user', () => {
  it('asks for the user group he is in now, not the one of the list row', async () => {
    const { w } = await render(solo)
    expect(mocks.get).toHaveBeenCalledWith('/users/7')
    expect(mocks.get).toHaveBeenCalledWith('/segments/31')
    expect(w.get('[data-testid="groups"]').attributes('data-editable')).toBe('true')
  })

  it('says so, instead of waiting, when the user comes without a user group', async () => {
    mocks.get.mockImplementation(async () => ({ ...amy, segment: undefined }))
    const w = mount(UserPricingDrawer, { props: { open: true, user: amy }, global: { plugins: [i18n], stubs: { Teleport: true, PriceSimulatorModal: true } } })
    await flushPromises()
    await new Promise((r) => setTimeout(r))
    await flushPromises()
    expect(w.get('[data-testid="user-pricing-drawer"] [role="alert"]').text()).toBe('读不到他所在的用户组，请稍后重试。')
    expect(mocks.get).not.toHaveBeenCalledWith(expect.stringMatching(/^\/segments\//))
  })

  it('saves the discount of a personal segment first, then the groups rated by it', async () => {
    const { w, click } = await render(solo)
    await w.get('[data-testid="discount"]').setValue('0.8')
    await click('[data-testid="user-pricing-save"]')
    expect(calls).toEqual(['PATCH /segments/31', 'groups at 0.8'])
    expect(mocks.patch).toHaveBeenCalledWith('/segments/31', { discount: '0.8' })
    expect(mocks.toast).toHaveBeenCalledWith('已保存', 'success')
  })

  it('saves only the groups when the discount did not change, and nothing when there is nothing to save', async () => {
    const { w } = await render(solo)
    expect(w.get('[data-testid="user-pricing-save"]').attributes('disabled')).toBeDefined()
    groups.dirty = true
    const drafted = await render(solo)
    await drafted.click('[data-testid="user-pricing-save"]')
    expect(calls).toEqual(['groups at 1'])
  })

  it('refuses a discount that is not one', async () => {
    const { w } = await render(solo)
    await w.get('[data-testid="discount"]').setValue('0')
    expect(w.get('[data-testid="user-pricing-save"]').attributes('disabled')).toBeDefined()
  })

  it('asks before closing with changes not saved', async () => {
    const plain = await render(solo)
    await plain.click('[data-testid="user-pricing-close"]')
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(plain.w.emitted('update:open')).toEqual([[false]])

    const { w, click } = await render(solo)
    await w.get('[data-testid="discount"]').setValue('0.8')
    await click('[data-testid="user-pricing-close"]')
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的改动？', confirmText: '放弃改动' }))
    expect(w.emitted('update:open')).toBeUndefined()
    mocks.confirm.mockResolvedValue(true)
    await click('[data-overlay-close]')
    expect(w.emitted('update:open')).toEqual([[false]])
    expect(mocks.patch).not.toHaveBeenCalled()
  })

  it('shows the settings of a named user group, which are every member\'s', async () => {
    const { w } = await render(segment())
    expect(w.get('[data-testid="groups"]').attributes('data-editable')).toBe('false')
    expect(w.get('[data-testid="user-pricing-inherited"]').text()).toContain('这些设置来自用户组「VIP」')
    expect(w.find('[data-testid="user-pricing-save"]').exists()).toBe(false)
  })

  it('edits nothing for a caller who may not manage user groups', async () => {
    mocks.denied.add('segment:manage')
    const { w } = await render(solo)
    expect(w.get('[data-testid="groups"]').attributes('data-editable')).toBe('false')
    expect(w.find('[data-testid="user-pricing-save"]').exists()).toBe(false)
  })

  it('opens the price simulator on the user, for a caller who may quote in a group only', async () => {
    const { w, click } = await render(solo)
    await click('[data-testid="user-pricing-simulate"]')
    expect(w.getComponent(PriceSimulatorModal).props()).toMatchObject({ open: true, preset: { userId: 7 } })

    mocks.denied.add('group:read')
    const blind = await render(solo)
    expect(blind.w.find('[data-testid="user-pricing-simulate"]').exists()).toBe(false)
    // Mounted, it would ask for the groups and be refused.
    expect(blind.w.findComponent(PriceSimulatorModal).exists()).toBe(false)
  })
})
