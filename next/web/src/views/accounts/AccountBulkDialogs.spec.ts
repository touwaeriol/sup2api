import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import EntityMultiPicker from '@/components/EntityMultiPicker.vue'
import EntityPicker from '@/components/EntityPicker.vue'
import ProxyPicker from '@/components/ProxyPicker.vue'
import { i18n } from '@/i18n'
import AccountBulkEditDialog from './AccountBulkEditDialog.vue'
import AccountBulkDeleteDialog from './AccountBulkDeleteDialog.vue'
import ModelTagSelect from '@/components/models/ModelTagSelect.vue'
import OwnProtocolsField from './protocols/OwnProtocolsField.vue'
import { openai } from './protocols/fixtures'

const mocks = vi.hoisted(() => ({ patch: vi.fn(), del: vi.fn(), toast: vi.fn() }))
// The group and proxy pickers look entities up; nothing answers here.
vi.mock('@sub2api/host', async (original) => ({ ...await original<typeof import('@sub2api/host')>(), api: { patch: mocks.patch, del: mocks.del, get: async () => ({ items: [], more: false }) } }))
vi.mock('@sub2api/ui', async (original) => ({ ...await original<typeof import('@sub2api/ui')>(), toast: mocks.toast }))
const perms = vi.hoisted(() => ({ has: (_: string | string[]): boolean => true }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string | string[]) => perms.has(p), hasAction: (a: string) => perms.has([a, `${a}:own`]), scopes: () => [] }) }))
enableAutoUnmount(afterEach)

const acc = (id: number, over: Partial<Account> = {}) => ({ id, name: `a${id}`, plugin_key: 'p', type: 't', priority: 10, weight: 1, max_concurrency: 10, tpm_limit: 0, rpm_limit: 0, group_ids: [1], proxy_id: null, auto_disable: true, ...over }) as unknown as Account
const SModal = { props: ['open', 'title'], template: '<div v-if="open"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>' }

