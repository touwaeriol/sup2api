// The pricing drawer with the real rates editor: a user made personal in the
// drawer can have his rates saved there at once (the editor turns editable in
// place, without being mounted again).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RouterLinkStub, enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, type Component } from 'vue'
import { i18n } from '@/i18n'
import type { User } from '@/api/types'
import type { Segment, SegmentGroupSetting } from '@/api/pricing'
import UserPricingDrawer from './UserPricingDrawer.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get, patch: mocks.patch } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
enableAutoUnmount(afterEach)

/** The membership panel: its button stands for converting the user in the dialogs it opens. */
const panel: Component = { props: ['user', 'segment', 'editable'], emits: ['changed'], setup: (_, { emit }) => () => h('button', { 'data-testid': 'convert', onClick: () => emit('changed') }) }

const segment = (over: Partial<Segment>): Segment => ({ id: 2, kind: 'named', name: 'VIP', description: '', is_default: false, member_count: 3, granted_groups: [], rate_overrides: [], ...over })
const named = segment({})
const solo = segment({ id: 31, kind: 'personal', name: 'amy@x', user_id: 7 })
const amy = { id: 7, email: 'amy@x', display_name: 'Amy', status: 'active', segment: named } as User
const setting = (s: Segment): SegmentGroupSetting => ({
  segment: { id: s.id, kind: s.kind, name: s.name },
  group: { id: 1, name: 'g1', description: '', status: 'active', visibility: 'public', rate_multiplier: '1' },
  accessible: true,
  granted: false,
  rate_override: null,
  rate: { value: '1', source: 'group', group_default: '1' },
  model_rates: []
})

/** The server: the user is in `current()`, whose settings are one public group. */
function serve(current: () => Segment) {
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/users/7') return { ...amy, segment: current() }
    return path.endsWith('/groups') ? [setting(current())] : current()
  })
}
async function settle() {
  await flushPromises()
  await new Promise((r) => setTimeout(r))
  await flushPromises()
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  mocks.toast.mockReset()
  mocks.patch.mockReset().mockResolvedValue(solo)
})

describe('a user made personal in the pricing drawer', () => {
  it('has his rates saved there at once', async () => {
    let current = named
    serve(() => current)
    const w = mount(UserPricingDrawer, { props: { open: true, user: amy }, global: { plugins: [i18n], stubs: { Teleport: true, UserSegmentPanel: panel, PriceSimulatorModal: true, RouterLink: RouterLinkStub } } })
    await settle()
    expect(w.find('[data-testid="user-pricing-save"]').exists()).toBe(false)
    current = solo
    await w.get('[data-testid="convert"]').trigger('click')
    await settle()
    await w.get('[data-group="1"] input[data-testid="rate-override-input"]').setValue('0.6')
    await w.get('[data-testid="user-pricing-save"]').trigger('click')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/segments/31', { group_rates: [{ group_id: 1, rate: '0.6' }] })
    expect(mocks.toast).toHaveBeenCalledWith('已保存', 'success')
  })
})
