import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Component } from 'vue'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Segment } from '@/api/pricing'
import SegmentEditor from './SegmentEditor.vue'

const mocks = vi.hoisted(() => ({ post: vi.fn(), patch: vi.fn(), toast: vi.fn(), confirm: vi.fn(), denied: new Set<string>() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { post: mocks.post, patch: mocks.patch } }))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast: (...a: unknown[]) => mocks.toast(...a), confirm: (...a: unknown[]) => mocks.confirm(...a) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !mocks.denied.has(key) }) }))
enableAutoUnmount(afterEach)

/** What the rates on the groups tab hold: what they send, whether they have drafts, whether they can be sent, and the errors they were given. */
const groups = vi.hoisted(() => ({ body: {} as Record<string, unknown>, dirty: false, valid: true, errors: undefined as Record<string, string> | undefined }))
// Mocked as a module, not stubbed: a stub may be a new component on every render, and a part registers once per mount.
vi.mock('@/components/pricing/SegmentGroupsEditor.vue', async (): Promise<{ default: Component }> => {
  const { h } = await import('vue')
  const { useTabbedFormPart } = await import('@sub2api/ui')
  return {
    default: {
      props: ['segmentId', 'editable', 'disabled', 'tab'],
      setup(props: Record<string, unknown>) {
        useTabbedFormPart({ tab: 'groups', fields: ['group_rates', 'model_rates'], body: () => groups.body, check: () => groups.valid, dirty: () => groups.dirty, applyErrors: (e) => (groups.errors = e) }, () => !!props.editable)
        return () => h('div', { 'data-testid': 'groups', 'data-editable': String(props.editable), 'data-segment': String(props.segmentId) })
      }
    }
  }
})

const segment = (over: Partial<Segment> = {}): Segment => ({ id: 2, kind: 'named', name: 'VIP', description: '', is_default: false, member_count: 3, granted_groups: [], rate_overrides: [], ...over })
const solo = segment({ id: 104, kind: 'personal', name: 'a@b.c', user_id: 9, user: { id: 9, email: 'a@b.c', display_name: '' } })
const fieldError = (field: string, message: string) => new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field, code: 'invalid', message }] } })

/** The state of each tab: its label, and its mark (error, unsaved) if any. */
function tabsOf(w: VueWrapper) {
  return w.findAll('[role="tab"]').map((t) => {
    const mark = t.find('[data-testid^="tab-mark-"]')
    const label = mark.exists() ? t.text().replace(mark.text(), '').trim() : t.text()
    return mark.exists() ? `${label} (${mark.attributes('data-testid')!.replace('tab-mark-', '')})` : label
  })
}

async function render(of: Segment | null, props: { readonly?: boolean } = {}) {
  const w = mount(SegmentEditor, { props: { open: true, segment: of, ...props }, attachTo: document.body, global: { plugins: [i18n], stubs: { Teleport: true } } })
  await flushPromises()
  const selected = () => w.find('[role="tab"][aria-selected="true"]').attributes('id')?.split('-tab-').pop()
  const submit = async () => {
    await w.get('[data-testid="segment-editor-submit"]').trigger('click')
    await flushPromises()
  }
  return { w, tabs: () => tabsOf(w), selected, submit }
}

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  Object.assign(groups, { body: {}, dirty: false, valid: true, errors: undefined })
  mocks.denied.clear()
  mocks.toast.mockReset()
  mocks.confirm.mockReset().mockResolvedValue(false)
  mocks.patch.mockReset().mockImplementation(async (path: string, body: Partial<Segment>) => segment({ id: Number(path.split('/').pop()), ...body }))
  mocks.post.mockReset().mockImplementation(async (_path: string, body: Partial<Segment>) => segment({ id: 9, ...body }))
})