describe('bulk account dialogs', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
  })

  it('patches each account with the ticked fields only and lists the failures', async () => {
    mocks.patch.mockImplementation(async (path: string, body: object) => {
      if (path === '/accounts/2') throw new Error('无权修改')
      return { id: Number(path.split('/')[2]), ...body }
    })
    const fixed = { common_controls: { auto_disable: { configurable: false } } } as unknown as AccountType
    const w = mount(AccountBulkEditDialog, {
      props: { open: false, accounts: [acc(1), acc(2), acc(3, { priority: 3 })], typeOf: (a: Account) => (a.id === 3 ? fixed : undefined) },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    await w.setProps({ open: true })
    expect(w.get('[data-testid="bulk-edit-apply"]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid="bulk-field-priority"] input[type="number"]').setValue('3')
    // A field one selected account's type fixes (common controls) is disabled.
    expect(w.get('[data-testid="bulk-field-auto_disable"] [role="switch"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="bulk-field-auto_disable"]').text()).toContain('1 个所选账号的类型不允许修改此项')
    expect(w.get('[data-testid="bulk-field-weight"] input').attributes('disabled')).toBeUndefined()
    await w.get('[data-testid="bulk-edit-apply"]').trigger('click')
    await flushPromises()

    expect(mocks.patch.mock.calls.map((c) => [c[0], c[1]])).toEqual([
      ['/accounts/1', { priority: 3 }],
      ['/accounts/2', { priority: 3 }]
    ])
    expect(w.get('[data-testid="bulk-failures"]').text()).toContain('a2')
    expect(w.emitted('saved')![0][0]).toEqual([{ id: 1, priority: 3 }])
    expect(w.emitted('update:open')).toBeUndefined()
    expect(mocks.toast).toHaveBeenCalledWith('成功 1，失败 1，跳过 1', 'warning')
  })

  it('applies the session limit and its period as one field, refusing a period over 24 hours or not a whole number', async () => {
    mocks.patch.mockImplementation(async (path: string, body: object) => ({ id: Number(path.split('/')[2]), ...body }))
    const w = mount(AccountBulkEditDialog, {
      props: { open: true, accounts: [acc(1), acc(2, { spm_limit: 20, spm_window_seconds: 3600 })], typeOf: () => undefined },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    const field = w.get('[data-testid="bulk-field-spm_limit"]')
    expect(field.text()).toContain('会话数限制')
    await field.get('input[data-testid="bulk-spm_limit"]').setValue('20')
    await field.get('select[data-testid="bulk-spm_limit-window-unit"]').setValue('1')
    for (const bad of ['25', '1.5']) {
      await field.get('input[data-testid="bulk-spm_limit-window"]').setValue(bad)
      expect(w.get('[data-testid="bulk-edit-apply"]').attributes('disabled')).toBeDefined()
    }
    await field.get('input[data-testid="bulk-spm_limit-window"]').setValue('1')
    await w.get('[data-testid="bulk-edit-apply"]').trigger('click')
    await flushPromises()
    expect(mocks.patch.mock.calls.map((c) => [c[0], c[1]])).toEqual([['/accounts/1', { spm_limit: 20, spm_window_seconds: 3600 }]])
  })

  it('picking groups or a proxy ticks the field and patches their ids as numbers', async () => {
    mocks.patch.mockImplementation(async (path: string, body: object) => ({ id: Number(path.split('/')[2]), ...body }))
    const w = mount(AccountBulkEditDialog, {
      props: { open: true, accounts: [acc(1), acc(2, { group_ids: [2, 5], proxy_id: 9 })], typeOf: () => undefined },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    const ticked = (k: string) => (w.get(`[data-testid="bulk-field-${k}"] input[type="checkbox"]`).element as HTMLInputElement).checked
    expect([ticked('groups'), ticked('proxy')]).toEqual([false, false])
    w.getComponent(EntityMultiPicker).vm.$emit('update:ids', ['2', '5'])
    w.getComponent(ProxyPicker).getComponent(EntityPicker).vm.$emit('update:id', '9')
    await flushPromises()
    expect([ticked('groups'), ticked('proxy')]).toEqual([true, true])
    expect(w.getComponent(EntityMultiPicker).props('ids')).toEqual(['2', '5'])
    await w.get('[data-testid="bulk-edit-apply"]').trigger('click')
    await flushPromises()
    // The second account has them already.
    expect(mocks.patch.mock.calls.map((c) => [c[0], c[1]])).toEqual([['/accounts/1', { group_ids: [2, 5], proxy_id: 9 }]])
  })

  it('"no proxy" is the ticked field with none picked: ticked alone, or a picked proxy cleared', async () => {
    mocks.patch.mockImplementation(async (path: string, body: object) => ({ id: Number(path.split('/')[2]), ...body }))
    const w = mount(AccountBulkEditDialog, {
      props: { open: true, accounts: [acc(1), acc(2, { proxy_id: 9 })], typeOf: () => undefined },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    // Asked for anew: reopening renders the dialog again.
    const tick = () => w.get<HTMLInputElement>('[data-testid="bulk-field-proxy"] input[type="checkbox"]')
    const picker = () => w.getComponent(ProxyPicker).getComponent(EntityPicker)
    const apply = async () => {
      await w.get('[data-testid="bulk-edit-apply"]').trigger('click')
      await flushPromises()
      return mocks.patch.mock.calls.map((c) => [c[0], c[1]])
    }
    await tick().setValue(true)
    // The first account has no proxy already.
    expect(await apply()).toEqual([['/accounts/2', { proxy_id: null }]])

    mocks.patch.mockClear()
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    expect(tick().element.checked).toBe(false)
    picker().vm.$emit('update:id', '9')
    await flushPromises()
    expect(picker().props('id')).toBe('9')
    picker().vm.$emit('update:id', '')
    await flushPromises()
    expect(tick().element.checked).toBe(true)
    expect(picker().props('id')).toBe('')
    expect(await apply()).toEqual([['/accounts/2', { proxy_id: null }]])
  })

  it('disables every number field a selected type fixes or locks', async () => {
    const types: Record<number, AccountType> = {
      1: { common_controls: { weight: { locked: true, default: 2 } } } as unknown as AccountType,
      2: { common_controls: { rpm_limit: { configurable: false }, priority: { visible: false } } } as unknown as AccountType
    }
    const w = mount(AccountBulkEditDialog, {
      props: { open: true, accounts: [acc(1), acc(2)], typeOf: (a: Account) => types[a.id] },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    const disabled = (k: string) => w.get(`[data-testid="bulk-field-${k}"] input[type="number"]`).attributes('disabled') !== undefined
    expect(['priority', 'weight', 'max_concurrency', 'rpm_limit', 'tpm_limit', 'spm_limit'].filter(disabled)).toEqual(['weight', 'rpm_limit'])
  })

  it('replaces the own protocols and the protocols per model of accounts of one type (DESIGN-2026-11-17-PROTOCOL-BINDING §4)', async () => {
    mocks.patch.mockImplementation(async (path: string, body: object) => ({ id: Number(path.split('/')[2]), ...body }))
    const rules = [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }]
    const w = mount(AccountBulkEditDialog, {
      props: { open: false, accounts: [acc(1, { models: ['gpt-5.6'] }), acc(2, { protocols: ['openai.chat'], model_protocols: rules })], typeOf: () => openai },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    await w.setProps({ open: true })
    const own = w.get('[data-testid="bulk-field-protocols"]')
    const perModel = w.get('[data-testid="bulk-field-model_protocols"]')
    const ticked = (field: typeof own) => (field.get('input[type="checkbox"]').element as HTMLInputElement).checked
    expect([ticked(own), ticked(perModel)]).toEqual([false, false])
    expect(w.get('[data-testid="bulk-edit-apply"]').attributes('disabled')).toBeDefined()

    // Choosing the own protocols ticks the field; the rules start empty and are edited as a list.
    const editor = w.getComponent(OwnProtocolsField).props('editor')
    editor.setMode('custom')
    editor.toggleOwn(editor.columns.value.own[3])
    editor.toggleOwn(editor.columns.value.own[2])
    editor.toggleOwn(editor.columns.value.own[1])
    await flushPromises()
    expect([ticked(own), ticked(perModel)]).toEqual([true, false])
    expect(perModel.find('[data-testid="protocol-list"]').exists()).toBe(true)
    perModel.get('[data-testid="protocol-entry-add"]').getComponent(ModelTagSelect).vm.$emit('update:modelValue', ['claude-sonnet-4-5'])
    await flushPromises()
    expect(ticked(perModel)).toBe(true)
    // The new entry starts from the default (the one own protocol left open): make it the conversion only.
    editor.rules.value = { 'claude-sonnet-4-5': ['anthropic.messages'] }
    await flushPromises()
    await w.get('[data-testid="bulk-edit-apply"]').trigger('click')
    await flushPromises()
    // Account 2 already has both: no request.
    expect(mocks.patch.mock.calls.map((c) => [c[0], c[1]])).toEqual([['/accounts/1', { protocols: ['openai.chat'], model_protocols: rules }]])
    expect(mocks.toast).toHaveBeenCalledWith('已更新 1 个账号（1 个无需修改）', 'success')
  })

  it('refuses a rule without protocols, and leaves "serves nothing" to the server unless both fields are ticked', async () => {
    const w = mount(AccountBulkEditDialog, { props: { open: false, accounts: [acc(1)], typeOf: () => openai }, global: { plugins: [i18n], stubs: { SModal } } })
    await w.setProps({ open: true })
    const editor = w.getComponent(OwnProtocolsField).props('editor')
    const apply = () => w.get('[data-testid="bulk-edit-apply"]').attributes('disabled')
    // Only the own protocols ticked, all closed: each account's own rules decide.
    editor.setMode('custom')
    for (const c of [...editor.columns.value.own]) editor.toggleOwn(c)
    await flushPromises()
    expect(w.find('[data-issue]').exists()).toBe(false)
    expect(apply()).toBeUndefined()
    // Both ticked: nothing would be served.
    editor.rules.value = { a: ['openai.chat'] }
    await flushPromises()
    expect(w.findAll('[data-issue]').map(i => i.attributes('data-issue'))).toEqual(['no_protocol', 'closed_protocol'])
    expect(apply()).toBeDefined()
    editor.rules.value = { a: [] }
    await flushPromises()
    expect(w.findAll('[data-issue]').map(i => i.attributes('data-issue'))).toEqual(['empty_rule', 'no_protocol'])
    editor.rules.value = { a: ['anthropic.messages'] }
    await flushPromises()
    expect(apply()).toBeUndefined()
  })

  it('checks the rules against each account\'s own protocols while the own protocols are not ticked', async () => {
    const w = mount(AccountBulkEditDialog, {
      props: { open: false, accounts: [acc(1, { protocols: null }), acc(2, { protocols: ['openai.chat', 'openai.responses'] })], typeOf: () => openai },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    await w.setProps({ open: true })
    const editor = w.getComponent(OwnProtocolsField).props('editor')
    const issues = () => w.findAll('[data-issue]').map(i => i.attributes('data-issue'))
    // Closed here, then left unticked: the dialog's own protocols do not apply.
    editor.setMode('custom')
    editor.toggleOwn(editor.columns.value.own[0])
    await flushPromises()
    await w.get('[data-testid="bulk-field-protocols"] input[type="checkbox"]').setValue(false)
    editor.rules.value = { a: ['openai.chat'] }
    await flushPromises()
    expect(issues()).toEqual([])
    // Account 2 does not open embeddings: the rule's protocol is closed there.
    editor.rules.value = { a: ['openai.embeddings'] }
    await flushPromises()
    expect(issues()).toEqual(['closed_protocol'])
  })

  it('offers the protocol fields only for accounts of one known type, and starts them over each time it opens', async () => {
    const mixed = mount(AccountBulkEditDialog, { props: { open: true, accounts: [acc(1), acc(2, { type: 'other' })], typeOf: () => openai }, global: { plugins: [i18n], stubs: { SModal } } })
    expect(mixed.get('[data-testid="bulk-protocols-mixed"]').text()).toContain('只能对同一类型的账号批量修改')
    expect(mixed.get('[data-testid="bulk-field-protocols"] input[type="checkbox"]').attributes('disabled')).toBeDefined()
    expect(mixed.find('[data-testid="bulk-field-model_protocols"]').exists()).toBe(false)
    // One type, but its plugin is not enabled: the protocols are unknown.
    const gone = mount(AccountBulkEditDialog, { props: { open: true, accounts: [acc(1), acc(2)], typeOf: () => undefined }, global: { plugins: [i18n], stubs: { SModal } } })
    expect(gone.find('[data-testid="bulk-protocols-mixed"]').exists()).toBe(true)

    const w = mount(AccountBulkEditDialog, { props: { open: false, accounts: [acc(1)], typeOf: () => openai }, global: { plugins: [i18n], stubs: { SModal } } })
    await w.setProps({ open: true })
    const editor = w.getComponent(OwnProtocolsField).props('editor')
    editor.setMode('custom')
    editor.rules.value = { a: ['openai.chat'] }
    await flushPromises()
    expect(w.get('[data-testid="bulk-edit-apply"]').attributes('disabled')).toBeUndefined()
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    expect(editor.binding()).toEqual({ protocols: null, model_protocols: [] })
    const ticked = (field: string) => (w.get(`[data-testid="bulk-field-${field}"] input[type="checkbox"]`).element as HTMLInputElement).checked
    expect([ticked('protocols'), ticked('model_protocols')]).toEqual([false, false])
    expect(w.get('[data-testid="bulk-edit-apply"]').attributes('disabled')).toBeDefined()
  })

  it('deletes every selected account', async () => {
    mocks.del.mockResolvedValue(undefined)
    const w = mount(AccountBulkDeleteDialog, {
      props: { open: false, accounts: [acc(1), acc(2)] },
      global: { plugins: [i18n], stubs: { SModal } }
    })
    await w.setProps({ open: true })
    await w.get('[data-testid="bulk-delete-confirm"]').trigger('click')
    await flushPromises()
    expect(mocks.del.mock.calls.map((c) => [c[0], c[1]])).toEqual([['/accounts/1', undefined], ['/accounts/2', undefined]])
    expect(w.emitted('deleted')![0][0]).toEqual([1, 2])
    expect(w.emitted('update:open')![0]).toEqual([false])
  })

  it('locks the groups for an own-level caller without a bind permission', async () => {
    perms.has = p => [p].flat().some(k => k === 'account:update:own')
    try {
      const w = mount(AccountBulkEditDialog, { props: { open: true, accounts: [acc(1)], typeOf: () => undefined }, global: { plugins: [i18n], stubs: { SModal } } })
      const field = w.get('[data-testid="bulk-field-groups"]')
      expect(field.get('input[type="checkbox"]').attributes('disabled')).toBeDefined()
      expect(field.text()).toContain('修改账号分组需要')
    } finally {
      perms.has = () => true
    }
  })
})
