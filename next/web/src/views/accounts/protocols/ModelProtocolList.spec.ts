import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { enableAutoUnmount, type VueWrapper } from '@vue/test-utils'
import type { Account } from '@/api/types'
import ModelTagSelect from '@/components/models/ModelTagSelect.vue'
import { i18n } from '@/i18n'
import { mountEditor } from './fixtures'
import ModelProtocolList from './ModelProtocolList.vue'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))
enableAutoUnmount(afterEach)

const render = (saved: Pick<Account, 'protocols' | 'model_protocols'> | null = null, errors?: Record<number, string[]>) =>
  mountEditor(editor => h(ModelProtocolList, { editor, options: ['gpt-5.6', 'gpt-5.6-mini'], errors }), { saved })
const entries = (w: VueWrapper) => w.findAll('[data-testid="protocol-entry"]')
/** The pressed protocols of an entry. */
const pressed = (w: VueWrapper, i: number) => entries(w)[i].findAll('[data-protocol]').filter(c => c.get('button').attributes('aria-pressed') === 'true').map(c => c.attributes('data-protocol'))
const chip = (w: VueWrapper, i: number, protocol: string) => entries(w)[i].get(`[data-protocol="${protocol}"] button`)

describe('model protocol list', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('says nothing is limited without a rule', () => {
    const { w } = render()
    expect(entries(w)).toHaveLength(0)
    expect(w.get('[data-testid="protocol-list-empty"]').text()).toBe('没有限定：所有模型在账号开放的本平台协议下提供，不做协议转换。')
  })

  it('shows a card per saved entry, its models and its protocols over every protocol of the type, and one for the other models', () => {
    const { w } = render({ model_protocols: [{ models: ['a', 'b'], protocols: ['openai.chat', 'anthropic.messages'] }, { models: ['*'], protocols: ['openai.chat'] }] })
    expect(entries(w)).toHaveLength(2)
    expect(entries(w)[0].getComponent(ModelTagSelect).props('modelValue')).toEqual(['a', 'b'])
    expect(entries(w)[0].findAll('[data-protocol]').map(c => c.text())).toEqual(['Chat Completions', 'Responses', 'Responses WebSocket', 'Embeddings', 'Anthropic Messages · 转换', 'Gemini generateContent · 转换'])
    expect(pressed(w, 0)).toEqual(['openai.chat', 'anthropic.messages'])
    expect(entries(w)[1].attributes('data-rest')).toBe('true')
    expect(entries(w)[1].text()).toContain('其余模型')
    expect(entries(w)[1].findComponent(ModelTagSelect).exists()).toBe(false)
    expect(w.find('[data-testid="protocol-entry-add-rest"]').exists()).toBe(false)
  })

  it('toggles a protocol for every model of the card; cards ticked alike stay apart and are saved as one entry', async () => {
    const { w, editor } = render({ model_protocols: [{ models: ['a', 'b'], protocols: ['openai.chat'] }, { models: ['c'], protocols: ['openai.chat', 'anthropic.messages'] }] })
    await chip(w, 0, 'anthropic.messages').trigger('click')
    expect(editor.binding().model_protocols).toEqual([{ models: ['a', 'b', 'c'], protocols: ['openai.chat', 'anthropic.messages'] }])
    expect(entries(w)).toHaveLength(2)
    // The Gemini chip presses both protocols, of this card only.
    await chip(w, 0, 'gemini.generate').trigger('click')
    expect(editor.rules.value.a).toEqual(['openai.chat', 'anthropic.messages', 'gemini.generate', 'gemini.stream_generate'])
    expect(editor.rules.value.c).toEqual(['openai.chat', 'anthropic.messages'])
  })

  it('changes one card chip by chip without touching a card it passes through', async () => {
    // m2 goes from chat to Responses; half way it has the protocols of m1.
    const { w, editor } = render({ model_protocols: [{ models: ['m1'], protocols: ['openai.chat', 'openai.responses'] }, { models: ['m2'], protocols: ['openai.chat'] }] })
    await chip(w, 1, 'openai.responses').trigger('click')
    await chip(w, 1, 'openai.chat').trigger('click')
    expect(editor.rules.value).toEqual({ m1: ['openai.chat', 'openai.responses'], m2: ['openai.responses'] })
  })

  it('keeps the card, and the focus in its model input, while its models change', async () => {
    const { w, editor } = render({ model_protocols: [{ models: ['a'], protocols: ['openai.chat'] }] })
    const card = entries(w)[0].element
    const input = entries(w)[0].getComponent(ModelTagSelect).get('input')
    ;(input.element as HTMLInputElement).focus()
    entries(w)[0].getComponent(ModelTagSelect).vm.$emit('update:modelValue', ['a', 'b'])
    await w.vm.$nextTick()
    expect(entries(w)[0].element).toBe(card)
    expect(document.activeElement).toBe(input.element)
    expect(editor.rules.value).toEqual({ a: ['openai.chat'], b: ['openai.chat'] })
  })

  it('starts the cards over when the rules are set elsewhere', async () => {
    const { w, editor } = render({ model_protocols: [{ models: ['a'], protocols: ['openai.chat'] }] })
    editor.setSaved({ model_protocols: [{ models: ['x'], protocols: ['openai.responses'] }, { models: ['*'], protocols: ['openai.chat'] }] })
    await w.vm.$nextTick()
    expect(entries(w)).toHaveLength(2)
    expect(entries(w)[0].getComponent(ModelTagSelect).props('modelValue')).toEqual(['x'])
  })

  it('starts a card from the default protocols, moves a model between cards and drops a card', async () => {
    const { w, editor } = render({ protocols: ['openai.chat', 'openai.responses'], model_protocols: [{ models: ['a'], protocols: ['anthropic.messages'] }] })
    const add = w.get('[data-testid="protocol-entry-add"]')
    add.getComponent(ModelTagSelect).vm.$emit('update:modelValue', ['gpt-5.6'])
    await w.vm.$nextTick()
    expect(editor.binding().model_protocols).toEqual([{ models: ['a'], protocols: ['anthropic.messages'] }, { models: ['gpt-5.6'], protocols: ['openai.chat', 'openai.responses'] }])
    // gpt-5.6 added to the first card takes that card's protocols and leaves its own.
    entries(w)[0].getComponent(ModelTagSelect).vm.$emit('update:modelValue', ['a', 'gpt-5.6'])
    await w.vm.$nextTick()
    expect(editor.binding().model_protocols).toEqual([{ models: ['a', 'gpt-5.6'], protocols: ['anthropic.messages'] }])
    // A model taken out goes back to the default.
    entries(w)[0].getComponent(ModelTagSelect).vm.$emit('update:modelValue', ['gpt-5.6'])
    await w.vm.$nextTick()
    expect(editor.rules.value).toEqual({ 'gpt-5.6': ['anthropic.messages'] })
    await add.get('[data-testid="protocol-entry-add-rest"]').trigger('click')
    expect(editor.rules.value['*']).toEqual(['openai.chat', 'openai.responses'])
    await entries(w)[0].get('[data-testid="protocol-entry-remove"]').trigger('click')
    expect(editor.binding().model_protocols).toEqual([{ models: ['*'], protocols: ['openai.chat', 'openai.responses'] }])
  })

  it('keeps a protocol the type no longer serves as a chip that can only be removed, and shows the errors of an entry under it', async () => {
    const { w, editor } = render({ model_protocols: [{ models: ['a'], protocols: ['openai.chat', 'zz.gone'] }] }, { 0: ['协议不可用'] })
    const gone = entries(w)[0].get('[data-protocol="zz.gone"]')
    expect(gone.find('button[aria-pressed]').exists()).toBe(false)
    expect(entries(w)[0].text()).toContain('协议不可用')
    await gone.get('button').trigger('click')
    expect(editor.rules.value).toEqual({ a: ['openai.chat'] })
  })
})
