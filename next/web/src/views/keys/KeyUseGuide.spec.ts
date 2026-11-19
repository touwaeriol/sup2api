import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { ApiKey, GroupServedProtocol } from '@/api/types'
import PlatformEndpointsPreview from '@/views/platforms/PlatformEndpointsPreview.vue'
import KeyUseGuide from './KeyUseGuide.vue'
import { gatewayBase } from './keyClients'

const { post, open, notifyError } = vi.hoisted(() => ({ post: vi.fn(), open: vi.fn(), notifyError: vi.fn() }))
vi.mock('@sub2api/host', () => ({ api: { post } }))
vi.mock('./openApp', () => ({ useOpenApp: () => open }))
vi.mock('@/utils/errors', () => ({ notifyError }))
vi.mock('@/composables/platforms', () => ({
  usePlatforms: () => ({ platforms: { value: [] }, load: vi.fn(), label: (v: string) => v, find: () => undefined })
}))
enableAutoUnmount(afterEach)
const key = (k: Partial<ApiKey> = {}) =>
  ({ id: 7, name: 'laptop', group_id: 1, key_prefix: 'sk-s2a-abcd', status: 'active', created_at: '', copyable: true, ...k }) as ApiKey
const base = gatewayBase()

async function guide(props: { apiKey?: ApiKey; platforms?: string[] | null; served?: GroupServedProtocol[] | null } = {}) {
  const w = mount(KeyUseGuide, {
    props: { apiKey: key(), platforms: ['anthropic', 'openai'], ...props },
    global: { plugins: [i18n], stubs: { Teleport: true } }
  })
  await flushPromises()
  return w
}
const tabs = (w: VueWrapper) => w.findAll('[role="tab"]')
const pickTab = (w: VueWrapper, label: string) =>
  tabs(w)
    .find(t => t.text() === label)!
    .trigger('click')
const imports = (w: VueWrapper) => w.findAll('[data-testid^="guide-import-"]').map(b => b.text())
/** The blocks of the open tab: [caption, content, whether it can be copied]. */
const blocks = (w: VueWrapper) =>
  w.findAll('[data-testid="key-use-snippet"]').map(code => {
    const head = code.element.previousElementSibling!
    return [head.querySelector('span')!.textContent, code.text(), !head.querySelector('button')!.hasAttribute('disabled')]
  })

