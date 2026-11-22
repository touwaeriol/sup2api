import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { h, type Component, type SetupContext } from 'vue'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import GroupEditModal from './GroupEditModal.vue'
import type { GroupEditTab } from './groupEditTabs'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), toast: vi.fn(), confirm: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get, post: mocks.post, patch: mocks.patch } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a), confirm: (...a: unknown[]) => mocks.confirm(...a) }))
enableAutoUnmount(afterEach)

/** What ran, in order: the group's own request and the price table's save with the id it got. */
const calls: string[] = []
/** The price table: what its save answers, whether it has drafts and whether they can be saved as they are. */
const table = { saves: true, dirty: false, valid: true }
const pricing: Component = {
  props: { groupId: null, rate: String, editable: Boolean },
  setup(_: unknown, { expose }: SetupContext) {
    const save = async (gid: number) => (calls.push(`pricing:${gid}`), table.saves)
    expose({ save, check: () => table.valid, get dirty() { return table.dirty } })
    return () => h('div')
  }
}
const policy: Component = { props: ['options'], setup: (_: unknown, { expose }: SetupContext) => (expose({ commit: () => true }), () => h('div')) }

const vip = { id: 2, name: 'vip', description: '', status: 'active', rate_multiplier: '0.8', visibility: 'restricted', model_allowlist: [] } as Group

async function render(group: Group | null, initialTab?: GroupEditTab) {
  const w = mount(GroupEditModal, {
    props: { open: true, group, initialTab },
    global: {
      plugins: [i18n],
      stubs: { Teleport: true, EnvironmentEditor: true, GroupModelPolicyEditor: policy, ModelPriceTable: pricing }
    }
  })
  await flushPromises()
  const selected = () => w.get('[role="tab"][aria-selected="true"]').text()
  const submit = async () => {
    await w.get('[data-testid="group-edit-submit"]').trigger('click')
    await flushPromises()
  }
  return { w, selected, submit }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  calls.length = 0
  Object.assign(table, { saves: true, dirty: false, valid: true })
  mocks.get.mockReset().mockResolvedValue({ models: [], unrestricted_accounts: 0 })
  mocks.toast.mockReset()
  mocks.confirm.mockReset().mockResolvedValue(false)
  mocks.patch.mockReset().mockImplementation(async (path: string) => void calls.push(`PATCH ${path}`))
  mocks.post.mockReset().mockImplementation(async (path: string) => (calls.push(`POST ${path}`), { ...vip, id: 9, name: 'new' }))
})

