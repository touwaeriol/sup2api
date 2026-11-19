import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import PlatformIcon from '@/components/platform/PlatformIcon.vue'
import { i18n } from '@/i18n'
import { codex, openai } from './protocols/fixtures'
import AccountProtocolMarks from './AccountProtocolMarks.vue'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))

/** `accountType` null: the type of the account is gone. */
const render = (account: Pick<Account, 'protocols' | 'model_protocols'>, accountType: AccountType | null = openai) =>
  mount(AccountProtocolMarks, { props: { account, accountType: accountType ?? undefined }, global: { plugins: [i18n] } })
type View = ReturnType<typeof render>
/** [platform, hover text] of each mark. */
const marks = (w: View) => w.findAllComponents(PlatformIcon).map(i => [i.props('id'), i.props('detail')])

describe('account protocol marks', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('marks the platform of an account left at the defaults, listing its protocols on hover', () => {
    const w = render({})
    expect(marks(w)).toEqual([['openai', 'OpenAI：全部本平台协议（Chat Completions、Responses、Responses WebSocket、Embeddings）']])
    expect(w.find('[data-testid="account-platform-partial"]').exists()).toBe(false)
    expect(w.find('[data-testid="account-platform-converted"]').exists()).toBe(false)
  })

  it('marks an account that opens some own protocols only as partial', () => {
    const w = render({ protocols: ['openai.chat'] })
    expect(w.get('[data-testid="account-platform-partial"]').text()).toBe('部分 1/4')
    expect(marks(w)).toEqual([['openai', 'OpenAI：Chat Completions']])
    expect(marks(render({ protocols: [], model_protocols: [{ models: ['a'], protocols: ['anthropic.messages'] }] }))[0]).toEqual(['openai', 'OpenAI：本平台协议均未开放'])
  })

  it('adds a marked platform for a conversion some models turn on, with the models on hover', () => {
    const w = render({ model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }, { models: ['*'], protocols: ['openai.chat', 'gemini.generate', 'gemini.stream_generate'] }] })
    expect(marks(w).slice(1)).toEqual([
      ['anthropic', 'Anthropic Messages（经转换）：1 个模型 claude-sonnet-4-5'],
      ['gemini', 'Gemini generateContent（经转换）：1 个模型 其余模型']
    ])
    expect(w.findAll('[data-converted]')).toHaveLength(2)
    expect(w.findAll('[data-testid="account-platform-converted"]').map(m => m.text())).toEqual(['转', '转'])
  })

  it('says which own protocol is adapted, and shows nothing for a type that is gone', () => {
    expect(marks(render({}, codex))).toEqual([['openai', 'OpenAI：全部本平台协议（Responses、Chat Completions（适配））']])
    expect(render({}, null).find('[data-testid="account-platforms"]').exists()).toBe(false)
  })
})
