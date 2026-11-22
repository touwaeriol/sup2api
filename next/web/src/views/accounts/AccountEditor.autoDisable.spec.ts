import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flip } from './testing/accountsView'
import { mocks } from './testing/accountsState'
import { flushPromises, shallowMount } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import AccountEditor from './AccountEditor.vue'

describe('account automatic disabling policy', () => {
  const account = { id: 23, name: 'test', auto_disable: true, models: [], model_mapping: {}, credentials: {} } as unknown as Account
  const accountType = { plugin_key: 'test', type: 'key', form: { mode: 'native' }, platforms: [], endpoints: [], default_models: [] } as unknown as AccountType
  const renderEditor = (saved: Account | null = account) => shallowMount(AccountEditor, { props: { account: saved, accountType }, global: { plugins: [i18n], stubs: {
    STabbedForm: false,
    STitleScope: false,
    AccountSchedulingPane: false,
    AccountCredentialsCard: false,
    SCard: { template: '<section><slot /></section>' },
    SSettingsGroup: false,
    SSettingRow: false,
    SSwitch: false,
  } } })

  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockReset().mockResolvedValue({ enabled: true })
    mocks.patch.mockResolvedValue(account)
    i18n.global.locale.value = 'zh'
  })

  it.each([true, false])('locks the switch without clearing a saved %s preference when globally off', async (preference) => {
    mocks.get.mockResolvedValue({ enabled: false })
    const wrapper = renderEditor({ ...account, auto_disable: preference })
    await flushPromises()
    const toggle = wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    expect(toggle.attributes('aria-checked')).toBe(String(preference))
    expect(wrapper.text()).toContain('全局自动禁用已关闭')
    await flip(toggle)
    expect(toggle.attributes('aria-checked')).toBe(String(preference))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalled()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('auto_disable')
  })

  it('locks new accounts too and permits an individual opt-out when globally enabled', async () => {
    mocks.get.mockResolvedValueOnce({ enabled: false })
    const wrapper = renderEditor(null)
    await flushPromises()
    const toggle = wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(toggle.attributes('disabled')).toBeUndefined()
    await flip(toggle)
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(mocks.get).toHaveBeenCalledWith('/accounts/auto-disable')
  })

  it('keeps the switch locked while loading and on failure, then recovers on focus', async () => {
    let reject!: (error: Error) => void
    mocks.get.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail }))
    const wrapper = renderEditor()
    const toggle = wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    reject(new Error('network unavailable'))
    await flushPromises()
    expect(toggle.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('暂时无法修改')
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(toggle.attributes('disabled')).toBeUndefined()
    await flip(toggle)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).toMatchObject({ auto_disable: false })
  })

  it('does not let an older enabled response overwrite a newer disabled policy', async () => {
    let resolve!: (policy: { enabled: boolean }) => void
    mocks.get.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = renderEditor()
    mocks.get.mockResolvedValue({ enabled: false })
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    resolve({ enabled: true })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('disabled')).toBeDefined()
  })

  it('applies plugin defaults to new accounts and keeps existing read-only preferences', async () => {
    const controlledType: AccountType = { ...accountType, common_controls: {
      auto_disable: { configurable: false, default: false }, schedulable: { configurable: false, default: false },
    } }
    const wrapper = renderEditor(null)
    await wrapper.setProps({ accountType: controlledType })
    await flushPromises()
    for (const key of ['account-auto-disable', 'account-scheduling-control']) {
      const button = wrapper.get(`[data-testid="${key}"] [role="switch"], [role="switch"][data-testid="${key}"]`)
      expect(button.attributes('disabled')).toBeDefined()
      expect(button.attributes('aria-checked')).toBe('false')
    }
    await wrapper.setProps({ account: { ...account, schedulable: true } })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="account-scheduling-control"] [role="switch"], [role="switch"][data-testid="account-scheduling-control"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('auto_disable')
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('schedulable')
  })

  it('updates untouched defaults when switching authentication variants', async () => {
    const wrapper = renderEditor(null)
    await wrapper.setProps({ accountType: { ...accountType, creation_group: 'test', common_controls: { auto_disable: { default: false } } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('aria-checked')).toBe('false')
    await wrapper.setProps({ accountType: { ...accountType, type: 'other', creation_group: 'test', common_controls: { auto_disable: { default: true } } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('aria-checked')).toBe('true')
  })

  it('rechecks the global gate before saving an edited preference', async () => {
    const wrapper = renderEditor()
    await flushPromises()
    await flip(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]'))
    mocks.get.mockResolvedValue({ enabled: false })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('auto_disable')
    expect(wrapper.get('[data-testid="account-auto-disable"] [role="switch"], [role="switch"][data-testid="account-auto-disable"]').attributes('disabled')).toBeDefined()
  })
})
