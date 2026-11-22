import { beforeEach, describe, expect, it, vi } from 'vitest'
import { accountType, marked, nav, render, savedAccount, sections } from './testing/accountEditor'
import { mocks, ownership } from './testing/editorState'
import { flushPromises } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import type { Account, AccountType } from '@/api/types'
import ModelLimitsEditor from './ModelLimitsEditor.vue'
import AccountUsageLimitCard from './AccountUsageLimitCard.vue'
import RateLimitEditor from './controls/RateLimitEditor.vue'

describe('account model limits', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation((path: string) => Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {}))
    mocks.patch.mockResolvedValue(savedAccount())
  })

  const caps = { max_concurrency: 0, rpm_limit: 0, tpm_limit: 0, spm_limit: 0 }
  /** A card's caps: the window is always there, saved entries leave out the default. */
  const cardCaps = { ...caps, spm_window_seconds: 60 }

  it('edits the saved lists as cards and submits one entry per list', async () => {
    const limits = [{ models: ['m-a'], ...caps, max_concurrency: 1, spm_limit: 2 }, { models: ['m-b'], ...caps, max_concurrency: 1, spm_limit: 2 }]
    const w = render({ ...savedAccount(), model_limits: limits } as Account)
    await flushPromises()
    const editor = w.getComponent(ModelLimitsEditor)
    expect(editor.props('modelValue')).toEqual([{ lists: [['m-a'], ['m-b']], ...cardCaps, max_concurrency: 1, spm_limit: 2 }])
    editor.vm.$emit('update:modelValue', [{ lists: [['m-a', 'm-c'], []], ...cardCaps, rpm_limit: 9, spm_limit: 3 }])
    await flushPromises()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ model_limits: [{ models: ['m-a', 'm-c'], ...caps, rpm_limit: 9, spm_limit: 3 }] }))
  })

  it('edits the session limit and its period of the account and of a model list', async () => {
    const w = render({ ...savedAccount(), spm_limit: 5, spm_window_seconds: 300, model_limits: [{ models: ['m-b'], ...caps, spm_window_seconds: 7200 }] } as Account)
    await flushPromises()
    const value = (id: string) => (w.get(`[data-testid="${id}"]`).element as HTMLInputElement).value
    expect([value('account-spm_limit'), value('account-spm_limit-window'), value('account-spm_limit-window-unit')]).toEqual(['5', '5', 'minute'])
    expect(w.getComponent(ModelLimitsEditor).props('modelValue')).toEqual([{ lists: [['m-b']], ...cardCaps, spm_window_seconds: 7200 }])
    await w.get('input[data-testid="account-spm_limit"]').setValue('7')
    await w.get('input[data-testid="account-spm_limit-window"]').setValue('2')
    await w.get('[data-testid="account-spm_limit-window-unit"]').setValue('hour')
    w.getComponent(ModelLimitsEditor).vm.$emit('update:modelValue', [{ lists: [['m-a']], ...cardCaps, spm_limit: 2 }, { lists: [['m-c']], ...cardCaps, spm_window_seconds: 600 }])
    await flushPromises()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({
      spm_limit: 7,
      spm_window_seconds: 7200,
      model_limits: [{ models: ['m-a'], ...caps, spm_limit: 2 }, { models: ['m-c'], ...caps, spm_window_seconds: 600 }]
    }))
  })

  it('sends the default period of an account saved without one, and shows the server\'s period error on the session row', async () => {
    mocks.patch.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'spm_window_seconds', code: 'invalid', message: 'spm_window_seconds: invalid' }] } }))
    const w = render(savedAccount())
    await flushPromises()
    expect((w.get('input[data-testid="account-spm_limit-window"]').element as HTMLInputElement).value).toBe('1')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/accounts/1', expect.objectContaining({ spm_window_seconds: 60, model_limits: [] }))
    expect(w.getComponent(RateLimitEditor).props('errors')).toEqual({ spm_window_seconds: 'spm_window_seconds: invalid' })
    expect(marked(w)).toContain('scheduling')
  })

  it('leaves a period the type fixes to the server, and hides one the type hides', async () => {
    const fixedType = { ...accountType, common_controls: { spm_window_seconds: { configurable: false } } } as unknown as AccountType
    const w = render({ ...savedAccount(), spm_window_seconds: 600 } as Account, false, fixedType)
    await flushPromises()
    expect(w.get('input[data-testid="account-spm_limit-window"]').attributes('disabled')).toBeDefined()
    expect(w.get('input[data-testid="account-spm_limit"]').attributes('disabled')).toBeUndefined()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch.mock.calls[0][1]).not.toHaveProperty('spm_window_seconds')
    const hidden = render(savedAccount(), false, { ...accountType, common_controls: { spm_window_seconds: { visible: false } } } as unknown as AccountType)
    await flushPromises()
    expect(hidden.find('[data-testid="account-spm_limit-window"]').exists()).toBe(false)
    expect(hidden.find('input[data-testid="account-spm_limit"]').exists()).toBe(true)
    // A fixed cap leaves its period editable.
    const capFixed = render(savedAccount(), false, { ...accountType, common_controls: { spm_limit: { configurable: false } } } as unknown as AccountType)
    await flushPromises()
    expect(capFixed.get('input[data-testid="account-spm_limit"]').attributes('disabled')).toBeDefined()
    expect(capFixed.get('input[data-testid="account-spm_limit-window"]').attributes('disabled')).toBeUndefined()
  })

  it('shows the concurrency cap in the limits group, ahead of the rate limits', async () => {
    const w = render(savedAccount())
    await flushPromises()
    expect(w.find('[data-testid="account-scheduling"] [data-testid="account-max_concurrency"]').exists()).toBe(false)
    const limits = w.get('[data-testid="account-limits"]').findAll('input').map(i => i.attributes('data-testid'))
    expect(limits.slice(0, 2)).toEqual(['account-max_concurrency', 'account-rpm_limit'])
  })

  it('hides the limits group of a type hiding every limit, its undeclared period included', async () => {
    const hidden = { visible: false }
    const w = render(savedAccount(), false, { ...accountType, common_controls: { max_concurrency: hidden, rpm_limit: hidden, tpm_limit: hidden, spm_limit: hidden } } as unknown as AccountType)
    await flushPromises()
    expect(w.find('[data-testid="account-limits"]').exists()).toBe(false)
  })

  it('refuses to save a model listed twice', async () => {
    const w = render(savedAccount())
    await flushPromises()
    w.getComponent(ModelLimitsEditor).vm.$emit('update:modelValue', [{ lists: [['m-a'], ['m-a']], ...cardCaps, rpm_limit: 1 }])
    await flushPromises()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(w.getComponent(ModelLimitsEditor).props('errors')).toEqual({ model_limits: 'm-a 已在另一个列表中' })
  })

  it.each([['all', '0'], ['own', '10']] as const)('starts a new account of an %s-level caller with max concurrency %s', async (scope, value) => {
    ownership.scope = scope
    const w = render(undefined)
    await flushPromises()
    expect((w.get('input[data-testid="account-max_concurrency"]').element as HTMLInputElement).value).toBe(value)
    ownership.scope = 'all'
  })
  it('fits the plugin\'s suggested limits into an own-level caller\'s caps', async () => {
    ownership.scope = 'own'
    const w = render(undefined, false, { ...accountType, default_limits: { priority: 500, weight: 300, max_concurrency: 0 } } as AccountType)
    await flushPromises()
    expect(['priority', 'weight', 'max_concurrency'].map(k => (w.get(`input[data-testid="account-${k}"]`).element as HTMLInputElement).value)).toEqual(['100', '100', '10'])
    ownership.scope = 'all'
  })
  it('starts a new account from the limits its plugin suggests', async () => {
    const type = { ...accountType, default_limits: { priority: 3, spm_limit: 6, model_limits: [{ models: ['m-a'], ...caps, max_concurrency: 2 }] } } as AccountType
    const w = render(undefined, false, type)
    await flushPromises()
    expect(w.getComponent(ModelLimitsEditor).props('modelValue')).toEqual([{ lists: [['m-a']], ...cardCaps, max_concurrency: 2 }])
    expect((w.get('input[data-testid="account-spm_limit-window"]').element as HTMLInputElement).value).toBe('1')
    expect((w.get('input[data-testid="account-spm_limit"]').element as HTMLInputElement).value).toBe('6')
  })
})

