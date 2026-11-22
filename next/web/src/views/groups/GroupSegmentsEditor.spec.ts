import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import type { Segment, SegmentGroupSetting, SegmentRef } from '@/api/pricing'
import AddSelect from '@/components/pricing/AddSelect.vue'
import GroupSegmentsEditor from './GroupSegmentsEditor.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), toast: vi.fn(), firstPage: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get, put: mocks.put } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: mocks.firstPage }))
enableAutoUnmount(afterEach)

const vip: SegmentRef = { id: 2, kind: 'named', name: 'VIP' }
const staff: SegmentRef = { id: 4, kind: 'named', name: 'Staff' }
const solo: SegmentRef = { id: 104, kind: 'personal', name: 'a@b.c', user_id: 9 }

function setting(segment: SegmentRef, over: Partial<SegmentGroupSetting> = {}): SegmentGroupSetting {
  return {
    segment,
    group: { id: 3, name: 'team', description: '', status: 'active', visibility: 'restricted', rate_multiplier: '0.8' },
    accessible: false,
    granted: false,
    rate_override: null,
    rate: { value: '0.8', source: 'group', group_default: '0.8', discount: '1' },
    member_count: 2,
    ...over
  }
}
const saved = () => [setting(vip, { granted: true, rate: { value: '0.72', source: 'discount', group_default: '0.8', discount: '0.9' } }), setting(staff)]

async function render(props: { groupId: number | null; visibility?: Group['visibility']; defaultRate?: string; editable?: boolean }) {
  const w = mount(GroupSegmentsEditor, {
    props: { visibility: 'restricted', defaultRate: '0.8', editable: true, ...props },
    global: { plugins: [i18n], stubs: { PersonalSegmentPicker: true } }
  })
  await flushPromises()
  const row = (id: number) => w.get(`[data-segment="${id}"]`)
  const listed = () => w.findAll('[data-testid="access-rate-row"]').map((r) => Number(r.attributes('data-segment')))
  const rate = (id: number) => row(id).get('[data-testid="access-rate-value"]').text()
  const source = (id: number) => row(id).get('[data-testid="access-rate-source"]').text()
  const addSelect = () => w.getComponent(AddSelect)
  const add = async (id: number) => {
    addSelect().vm.$emit('add', id)
    await flushPromises()
  }
  return { w, row, listed, rate, source, addSelect, add }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  mocks.get.mockReset().mockResolvedValue(saved())
  mocks.put.mockReset()
  mocks.toast.mockReset()
  mocks.firstPage.mockReset()
})

