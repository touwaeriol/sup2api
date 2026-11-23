import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { h, type Component, type SetupContext } from 'vue'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import GroupEditModal from './GroupEditModal.vue'
import type { GroupEditTab } from './groupForm'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), toast: vi.fn(), confirm: vi.fn(), denied: new Set<string>() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get, post: mocks.post, patch: mocks.patch } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a), confirm: (...a: unknown[]) => mocks.confirm(...a) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !mocks.denied.has(key) }) }))
enableAutoUnmount(afterEach)

/** What a table on a tab holds: what it sends, whether it has drafts, whether they can be sent, and the errors it was given. */
interface Part {
  body: Record<string, unknown>
  dirty: boolean
  valid: boolean
  errors?: Record<string, string>
}
const parts = vi.hoisted(() => ({}) as Record<'pricing' | 'segments', Part>)
/** A table that holds a part of the group's request, as ModelPriceTable and GroupSegmentsEditor do. */
const tablePart = vi.hoisted(() => async (key: 'pricing' | 'segments', fields: string[], props: string[]): Promise<Component> => {
  const { h: render } = await import('vue')
  const { useTabbedFormPart: usePart } = await import('@sub2api/ui')
  return {
    props: [...props, 'editable', 'disabled', 'tab'],
    setup(p: Record<string, unknown>) {
      usePart({ tab: key, fields, body: () => parts[key].body, check: () => parts[key].valid, dirty: () => parts[key].dirty, applyErrors: (e) => (parts[key].errors = e) })
      return () => render('div', { 'data-testid': `stub-${key}`, ...Object.fromEntries(props.map((k) => [`data-${k}`, String(p[k])])) })
    }
  }
})
// Mocked as modules, not stubbed: a stub may be a new component on every render, and a part registers once per mount.
vi.mock('@/components/pricing/ModelPriceTable.vue', async () => ({ default: await tablePart('pricing', ['model_prices', 'model_rates'], ['groupId', 'baseRate']) }))
vi.mock('./GroupSegmentsEditor.vue', async () => ({ default: await tablePart('segments', ['segment_rates', 'segment_model_rates'], ['groupId', 'visibility', 'defaultRate']) }))
const policy: Component = { props: ['options'], setup: (_: unknown, { expose }: SetupContext) => (expose({ commit: () => true }), () => h('div')) }

const vip = { id: 2, name: 'vip', description: '', status: 'active', rate_multiplier: '0.8', visibility: 'restricted', model_allowlist: [] } as unknown as Group
const fieldError = (field: string, message: string) => new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field, code: 'invalid', message }] } })

/** The state of each tab: its label, and its mark (error, unsaved) if any. */
function tabsOf(w: VueWrapper) {
  return w.findAll('[role="tab"]').map((t) => {
    const mark = t.find('[data-testid^="tab-mark-"]')
    const label = mark.exists() ? t.text().replace(mark.text(), '').trim() : t.text()
    return mark.exists() ? `${label} (${mark.attributes('data-testid')!.replace('tab-mark-', '')})` : label
  })
}

async function render(group: Group | null, initialTab?: GroupEditTab) {
  const w = mount(GroupEditModal, {
    props: { open: true, group, initialTab },
    global: { plugins: [i18n], stubs: { Teleport: true, EnvironmentEditor: true, GroupModelPolicyEditor: policy } }
  })
  await flushPromises()
  const selected = () => w.get('[role="tab"][aria-selected="true"]').attributes('id')?.split('-tab-').pop()
  const submit = async () => {
    await w.get('[data-testid="group-edit-submit"]').trigger('click')
    await flushPromises()
  }
  return { w, selected, submit, tabs: () => tabsOf(w) }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  parts.pricing = { body: {}, dirty: false, valid: true }
  parts.segments = { body: {}, dirty: false, valid: true }
  mocks.get.mockReset().mockResolvedValue({ models: [], unrestricted_accounts: 0 })
  mocks.denied.clear()
  mocks.toast.mockReset()
  mocks.confirm.mockReset().mockResolvedValue(false)
  mocks.patch.mockReset().mockResolvedValue(vip)
  mocks.post.mockReset().mockResolvedValue({ ...vip, id: 9, name: 'new' })
})

