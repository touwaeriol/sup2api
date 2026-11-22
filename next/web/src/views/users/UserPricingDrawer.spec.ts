import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RouterLinkStub, enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, type Component } from 'vue'
import { ApiError } from '@sub2api/host'
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

/** What the rates of the drawer hold: what they send, whether they have drafts, the errors they were given, and how often they were mounted. */
const groups = vi.hoisted(() => ({ body: {} as Record<string, unknown>, dirty: false, errors: undefined as Record<string, string> | undefined, mounts: 0 }))
// Mocked as a module, not stubbed: a stub may be a new component on every render, and a part registers once per mount.
vi.mock('@/components/pricing/SegmentGroupsEditor.vue', async (): Promise<{ default: Component }> => {
  const { h: render } = await import('vue')
  const { useTabbedFormPart } = await import('@sub2api/ui')
  return {
    default: {
      props: ['segmentId', 'editable', 'disabled', 'tab'],
      setup(props: Record<string, unknown>) {
        groups.mounts++
        if (props.editable) useTabbedFormPart({ tab: 'groups', fields: ['group_rates', 'model_rates'], body: () => groups.body, check: () => true, dirty: () => groups.dirty, applyErrors: (e) => (groups.errors = e) })
        return () => render('div', { 'data-testid': 'groups', 'data-editable': String(!!props.editable) })
      }
    }
  }
})
const panel: Component = { props: ['user', 'segment', 'editable'], emits: ['changed'], setup: () => () => h('div') }

const segment = (over: Partial<Segment> = {}): Segment => ({ id: 2, kind: 'named', name: 'VIP', description: '', is_default: false, member_count: 3, granted_groups: [], rate_overrides: [], ...over })
const solo = segment({ id: 31, kind: 'personal', name: 'amy@x', user_id: 7 })
const amy = { id: 7, email: 'amy@x', display_name: 'Amy', status: 'active', segment: { id: 1, kind: 'named', name: '默认' } } as User

async function render(of: Segment) {
  mocks.get.mockImplementation(async (path: string) => (path === '/users/7' ? { ...amy, segment: of } : of))
  const w = mount(UserPricingDrawer, {
    props: { open: true, user: amy },
    global: { plugins: [i18n], stubs: { Teleport: true, UserSegmentPanel: panel, PriceSimulatorModal: true, RouterLink: RouterLinkStub } }
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
  Object.assign(groups, { body: {}, dirty: false, errors: undefined, mounts: 0 })
  mocks.denied.clear()
  mocks.toast.mockReset()
  mocks.confirm.mockReset().mockResolvedValue(false)
  mocks.patch.mockReset().mockResolvedValue(solo)
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
    expect(w.get('[data-testid="user-pricing-drawer"] [role="alert"]').text()).toBe('读不到他所在的用户分组，请稍后重试。')
    expect(mocks.get).not.toHaveBeenCalledWith(expect.stringMatching(/^\/segments\//))
  })

  it('saves the rates of a personal segment in one request, then loads them again', async () => {
    const { w } = await render(solo)
    expect(w.get('[data-testid="user-pricing-save"]').attributes('disabled')).toBeDefined()
    groups.dirty = true
    groups.body = { group_rates: [{ group_id: 2, rate: '0.5' }], model_rates: [{ group_id: 1, model: 'gpt-4o', rate: '0.8' }] }
    const drafted = await render(solo)
    const mounted = groups.mounts
    await drafted.click('[data-testid="user-pricing-save"]')
    expect(mocks.patch).toHaveBeenCalledTimes(1)
    expect(mocks.patch).toHaveBeenCalledWith('/segments/31', groups.body)
    expect(mocks.toast).toHaveBeenCalledWith('已保存', 'success')
    expect(groups.mounts).toBe(mounted + 1)
    expect(drafted.w.emitted('update:open')).toBeUndefined()
  })

  it('takes the field errors of the answer to the rates that sent them', async () => {
    groups.dirty = true
    const { click } = await render(solo)
    mocks.patch.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'group_rates.0.rate', code: 'invalid', message: 'too low' }] } }))
    await click('[data-testid="user-pricing-save"]')
    expect(groups.errors).toEqual({ 'group_rates.0.rate': 'too low' })
    expect(mocks.toast).not.toHaveBeenCalledWith('已保存', 'success')
  })

  it('asks before closing with changes not saved', async () => {
    const plain = await render(solo)
    await plain.click('[data-testid="user-pricing-close"]')
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(plain.w.emitted('update:open')).toEqual([[false]])

    groups.dirty = true
    const { w, click } = await render(solo)
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
    expect(w.get('[data-testid="user-pricing-inherited"]').text()).toContain('这些设置来自用户分组「VIP」')
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
