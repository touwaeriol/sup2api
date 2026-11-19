import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { AccountServedProtocol } from '@/api/types'
import { i18n } from '@/i18n'
import { claude, openai } from './protocols/fixtures'
import AccountTypeEndpoints from './AccountTypeEndpoints.vue'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))

const render = (props: Partial<InstanceType<typeof AccountTypeEndpoints>['$props']> = {}) => mount(AccountTypeEndpoints, { props: { endpoints: openai.endpoints, ...props }, global: { plugins: [i18n] } })
type View = ReturnType<typeof render>
const group = (w: View, platform: string) => w.get(`[data-platform="${platform}"]`)
const lines = (w: View, platform: string) => group(w, platform).findAll('li').map(li => li.text())

describe('endpoints of an account type', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('lists the endpoints by platform, converted ones with their grade', () => {
    const w = render({ compact: true })
    expect(lines(w, 'openai')).toEqual(['POST /openai.chat', 'POST /openai.responses', 'POST /openai.responses_ws', 'POST /openai.embeddings'])
    expect(lines(w, 'anthropic')).toEqual(['POST /anthropic.count_tokens本地估算', 'POST /anthropic.messages一般'])
    // Without the declared platforms nothing is said about how a conversion is turned on.
    expect(w.find('[data-testid="type-endpoints-needs-model"]').exists()).toBe(false)
  })

  it('says a platform the type does not declare is turned on per model on the account (type picker)', () => {
    const w = render({ compact: true, platforms: ['openai'] })
    expect(group(w, 'openai').find('[data-testid="type-endpoints-needs-model"]').exists()).toBe(false)
    expect(group(w, 'anthropic').get('[data-testid="type-endpoints-needs-model"]').text()).toBe('· 需在账号上为模型开启')
    expect(group(w, 'gemini').find('[data-testid="type-endpoints-needs-model"]').exists()).toBe(true)
  })

  it('greys what an account does not open and lists the models a protocol is limited to (account detail)', () => {
    const served: AccountServedProtocol[] = [
      { protocol: 'openai.chat', via: 'native', grade: 'native', models: null },
      { protocol: 'openai.embeddings', via: 'native', grade: 'native', models: ['text-embedding-3-large'] },
      { protocol: 'anthropic.messages', via: 'converted', grade: 'fair', hops: ['anthropic.messages', 'openai.chat'], models: ['a', 'b', 'c', '*'] }
    ]
    const w = render({ platforms: ['openai'], served })
    expect(lines(w, 'openai')).toEqual([
      'POST /openai.chatopenai.chat',
      'POST /openai.responsesopenai.responses未开放',
      'POST /openai.responses_wsopenai.responses_ws未开放',
      'POST /openai.embeddingsopenai.embeddings1 个模型：text-embedding-3-large'
    ])
    // count_tokens follows the generation protocol of its family: open with Messages, closed with Gemini.
    expect(lines(w, 'anthropic')).toEqual(['POST /anthropic.count_tokensanthropic.count_tokens本地估算4 个模型：a、b、c …', 'POST /anthropic.messagesanthropic.messages一般4 个模型：a、b、c …'])
    expect(group(w, 'anthropic').get('[data-testid="endpoint-models"]').attributes('title')).toBe('a、b、c、其余模型')
    expect(group(w, 'gemini').findAll('[data-closed]')).toHaveLength(3)
    expect(group(w, 'openai').findAll('[data-testid="endpoint-closed"]')).toHaveLength(2)
  })

  it('takes a count_tokens entry the server lists over its family', () => {
    const served: AccountServedProtocol[] = [{ protocol: 'anthropic.messages', via: 'native', grade: 'native', models: ['a'] }, { protocol: 'anthropic.count_tokens', via: 'native', grade: 'native', models: null }]
    const w = render({ endpoints: claude.endpoints, platforms: ['anthropic'], served })
    expect(lines(w, 'anthropic')).toEqual(['POST /anthropic.messagesanthropic.messages1 个模型：a', 'POST /anthropic.count_tokensanthropic.count_tokens'])
  })

  it('warns when the type serves no endpoint now', () => {
    expect(render({ endpoints: [] }).text()).toBe('当前没有已启用的端点')
  })
})
