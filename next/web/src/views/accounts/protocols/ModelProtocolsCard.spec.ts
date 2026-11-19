import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { enableAutoUnmount, type VueWrapper } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { claude, mountEditor, openai } from './fixtures'
import ModelProtocolsCard from './ModelProtocolsCard.vue'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))
enableAutoUnmount(afterEach)

interface Options { models?: string[]; accountType?: AccountType; errors?: Record<string, string>; compact?: boolean }
const render = (saved: Pick<Account, 'protocols' | 'model_protocols'> | null = null, { models = ['gpt-5.6', 'claude-sonnet-4-5'], accountType = openai, errors = {}, compact = false }: Options = {}) =>
  mountEditor(editor => h(ModelProtocolsCard, { editor, models, options: models, errors }), { saved, models, accountType, compact })
const view = (w: VueWrapper, label: string) => w.get('[data-testid="model-protocols-view"]').findAll('label').find(l => l.text() === label)!.get('input')
const issues = (w: VueWrapper) => w.findAll('[data-issue]').map(i => [i.attributes('data-issue'), i.text()])

describe('model protocols card', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('opens on the matrix for an account with models, on the list for one serving every model or on a phone', () => {
    expect(render().w.find('[data-testid="protocol-matrix"]').exists()).toBe(true)
    expect(render(null, { models: [] }).w.find('[data-testid="protocol-list"]').exists()).toBe(true)
    expect(render(null, { compact: true }).w.find('[data-testid="protocol-list"]').exists()).toBe(true)
  })

  it('switches between the matrix, the list and JSON over the same rules', async () => {
    const { w, editor } = render({ model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }] })
    await view(w, '列表').setValue(true)
    expect(w.findAll('[data-testid="protocol-entry"]')).toHaveLength(1)
    await view(w, 'JSON').setValue(true)
    const box = w.get('[data-testid="model-protocols-json"]')
    expect((box.element as HTMLTextAreaElement).value).toContain('{"models": ["claude-sonnet-4-5"], "protocols": ["anthropic.messages"]}')
    await box.setValue('{"protocols": ["openai.chat"], "model_protocols": [{"models": ["*"], "protocols": ["openai.chat", "anthropic.messages"]}]}')
    await box.trigger('blur')
    expect(editor.binding()).toEqual({ protocols: ['openai.chat'], model_protocols: [{ models: ['*'], protocols: ['openai.chat', 'anthropic.messages'] }] })
    await view(w, '矩阵').setValue(true)
    expect(w.findAll('tbody tr[data-default]').map(r => r.attributes('data-model'))).toEqual(['gpt-5.6', 'claude-sonnet-4-5'])
  })

  it('keeps the JSON view open on a text it cannot take, and says why', async () => {
    const { w, editor } = render()
    await view(w, 'JSON').setValue(true)
    const box = w.get('[data-testid="model-protocols-json"]')
    await box.setValue('{"model_protocols": [{"models": ["*", "a"], "protocols": []}]}')
    await view(w, '矩阵').setValue(true)
    expect(editor.view.value).toBe('json')
    expect(w.text()).toContain('"*" 只能单独成一条')
    await box.setValue('{"model_protocols": ')
    await box.trigger('blur')
    expect(w.text()).toContain('JSON 格式错误')
    await box.setValue('{"model_protocols": [{"models": ["a"], "protocols": []}, {"models": ["a"], "protocols": []}]}')
    await box.trigger('blur')
    expect(w.text()).toContain('模型重复：a')
  })

  it('reports what blocks saving, what has no effect and what to be careful with', () => {
    const { w } = render({
      protocols: ['openai.chat'],
      model_protocols: [{ models: ['gpt-5.6'], protocols: [] }, { models: ['claude-sonnet-4-5'], protocols: ['openai.embeddings', 'anthropic.messages', 'zz.gone'] }, { models: ['*'], protocols: ['openai.chat', 'gemini.generate', 'gemini.stream_generate'] }]
    })
    expect(issues(w)).toEqual([
      ['empty_rule', 'gpt-5.6：至少选一个协议，或恢复默认。'],
      ['closed_protocol', 'claude-sonnet-4-5 指定了账号未开放的协议 Embeddings，这一项不生效。'],
      ['unknown_protocol', 'claude-sonnet-4-5 指定了此账号类型不能服务的协议 zz.gone，保存时会被拒绝。'],
      ['rest_conversion', '“其余模型”开了协议转换：所有没有单独列出的模型都会接受转换，上游不认识的模型名可能触发故障策略。建议只给有映射的模型开。']
    ])
    expect(issues(render({ protocols: [] }).w)).toEqual([['no_protocol', '账号没有开放任何协议。要停用账号请用“参与调度”开关。']])
    expect(render().w.find('[data-testid="protocol-issues"]').exists()).toBe(false)
  })

  it('notes a conversion through two converters', () => {
    const { w } = render({ model_protocols: [{ models: ['claude-opus-4-1'], protocols: ['anthropic.messages', 'gemini.generate', 'gemini.stream_generate'] }] }, { accountType: claude, models: ['claude-opus-4-1'] })
    expect(issues(w)).toEqual([['two_hop', 'claude-opus-4-1 → Gemini generateContent：要经过两次转换，质量较差。']])
  })

  it('words the server errors with the models of the entry they are about', async () => {
    const errors = { model_protocols: '最多 50 条', 'model_protocols[1].protocols[0]': '该账号类型不能服务此协议', name: '已被使用' }
    const { w } = render({ model_protocols: [{ models: ['gpt-5.6'], protocols: ['openai.chat'] }, { models: ['*'], protocols: ['openai.images'] }] }, { errors })
    const lines = () => w.findAll('[data-testid="model-protocols-error"]').map(l => l.text())
    expect(lines()).toEqual(['最多 50 条', '第 2 条（其余模型）：该账号类型不能服务此协议'])
    // In the list the entry's error sits under its card.
    await view(w, '列表').setValue(true)
    expect(lines()).toEqual(['最多 50 条'])
    expect(w.findAll('[data-testid="protocol-entry"]')[1].text()).toContain('该账号类型不能服务此协议')
  })
})
