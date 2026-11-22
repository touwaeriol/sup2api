import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { i18n } from '@/i18n'
import type { Segment } from '@/api/pricing'
import type { Role } from '@/api/types'
import UserCreateDialog from './UserCreateDialog.vue'

const mocks = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), denied: new Set<string>(), epoch: 0 }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { post: mocks.post, get: mocks.get } }))
// A new epoch per test: the managed roles are cached per permissions reload.
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !mocks.denied.has(key), hasAction: (a: string) => !mocks.denied.has(a), me: { id: 1, superuser: false }, permissionsEpoch: mocks.epoch }) }))
const managed = (key: string, grantable = true) => ({ key, name: { en: key }, builtin: true, grantable, missing: grantable ? [] : ['x:y'] })
const segments = ref([{ id: 1, name: 'Default', is_default: true }, { id: 2, name: 'VIP', is_default: false }] as Segment[])
vi.mock('@/components/pricing/segmentLookup', () => ({ useSegmentsLookup: () => ({ segments }) }))
enableAutoUnmount(afterEach)

async function render(roles: Role[] = []) {
  const w = mount(UserCreateDialog, { props: { open: false, roles }, global: { plugins: [i18n], stubs: { Teleport: true, EntityPicker: { props: ['id'], emits: ['update:id'], template: '<input @change="$emit(\'update:id\', $event.target.value)" />' } } } })
  await w.setProps({ open: true })
  await flushPromises()
  await w.get('input[type="email"]').setValue(' amy@example.com ')
  await w.get('input[type="password"]').setValue('password1')
  const submit = async () => {
    await w.get('[data-testid="user-create-submit"]').trigger('click')
    await flushPromises()
  }
  return { w, submit }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  mocks.denied.clear()
  mocks.epoch++
  mocks.post.mockReset().mockResolvedValue({ id: 9 })
  mocks.get.mockReset().mockResolvedValue([managed('user'), managed('supplier')])
})

describe('user create dialog', () => {
  it('creates the user in the segment chosen, in the one request', async () => {
    const { w, submit } = await render()
    const select = w.get('[data-testid="user-create-segment"]')
    // The default segment is where a new user goes unless told otherwise.
    expect((select.element as HTMLSelectElement).selectedOptions[0].text).toContain('Default')
    // Option values are positions in the list.
    await select.setValue('1')
    await submit()
    expect(mocks.post).toHaveBeenCalledTimes(1)
    expect(mocks.post).toHaveBeenCalledWith('/users', { email: 'amy@example.com', display_name: '', password: 'password1', role_keys: ['user'], segment_id: 2 })
    expect(w.emitted('created')).toHaveLength(1)
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it.each(['segment:read', 'segment:manage'])('leaves the segment to the server for a caller without %s', async (perm) => {
    mocks.denied.add(perm)
    const { w, submit } = await render()
    expect(w.find('[data-testid="user-create-segment"]').exists()).toBe(false)
    await submit()
    expect(mocks.post.mock.calls[0][1].segment_id).toBeUndefined()
  })

  it('checks the default role along with the add-on supplier role', async () => {
    const roles = ['user', 'supplier'].map((key) => ({ key, name: { en: key } }) as unknown as Role)
    const { w, submit } = await render(roles)
    const boxes = w.findAll('input[type="checkbox"]')
    // Unchecked first, so that supplier is chosen without the default role.
    await boxes[0].setValue(false)
    await boxes[1].setValue(true)
    await submit()
    expect(mocks.post.mock.calls[0][1].role_keys).toEqual(['supplier', 'user'])
  })

  it('offers only the managed roles and the default role; ungrantable ones are disabled', async () => {
    mocks.get.mockResolvedValue([managed('supplier'), managed('operator', false)])
    const { w } = await render([{ key: 'user', name: { en: 'User' } } as unknown as Role, { key: 'admin', name: { en: 'Admin' } } as unknown as Role])
    const box = (k: string) => w.find(`input[data-role="${k}"]`)
    expect(box('admin').exists()).toBe(false)
    expect(box('user').attributes('disabled')).toBeUndefined()
    expect(box('supplier').attributes('disabled')).toBeUndefined()
    expect(box('operator').attributes('disabled')).toBeDefined()
    expect(mocks.get).toHaveBeenCalledWith('/me/assignable-roles')
  })

  it('without role:assign only the default role is selectable', async () => {
    mocks.denied.add('role:assign')
    const { w } = await render()
    expect(w.find('input[data-role="user"]').attributes('disabled')).toBeUndefined()
    expect(w.find('input[data-role="supplier"]').attributes('disabled')).toBeDefined()
  })

  it.each([
    ['the default role', [managed('user'), managed('supplier')], ['user']],
    ['the default role where it is not on the list', [managed('supplier'), managed('supplier_agent')], ['user']],
    ['no other role by fallback', [managed('supplier', false), managed('operator')], ['user']],
    ['none where the default role is beyond the caller', [managed('user', false), managed('operator')], []]
  ])('sends the roles explicitly, preselecting %s', async (_, list, keys) => {
    mocks.get.mockResolvedValue(list)
    const { submit } = await render()
    await submit()
    expect(mocks.post.mock.calls[0][1].role_keys).toEqual(keys)
  })

  it('preselects the default role and supplier for a team holder (a supplier agent), and keeps them together', async () => {
    mocks.denied.add('user:create')
    mocks.get.mockResolvedValue([managed('supplier'), managed('supplier_agent')])
    const { w, submit } = await render([{ key: 'user', name: { en: 'User' } } as unknown as Role])
    await submit()
    expect(mocks.post.mock.calls[0][1].role_keys).toEqual(['user', 'supplier'])
    // Supplier checked again brings the default role back: the server gives it to a role-less new user from any creator.
    const box = (k: string) => w.get(`input[data-role="${k}"]`)
    await box('supplier').setValue(false)
    await box('user').setValue(false)
    await box('supplier').setValue(true)
    await submit()
    expect(mocks.post.mock.calls[1][1].role_keys).toEqual(['supplier', 'user'])
  })

  it('names the owner only for a caller holding user:create over every user', async () => {
    const { w, submit } = await render()
    await w.get('[data-testid="user-create-owner"]').setValue('7')
    await submit()
    expect(mocks.post.mock.calls[0][1].owner_id).toBe(7)

    mocks.denied.add('user:create')
    const team = await render()
    expect(team.w.find('[data-testid="user-create-owner"]').exists()).toBe(false)
    await team.submit()
    expect(mocks.post.mock.calls[1][1].owner_id).toBeUndefined()
  })
})
