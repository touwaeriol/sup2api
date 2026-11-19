import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { SSettingRow } from '@sub2api/ui'
import { i18n } from '@/i18n'
import GatewaySettingsCard from './GatewaySettingsCard.vue'
import { maxTokensMap, maxTokensRows } from './claudeMaxTokens'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), api }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
enableAutoUnmount(afterEach)

const saved = {
  max_attempts: 3,
  platform_call_timeout_ms: 2000,
  default_hook_timeout_ms: 300,
  platform_hotpath_timeout_ms: 300,
  queue_sticky_max_waiting: 3,
  queue_sticky_wait_seconds: 120,
  queue_fallback_max_waiting: 100,
  queue_fallback_wait_seconds: 30,
  claude_max_tokens: { enabled: true, models: { default: 16000, 'claude-x': 4096 } },
  media_download_max_mb: 32,
  max_request_body_mb: 256
}

beforeEach(() => {
  vi.clearAllMocks()
  i18n.global.locale.value = 'zh'
  api.get.mockResolvedValue(saved)
  api.put.mockImplementation((_path, body) => Promise.resolve(body))
})

async function render() {
  const w = mount(GatewaySettingsCard, { global: { plugins: [i18n] } })
  await flushPromises()
  return w
}

const rows = (w: Awaited<ReturnType<typeof render>>) => w.findAll('[data-testid="claude-max-tokens-row"]')
const inputs = (w: Awaited<ReturnType<typeof render>>, i: number) => rows(w)[i].findAll('input')
const turnOff = async (w: Awaited<ReturnType<typeof render>>) => {
  await w.get('[data-testid="gateway-claude-max-tokens-enabled"]').setValue(false)
}
const save = async (w: Awaited<ReturnType<typeof render>>) => {
  await w.get('[data-testid="settings-save"]').trigger('click')
  await flushPromises()
}

describe('gateway conversion settings (CONVERT-MATRIX §6.1)', () => {
  it('lists the rule rows in server order, default removable, and saves them with the media cap', async () => {
    const w = await render()
    expect(rows(w).map(r => (r.find('input').element as HTMLInputElement).value)).toEqual(['default', 'claude-x'])
    expect(inputs(w, 0)[0].attributes('readonly')).toBeUndefined()
    expect((w.get('input[name="media_download_max_mb"]').element as HTMLInputElement).value).toBe('32')

    await rows(w)[0].get('[data-testid="claude-max-tokens-remove"]').trigger('click')
    await w.get('[data-testid="claude-max-tokens-add"]').trigger('click')
    await inputs(w, 1)[0].setValue(' gpt-5 ')
    await inputs(w, 1)[1].setValue('2048')
    await w.get('input[name="media_download_max_mb"]').setValue('128')
    await save(w)
    expect(api.put).toHaveBeenCalledWith(
      '/settings/gateway',
      expect.objectContaining({
        claude_max_tokens: { enabled: true, models: { 'claude-x': 4096, 'gpt-5': 2048 } },
        media_download_max_mb: 128
      })
    )
    // max_attempts belongs to the failure policy once one is published (409 when changed here): never sent.
    expect(api.put.mock.calls[0][1]).not.toHaveProperty('max_attempts')
  })

  it('hides the rows while the rule is off and still saves them', async () => {
    const w = await render()
    await turnOff(w)
    expect(w.find('[data-testid="gateway-claude-max-tokens"]').exists()).toBe(false)
    await save(w)
    expect(api.put).toHaveBeenCalledWith(
      '/settings/gateway',
      expect.objectContaining({ claude_max_tokens: { enabled: false, models: { default: 16000, 'claude-x': 4096 } } })
    )
  })

  it('restores the loaded rows on reset', async () => {
    const w = await render()
    await w.get('[data-testid="claude-max-tokens-add"]').trigger('click')
    await inputs(w, 2)[0].setValue('gpt-5')
    await inputs(w, 2)[1].setValue('1')
    expect(rows(w)).toHaveLength(3)
    await w.get('[data-testid="settings-reset"]').trigger('click')
    expect(rows(w)).toHaveLength(2)
  })

  it('refuses invalid rows before saving, keeps them in sight when turned off, and shows server field errors', async () => {
    const w = await render()
    await inputs(w, 1)[1].setValue('0')
    await turnOff(w)
    await save(w)
    expect(api.put).not.toHaveBeenCalled()
    expect(w.get('[data-testid="gateway-claude-max-tokens"]').text()).toContain('每行都要填 1 到 1048576 之间的整数')

    await inputs(w, 1)[1].setValue('100')
    api.put.mockRejectedValueOnce(
      new ApiError(400, {
        code: 'invalid_argument',
        message: 'invalid',
        details: { fields: [{ field: 'media_download_max_mb', code: 'out_of_range', message: '取值范围为 1 到 1024' }] }
      })
    )
    await save(w)
    expect(w.text()).toContain('取值范围为 1 到 1024')
  })

  it('shows a server error of the rule, such as too_large, beside its rows while it is off', async () => {
    const w = await render()
    await turnOff(w)
    api.put.mockRejectedValueOnce(
      new ApiError(400, {
        code: 'invalid_argument',
        message: 'invalid',
        details: { fields: [{ field: 'claude_max_tokens', code: 'too_large', message: '模型名总长度过长' }] }
      })
    )
    await save(w)
    expect(w.get('[data-testid="gateway-claude-max-tokens"]').text()).toContain('模型名总长度过长')
  })
})

