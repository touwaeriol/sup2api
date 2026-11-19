import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { SSelect } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { PluginEgressPolicy } from './pluginEgress'
import PluginEgressSettingsCard from './PluginEgressSettingsCard.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
const toast = vi.hoisted(() => vi.fn())
const confirm = vi.hoisted(() => vi.fn(async () => true))
const perms = vi.hoisted(() => ({ manage: true }))
vi.mock('@sub2api/host', async original => ({ ...(await original<object>()), api }))
vi.mock('@sub2api/ui', async original => ({ ...(await original<object>()), toast, confirm }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (k: string) => k !== 'settings:manage' || perms.manage }) }))
enableAutoUnmount(afterEach)

let stored: PluginEgressPolicy

beforeEach(() => {
  vi.clearAllMocks()
  i18n.global.locale.value = 'zh'
  perms.manage = true
  stored = { mode: 'allow_all', blocked_domains: ['evil.com'], allowed_domains: ['good.com'] }
  api.get.mockImplementation(async () => structuredClone(stored))
  api.put.mockImplementation(async (_: string, patch: Partial<PluginEgressPolicy>) => {
    stored = { ...stored, ...patch }
    return structuredClone(stored)
  })
})

async function render() {
  const w = mount(PluginEgressSettingsCard, { global: { plugins: [i18n] }, attachTo: document.body })
  await flushPromises()
  return w
}

const body = () => document.body
const q = <T extends HTMLElement = HTMLElement>(sel: string) => body().querySelector<T>(sel)
const listText = (w: Awaited<ReturnType<typeof render>>) => w.get('[data-testid="plugin-egress-list"]').text()

async function openDialog(w: Awaited<ReturnType<typeof render>>) {
  await w.get('[data-testid="plugin-egress-add"]').trigger('click')
  await flushPromises()
}

/** The input or textarea of a field (the test id may sit on its wrapper). */
function field(sel: string): HTMLInputElement | HTMLTextAreaElement {
  const el = q(sel)!
  return el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement ? el : el.querySelector('input, textarea')!
}

async function type(sel: string, value: string) {
  const el = field(sel)
  el.value = value
  el.dispatchEvent(new Event('input'))
  await flushPromises()
}

async function click(sel: string) {
  q(sel)!.click()
  await flushPromises()
}

