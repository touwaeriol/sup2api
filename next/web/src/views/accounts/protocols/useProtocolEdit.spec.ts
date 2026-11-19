import { describe, expect, it } from 'vitest'
import { defineComponent, ref } from 'vue'
import { mountPinned } from '@sub2api/ui/testing'
import type { AccountType } from '@/api/types'
import { codex, openai } from './fixtures'
import { useProtocolEdit, type ProtocolEditor } from './useProtocolEdit'

/** The editor state inside a component, on a wide screen unless `compact`. */
function editorOf(models: string[] = [], compact = false, at: AccountType | null = openai) {
  const type = ref<AccountType | null>(at)
  let editor!: ProtocolEditor
  mountPinned(defineComponent({ setup() { editor = useProtocolEdit(() => type.value, () => models); return () => null } }), {}, { compact })
  return { editor, type }
}
const column = (editor: ProtocolEditor, key: string) => [...editor.columns.value.own, ...editor.columns.value.cross].find(c => c.key === key)!

describe('protocol editor state', () => {
  it('starts a new account on every own protocol and no rule, saved as null and []', () => {
    const { editor } = editorOf()
    expect(editor.mode.value).toBe('all')
    expect(editor.binding()).toEqual({ protocols: null, model_protocols: [] })
    expect(editor.summary.value).toEqual({ open: 4, total: 4, limited: 0, converting: 0 })
    expect(editor.issues.value).toEqual([])
  })

  it('loads an account, the own protocols it chose and its rules by model', () => {
    const { editor } = editorOf()
    editor.setSaved({ protocols: ['openai.chat'], model_protocols: [{ models: ['a', 'b'], protocols: ['openai.chat', 'anthropic.messages'] }] })
    expect(editor.mode.value).toBe('custom')
    expect(editor.protocols.value).toEqual(['openai.chat'])
    expect(editor.rules.value).toEqual({ a: ['openai.chat', 'anthropic.messages'], b: ['openai.chat', 'anthropic.messages'] })
    expect(editor.defaults.value).toEqual(['openai.chat'])
    expect(editor.conversionUsed.value).toBe(true)
    expect(editor.binding()).toEqual({ protocols: ['openai.chat'], model_protocols: [{ models: ['a', 'b'], protocols: ['openai.chat', 'anthropic.messages'] }] })
    editor.setSaved(null)
    expect(editor.binding()).toEqual({ protocols: null, model_protocols: [] })
  })

  it('"chosen" starts from every own protocol and "all" remembers what was unticked', () => {
    const { editor } = editorOf()
    editor.toggleOwn(column(editor, 'openai.embeddings'))
    expect(editor.protocols.value).toBeNull()
    editor.setMode('custom')
    expect(editor.protocols.value).toEqual(['openai.chat', 'openai.responses', 'openai.responses_ws', 'openai.embeddings'])
    editor.toggleOwn(column(editor, 'openai.embeddings'))
    editor.toggleOwn(column(editor, 'openai.responses_ws'))
    expect(editor.binding().protocols).toEqual(['openai.chat', 'openai.responses'])
    editor.setMode('all')
    expect(editor.binding().protocols).toBeNull()
    editor.setMode('custom')
    expect(editor.binding().protocols).toEqual(['openai.chat', 'openai.responses'])
    // Every own protocol unticked and no conversion: nothing is served.
    editor.toggleOwn(column(editor, 'openai.chat'))
    editor.toggleOwn(column(editor, 'openai.responses'))
    expect(editor.binding().protocols).toEqual([])
    expect(editor.issues.value.map(i => i.code)).toEqual(['no_protocol'])
  })

  it('opens the matrix for an account with a model list on a wide screen, the list otherwise', () => {
    expect(editorOf(['gpt-5.6']).editor.view.value).toBe('matrix')
    expect(editorOf([]).editor.view.value).toBe('list')
    expect(editorOf(['gpt-5.6'], true).editor.view.value).toBe('list')
    // Loading an account picks the view again.
    const models: string[] = []
    const { editor } = editorOf(models)
    models.push('gpt-5.6')
    editor.setSaved({})
    expect(editor.view.value).toBe('matrix')
  })

  it('edits both fields as JSON and leaves the JSON view only once the text parses', () => {
    const { editor } = editorOf(['gpt-5.6'])
    editor.setView('json')
    expect(editor.text.value).toBe('{\n  "protocols": null,\n  "model_protocols": []\n}')
    editor.text.value = '{"protocols": ["openai.chat"], "model_protocols": [{"models": ["gpt-5.6"], "protocols": ["anthropic.messages"]}]}'
    expect(editor.syncText()).toBe(true)
    expect(editor.mode.value).toBe('custom')
    expect(editor.rules.value).toEqual({ 'gpt-5.6': ['anthropic.messages'] })
    editor.text.value = '{"model_protocols": [{"models": ["a"], "protocols": []}, {"models": ["a"], "protocols": []}]}'
    editor.setView('matrix')
    expect(editor.view.value).toBe('json')
    expect(editor.textProblem.value).toEqual({ problem: 'duplicate', model: 'a' })
    expect(editor.commitPending()).toBe(false)
    // The state keeps the last text that parsed.
    expect(editor.rules.value).toEqual({ 'gpt-5.6': ['anthropic.messages'] })
    editor.text.value = '{}'
    editor.setView('matrix')
    expect(editor.view.value).toBe('matrix')
    expect(editor.textProblem.value).toBeNull()
    expect(editor.binding()).toEqual({ protocols: null, model_protocols: [] })
    expect(editor.commitPending()).toBe(true)
  })

  it('writes a change of the own protocols into the open JSON view, so its text does not undo it', () => {
    const { editor } = editorOf(['gpt-5.6'])
    editor.setView('json')
    editor.setMode('custom')
    editor.toggleOwn(column(editor, 'openai.embeddings'))
    expect(editor.text.value).toContain('"protocols": ["openai.chat", "openai.responses", "openai.responses_ws"]')
    expect(editor.commitPending()).toBe(true)
    expect(editor.binding().protocols).toEqual(['openai.chat', 'openai.responses', 'openai.responses_ws'])
    // A text that parses is rewritten in the saved form: entries ticked alike on one line.
    editor.text.value = '{"model_protocols":[{"models":["a"],"protocols":["openai.chat"]},{"models":["b"],"protocols":["openai.chat"]}]}'
    expect(editor.syncText()).toBe(true)
    expect(editor.text.value).toContain('{"models": ["a", "b"], "protocols": ["openai.chat"]}')
  })

  it('keeps a JSON text that does not parse while the own protocols change, until it is corrected', () => {
    const { editor } = editorOf(['gpt-5.6'])
    editor.setView('json')
    const broken = '{"protocols": null, "model_protocols": [],}'
    editor.text.value = broken
    expect(editor.syncText()).toBe(false)
    editor.setMode('custom')
    editor.toggleOwn(column(editor, 'openai.embeddings'))
    expect(editor.text.value).toBe(broken)
    expect(editor.textProblem.value).toEqual({ problem: 'json' })
    expect(editor.commitPending()).toBe(false)
    // Corrected, the text is the state again and is rewritten in the saved form.
    editor.text.value = '{"protocols": ["openai.chat"]}'
    expect(editor.syncText()).toBe(true)
    expect(editor.binding().protocols).toEqual(['openai.chat'])
    expect(editor.text.value).toBe('{\n  "protocols": ["openai.chat"],\n  "model_protocols": []\n}')
  })

  it('follows the account type: other columns, and nothing checked for a type that is gone', () => {
    const { editor, type } = editorOf()
    editor.setSaved({ protocols: [] })
    expect(editor.issues.value.map(i => i.code)).toEqual(['no_protocol'])
    type.value = codex
    expect(editor.columns.value.own.map(c => c.key)).toEqual(['openai.responses', 'openai.chat'])
    type.value = null
    expect(editor.columns.value).toEqual({ own: [], cross: [] })
    expect(editor.issues.value).toEqual([])
  })
})
