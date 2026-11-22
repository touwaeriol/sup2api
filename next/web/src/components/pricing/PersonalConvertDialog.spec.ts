import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Segment } from '@/api/pricing'
import PersonalConvertDialog from './PersonalConvertDialog.vue'

const mocks = vi.hoisted(() => ({ post: vi.fn(), toast: vi.fn() }))
vi.mock('@sub2api/host', async (importOriginal) => ({ ...(await importOriginal<typeof import('@sub2api/host')>()), api: { post: mocks.post } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))

const user = { id: 7, email: 'amy@example.com', display_name: 'Amy', segment: { id: 2, kind: 'named' as const, name: 'VIP' } }
const personal: Segment = {
  id: 31,
  kind: 'personal',
  name: user.email,
  user_id: user.id,
  description: '',
  discount: '0.9',
  is_default: false,
  member_count: 1,
  granted_groups: [],
  rate_overrides: [],
}

/** The user search: a button per user it would find. */
const userPick = defineComponent({
  emits: ['select'],
  setup: (_, { emit }) => () => [user, { ...user, id: 8, email: 'bo@example.com', segment: { id: 32, kind: 'personal' as const, name: 'bo@example.com' } }].map((u) => h('button', { type: 'button', 'data-testid': `pick-${u.id}`, onClick: () => emit('select', u) }))
})

function render(of: typeof user | null = user) {
  return mount(PersonalConvertDialog, { props: { open: true, user: of }, global: { plugins: [i18n], stubs: { UserPick: userPick } }, attachTo: document.body })
}
const dialog = () => document.body.querySelector<HTMLElement>('[data-testid="personal-convert-dialog"]')!
const submit = () => document.body.querySelector<HTMLElement>('[data-testid="personal-convert-submit"]')!.click()

describe('personal convert dialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.post.mockResolvedValue(personal)
  })

  it('names the segment the user leaves and copies its settings by default', async () => {
    const wrapper = render()
    expect(dialog().textContent).toContain('不再属于「VIP」')
    expect(dialog().querySelector('[data-testid="pick-7"]')).toBeNull()
    expect(dialog().textContent).toContain('复制「VIP」现在的设置')

    submit()
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/users/7/personal-segment', { copy: true })
    expect(mocks.toast).toHaveBeenCalledWith('amy@example.com 现在是个人', 'success')
    expect(wrapper.emitted('done')).toEqual([[personal, true]])
    expect(wrapper.emitted('update:open')).toEqual([[false]])
    wrapper.unmount()
  })

  it('starts from blank when asked to', async () => {
    const wrapper = render()
    const blank = dialog().querySelectorAll<HTMLInputElement>('input[type="radio"]')[1]
    blank.checked = true
    blank.dispatchEvent(new Event('change'))
    await flushPromises()

    submit()
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/users/7/personal-segment', { copy: false })
    wrapper.unmount()
  })

  it('stays open and reports the failure when the server refuses', async () => {
    mocks.post.mockRejectedValue(new ApiError(409, { code: 'conflict', message: 'already personal' }))
    const wrapper = render()
    submit()
    await flushPromises()
    expect(mocks.toast).toHaveBeenCalledWith('already personal', 'error')
    expect(wrapper.emitted('done')).toBeUndefined()
    expect(wrapper.emitted('update:open')).toBeUndefined()
    wrapper.unmount()
  })

  it('asks whom first when the opener names nobody', async () => {
    const wrapper = render(null)
    expect(document.body.textContent).toContain('给某个用户单独设倍率')
    expect(document.body.querySelector<HTMLButtonElement>('[data-testid="personal-convert-submit"]')!.disabled).toBe(true)
    dialog().querySelector<HTMLElement>('[data-testid="pick-7"]')!.click()
    await flushPromises()
    expect(document.body.textContent).toContain('给 amy@example.com 单独设倍率')
    expect(dialog().textContent).toContain('复制「VIP」现在的设置')
    submit()
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/users/7/personal-segment', { copy: true })
    expect(wrapper.emitted('done')).toEqual([[personal, true]])
    wrapper.unmount()
  })

  it('answers at once with the personal segment a picked user has already', async () => {
    const wrapper = render(null)
    dialog().querySelector<HTMLElement>('[data-testid="pick-8"]')!.click()
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(wrapper.emitted('done')).toEqual([[{ id: 32, kind: 'personal', name: 'bo@example.com' }, false]])
    expect(wrapper.emitted('update:open')).toEqual([[false]])
    wrapper.unmount()
  })
})