describe('request body limit and platform call timeout (forwarding v2 §8.1, §8.4)', () => {
  const field = (w: Awaited<ReturnType<typeof render>>, name: string) =>
    w.findAllComponents(SSettingRow).find(r => r.find(`input[name="${name}"]`).exists())!

  it('shows the saved limit with its range and saves a new one', async () => {
    const w = await render()
    const input = w.get('input[name="max_request_body_mb"]')
    expect((input.element as HTMLInputElement).value).toBe('256')
    expect(input.attributes('min')).toBe('1')
    expect(input.attributes('max')).toBe('1024')
    const row = field(w, 'max_request_body_mb').text()
    expect(row).toContain('请求体上限')
    expect(row).toContain('每个网关请求')
    expect(row).toContain('端点、分组和插件都不能另设')
    expect(row).not.toContain('内部上限')
    expect(row).toContain('MB')

    await input.setValue('512')
    await save(w)
    expect(api.put).toHaveBeenCalledWith('/settings/gateway', expect.objectContaining({ max_request_body_mb: 512 }))
  })

  it('falls back to 128 when the server sends none, refuses a fraction and shows the server range error', async () => {
    api.get.mockResolvedValue({ ...saved, max_request_body_mb: undefined })
    const w = await render()
    const input = w.get('input[name="max_request_body_mb"]')
    expect((input.element as HTMLInputElement).value).toBe('128')

    await input.setValue('1.5')
    await save(w)
    expect(api.put).not.toHaveBeenCalled()
    expect(field(w, 'max_request_body_mb').text()).toContain('整数')

    await input.setValue('2048')
    api.put.mockRejectedValueOnce(
      new ApiError(400, {
        code: 'invalid_argument',
        message: 'invalid',
        details: { fields: [{ field: 'max_request_body_mb', code: 'out_of_range', message: '取值范围为 1 到 1024' }] }
      })
    )
    await save(w)
    expect(field(w, 'max_request_body_mb').text()).toContain('取值范围为 1 到 1024')
  })

  it('describes the platform call timeout as the error classification and disconnect policy calls, in both languages', async () => {
    const zh = field(await render(), 'platform_call_timeout_ms').text()
    expect(zh).toContain('错误分类')
    expect(zh).toContain('客户端中途断开时')
    expect(zh).toContain('不限时间')
    i18n.global.locale.value = 'en'
    const en = await render()
    const timeout = field(en, 'platform_call_timeout_ms').text()
    expect(timeout).toContain('classifying a failed upstream request')
    expect(timeout).toContain('When the client disconnects mid-response')
    expect(timeout).toContain('has no time limit')
    expect(field(en, 'max_request_body_mb').text()).toContain('Request body limit')
  })
})

describe('claude_max_tokens rows', () => {
  it('keeps the server rows as they are and checks names, duplicates and values', () => {
    expect(maxTokensRows(null)).toEqual([])
    expect(maxTokensMap([])).toEqual({ map: {} })
    const base = maxTokensRows({ m: 1 })
    expect(maxTokensMap([...base, { model: '', tokens: null }])).toEqual({ map: { m: 1 } })
    expect(maxTokensMap([...base, { model: ' m ', tokens: 2 }])).toEqual({ error: 'duplicate' })
    expect(maxTokensMap([...base, { model: '', tokens: 2 }])).toEqual({ error: 'model' })
    expect(maxTokensMap([...base, { model: 'x'.repeat(201), tokens: 2 }])).toEqual({ error: 'model' })
    // Characters, not UTF-16 units, as the server counts them.
    expect(maxTokensMap([{ model: '😀'.repeat(200), tokens: 2 }])).toEqual({ map: { ['😀'.repeat(200)]: 2 } })
    expect(maxTokensMap([{ model: '😀'.repeat(201), tokens: 2 }])).toEqual({ error: 'model' })
    expect(maxTokensMap([...base, { model: 'n', tokens: 0 }])).toEqual({ error: 'value' })
    expect(maxTokensMap([...base, { model: 'n', tokens: 1048577 }])).toEqual({ error: 'value' })
    expect(maxTokensMap([...base, { model: 'n', tokens: 1.5 }])).toEqual({ error: 'value' })
    const hundred = Array.from({ length: 100 }, (_, i) => ({ model: `m${i}`, tokens: 1 }))
    expect('map' in maxTokensMap(hundred)).toBe(true)
    expect(maxTokensMap([...hundred, { model: 'n', tokens: 1 }])).toEqual({ error: 'too_many' })
  })
})