describe('API key use guide', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    post.mockResolvedValue({ key: 'sk-s2a-full-secret' })
  })
  it("fills the full key into the client configuration of the key's platforms", async () => {
    const w = await guide()
    expect(post).toHaveBeenCalledWith('/me/api-keys/7/reveal', {})
    expect(tabs(w).map(t => t.text())).toEqual(['Claude Code', 'Codex CLI', 'Magpie'])
    expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('sk-s2a-full-secret')
    expect(blocks(w).map(([, , copyable]) => copyable)).toEqual([true, true])
  })
  it('imports the open client into CC Switch or the whole key into Magpie', async () => {
    const w = await guide()
    expect(imports(w)).toEqual(['导入 CC Switch', '导入 Magpie'])
    await w.get('[data-testid="guide-import-ccswitch:claude"]').trigger('click')
    expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'ccswitch:claude' }), 'sk-s2a-full-secret')
    await pickTab(w, 'Codex CLI')
    await w.get('[data-testid="guide-import-ccswitch:codex"]').trigger('click')
    expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'ccswitch:codex' }), 'sk-s2a-full-secret')
    await w.get('[data-testid="guide-import-magpie"]').trigger('click')
    expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'magpie' }), 'sk-s2a-full-secret')
  })
  it("lists what to fill into Magpie under the names of Magpie's own form: the key, each protocol's address and the balance query", async () => {
    const w = await guide({ platforms: ['anthropic', 'openai', 'gemini'] })
    await pickTab(w, 'Magpie')
    expect(imports(w)).toEqual(['导入 Magpie'])
    expect(blocks(w)).toEqual([
      ['API 密钥', 'sk-s2a-full-secret', true],
      ['Anthropic 地址（网关根地址，不带 /v1）', base, true],
      ['OpenAI 地址（Chat Completions）', `${base}/v1`, true],
      ['Responses 地址', `${base}/v1`, true],
      ['Gemini 地址', `${base}/v1beta`, true],
      ['余额查询地址（Magpie 用这个 Key 请求）', `${base}/v1/usage`, true],
      ['余额字段', 'remaining', true]
    ])
  })
  it('lists only the addresses of the protocols the key reaches, in English too', async () => {
    i18n.global.locale.value = 'en'
    const w = await guide({ platforms: ['anthropic'] })
    await pickTab(w, 'Magpie')
    expect(blocks(w).map(([caption]) => caption)).toEqual([
      'API key',
      'Anthropic URL (the gateway root, without /v1)',
      'Balance URL (Magpie asks it with this key)',
      'Balance field'
    ])
  })
  it('shows a legacy key by its prefix without revealing or importing it', async () => {
    const w = await guide({ apiKey: key({ copyable: false }), platforms: ['anthropic'] })
    expect(post).not.toHaveBeenCalled()
    expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('sk-s2a-abcd…')
    expect(imports(w)).toEqual([])
    await pickTab(w, 'Magpie')
    expect(imports(w)).toEqual([])
    // The addresses are still the gateway's own; only the stand-in key is not worth copying.
    expect(blocks(w).map(([, content, copyable]) => [content, copyable])).toEqual([
      ['sk-s2a-abcd…', false],
      [base, true],
      [`${base}/v1/usage`, true],
      ['remaining', true]
    ])
  })
  it('falls back to the prefix when the key cannot be revealed', async () => {
    const failure = new Error('boom')
    post.mockRejectedValue(failure)
    const w = await guide()
    expect(notifyError).toHaveBeenCalledWith(failure)
    expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('sk-s2a-abcd…')
    expect(imports(w)).toEqual([])
  })
  it('lists the endpoints the key can call by the protocols its group serves', async () => {
    const served = [{ protocol: 'anthropic.messages' }]
    const w = await guide({ platforms: ['anthropic'], served })
    expect(w.getComponent(PlatformEndpointsPreview).props()).toEqual({ served, max: undefined })
    expect((await guide({ platforms: ['anthropic'], served: [] })).findComponent(PlatformEndpointsPreview).exists()).toBe(false)
    expect((await guide({ platforms: ['anthropic'] })).findComponent(PlatformEndpointsPreview).exists()).toBe(false)
  })
  it('has no client or app to set up for other platforms', async () => {
    const w = await guide({ platforms: ['volcengine'] })
    expect(tabs(w)).toEqual([])
    expect(w.find('[data-testid="key-use-snippet"]').exists()).toBe(false)
    expect(w.get('[data-testid="key-use-guide"]').text()).toContain('没有内置的客户端配置')
    expect(w.get('[data-testid="key-usage-query"]').text()).toBe(`curl -H "Authorization: Bearer sk-s2a-full-secret" ${base}/v1/usage`)
  })
  it('starts over on the first tab for another key', async () => {
    const w = await guide()
    await pickTab(w, 'Magpie')
    await w.setProps({ apiKey: key({ id: 8 }) })
    await flushPromises()
    expect(post).toHaveBeenLastCalledWith('/me/api-keys/8/reveal', {})
    expect(
      tabs(w)
        .find(t => t.attributes('aria-selected') === 'true')!
        .text()
    ).toBe('Claude Code')
    expect(imports(w)).toEqual(['导入 CC Switch', '导入 Magpie'])
  })

  describe('when a reveal answers after the guide moved on', () => {
    /** Reveals that answer when told to, by key id. */
    function pendingReveals() {
      const answer = new Map<number, (v: { key: string }) => void>()
      post.mockImplementation((path: string) => new Promise(resolve => answer.set(Number(path.split('/')[3]), resolve)))
      return (id: number) => answer.get(id)!({ key: `sk-s2a-secret-of-${id}` })
    }
    const legacy = key({ id: 9, name: 'old-script', key_prefix: 'sk-s2a-old9', copyable: false })

    it('does not fill the key of the guide closed before into a legacy key opened next', async () => {
      const answer = pendingReveals()
      const w = await guide()
      expect(w.find('[data-testid="key-use-guide"]').exists()).toBe(false)
      await w.setProps({ apiKey: null })
      await w.setProps({ apiKey: legacy })
      // The legacy key needs no reveal: its guide is not kept waiting for the other's.
      expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('sk-s2a-old9…')
      answer(7)
      await flushPromises()
      expect(w.get('[data-testid="key-use-guide"]').text()).not.toContain('secret-of-7')
      expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('sk-s2a-old9…')
      expect(imports(w)).toEqual([])
    })

    it('shows the key asked for last, whichever reveal answers last', async () => {
      const answer = pendingReveals()
      const w = await guide()
      await w.setProps({ apiKey: key({ id: 8, name: 'ci-runner' }) })
      answer(8)
      await flushPromises()
      expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('secret-of-8')
      answer(7)
      await flushPromises()
      expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('secret-of-8')
      expect(w.get('[data-testid="key-use-guide"]').text()).not.toContain('secret-of-7')
    })

    it('keeps waiting for the key it shows when an earlier reveal answers or fails first', async () => {
      const reject = new Map<number, (e: Error) => void>()
      post.mockImplementation((path: string) => new Promise((_, no) => reject.set(Number(path.split('/')[3]), no)))
      const w = await guide()
      await w.setProps({ apiKey: key({ id: 8 }) })
      reject.get(7)!(new Error('gone'))
      await flushPromises()
      expect(notifyError).not.toHaveBeenCalled()
      expect(w.find('[data-testid="key-use-guide"]').exists()).toBe(false)
      reject.get(8)!(new Error('boom'))
      await flushPromises()
      expect(notifyError).toHaveBeenCalledTimes(1)
      expect(w.get('[data-testid="key-use-snippet"]').text()).toContain('sk-s2a-abcd…')
    })
  })
})
