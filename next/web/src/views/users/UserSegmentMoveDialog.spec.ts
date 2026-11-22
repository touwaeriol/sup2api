import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { i18n } from '@/i18n'
import type { User } from '@/api/types'
import type { Segment, SegmentRef } from '@/api/pricing'
import SegmentSelect from '@/components/pricing/SegmentSelect.vue'
import UserSegmentMoveDialog from './UserSegmentMoveDialog.vue'

const mocks = vi.hoisted(() => ({ post: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { post: mocks.post } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
const named = (id: number, name: string): Segment => ({ id, kind: 'named', name, description: '', is_default: id === 1, member_count: 1, granted_groups: [], rate_overrides: [] })
vi.mock('@/components/pricing/segmentLookup', () => ({ useSegmentsLookup: () => ({ segments: ref([named(1, '默认'), named(2, 'VIP')]) }) }))
enableAutoUnmount(afterEach)

const vip: SegmentRef = { id: 2, kind: 'named', name: 'VIP' }
const user = (id: number, segment: SegmentRef): User => ({ id, email: `u${id}@x`, display_name: '', status: 'active', segment }) as User
const users = [user(1, vip), user(2, { id: 1, kind: 'named', name: '默认' }), user(3, { id: 31, kind: 'personal', name: 'u3@x', user_id: 3 })]

async function render(selected = users) {
  const w = mount(UserSegmentMoveDialog, { props: { open: true, users: selected }, global: { plugins: [i18n] }, attachTo: document.body })
  await flushPromises()
  const choose = async (id: number) => {
    w.getComponent(SegmentSelect).vm.$emit('update:modelValue', id)
    await flushPromises()
  }
  const text = (id: string) => document.body.querySelector(`[data-testid="${id}"]`)?.textContent?.trim()
  const submitButton = () => document.body.querySelector<HTMLButtonElement>('[data-testid="user-move-submit"]')!
  return { w, choose, text, submitButton }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  vi.clearAllMocks()
  mocks.post.mockResolvedValue({ moved: 2 })
})

describe('moving users to a user group', () => {
  it('says who moves and who loses a personal segment, and moves only those not in it yet', async () => {
    const { w, choose, text, submitButton } = await render()
    expect(document.body.textContent).toContain('把 3 位用户移到用户分组')
    expect(submitButton().disabled).toBe(true)

    await choose(2)
    expect(text('user-move-impact')).toBe('2 位用户将移到「VIP」。1 位已在「VIP」，不受影响。')
    expect(text('user-move-personal')).toBe('包含 1 位专享用户，移动后他们单独设的开通和倍率会被删除。')

    submitButton().click()
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/segments/2/members', { user_ids: [2, 3] })
    expect(mocks.toast).toHaveBeenCalledWith('已把 2 位用户移到「VIP」', 'success')
    expect(w.emitted('done')).toHaveLength(1)
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('has nothing to move when every user is in the target already', async () => {
    const { choose, text, submitButton } = await render([users[0]])
    await choose(2)
    expect(text('user-move-impact')).toBe('1 位已在「VIP」，不受影响。')
    expect(text('user-move-personal')).toBeUndefined()
    expect(submitButton().disabled).toBe(true)
  })

  it('stays open when the move fails', async () => {
    mocks.post.mockRejectedValue(new Error('down'))
    const { w, choose, submitButton } = await render()
    await choose(1)
    submitButton().click()
    await flushPromises()
    expect(mocks.toast).toHaveBeenCalledWith('down', 'error')
    expect(w.emitted('update:open')).toBeUndefined()
  })
})