describe('plugin network access (CONTRACTS §113)', () => {
  it('shows the blocked list and its texts while everything is allowed, the allowed list once everything is blocked', async () => {
    const w = await render()
    expect(api.get).toHaveBeenCalledWith('/settings/plugin-egress')
    expect(w.get('[data-testid="plugin-egress-info"]').text()).toBe('插件可以访问所有域名，但您在下方屏蔽的域名除外。')
    expect(listText(w)).toContain('已屏蔽的域名')
    expect(listText(w)).toContain('evil.com')
    expect(listText(w)).not.toContain('good.com')

    expect(w.find('[data-testid="plugin-egress-block-all-warning"]').exists()).toBe(false)

    w.getComponent(SSelect).vm.$emit('update:modelValue', 'block_all')
    await flushPromises()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(api.put).toHaveBeenCalledWith('/settings/plugin-egress', { mode: 'block_all' })
    expect(toast).toHaveBeenCalledWith('已保存', 'success')
    expect(w.get('[data-testid="plugin-egress-info"]').text()).toBe('插件只能访问您在下方允许的域名。')
    expect(listText(w)).toContain('已允许的域名')
    expect(listText(w)).toContain('good.com')
    expect(listText(w)).not.toContain('evil.com')
    // The other list was not touched: switching back shows it again, and
    // allowing all domains needs no confirmation.
    w.getComponent(SSelect).vm.$emit('update:modelValue', 'allow_all')
    await flushPromises()
    expect(listText(w)).toContain('evil.com')
    expect(confirm).toHaveBeenCalledTimes(1)
  })

  it('confirms blocking all domains, which reaches the platform plugins upstreams too, and warns while it lasts', async () => {
    const w = await render()
    confirm.mockResolvedValueOnce(false)
    w.getComponent(SSelect).vm.$emit('update:modelValue', 'block_all')
    await flushPromises()
    const ask = (confirm.mock.calls[0] as unknown as [{ title: string; message: string; danger: boolean }])[0]
    expect(ask.title).toBe('屏蔽所有域名？')
    expect(ask.message).toContain('平台插件发往上游的请求也一样')
    expect(ask.danger).toBe(true)
    // Cancelled: nothing saved, still allowing all domains.
    expect(api.put).not.toHaveBeenCalled()
    expect(w.getComponent(SSelect).props('modelValue')).toBe('allow_all')
    expect(w.find('[data-testid="plugin-egress-block-all-warning"]').exists()).toBe(false)

    w.getComponent(SSelect).vm.$emit('update:modelValue', 'block_all')
    await flushPromises()
    expect(api.put).toHaveBeenCalledWith('/settings/plugin-egress', { mode: 'block_all' })
    const warning = w.get('[data-testid="plugin-egress-block-all-warning"]').text()
    expect(warning).toContain('平台插件发往上游的请求同样只能去允许的域名')
    expect(warning).toContain('api.anthropic.com')
  })

  it('warns on load when all domains are blocked already', async () => {
    stored.mode = 'block_all'
    const w = await render()
    expect(w.find('[data-testid="plugin-egress-block-all-warning"]').exists()).toBe(true)
    expect(w.get('[data-testid="plugin-egress-settings"]').text()).toContain('平台插件发往上游的请求')
  })

  it('says when the list is empty', async () => {
    stored.blocked_domains = []
    const w = await render()
    expect(listText(w)).toContain('尚未添加任何域名。')
  })

  it('adds one domain to the list of the mode', async () => {
    stored.mode = 'block_all'
    const w = await render()
    await openDialog(w)
    expect(body().textContent).toContain('添加要允许的域名')
    expect(field('[data-testid="plugin-egress-row-0"]').placeholder).toBe('website.com')
    await type('[data-testid="plugin-egress-row-0"]', ' API.Example.com ')
    await click('[data-testid="plugin-egress-submit"]')
    expect(api.put).toHaveBeenCalledWith('/settings/plugin-egress', { allowed_domains: ['good.com', 'api.example.com'] })
    expect(listText(w)).toContain('api.example.com')
    expect(q('[data-testid="plugin-egress-dialog"]')).toBeNull()
  })

  it('adds several domains, one field each, dropping duplicates and listed ones', async () => {
    const w = await render()
    await openDialog(w)
    expect(body().textContent).toContain('添加要屏蔽的域名')
    await click('[data-testid="plugin-egress-add-row"]')
    await click('[data-testid="plugin-egress-add-row"]')
    await type('[data-testid="plugin-egress-row-0"]', 'a.com')
    await type('[data-testid="plugin-egress-row-1"]', 'evil.com')
    await type('[data-testid="plugin-egress-row-2"]', 'A.com')
    await click('[data-testid="plugin-egress-submit"]')
    expect(api.put).toHaveBeenCalledWith('/settings/plugin-egress', { blocked_domains: ['evil.com', 'a.com'] })
  })

  it('adds in bulk, one domain per line, and names each line it cannot take', async () => {
    const w = await render()
    await openDialog(w)
    await type('[data-testid="plugin-egress-row-0"]', 'kept.com')
    await click('[data-testid="plugin-egress-bulk-toggle"]')
    expect(field('[data-testid="plugin-egress-bulk"]').value).toBe('kept.com')
    await type('[data-testid="plugin-egress-bulk"]', 'kept.com\n\nhttps://x.com/a\n b.org \nbad host\nb.org')
    await click('[data-testid="plugin-egress-submit"]')
    expect(api.put).not.toHaveBeenCalled()
    const errors = q('[data-testid="plugin-egress-bulk-errors"]')!.textContent
    expect(errors).toContain('第 3 行“https://x.com/a”不是有效的域名或 IP 地址')
    expect(errors).toContain('第 5 行“bad host”不是有效的域名或 IP 地址')
    await type('[data-testid="plugin-egress-bulk"]', 'kept.com\n\n b.org \nb.org')
    await click('[data-testid="plugin-egress-submit"]')
    expect(api.put).toHaveBeenCalledWith('/settings/plugin-egress', { blocked_domains: ['evil.com', 'kept.com', 'b.org'] })
  })

  it('keeps the dialog open on invalid or empty input', async () => {
    const w = await render()
    await openDialog(w)
    await click('[data-testid="plugin-egress-submit"]')
    expect(q('[data-testid="plugin-egress-empty"]')!.textContent).toBe('请至少填写一个域名。')
    await type('[data-testid="plugin-egress-row-0"]', 'example.com:443')
    await click('[data-testid="plugin-egress-submit"]')
    expect(api.put).not.toHaveBeenCalled()
    expect(q('[data-testid="plugin-egress-dialog"]')!.textContent).toContain('不是有效的域名或 IP 地址（不带 https://、端口和路径）')
  })

  it('removes a domain from the list', async () => {
    const w = await render()
    await w.get('[data-testid="plugin-egress-remove-evil.com"]').trigger('click')
    await flushPromises()
    expect(api.put).toHaveBeenCalledWith('/settings/plugin-egress', { blocked_domains: [] })
    expect(listText(w)).toContain('尚未添加任何域名。')
  })

  it('is read-only without settings:manage', async () => {
    perms.manage = false
    const w = await render()
    expect(w.find('[data-testid="plugin-egress-add"]').exists()).toBe(false)
    expect(w.find('[data-testid="plugin-egress-remove-evil.com"]').exists()).toBe(false)
    expect(w.getComponent(SSelect).props('disabled')).toBe(true)
  })
})
