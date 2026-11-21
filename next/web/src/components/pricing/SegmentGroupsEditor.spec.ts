import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import type { SegmentGroupSetting } from '@/api/pricing'
import SegmentGroupsEditor from './SegmentGroupsEditor.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), toast: vi.fn(), allPages: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get, put: mocks.put } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a) }))
vi.mock('@/composables/lookups', () => ({ fetchAllPages: mocks.allPages }))

i18n.global.locale.value = 'en'

function setting(id: number, visibility: 'public' | 'restricted', over: Partial<SegmentGroupSetting> = {}): SegmentGroupSetting {
  return {
    segment: { id: 2, kind: 'named', name: 'VIP' },
    group: { id, name: `g${id}`, description: '', status: 'active', visibility, rate_multiplier: '1.2' },
    accessible: visibility === 'public',
    granted: false,
    rate_override: null,
    rate: { value: '1.08', source: 'discount', group_default: '1.2', discount: '0.9' },
    ...over
  }
}
const saved = () => [setting(1, 'public'), setting(2, 'restricted', { rate_override: '0.5' })]
const group = (id: number, visibility: Group['visibility'], rate: string): Group => ({ id, name: `g${id}`, description: '', status: 'active', visibility, rate_multiplier: rate, model_allowlist: [] })

async function render(props: { segmentId: number | null; editable?: boolean; discount?: string }) {
  const w = mount(SegmentGroupsEditor, { props, global: { plugins: [i18n] } })
  await flushPromises()
  const row = (id: number) => w.get(`[data-group="${id}"]`)
  const rate = (id: number) => row(id).get('[data-testid="access-rate-value"]').text()
  const source = (id: number) => row(id).get('[data-testid="access-rate-source"]').text()
  return { w, row, rate, source }
}

beforeEach(() => {
  mocks.get.mockReset().mockResolvedValue(saved())
  mocks.put.mockReset()
  mocks.toast.mockReset()
  mocks.allPages.mockReset()
})

describe('the groups of a segment', () => {
  it('lists what the segment gets in each group, in words without fields', async () => {
    const { w, row, source } = await render({ segmentId: 2 })
    expect(mocks.get).toHaveBeenCalledWith('/segments/2/groups')
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['Group', 'Available', 'Rate', 'Paid rate'])
    expect(row(1).text()).toContain('Group rate ×1.2')
    expect(row(1).get('[data-testid="access-rate-public"]').text()).toBe('Public')
    expect(row(1).find('[data-testid="access-rate-access"]').exists()).toBe(false)
    expect(row(1).get('[data-testid="access-rate-override"]').text()).toBe('Follows')
    expect(source(1)).toBe('Group rate 1.2 × discount 0.9')
    expect(row(2).get('[data-testid="segment-group-private"]').text()).toBe('Private')
    expect(row(2).get('[data-testid="access-rate-access"]').text()).toBe('Not enabled')
    expect(row(2).get('[data-testid="access-rate-override"]').text()).toBe('×0.5')
    expect(source(2)).toBe('Not available')
    expect(w.find('[data-testid="rate-override-input"], [data-testid="access-rate-grant"]').exists()).toBe(false)
  })

  it('follows the discount of the form before it is saved', async () => {
    const { w, row, rate } = await render({ segmentId: 2, discount: '0.5', editable: true })
    expect(rate(1)).toBe('×0.6')
    expect(row(1).get('[data-testid="rate-override-input"]').attributes('placeholder')).toBe('Follows 0.6')
    await w.setProps({ discount: '1' })
    expect(rate(1)).toBe('×1.2')
  })

  it('keeps grants and rates in drafts and saves the changed groups', async () => {
    const { w, row, rate, source } = await render({ segmentId: 2, editable: true })
    // A public group has no switch on this side: everyone may use it.
    expect(row(1).find('[data-testid="access-rate-grant"]').exists()).toBe(false)
    await row(1).get('[data-testid="rate-override-input"]').setValue('0.8')
    await row(2).get('[data-testid="access-rate-grant"]').setValue(true)
    expect([rate(1), source(1)]).toEqual(['×0.8', 'Own rate'])
    expect([rate(2), source(2)]).toEqual(['×0.5', 'Own rate'])
    expect(w.findAll('[data-testid="access-rate-unsaved"]')).toHaveLength(2)
    expect(w.vm.dirty).toBe(true)

    mocks.put.mockResolvedValue([setting(1, 'public', { rate_override: '0.8' }), setting(2, 'restricted', { granted: true, accessible: true })])
    expect(await w.vm.save()).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/segments/2/groups', {
      items: [
        { group_id: 1, rate_override: '0.8' },
        { group_id: 2, granted: true }
      ]
    })
    await flushPromises()
    expect(w.vm.dirty).toBe(false)
    expect(w.find('[data-testid="access-rate-unsaved"]').exists()).toBe(false)
  })

  it('starts a segment not created yet from every group and saves under the id it gets', async () => {
    mocks.allPages.mockResolvedValue([group(1, 'public', '1'), group(3, 'restricted', '1.5')])
    const { w, row, rate, source } = await render({ segmentId: null, editable: true, discount: '0.9' })
    expect(mocks.allPages).toHaveBeenCalledWith('/groups')
    expect(mocks.get).not.toHaveBeenCalled()
    expect(rate(1)).toBe('×0.9')
    expect(source(3)).toBe('Not available')
    expect(row(3).get('[data-testid="rate-override-input"]').attributes('placeholder')).toBe('Follows 1.35')
    await row(3).get('[data-testid="access-rate-grant"]').setValue(true)
    expect(rate(3)).toBe('×1.35')
    expect(await w.vm.save(null)).toBe(true)
    expect(mocks.put).not.toHaveBeenCalled()

    mocks.put.mockResolvedValue([])
    expect(await w.vm.save(9)).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/segments/9/groups', { items: [{ group_id: 3, granted: true }] })
  })

  it('sends nothing without a change, and refuses a rate that is not one', async () => {
    const { w, row } = await render({ segmentId: 2, editable: true })
    expect(await w.vm.save()).toBe(true)
    await row(1).get('[data-testid="rate-override-input"]').setValue('abc')
    expect(await w.vm.save()).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining('g1'), 'error')
  })

  it('keeps the drafts when saving fails', async () => {
    const { w, row } = await render({ segmentId: 2, editable: true })
    await row(1).get('[data-testid="rate-override-input"]').setValue('0.8')
    mocks.put.mockRejectedValue(new Error('down'))
    await expect(w.vm.save()).rejects.toThrow('down')
    expect(w.vm.dirty).toBe(true)
  })

  it('holds its fields still while the parent saves', async () => {
    const { w, row } = await render({ segmentId: 2, editable: true, discount: '0.9' })
    await w.setProps({ disabled: true })
    expect(row(1).get('[data-testid="rate-override-input"]').attributes('disabled')).toBeDefined()
    expect(row(2).get('[data-testid="access-rate-grant"]').html()).toContain('disabled')
  })
})