describe('account usage limit', () => {
  const saved = { raw_cost_accounting: false, enabled: true, amount: '200', period: 'week', period_count: 1, anchor: null, timezone: 'UTC', configured: true }
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  let limitGet: () => Promise<unknown>

  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    limitGet = () => Promise.resolve(saved)
    mocks.get.mockImplementation((path: string) =>
      path.endsWith('/usage-limit') ? limitGet() : Promise.resolve(path === '/accounts/auto-disable' ? { enabled: true } : {})
    )
    mocks.patch.mockResolvedValue(savedAccount())
    mocks.put.mockResolvedValue({})
  })

  const card = (w: ReturnType<typeof render>) => w.getComponent(AccountUsageLimitCard)
  async function open() {
    const w = render(savedAccount())
    await flushPromises()
    nav(w).vm.$emit('update:modelValue', 'scheduling')
    await flushPromises()
    return w
  }
  async function submit(w: ReturnType<typeof render>) {
    await w.get('form').trigger('submit')
    await flushPromises()
  }

  it('loads the limit when the section is shown and saves it after the account, in the browser time zone', async () => {
    const w = render(savedAccount())
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalledWith('/accounts/1/usage-limit')
    nav(w).vm.$emit('update:modelValue', 'scheduling')
    await flushPromises()
    expect(card(w).props()).toMatchObject({ status: 'ready', modelValue: saved })
    expect(sections(w)[2].summary).toContain('用量限制 $200.00 每周')
    await submit(w)
    expect(mocks.patch).toHaveBeenCalledOnce()
    expect(mocks.put).not.toHaveBeenCalled()

    card(w).vm.$emit('update:modelValue', { ...saved, amount: '300', period_count: 2 })
    await flushPromises()
    expect(card(w).props('modelValue').timezone).toBe(zone)
    expect(sections(w)[2].summary).toContain('用量限制 $300.00 每 2 周')
    await submit(w)
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, amount: '300', period_count: 2, timezone: zone })
    expect(mocks.patch.mock.invocationCallOrder[1]).toBeLessThan(mocks.put.mock.invocationCallOrder[0])
    expect(w.emitted('saved')).toHaveLength(2)
  })

  it('keeps the saved time zone unless the periods are edited', async () => {
    const chatham = { ...saved, timezone: 'Pacific/Chatham' }
    limitGet = () => Promise.resolve(chatham)
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...chatham, raw_cost_accounting: true })
    await flushPromises()
    await submit(w)
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...chatham, raw_cost_accounting: true })

    card(w).vm.$emit('update:modelValue', { ...chatham, raw_cost_accounting: true, period: 'month' })
    card(w).vm.$emit('update:modelValue', { ...chatham, raw_cost_accounting: true })
    await flushPromises()
    expect(card(w).props('modelValue').timezone).toBe(zone)
    await submit(w)
    expect(mocks.put).toHaveBeenCalledOnce()
  })

  it('keeps the card read-only and never saves a limit that did not load', async () => {
    limitGet = () => Promise.reject(new Error('network down'))
    const w = await open()
    expect(card(w).props()).toMatchObject({ status: 'failed', loadError: 'network down' })
    card(w).vm.$emit('update:modelValue', { ...saved, amount: '1' })
    await submit(w)
    expect(mocks.patch).toHaveBeenCalledOnce()
    expect(mocks.put).not.toHaveBeenCalled()

    limitGet = () => Promise.resolve(saved)
    card(w).vm.$emit('retry')
    await flushPromises()
    expect(card(w).props()).toMatchObject({ status: 'ready', modelValue: saved })
  })

  it('holds the card while the limit loads', async () => {
    let resolve!: (v: unknown) => void
    limitGet = () => new Promise((r) => (resolve = r))
    const w = await open()
    expect(card(w).props('status')).toBe('loading')
    await submit(w)
    expect(mocks.put).not.toHaveBeenCalled()
    resolve(saved)
    await flushPromises()
    expect(card(w).props()).toMatchObject({ status: 'ready', modelValue: saved })
  })

  it('reports a refused limit without failing the saved account', async () => {
    mocks.put.mockRejectedValue(new Error('period_count must be 1-12 for month'))
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, amount: '300' })
    await submit(w)
    expect(mocks.toast).toHaveBeenCalledWith('账号已保存，但用量限制保存失败：period_count must be 1-12 for month', 'error')
    expect(w.emitted('saved')).toHaveLength(1)
  })

  it('checks the period length against its period before saving anything', async () => {
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, period: 'month', period_count: 13 })
    await submit(w)
    expect(mocks.patch).not.toHaveBeenCalled()
    expect(card(w).props('errors')).toEqual({ period_count: '周期长度必须为 1-12' })
    expect(marked(w)).toContain('scheduling')
  })

  it('starts a limit the account never saved in the browser time zone', async () => {
    const never = { raw_cost_accounting: false, enabled: false, amount: '0', period: 'month', period_count: 1, anchor: null, timezone: '', configured: false }
    limitGet = () => Promise.resolve(never)
    const w = await open()
    expect(card(w).props('modelValue').timezone).toBe(zone)
    card(w).vm.$emit('update:modelValue', { ...card(w).props('modelValue'), enabled: true, amount: '50' })
    await submit(w)
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...never, enabled: true, amount: '50', timezone: zone })
  })

  it('saves a switched-off limit whose amount was cleared', async () => {
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, enabled: false, amount: '' })
    await submit(w)
    expect(card(w).props('errors')).toEqual({})
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, enabled: false, amount: '' })
  })

  it('saves a lifetime limit whatever the hidden period length was', async () => {
    limitGet = () => Promise.resolve({ ...saved, period: 'month', period_count: 12 })
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, period: 'none', period_count: 12 })
    await submit(w)
    expect(card(w).props('errors')).toEqual({})
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, period: 'none', period_count: 12, timezone: zone })
  })

  it('saves an amount with more than 2 decimals (the card input takes step="any")', async () => {
    const w = await open()
    card(w).vm.$emit('update:modelValue', { ...saved, amount: '0.12345678' })
    await submit(w)
    expect(mocks.patch).toHaveBeenCalledOnce()
    expect(mocks.put).toHaveBeenCalledWith('/accounts/1/usage-limit', { ...saved, amount: '0.12345678' })
  })
})
