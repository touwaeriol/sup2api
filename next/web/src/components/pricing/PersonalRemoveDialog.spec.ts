import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { PersonalDialogUser, Segment, SegmentRef } from '@/api/pricing'
import PersonalRemoveDialog from './PersonalRemoveDialog.vue'
import SegmentSelect from './SegmentSelect.vue'

const mocks = vi.hoisted(() => ({ del: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { del: mocks.del } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
const named = (id: number, name: string, over: Partial<Segment> = {}): Segment => ({ id, kind: 'named', name, description: '', discount: '1', is_default: false, member_count: 1, granted_groups: [], rate_overrides: [], ...over })
const segments = ref([named(1, 'Default', { is_default: true }), named(2, 'VIP')])
vi.mock('./segmentLookup', () => ({ useSegmentsLookup: () => ({ segments, ready: Promise.resolve() }) }))
enableAutoUnmount(afterEach)

const solo: SegmentRef = { id: 31, kind: 'personal', name: 'amy@example.com', user_id: 7 }
const amy: PersonalDialogUser = { id: 7, email: 'amy@example.com', display_name: 'Amy', segment: solo }

async function render(user: PersonalDialogUser, origin?: SegmentRef | null) {
  const w = mount(PersonalRemoveDialog, { props: { open: true, user, origin }, global: { plugins: [i18n] }, attachTo: document.body })
  await flushPromises()
  const dialog = () => document.body.querySelector<HTMLElement>('[data-testid="personal-remove-dialog"]')!
  const submit = async () => {
    document.body.querySelector<HTMLElement>('[data-testid="personal-remove-submit"]')!.click()
    await flushPromises()
  }
  return { w, dialog, submit, target: () => w.getComponent(SegmentSelect).props('modelValue') }
}

beforeEach(() => {
  vi.clearAllMocks()
  i18n.global.locale.value = 'zh'
  mocks.del.mockResolvedValue(undefined)
})

describe('moving a user back from his personal segment', () => {
  it('offers the user group he came from and moves him there', async () => {
    const { w, target, submit } = await render(amy, segments.value[1])
    expect(target()).toBe(2)
    expect(document.body.textContent).toContain('把 amy@example.com 移回用户组')
    await submit()
    expect(mocks.del).toHaveBeenCalledWith('/users/7/personal-segment', { to: 2 })
    expect(mocks.toast).toHaveBeenCalledWith('已把 amy@example.com 移到「VIP」', 'success')
    expect(w.emitted('done')).toHaveLength(1)
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('offers the default user group when the one he came from is gone', async () => {
    const { target } = await render(amy, null)
    expect(target()).toBe(1)
  })

  it('moves a user the caller only knows by id', async () => {
    // Without user:read the segment is named "#7" and carries no user: the id is all it takes.
    const masked: PersonalDialogUser = { id: 7, email: '#7', display_name: '', segment: { ...solo, name: '#7' } }
    const { submit } = await render(masked)
    expect(document.body.textContent).toContain('把 #7 移回用户组')
    await submit()
    expect(mocks.del).toHaveBeenCalledWith('/users/7/personal-segment', { to: 1 })
  })

  it('stays open and reports a refusal', async () => {
    mocks.del.mockRejectedValue(new ApiError(404, { code: 'not_found', message: 'not personal' }))
    const { w, submit } = await render(amy, segments.value[1])
    await submit()
    expect(mocks.toast).toHaveBeenCalledWith('not personal', 'error')
    expect(w.emitted('done')).toBeUndefined()
    expect(w.emitted('update:open')).toBeUndefined()
  })
})