describe('group editor', () => {
  it('shows no user group rates to a caller who may not read user groups', async () => {
    mocks.denied.add('segment:read')
    const { w, selected, tabs } = await render(vip, 'segments')
    expect(tabs()).toEqual(['基本信息', '模型名单', '价格', '高级'])
    expect(selected()).toBe('basic')
    expect(w.find('[data-testid="stub-segments"]').exists()).toBe(false)
  })

  it('shows the five tabs and opens on the one asked for', async () => {
    const { w, selected, tabs } = await render(vip, 'pricing')
    expect(tabs()).toEqual(['基本信息', '模型名单', '价格', '用户分组倍率', '高级'])
    expect(selected()).toBe('pricing')
    expect(w.get('[data-testid="group-edit-save-hint"]').text()).toBe('各标签的改动点“保存”后一次提交、一起生效')
  })

  it('lets every tab of a new group be filled in at once, the tables following the form', async () => {
    const { w } = await render(null)
    expect(w.get('[data-testid="group-edit-save-hint"]').text()).toBe('还没保存：各标签都可以先填，点“新建”时一次提交')
    const table = () => w.get('[data-testid="stub-pricing"]')
    expect(table().attributes()).toMatchObject({ 'data-groupid': 'null', 'data-baserate': '1' })
    // A new group has no accounts: its model policy offers no models to pick and nothing is asked for.
    expect(w.findComponent(policy).props('options')).toEqual([])
    expect(mocks.get).not.toHaveBeenCalled()
    await w.get('[data-testid="group-default-rate"]').setValue('0.9')
    expect(table().attributes('data-baserate')).toBe('0.9')
    await w.get('[data-testid="group-default-rate"]').setValue('1.')
    expect(table().attributes('data-baserate')).toBe('')
    expect(w.get('[data-testid="stub-segments"]').attributes()).toMatchObject({ 'data-visibility': 'public', 'data-defaultrate': '1.' })
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

  it('creates the group with its prices and user group rates in one request', async () => {
    const { w, submit } = await render(null)
    await w.get('[data-testid="group-name"]').setValue(' new ')
    parts.pricing.body = { model_rates: [{ model: 'gpt-4o', rate: '1.2' }] }
    parts.segments.body = { segment_rates: [{ segment_id: 2, granted: true, rate: '0.7' }] }
    await submit()
    expect(mocks.post).toHaveBeenCalledTimes(1)
    expect(mocks.post).toHaveBeenCalledWith('/groups', {
      name: 'new', description: '', status: 'active', visibility: 'public',
      model_allowlist: [], model_filter_mode: 'blacklist', rate_multiplier: '1', environment: {}, conversion_loss_policy: null,
      model_rates: [{ model: 'gpt-4o', rate: '1.2' }], segment_rates: [{ segment_id: 2, granted: true, rate: '0.7' }]
    })
    expect(mocks.toast).toHaveBeenCalledWith('已创建', 'success')
    expect(w.emitted('saved')).toHaveLength(1)
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('sends of a saved group only what changed', async () => {
    const { submit } = await render(vip)
    await submit()
    expect(mocks.patch).toHaveBeenCalledWith('/groups/2', {})
    mocks.patch.mockClear()
    const again = await render(vip)
    await again.w.get('[data-testid="group-default-rate"]').setValue('0.9')
    parts.segments.body = { segment_rates: [{ segment_id: 2, rate: null }] }
    await again.submit()
    expect(mocks.patch).toHaveBeenCalledWith('/groups/2', { rate_multiplier: '0.9', segment_rates: [{ segment_id: 2, rate: null }] })
  })

  it('jumps to the tab of a rate that is wrong, marks it and sends nothing', async () => {
    const { w, selected, submit, tabs } = await render(vip, 'advanced')
    await w.get('[data-testid="group-default-rate"]').setValue('0.12345')
    await submit()
    expect(selected()).toBe('pricing')
    expect(tabs()).toContain('价格 (error)')
    expect(w.text()).toContain('最多 4 位小数')
    expect(mocks.patch).not.toHaveBeenCalled()
    await w.get('[data-testid="group-default-rate"]').setValue('0.1234')
    await submit()
    expect(mocks.patch).toHaveBeenCalledWith('/groups/2', { rate_multiplier: '0.1234' })
  })

  it('creates nothing while a table on another tab cannot be sent, and opens that tab', async () => {
    const { w, selected, submit } = await render(null)
    await w.get('[data-testid="group-name"]').setValue('new')
    parts.segments.valid = false
    await submit()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(selected()).toBe('segments')
    expect(w.emitted('saved')).toBeUndefined()
  })

  it('takes the field errors of the answer to the tab that sent them', async () => {
    const { w, selected, submit, tabs } = await render(vip)
    mocks.patch.mockRejectedValueOnce(fieldError('segment_rates.0.rate', 'too low'))
    await submit()
    expect(selected()).toBe('segments')
    expect(parts.segments.errors).toEqual({ 'segment_rates.0.rate': 'too low' })
    expect(tabs()).toContain('用户分组倍率 (error)')
    expect(w.emitted('update:open')).toBeUndefined()

    mocks.patch.mockRejectedValueOnce(fieldError('name', 'name already in use'))
    await submit()
    expect(selected()).toBe('basic')
    expect(w.text()).toContain('name already in use')
  })

  it('stays open and says so when the request fails otherwise', async () => {
    const { w, submit } = await render(vip)
    mocks.patch.mockRejectedValueOnce(new ApiError(409, { code: 'conflict', message: 'busy' }))
    await submit()
    expect(mocks.toast).toHaveBeenCalledWith(expect.any(String), 'error')
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('marks the tabs with unsaved changes', async () => {
    parts.pricing.dirty = true
    const { w, tabs } = await render(vip)
    expect(tabs()).toEqual(['基本信息', '模型名单', '价格 (unsaved)', '用户分组倍率', '高级'])
    await w.get('[data-testid="group-name"]').setValue('renamed')
    expect(tabs()[0]).toBe('基本信息 (unsaved)')
  })

  it('asks before closing with a tab not saved, and closes at once without', async () => {
    const plain = await render(vip)
    await plain.w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(plain.w.emitted('update:open')).toEqual([[false]])

    parts.segments.dirty = true
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
})
