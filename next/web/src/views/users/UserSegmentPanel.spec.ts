import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { i18n } from '@/i18n'
import type { PersonalDialogUser, Segment, SegmentRef } from '@/api/pricing'
import PersonalConvertDialog from '@/components/pricing/PersonalConvertDialog.vue'
import PersonalRemoveDialog from '@/components/pricing/PersonalRemoveDialog.vue'
import SegmentSelect from '@/components/pricing/SegmentSelect.vue'
import UserSegmentPanel from './UserSegmentPanel.vue'

const mocks = vi.hoisted(() => ({ post: vi.fn(), toast: vi.fn(), confirm: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { post: mocks.post } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a), confirm: (...a: unknown[]) => mocks.confirm(...a) }))
const segment = (id: number, name: string, over: Partial<Segment> = {}): Segment => ({ id, kind: 'named', name, description: '', is_default: false, member_count: 2, granted_groups: [], rate_overrides: [], ...over })
const vip = segment(2, 'VIP')
const staff = segment(4, 'Staff')
vi.mock('@/components/pricing/segmentLookup', () => ({ useSegmentsLookup: () => ({ segments: ref([segment(1, '默认', { is_default: true }), vip, staff]) }) }))
enableAutoUnmount(afterEach)

const origin: SegmentRef = { id: 2, kind: 'named', name: 'VIP' }
const solo = segment(31, 'amy@x', { kind: 'personal', user_id: 7, origin })
/** The list row may still say where he was before. */
const amy: PersonalDialogUser = { id: 7, email: 'amy@x', display_name: 'Amy', segment: { id: 1, kind: 'named', name: '默认' } }

function render(of: Segment, editable = true) {
  return mount(UserSegmentPanel, {
    props: { user: amy, segment: of, editable },
    global: { plugins: [i18n], stubs: { PersonalConvertDialog: true, PersonalRemoveDialog: true } }
  })
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  vi.clearAllMocks()
  mocks.post.mockResolvedValue({ moved: 1 })
  mocks.confirm.mockResolvedValue(true)
})

describe('the user group of a user', () => {
  it('moves him to another user group at once, after asking', async () => {
    const w = render(vip)
    w.getComponent(SegmentSelect).vm.$emit('update:modelValue', 4)
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ message: '把 amy@x 从「VIP」移到「Staff」？之后他按「Staff」的开通和倍率付费。' }))
    expect(mocks.post).toHaveBeenCalledWith('/segments/4/members', { user_ids: [7] })
    expect(mocks.toast).toHaveBeenCalledWith('已把 amy@x 移到「Staff」', 'success')
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('goes back to his user group when the move is declined', async () => {
    mocks.confirm.mockResolvedValue(false)
    const w = render(vip)
    w.getComponent(SegmentSelect).vm.$emit('update:modelValue', 4)
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(w.getComponent(SegmentSelect).props('modelValue')).toBe(2)
    expect(w.emitted('changed')).toBeUndefined()
  })

  it('gives him a personal segment from the one he is in now, whatever the list row says', async () => {
    const w = render(vip)
    await w.get('[data-testid="user-convert"]').trigger('click')
    expect(w.getComponent(PersonalConvertDialog).props()).toMatchObject({ open: true, user: { id: 7, segment: vip } })
    w.getComponent(PersonalConvertDialog).vm.$emit('done', solo)
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('moves a personal user back to the user group he came from', async () => {
    const w = render(solo)
    expect(w.get('[data-testid="user-personal-banner"]').text()).toContain('他原来在「VIP」')
    await w.get('[data-testid="user-move-back"]').trigger('click')
    expect(w.getComponent(PersonalRemoveDialog).props()).toMatchObject({ open: true, origin, user: { id: 7, segment: solo } })
    w.getComponent(PersonalRemoveDialog).vm.$emit('done')
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('only shows it to a caller who may not manage user groups', () => {
    const named = render(vip, false)
    expect(named.getComponent(SegmentSelect).props('disabled')).toBe(true)
    expect(named.find('[data-testid="user-convert"]').exists()).toBe(false)
    const personal = render(solo, false)
    expect(personal.find('[data-testid="user-move-back"]').exists()).toBe(false)
  })
})
