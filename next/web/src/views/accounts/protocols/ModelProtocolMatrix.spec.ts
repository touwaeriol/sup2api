import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { enableAutoUnmount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { claude, mountEditor, openai } from './fixtures'
import ModelProtocolMatrix from './ModelProtocolMatrix.vue'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))
enableAutoUnmount(afterEach)

const MODELS = ['gpt-5.6', 'gpt-5.6-mini', 'claude-sonnet-4-5']
const render = (saved: Pick<Account, 'protocols' | 'model_protocols'> | null = null, models = MODELS, accountType: AccountType = openai) =>
  mountEditor(editor => h(ModelProtocolMatrix, { editor, models, mapping: { 'claude-sonnet-4-5': 'gpt-5.6' } }), { saved, models, accountType })
const row = (w: VueWrapper, model: string) => w.get(`tr[data-model="${model}"]`)
const cell = (r: Omit<DOMWrapper<Element>, 'exists'>, protocol: string) => r.get(`td[data-protocol="${protocol}"] input`)
/** The ticked protocols of a row. */
const ticked = (r: Omit<DOMWrapper<Element>, 'exists'>) => r.findAll('td[data-protocol]').filter(td => (td.get('input').element as HTMLInputElement).checked).map(td => td.attributes('data-protocol'))