describe('group editor', () => {
  it('shows the four tabs and opens on the one asked for', async () => {
    const { w, selected } = await render(vip, 'pricing')
    expect(w.findAll('[role="tab"]').map((t) => t.text())).toEqual(['基本信息', '模型名单', '价格', '高级'])
    expect(selected()).toBe('价格')
    expect(w.get('[data-testid="group-edit-save-hint"]').text()).toBe('改动点“保存”后一起生效')
  })

  it('prices a new group in its own table, paid at the group rate of the form once it is one', async () => {
    const { w } = await render(null)
    expect(w.get('[data-testid="group-edit-save-hint"]').text()).toBe('还没保存：点“新建”时一起保存')
    const table = () => w.findComponent(pricing)
    expect(table().props()).toMatchObject({ groupId: null, rate: '1', editable: true })
    // A new group has no accounts: its model policy offers no models to pick and nothing is asked for.
    expect(w.findComponent(policy).props('options')).toEqual([])
    expect(mocks.get).not.toHaveBeenCalled()
    await w.get('[data-testid="group-default-rate"]').setValue('0.9')
    expect(table().props('rate')).toBe('0.9')
    await w.get('[data-testid="group-default-rate"]').setValue('1.')
    expect(table().props('rate')).toBe('')
  })

  it('lists the models of the group opened last, whichever answers first', async () => {
    const answers: Record<string, (v: object) => void> = {}
    mocks.get.mockImplementation((path: string) => new Promise((r) => (answers[path] = r)))
    const { w } = await render(vip)
    await w.setProps({ open: false })
    await w.setProps({ open: true, group: { ...vip, id: 3, name: 'trial' } })
    await flushPromises()
    answers['/groups/3/models']({ models: ['new'], unrestricted_accounts: 0 })
    answers['/groups/2/models']({ models: ['old'], unrestricted_accounts: 0 })
    await flushPromises()
    expect(w.findComponent(policy).props('options')).toEqual(['new'])
  })

  it('saves the group, then its model prices', async () => {
    const { w, submit } = await render(vip)
    await submit()
    expect(calls).toEqual(['PATCH /groups/2', 'pricing:2'])
    expect(mocks.patch.mock.calls[0][1]).toMatchObject({ name: 'vip', rate_multiplier: '0.8', visibility: 'restricted' })
    expect(w.emitted('update:open')).toEqual([[false]])
    expect(w.emitted('saved')).toHaveLength(1)
  })

  it('stays open on the tab of the step that failed, and saves the created group again instead of creating another', async () => {
    const { w, selected, submit } = await render(null)
    await w.get('input').setValue('new')
    table.saves = false
    await submit()
    expect(calls).toEqual(['POST /groups', 'pricing:9'])
    expect(w.emitted('update:open')).toBeUndefined()
    // The list shows the group that was created all the same.
    expect(w.emitted('saved')).toHaveLength(1)
    expect(selected()).toBe('价格')

    calls.length = 0
    table.saves = true
    await submit()
    expect(calls).toEqual(['PATCH /groups/9', 'pricing:9'])
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('creates nothing while a model price cannot be saved, and opens its tab', async () => {
    const { w, selected, submit } = await render(null)
    await w.get('input').setValue('new')
    table.valid = false
    await submit()
    expect(calls).toEqual([])
    expect(selected()).toBe('价格')
    expect(w.emitted('saved')).toBeUndefined()
  })

  it('opens the tab of a field that is wrong before saving anything', async () => {
    const { w, selected, submit } = await render(vip, 'advanced')
    await w.get('[data-testid="group-default-rate"]').setValue('-1')
    await submit()
    expect(selected()).toContain('价格')
    expect(w.text()).toContain('填 0 到 999999.9999 之间的数字')
    expect(calls).toEqual([])
  })

  it('marks the tabs with unsaved changes', async () => {
    table.dirty = true
    const { w } = await render(vip)
    const marked = () => w.findAll('[role="tab"]').filter((t) => t.text().includes('•')).map((t) => t.text().replace(/\s*•/, ''))
    expect(marked()).toEqual(['价格'])
    await w.get('input').setValue('renamed')
    expect(marked()).toEqual(['基本信息', '价格'])
  })

  it('takes a group rate as the server does: up to four decimals, below 1000000', async () => {
    const { w, submit } = await render(vip)
    await w.get('[data-testid="group-default-rate"]').setValue('0.12345')
    await submit()
    expect(calls).toEqual([])
    expect(w.text()).toContain('最多 4 位小数')
    await w.get('[data-testid="group-default-rate"]').setValue('0.1234')
    await submit()
    expect(calls[0]).toBe('PATCH /groups/2')
  })

  it('asks before closing with a tab not saved, and closes at once without', async () => {
    const plain = await render(vip)
    await plain.w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(plain.w.emitted('update:open')).toEqual([[false]])

    table.dirty = true
    const { w } = await render(vip)
    await w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的改动？', cancelText: '继续编辑' }))
    expect(w.emitted('update:open')).toBeUndefined()
    mocks.confirm.mockResolvedValue(true)
    await w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('counts the fields as saved once the group is, though a later step keeps the dialog open', async () => {
    const { w, submit } = await render(vip)
    await w.get('[data-testid="group-default-rate"]').setValue('0.9')
    table.saves = false
    await submit()
    const marked = w.findAll('[role="tab"]').filter((t) => t.text().includes('•'))
    expect(marked).toHaveLength(0)
  })
})
