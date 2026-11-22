import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { SDropdown } from '@sub2api/ui'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import { i18n } from '@/i18n'
import type { Segment } from '@/api/pricing'
import PersonalRemoveDialog from '@/components/pricing/PersonalRemoveDialog.vue'
import PersonalSegmentPicker from '@/components/pricing/PersonalSegmentPicker.vue'
import SegmentEditor from './SegmentEditor.vue'
import SegmentsView from './SegmentsView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), list: vi.fn(), replace: vi.fn(), useList: vi.fn(), reload: vi.fn(), denied: new Set<string>() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get, list: mocks.list } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !mocks.denied.has(key) }) }))
vi.mock('@/components/pricing/segmentLookup', () => ({ useSegmentsLookup: vi.fn() }))
const route = reactive<{ query: Record<string, string | undefined> }>({ query: {} })
vi.mock('vue-router', async (original) => ({ ...(await original<object>()), useRoute: () => route, useRouter: () => ({ replace: mocks.replace }) }))

const segment = (id: number, name: string, over: Partial<Segment> = {}): Segment => ({ id, kind: 'named', name, description: '', discount: '1', is_default: id === 1, member_count: 3, granted_groups: [], rate_overrides: [], ...over })
const items = ref<Segment[]>([])
const filters = reactive<Record<string, string>>({})
vi.mock('@/composables/useList', () => ({
  useList: (...a: unknown[]) => (mocks.useList(...a), { items, filters, loading: ref(false), error: ref(null), page: ref(1), pageSize: ref(20), total: ref(2), reload: mocks.reload })
}))
const solo = segment(104, 'a@b.c', { kind: 'personal', user_id: 9, member_count: 1, rate_overrides: [{ id: 1, name: 'Default', rate: '0.5' }, { id: 3, name: 'Pro', rate: '0.8' }], user: { id: 9, email: 'a@b.c', display_name: '' }, origin: { id: 2, kind: 'named', name: 'VIP' } })
enableAutoUnmount(afterEach)

async function render() {
  const w = mount(SegmentsView, { global: { plugins: [i18n], stubs: { Teleport: true, SegmentEditor: true, PersonalRemoveDialog: true, PersonalSegmentPicker: true, PriceSimulatorModal: true } } })
  await flushPromises()
  return { w, editor: () => w.getComponent(SegmentEditor).props() }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  mocks.denied.clear()
  route.query = {}
  items.value = [segment(1, '默认'), segment(2, 'VIP', { discount: '0.9', granted_groups: [{ id: 3, name: 'Claude' }] }), solo]
  Object.assign(filters, { q: '', kind: 'all' })
  mocks.replace.mockReset()
  mocks.reload.mockReset()
  mocks.useList.mockReset()
  mocks.list.mockReset().mockImplementation(async (_: string, q: { kind: string }) => ({ items: [], page: { total: q.kind === 'named' ? 2 : 1 } }))
  mocks.get.mockReset().mockImplementation(async (path: string) => items.value.find((s) => path === `/segments/${s.id}`))
})