describe('group user groups editor', () => {
  it('lists only the user groups set apart here, with the rate each pays and why', async () => {
    const { w, row, listed, rate, source, addSelect } = await render({ groupId: 3 })
    expect(mocks.get).toHaveBeenCalledWith('/groups/3/segments', undefined)
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['用户组', '可用', '倍率', '实付倍率', '操作'])
    expect(w.get('[data-testid="group-segments-intro"]').text()).toBe('私有只有开通的用户组能用这个分组。 分组倍率 ×0.8')
    // Staff has nothing here: it is not listed, and may be added.
    expect(listed()).toEqual([2])
    expect(addSelect().props('options')).toEqual([{ value: 4, label: 'Staff' }])
    expect(row(2).text()).toContain('VIP')
    expect(row(2).text()).toContain('2 人')
    expect(row(2).text()).toContain('折扣 0.9')
    expect(row(2).get('[data-testid="access-rate-grant"]').element.parentElement?.textContent).toContain('已开通')
    expect(row(2).get('[data-testid="rate-override-input"]').attributes('placeholder')).toBe('跟随 0.72')
    expect([rate(2), source(2)]).toEqual(['×0.72', '分组倍率 0.8 × 折扣 0.9'])
    expect(w.find('[data-testid="group-segments-nobody"]').exists()).toBe(false)
    expect(w.find('[data-testid="group-segments-kept"]').exists()).toBe(false)
    // Every row goes to the parent, listed or not.
    expect(w.emitted('rows')?.at(-1)?.[0]).toHaveLength(2)
  })

  it('adds a user group, and removing it clears what it sets', async () => {
    const { w, row, listed, source, add, addSelect } = await render({ groupId: 3 })
    await add(4)
    expect(listed()).toEqual([2, 4])
    expect(addSelect().props('options')).toEqual([])
    // Not enabled: it cannot pay anything.
    expect(row(4).get('[data-testid="access-rate-grant"]').element.parentElement?.textContent).toContain('未开通')
    expect(source(4)).toBe('用不了')
    expect(w.vm.dirty).toBe(false)
    // Added with nothing set, it goes at once.
    await row(4).get('[data-testid="access-rate-remove"]').trigger('click')
    expect(listed()).toEqual([2])
    // A saved row stays, marked, until the removal is saved.
    await row(2).get('[data-testid="access-rate-remove"]').trigger('click')
    expect(row(2).find('[data-testid="access-rate-unsaved"]').exists()).toBe(true)
    mocks.put.mockResolvedValue([setting(vip), setting(staff)])
    expect(await w.vm.save(3)).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/groups/3/segments', { items: [{ segment_id: 2, granted: false }] }, { query: undefined })
    await flushPromises()
    expect(listed()).toEqual([])
    expect(w.get('table').text()).toContain('还没有开通给任何用户组或用户，现在没有人能用。')
  })

  it('follows the visibility and the group rate being edited, keeping the grants on switches nobody can turn', async () => {
    const { w, row, rate, add, source } = await render({ groupId: 3, visibility: 'public', defaultRate: '2' })
    await add(4)
    expect(w.findAll('[data-testid="access-rate-public"]').map((b) => b.text())).toEqual(['公开', '公开'])
    const grant = row(2).get<HTMLInputElement>('[data-testid="access-rate-grant"]')
    expect([grant.element.checked, grant.element.disabled]).toEqual([true, true])
    expect(w.get('[data-testid="group-segments-kept"]').text()).toBe('公开分组所有人可用；改回私有时这些开通会恢复。')
    expect(w.get('[data-testid="group-segments-intro"]').text()).toContain('所有人都能用这个分组。')
    expect(rate(2)).toBe('×1.8')
    expect([rate(4), source(4)]).toEqual(['×2', '跟随分组倍率'])
  })

  it('says a group with nothing set follows the group rate, and warns when listed rows enable nobody', async () => {
    mocks.get.mockResolvedValue([setting(vip), setting(staff)])
    const { w, row, add } = await render({ groupId: 3, visibility: 'public' })
    expect(w.get('table').text()).toContain('还没有单独设置：所有人按分组倍率 × 各自的折扣计费。')
    await w.setProps({ visibility: 'restricted' })
    expect(w.find('[data-testid="group-segments-nobody"]').exists()).toBe(false)
    await add(4)
    expect(w.get('[data-testid="group-segments-nobody"]').text()).toContain('用“添加用户组”或“添加用户”来开通。')
    await row(4).get('[data-testid="access-rate-grant"]').setValue(true)
    expect(w.find('[data-testid="group-segments-nobody"]').exists()).toBe(false)
  })

  it('keeps edits in drafts and saves the changed rows only', async () => {
    const { w, row, rate, source, add } = await render({ groupId: 3 })
    expect(w.vm.dirty).toBe(false)
    await add(4)
    await row(4).get('[data-testid="access-rate-grant"]').setValue(true)
    await row(2).get('[data-testid="rate-override-input"]').setValue('0.5')
    expect([rate(2), source(2)]).toEqual(['×0.5', '单独设的倍率'])
    expect(rate(4)).toBe('×0.8')
    expect(w.vm.dirty).toBe(true)
    expect(w.findAll('[data-testid="access-rate-unsaved"]')).toHaveLength(2)
    expect(w.emitted('rows')?.at(-1)?.[0]).toEqual(expect.arrayContaining([expect.objectContaining({ id: 2, accessible: true, rate: expect.objectContaining({ value: '0.5' }) })]))

    mocks.put.mockResolvedValue([setting(vip, { granted: true, rate_override: '0.5' }), setting(staff, { granted: true })])
    expect(await w.vm.save(3)).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/groups/3/segments', { items: [{ segment_id: 4, granted: true }, { segment_id: 2, rate_override: '0.5' }] }, { query: undefined })
    await flushPromises()
    expect(w.vm.dirty).toBe(false)
    expect(w.find('[data-testid="access-rate-unsaved"]').exists()).toBe(false)
  })

  it('sends nothing without changes, and nothing while a rate is not one', async () => {
    const { w, row } = await render({ groupId: 3 })
    expect(await w.vm.save(3)).toBe(true)
    await row(2).get('[data-testid="rate-override-input"]').setValue('abc')
    expect(row(2).find('[data-testid="rate-override-invalid"]').exists()).toBe(true)
    expect(await w.vm.save(3)).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining('VIP'), 'error')
    expect(w.vm.dirty).toBe(true)
  })

  it('starts a group not created yet from the named user groups and saves under the id it gets', async () => {
    mocks.firstPage.mockResolvedValue([{ ...vip, discount: '0.9', member_count: 3 } as Segment])
    const { w, row, rate, listed, add } = await render({ groupId: null, defaultRate: '1' })
    expect(mocks.firstPage).toHaveBeenCalledWith('/segments')
    expect(mocks.get).not.toHaveBeenCalled()
    expect(listed()).toEqual([])
    await add(2)
    await row(2).get('[data-testid="access-rate-grant"]').setValue(true)
    expect(rate(2)).toBe('×0.9')
    mocks.put.mockResolvedValue([])
    await w.vm.save(9)
    expect(mocks.put).toHaveBeenCalledWith('/groups/9/segments', { items: [{ segment_id: 2, granted: true }] }, { query: undefined })
  })

  it('lists a personal user group picked here and keeps the drafts while it is fetched', async () => {
    const { w, row, add } = await render({ groupId: 3 })
    await add(4)
    await row(4).get('[data-testid="access-rate-grant"]').setValue(true)
    mocks.get.mockResolvedValue([...saved(), setting(solo)])
    w.findComponent({ name: 'PersonalSegmentPicker' }).vm.$emit('select', solo)
    await flushPromises()
    expect(mocks.get).toHaveBeenLastCalledWith('/groups/3/segments', { include: '104' })
    expect(row(104).get('[data-testid="segment-badge"]').attributes('data-kind')).toBe('personal')
    expect(row(104).text()).toContain('a@b.c')
    expect(row(4).find('[data-testid="access-rate-unsaved"]').exists()).toBe(true)

    await row(104).get('[data-testid="access-rate-grant"]').setValue(true)
    mocks.put.mockResolvedValue([])
    await w.vm.save(3)
    expect(mocks.put.mock.calls[0][2]).toEqual({ query: { include: '104' } })
  })

  it('lists a personal user group picked for a group not created yet', async () => {
    mocks.firstPage.mockResolvedValue([{ ...vip, discount: '1', member_count: 3 } as Segment])
    const { w, row } = await render({ groupId: null })
    mocks.get.mockResolvedValue({ ...solo, discount: '0.5', member_count: 1 } as Segment)
    w.findComponent({ name: 'PersonalSegmentPicker' }).vm.$emit('select', solo)
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('/segments/104')
    expect(row(104).get('[data-testid="rate-override-input"]').attributes('placeholder')).toBe('跟随 0.4')
  })

  it('only shows the rows in words to a caller who may not edit them', async () => {
    mocks.get.mockResolvedValue([setting(vip, { rate_override: '0.5' }), setting(staff)])
    const { w, row, listed } = await render({ groupId: 3, editable: false })
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['用户组', '可用', '倍率', '实付倍率'])
    expect(w.find('[data-testid="access-rate-grant"], [data-testid="rate-override-input"], [data-testid="group-segments-add"]').exists()).toBe(false)
    expect(w.findComponent({ name: 'PersonalSegmentPicker' }).exists()).toBe(false)
    expect(listed()).toEqual([2])
    expect(row(2).get('[data-testid="access-rate-access"]').text()).toBe('未开通')
    expect(row(2).get('[data-testid="access-rate-override"]').text()).toBe('×0.5')
    // Nobody is enabled, and there is nothing to add with.
    expect(w.get('[data-testid="group-segments-nobody"]').text()).toBe('这个私有分组还没有开通给任何用户组，现在没有人能用。')
  })
})
