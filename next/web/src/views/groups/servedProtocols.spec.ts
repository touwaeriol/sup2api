import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { BUILTIN_PLATFORMS } from '@/composables/platforms'
import { i18n } from '@/i18n'
import GroupServedProtocols from './GroupServedProtocols.vue'
import { endpointLine } from '@/views/platforms/listedEndpoint'
import { servedEndpoints } from './servedProtocols'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))

const served = [
  { protocol: 'openai.chat', accounts: 1 },
  { protocol: 'anthropic.messages', accounts: 3 },
  { protocol: 'anthropic.count_tokens', accounts: 3 }
]

describe('protocols a group serves', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('groups the endpoints of the served protocols by platform, in the catalog order', () => {
    expect(servedEndpoints(BUILTIN_PLATFORMS, served).map(g => [g.id, g.endpoints.map(endpointLine)])).toEqual([
      ['anthropic', ['POST /v1/messages', 'POST /v1/messages/count_tokens']],
      ['openai', ['POST /v1/chat/completions']]
    ])
    expect(servedEndpoints(BUILTIN_PLATFORMS, [])).toEqual([])
  })

  it('lists a protocol the catalog does not know by its id, under the platform it names', () => {
    const groups = servedEndpoints(BUILTIN_PLATFORMS, [{ protocol: 'openai.responses_ws', accounts: 1 }, { protocol: 'volcengine.images', accounts: 2 }])
    expect(groups.map(g => [g.id, g.endpoints])).toEqual([['openai', [{ protocol: 'openai.responses_ws' }]], ['volcengine', [{ protocol: 'volcengine.images' }]]])
  })

  it('shows each endpoint with the number of member accounts serving it', async () => {
    const w = mount(GroupServedProtocols, { props: { served }, global: { plugins: [i18n] } })
    await flushPromises()
    expect(w.findAll('[data-platform] li').map(li => li.text())).toEqual(['POST /v1/messages3 个账号', 'POST /v1/messages/count_tokens(不计费)3 个账号', 'POST /v1/chat/completions1 个账号'])
    // Gemini is served by no account: not listed, and the note says what that means.
    expect(w.find('div[data-platform="gemini"]').exists()).toBe(false)
    expect(w.text()).toContain('没有列出的端点没有账号提供，请求返回 404')
  })

  it('says so when the member accounts open nothing', async () => {
    const w = mount(GroupServedProtocols, { props: { served: [] }, global: { plugins: [i18n] } })
    await flushPromises()
    expect(w.get('[data-testid="group-served-none"]').text()).toBe('成员账号没有开放任何协议，请求返回 404。')
  })
})