describe('segment editor', () => {
  it('has two tabs', async () => {
    const { w, tabs } = await render(segment())
    expect(tabs()).toEqual(['基本信息', '账号分组倍率'])
    expect(w.get('[data-testid="groups"]').attributes()).toMatchObject({ 'data-segment': '2', 'data-editable': 'true' })
  })

  it('saves what changed and the account group rates in one request, and closes', async () => {
    const { w, submit } = await render(segment())
    await w.get('[data-testid="segment-name"]').setValue('Gold')
    groups.body = { group_rates: [{ group_id: 2, rate: '0.6' }], model_rates: [{ group_id: 1, model: 'gpt-4o', rate: '0.8' }] }
    await submit()
    expect(mocks.patch).toHaveBeenCalledTimes(1)
    expect(mocks.patch).toHaveBeenCalledWith('/segments/2', { name: 'Gold', group_rates: [{ group_id: 2, rate: '0.6' }], model_rates: [{ group_id: 1, model: 'gpt-4o', rate: '0.8' }] })
    expect(mocks.toast).toHaveBeenCalledWith('已更新', 'success')
    expect(w.emitted('changed')).toHaveLength(1)
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('creates a new segment with its rates in one request', async () => {
    const { w, submit } = await render(null)
    expect(w.get('[data-testid="groups"]').attributes('data-segment')).toBe('null')
    expect(w.get('[data-testid="segment-editor-unsaved"]').text()).toContain('成员在用户页里加入')
    await w.get('[data-testid="segment-name"]').setValue(' Corp ')
    groups.body = { group_rates: [{ group_id: 2, granted: true, rate: null }] }
    await submit()
    expect(mocks.post).toHaveBeenCalledWith('/segments', { name: 'Corp', description: '', group_rates: [{ group_id: 2, granted: true, rate: null }] })
    expect(mocks.toast).toHaveBeenCalledWith('已新建用户分组', 'success')
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('opens the tab of what is wrong before sending anything', async () => {
    const { w, selected, submit, tabs } = await render(null)
    groups.valid = false
    await submit()
    // Both are wrong: each tab is marked, the first opened.
    expect(selected()).toBe('basic')
    expect(tabs()).toEqual(['基本信息 (error)', '账号分组倍率 (error)'])
    expect(w.text()).toContain('必填')
    await w.get('[data-testid="segment-name"]').setValue('Corp')
    await submit()
    expect(selected()).toBe('groups')
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('takes the field errors of the answer to the tab that sent them', async () => {
    const { w, selected, submit } = await render(segment())
    await w.findAll('[role="tab"]')[1].trigger('click')
    mocks.patch.mockRejectedValueOnce(fieldError('name', 'name already in use'))
    await submit()
    expect(selected()).toBe('basic')
    expect(w.text()).toContain('name already in use')
    mocks.patch.mockRejectedValueOnce(fieldError('model_rates.0.rate', 'too low'))
    await submit()
    expect(selected()).toBe('groups')
    expect(groups.errors).toEqual({ 'model_rates.0.rate': 'too low' })
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('marks the tabs with unsaved changes', async () => {
    groups.dirty = true
    const { w, tabs } = await render(segment())
    expect(tabs()).toEqual(['基本信息', '账号分组倍率 (unsaved)'])
    await w.get('[data-testid="segment-name"]').setValue('Gold')
    expect(tabs()).toEqual(['基本信息 (unsaved)', '账号分组倍率 (unsaved)'])
  })

  it('shows a personal segment its rates only, under its user, and offers to move him back', async () => {
    const { w, tabs, submit } = await render(solo)
    expect(w.get('[data-testid="segment-editor-personal"]').text()).toContain('a@b.c')
    // A single tab shows no tab bar.
    expect(tabs()).toEqual([])
    expect(w.find('[data-testid="segment-name"]').exists()).toBe(false)
    expect(w.get('[data-testid="groups"]').isVisible()).toBe(true)
    await w.get('[data-testid="segment-editor-remove"]').trigger('click')
    expect(w.emitted('remove')).toEqual([[solo]])
    groups.body = { group_rates: [{ group_id: 2, rate: '0.5' }] }
    await submit()
    expect(mocks.patch).toHaveBeenCalledWith('/segments/104', { group_rates: [{ group_id: 2, rate: '0.5' }] })
  })

  it('only shows the segment to a caller who may not change it', async () => {
    const { w } = await render(segment(), { readonly: true })
    expect(w.text()).toContain('用户分组 VIP')
    expect(w.text()).not.toContain('编辑用户分组')
    expect(w.get('[data-testid="segment-name"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="groups"]').attributes('data-editable')).toBe('false')
    expect(w.find('[data-testid="segment-editor-submit"]').exists()).toBe(false)
    await w.get('[data-testid="segment-editor-close"]').trigger('click')
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('sets the account groups of a new segment only for a caller who reads them, and of a saved one always', async () => {
    mocks.denied.add('group:read')
    const { w, tabs, submit } = await render(null)
    expect(tabs()).toEqual([])
    expect(w.find('[data-testid="groups"]').exists()).toBe(false)
    await w.get('[data-testid="segment-name"]').setValue('Corp')
    await submit()
    expect(mocks.post).toHaveBeenCalledWith('/segments', { name: 'Corp', description: '' })
    expect((await render(segment())).tabs()).toEqual(['基本信息', '账号分组倍率'])
  })

  it('asks before closing with changes not saved, and closes at once without', async () => {
    const { w } = await render(segment())
    await w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(w.emitted('update:open')).toEqual([[false]])

    const changed = await render(segment())
    await changed.w.get('[data-testid="segment-description"]').setValue('gold')
    await changed.w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的改动？', confirmText: '放弃改动', cancelText: '继续编辑' }))
    expect(changed.w.emitted('update:open')).toBeUndefined()

    groups.dirty = true
    mocks.confirm.mockResolvedValue(true)
    const drafted = await render(segment())
    await drafted.w.get('[data-overlay-close]').trigger('click')
    await flushPromises()
    expect(drafted.w.emitted('update:open')).toEqual([[false]])
  })
})