describe('model protocol matrix', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('rows the models of the account and the other models, columns the open own protocols and the conversions', () => {
    const { w } = render({ protocols: ['openai.chat', 'openai.responses'] })
    expect(w.findAll('tbody tr').map(r => r.attributes('data-model'))).toEqual([...MODELS, '*'])
    expect(w.findAll('thead tr:last-child th[data-protocol]').map(th => th.attributes('data-protocol'))).toEqual(['openai.chat', 'openai.responses', 'anthropic.messages', 'gemini.generate'])
    expect(w.get('[data-testid="protocol-matrix-own"]').text()).toBe('本平台（OpenAI）')
    expect(w.get('[data-testid="protocol-matrix-own"]').attributes('colspan')).toBe('2')
    expect(w.get('[data-testid="protocol-matrix-cross"]').text()).toBe('协议转换（默认不提供）')
    // A conversion column names its platform and grade; the other models read as such, a mapped model shows its target.
    expect(w.get('th[data-protocol="anthropic.messages"]').text()).toBe('MessagesAnthropic一般')
    expect(row(w, '*').get('th').text()).toBe('其余模型')
    expect(row(w, 'claude-sonnet-4-5').get('th').text()).toBe('claude-sonnet-4-5映射到 gpt-5.6')
  })

  it('shows a row without a rule as the default: the open own protocols ticked, no conversion', () => {
    const { w } = render({ protocols: ['openai.chat', 'openai.responses'], model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }] })
    const plain = row(w, 'gpt-5.6')
    expect(plain.attributes('data-default')).toBe('true')
    expect(ticked(plain)).toEqual(['openai.chat', 'openai.responses'])
    expect(plain.get('td:last-child').text()).toBe('默认')
    expect(plain.find('[data-testid="protocol-matrix-reset-row"]').exists()).toBe(false)
    const explicit = row(w, 'claude-sonnet-4-5')
    expect(explicit.attributes('data-default')).toBeUndefined()
    expect(ticked(explicit)).toEqual(['anthropic.messages'])
    expect(explicit.get('td:last-child').text()).toBe('恢复默认')
    expect(ticked(row(w, '*'))).toEqual(['openai.chat', 'openai.responses'])
  })

  it('shows a row without a rule on the protocols of the other models once those have a rule', async () => {
    // As the gateway judges: gpt-5.6-mini has no rule, so it goes by "*": chat and the Messages conversion, not Responses.
    const { w, editor } = render({ model_protocols: [{ models: ['*'], protocols: ['openai.chat', 'anthropic.messages'] }, { models: ['gpt-5.6'], protocols: ['openai.responses'] }] })
    const follows = row(w, 'gpt-5.6-mini')
    expect(follows.attributes('data-default')).toBe('true')
    expect(follows.attributes('data-follows-rest')).toBe('true')
    expect(ticked(follows)).toEqual(['openai.chat', 'anthropic.messages'])
    expect(follows.get('td:last-child').text()).toBe('跟随其余模型')
    expect(ticked(row(w, 'gpt-5.6'))).toEqual(['openai.responses'])
    expect(row(w, 'gpt-5.6').attributes('data-follows-rest')).toBeUndefined()
    expect(row(w, '*').attributes('data-follows-rest')).toBeUndefined()
    expect(w.text()).toContain('浅色 = 未单独指定：跟随“其余模型”一行，只在它勾选的协议下提供')
    // A cell ticked on it starts from what it showed.
    await cell(follows, 'openai.responses').setValue(true)
    expect(editor.rules.value['gpt-5.6-mini']).toEqual(['openai.chat', 'anthropic.messages', 'openai.responses'])
    // Restored, gpt-5.6 follows the other models too.
    await row(w, 'gpt-5.6').get('[data-testid="protocol-matrix-reset-row"]').trigger('click')
    expect(ticked(row(w, 'gpt-5.6'))).toEqual(['openai.chat', 'anthropic.messages'])
    expect(row(w, 'gpt-5.6').get('td:last-child').text()).toBe('跟随其余模型')
  })

  it('makes a row explicit when a cell is ticked, keeping the defaults, and default again from its end', async () => {
    const { w, editor } = render()
    await cell(row(w, 'gpt-5.6'), 'anthropic.messages').setValue(true)
    expect(editor.rules.value).toEqual({ 'gpt-5.6': ['openai.chat', 'openai.responses', 'openai.responses_ws', 'openai.embeddings', 'anthropic.messages'] })
    expect(row(w, 'gpt-5.6').attributes('data-default')).toBeUndefined()
    // The Gemini column ticks both protocols.
    await cell(row(w, '*'), 'gemini.generate').setValue(true)
    expect(editor.rules.value['*'].slice(-2)).toEqual(['gemini.generate', 'gemini.stream_generate'])
    await row(w, 'gpt-5.6').get('[data-testid="protocol-matrix-reset-row"]').trigger('click')
    expect(Object.keys(editor.rules.value)).toEqual(['*'])
    expect(row(w, 'gpt-5.6').attributes('data-default')).toBe('true')
    await w.get('[data-testid="protocol-matrix-reset-all"]').trigger('click')
    expect(editor.rules.value).toEqual({})
    expect(w.get('[data-testid="protocol-matrix-reset-all"]').attributes('disabled')).toBeDefined()
  })

  it('gives the selected models one protocol set, or their default back', async () => {
    const { w, editor } = render({ protocols: ['openai.chat', 'openai.responses'] })
    expect(w.find('[data-testid="protocol-matrix-bulk"]').exists()).toBe(false)
    // The other models are not a row to select.
    expect(row(w, '*').find('[data-testid="protocol-matrix-select"]').exists()).toBe(false)
    await row(w, 'gpt-5.6').get('[data-testid="protocol-matrix-select"]').setValue(true)
    await row(w, 'claude-sonnet-4-5').get('[data-testid="protocol-matrix-select"]').setValue(true)
    const bar = w.get('[data-testid="protocol-matrix-bulk"]')
    expect(bar.text()).toContain('已选 2 个模型')
    // The draft starts from the default: the open own protocols.
    // The chip's root carries the protocol, its button the state.
    const chip = (protocol: string) => bar.get(`[data-protocol="${protocol}"] button`)
    expect(bar.findAll('[data-protocol]').map(c => [c.attributes('data-protocol'), c.get('button').attributes('aria-pressed'), c.text()])).toEqual([['openai.chat', 'true', 'Chat Completions'], ['openai.responses', 'true', 'Responses'], ['anthropic.messages', 'false', 'Anthropic Messages · 转换'], ['gemini.generate', 'false', 'Gemini generateContent · 转换']])
    await chip('openai.responses').trigger('click')
    await chip('anthropic.messages').trigger('click')
    await bar.get('[data-testid="protocol-matrix-apply"]').trigger('click')
    expect(editor.binding().model_protocols).toEqual([{ models: ['gpt-5.6', 'claude-sonnet-4-5'], protocols: ['openai.chat', 'anthropic.messages'] }])
    expect(w.find('[data-testid="protocol-matrix-bulk"]').exists()).toBe(false)

    await w.get('[data-testid="protocol-matrix-select-all"]').setValue(true)
    expect(w.get('[data-testid="protocol-matrix-bulk"]').text()).toContain('已选 3 个模型')
    await w.get('[data-testid="protocol-matrix-reset-selected"]').trigger('click')
    expect(editor.rules.value).toEqual({})
  })

  it('adds a row for a model that only has a rule, and drops a column the account closes', async () => {
    const { w, editor } = render({ model_protocols: [{ models: ['extra-model'], protocols: ['openai.embeddings'] }] })
    expect(w.findAll('tbody tr').map(r => r.attributes('data-model'))).toEqual([...MODELS, 'extra-model', '*'])
    editor.setMode('custom')
    editor.toggleOwn(editor.columns.value.own[3])
    await w.vm.$nextTick()
    expect(w.find('th[data-protocol="openai.embeddings"]').exists()).toBe(false)
    expect(ticked(row(w, 'gpt-5.6'))).toEqual(['openai.chat', 'openai.responses', 'openai.responses_ws'])
    expect(editor.issues.value.map(i => i.code)).toEqual(['closed_protocol'])
  })

  it('half-ticks a column whose protocols are only partly saved', () => {
    const { w } = render({ model_protocols: [{ models: ['gpt-5.6'], protocols: ['openai.chat', 'gemini.generate'] }] })
    expect((cell(row(w, 'gpt-5.6'), 'gemini.generate').element as HTMLInputElement).indeterminate).toBe(true)
  })

  it('heads a two-hop conversion with its grade', () => {
    const { w } = render(null, ['claude-opus-4-1'], claude)
    expect(w.get('[data-testid="protocol-matrix-own"]').text()).toBe('本平台（Anthropic）')
    expect(w.get('th[data-protocol="gemini.generate"]').text()).toBe('generateContentGemini不推荐')
  })
})
