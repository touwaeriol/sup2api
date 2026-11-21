import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RouterLinkStub, enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, reactive, ref, type Component } from 'vue'
import { i18n } from '@/i18n'
import type { User } from '@/api/types'
import type { Segment, SegmentRef } from '@/api/pricing'
import SegmentMembers from './SegmentMembers.vue'

const mocks = vi.hoisted(() => ({ post: vi.fn(), toast: vi.fn(), confirm: vi.fn(), reload: vi.fn(), useList: vi.fn(), denied: new Set<string>() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { post: mocks.post } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a), confirm: (...a: unknown[]) => mocks.confirm(...a) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !mocks.denied.has(key) }) }))
const named = (id: number, name: string, over: Partial<Segment> = {}): Segment => ({ id, kind: 'named', name, description: '', discount: '1', is_default: false, member_count: 2, granted_groups: [], rate_override_count: 0, ...over })
const fallback = named(1, '默认', { is_default: true })
vi.mock('@/components/pricing/segmentLookup', () => ({ useSegmentsLookup: () => ({ segments: ref([fallback, named(2, 'VIP')]) }) }))
const user = (id: number, email: string, segment: SegmentRef): User => ({ id, email, display_name: '', status: 'active', segment }) as User
const vip = named(2, 'VIP')
const items = ref<User[]>([])
vi.mock('@/composables/useList', () => ({
  useList: (...a: unknown[]) => (mocks.useList(...a), { items, filters: reactive({ q: '' }), loading: ref(false), error: ref(null), page: ref(1), pageSize: ref(20), total: ref(1), reload: mocks.reload })
}))
enableAutoUnmount(afterEach)

/** The user picker: hands over the user a test names. */
const picker: Component = { emits: ['select'], setup: () => () => h('div', { 'data-testid': 'picker' }) }

async function render(segment: Segment | null = vip, name = segment?.name ?? '') {
  const w = mount(SegmentMembers, { props: { segment, name }, global: { plugins: [i18n], stubs: { UserPick: picker, RouterLink: RouterLinkStub } } })
  await flushPromises()
  const add = async (u: User) => {
    w.getComponent(picker).vm.$emit('select', u)
    await flushPromises()
  }
  return { w, add }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  mocks.denied.clear()
  vi.clearAllMocks()
  mocks.post.mockResolvedValue({ moved: 1 })
  mocks.confirm.mockResolvedValue(true)
  items.value = [user(5, 'bob@x', vip)]
})

describe('the members of a user group', () => {
  it('lists the users in it', async () => {
    const { w } = await render()
    expect(mocks.useList).toHaveBeenCalledWith(expect.any(Function), { q: '' }, { immediate: true })
    expect((mocks.useList.mock.calls[0][0] as () => string)()).toBe('/segments/2/members')
    expect(w.findAll('[data-testid="segment-member-row"]').map((r) => r.attributes('data-user'))).toEqual(['5'])
  })

  it('adds a user at once, from wherever he was', async () => {
    const { w, add } = await render()
    await add(user(6, 'cat@x', named(4, 'Staff')))
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(mocks.post).toHaveBeenCalledWith('/segments/2/members', { user_ids: [6] })
    expect(mocks.toast).toHaveBeenCalledWith('已把 cat@x 从「Staff」移到「VIP」', 'success')
    expect(mocks.reload).toHaveBeenCalled()
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('asks before a personal user joins, since his settings go', async () => {
    mocks.confirm.mockResolvedValue(false)
    const { w, add } = await render()
    await add(user(7, 'amy@x', { id: 31, kind: 'personal', name: 'amy@x', user_id: 7 }))
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ danger: true, confirmText: '加入并删除单独设置' }))
    expect(mocks.post).not.toHaveBeenCalled()
    expect(w.emitted('changed')).toBeUndefined()
  })

  it('says so of a user who is in it already', async () => {
    const { add } = await render()
    await add(user(5, 'bob@x', vip))
    expect(mocks.post).not.toHaveBeenCalled()
    expect(mocks.toast).toHaveBeenCalledWith('bob@x 已经在这个用户组里')
  })

  it('removes a member to the default user group after asking', async () => {
    const { w } = await render()
    await w.get('[data-testid="segment-member-remove"]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ message: '把 bob@x 移出「VIP」？他会回到「默认」，之后按「默认」付费。' }))
    expect(mocks.post).toHaveBeenCalledWith('/segments/1/members', { user_ids: [5] })
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('keeps the members of the default user group', async () => {
    const { w } = await render(fallback)
    expect(w.find('[data-testid="segment-member-remove"]').exists()).toBe(false)
    expect(w.find('[data-testid="segment-members-default"]').exists()).toBe(true)
  })

  it('only lists the members to a caller who may not manage user groups', async () => {
    mocks.denied.add('segment:manage')
    const { w } = await render()
    expect(w.find('[data-testid="picker"]').exists()).toBe(false)
    expect(w.find('[data-testid="segment-member-remove"]').exists()).toBe(false)
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['用户', '状态'])
  })

  it('collects the members of a user group not created yet, to be added once it is', async () => {
    const { w, add } = await render(null, 'Gold')
    expect(mocks.useList).toHaveBeenCalledWith(expect.any(Function), { q: '' }, { immediate: false })
    expect(w.text()).toContain('选好的成员在点“新建”时一起加入。')
    expect(w.text()).toContain('还没选成员')
    expect(w.find('[data-testid="segment-member-search"]').exists()).toBe(false)
    await add(user(6, 'cat@x', named(4, 'Staff')))
    await add(user(7, 'amy@x', { id: 31, kind: 'personal', name: 'amy@x', user_id: 7 }))
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringContaining('加入「Gold」后') }))
    await add(user(6, 'cat@x', named(4, 'Staff')))
    expect(mocks.toast).toHaveBeenCalledWith('cat@x 已经在这个用户组里')
    expect(mocks.post).not.toHaveBeenCalled()
    expect(w.emitted('update:pending')?.at(-1)?.[0]).toEqual([expect.objectContaining({ id: 6 }), expect.objectContaining({ id: 7 })])

    await w.setProps({ pending: [user(6, 'cat@x', named(4, 'Staff'))] })
    expect(w.findAll('[data-testid="segment-member-row"]').map((r) => r.attributes('data-user'))).toEqual(['6'])
    await w.get('[data-testid="segment-member-remove"]').trigger('click')
    expect(w.emitted('update:pending')?.at(-1)?.[0]).toEqual([])
  })
})