describe('user groups page', () => {
  /** The actions a row's menu offers. */
  const menu = (w: Awaited<ReturnType<typeof render>>['w'], id: number) =>
    w.get(`[data-segment="${id}"]`).getComponent(SDropdown).props('actions').filter((a: { hidden?: boolean }) => !a.hidden).map((a: { key: string }) => a.key)

  it('lists the user groups and the users priced on their own in one table', async () => {
    const { w } = await render()
    expect(mocks.useList).toHaveBeenCalledWith('/segments', { q: '', kind: 'all' })
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['名称', '成员', '折扣', '可用的私有分组', '单独倍率', '操作'])
    expect(w.get('[data-segment="1"]').get('[data-testid="segment-default"]').text()).toBe('新用户默认')
    const vip = w.get('[data-segment="2"]')
    expect(vip.get('[data-testid="segment-discount-cell"]').text()).toBe('×0.9')
    expect(vip.text()).toContain('Claude')
    expect(vip.get('[data-testid="segment-overrides-cell"]').text()).toBe('无')
    const personal = w.get('[data-segment="104"]')
    expect(personal.get('[data-testid="segment-name-cell"]').text()).toBe('a@b.c')
    expect(personal.get('[data-testid="segment-badge"]').text()).toBe('专享')
    expect(personal.get('[data-testid="segment-origin"]').text()).toBe('原来在 VIP')
    expect(personal.find('[data-testid="segment-members-link"]').exists()).toBe(false)
    expect(personal.get('[data-testid="segment-overrides-cell"]').text()).toContain('Default ×0.5')
  })

  it('narrows the list to one kind, counting each once the list is loaded', async () => {
    const { w } = await render()
    items.value = [...items.value]
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledWith('/segments', { kind: 'named', q: '', page_size: 1 })
    expect(mocks.list).toHaveBeenCalledWith('/segments', { kind: 'personal', q: '', page_size: 1 })
    const kinds = w.get('[data-testid="segment-kind"]')
    expect(kinds.findAll('label').map((l) => l.text())).toEqual(['全部 3', '用户组 2', '专享 1'])
    await kinds.findAll('input')[2].setValue(true)
    expect(filters.kind).toBe('personal')
  })

  it('opens the editor of a row, and its members from the member count', async () => {
    const { w, editor } = await render()
    const vip = w.get('[data-segment="2"]')
    await vip.get('[data-testid="segment-edit"]').trigger('click')
    expect(editor()).toMatchObject({ open: true, segment: items.value[1], tab: 'basic', readonly: false })
    await vip.get('[data-testid="segment-members-link"]').trigger('click')
    expect(editor()).toMatchObject({ tab: 'members' })
  })

  it('opens the user group ?edit= names, a personal one too, and takes the parameter off the address bar', async () => {
    route.query = { edit: '2', q: 'x' }
    const { editor } = await render()
    expect(mocks.get).toHaveBeenCalledWith('/segments/2')
    expect(editor()).toMatchObject({ open: true, segment: items.value[1], tab: 'basic' })
    expect(mocks.replace).toHaveBeenCalledWith({ query: { q: 'x', edit: undefined } })

    route.query = { edit: '104' }
    const personal = await render()
    expect(personal.editor()).toMatchObject({ open: true, segment: solo })
  })

  it('moves a personal user back from his row or his editor', async () => {
    const { w, editor } = await render()
    expect(menu(w, 104)).toEqual(['moveBack'])
    w.get('[data-segment="104"]').getComponent(SDropdown).vm.$emit('select', 'moveBack')
    await flushPromises()
    const dialog = w.getComponent(PersonalRemoveDialog)
    expect(dialog.props()).toMatchObject({ open: true, user: { id: 9, email: 'a@b.c' }, origin: solo.origin })
    dialog.vm.$emit('done')
    expect(mocks.reload).toHaveBeenCalled()

    w.getComponent(SegmentEditor).vm.$emit('remove', solo)
    await flushPromises()
    expect(editor().open).toBe(false)
    expect(w.getComponent(PersonalRemoveDialog).props('open')).toBe(true)
  })

  it('opens the personal user group of the user given his own rates', async () => {
    const { w, editor } = await render()
    w.getComponent(PersonalSegmentPicker).vm.$emit('select', { id: 104, kind: 'personal', name: 'a@b.c' }, true)
    await flushPromises()
    expect(mocks.reload).toHaveBeenCalled()
    expect(editor()).toMatchObject({ open: true, segment: solo })
  })

  it('lets a caller who may not manage user groups look at one', async () => {
    mocks.denied.add('segment:manage')
    const { w, editor } = await render()
    expect(w.find('[data-testid="segment-create"]').exists()).toBe(false)
    expect(menu(w, 104)).toEqual([])
    const view = w.get('[data-segment="2"]').get('[data-testid="segment-edit"]')
    expect(view.text()).toBe('查看')
    await view.trigger('click')
    expect(editor()).toMatchObject({ open: true, readonly: true })
  })

  it('opens the members of a user group only for a caller who reads users', async () => {
    const all = await render()
    expect(menu(all.w, 2)).toEqual(['members', 'simulate', 'delete'])

    mocks.denied.add('user:read')
    const { w, editor } = await render()
    expect(menu(w, 2)).toEqual(['simulate', 'delete'])
    const vip = w.get('[data-segment="2"]')
    expect(vip.find('[data-testid="segment-members-link"]').exists()).toBe(false)
    expect(vip.get('[data-testid="segment-member-count"]').text()).toBe('3')
    expect(editor().open).toBe(false)
  })

  it('offers the price simulator, and mounts it, only to a caller who may quote in a group', async () => {
    const all = await render()
    expect(all.w.findComponent(PriceSimulatorModal).exists()).toBe(true)

    mocks.denied.add('group:read')
    const { w } = await render()
    expect(menu(w, 2)).toEqual(['members', 'delete'])
    // Mounted, it would ask for the groups and be refused.
    expect(w.findComponent(PriceSimulatorModal).exists()).toBe(false)
  })
})
