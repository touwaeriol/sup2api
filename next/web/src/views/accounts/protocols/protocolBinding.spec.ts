import { describe, expect, it } from 'vitest'
import type { AccountType } from '@/api/types'
import { claude, codex, ep, openai, type } from './fixtures'
import {
  bindingIssues, bindingJSON, bindingSummary, columnState, conversionInUse, defaultProtocols, matrixModels, modelProtocolRules, openColumns, parseBinding,
  cardRules, platformMarks, protocolColumns, protocolsOf, REST, ruleCards, rulesByModel, sameModelProtocols, sameProtocolColumns, sameProtocols, toggleCell, toggleColumn,
  sharedOwnProtocols, unknownOwnProtocols, withCardModels, withModels, withNewCard, withoutModels, type ModelRules
} from './protocolBinding'

const columns = protocolColumns(openai)
const column = (key: string, c = columns) => [...c.own, ...c.cross].find(x => x.key === key)!

describe('protocol columns of an account type', () => {
  it('splits the protocols into the own ones and the conversions, without count_tokens and local estimates', () => {
    expect(columns.own.map(c => [c.key, c.via])).toEqual([['openai.chat', 'native'], ['openai.responses', 'native'], ['openai.responses_ws', 'native'], ['openai.embeddings', 'native']])
    expect(columns.cross.map(c => [c.key, c.via, c.grade])).toEqual([['anthropic.messages', 'converted', 'fair'], ['gemini.generate', 'converted', 'fair']])
    expect(protocolColumns(claude).own.map(c => c.key)).toEqual(['anthropic.messages'])
  })

  it('shows the two Gemini generation protocols as one column that keeps both ids', () => {
    expect(column('gemini.generate').ids).toEqual(['gemini.generate', 'gemini.stream_generate'])
    const gemini = type(['gemini'], [ep('gemini.generate', true), ep('gemini.stream_generate', true), ep('gemini.count_tokens', true)])
    expect(protocolColumns(gemini).own.map(c => c.ids)).toEqual([['gemini.generate', 'gemini.stream_generate']])
    // Only one of them served: a column of one id.
    expect(protocolColumns(type(['gemini'], [ep('gemini.stream_generate', true)])).own.map(c => c.ids)).toEqual([['gemini.stream_generate']])
  })

  it('marks a protocol of a declared platform served through another endpoint as adapted, not converted', () => {
    const c = protocolColumns(codex)
    expect(c.own.map(x => [x.key, x.via])).toEqual([['openai.responses', 'native'], ['openai.chat', 'adapted']])
    expect(c.cross.map(x => x.key)).toEqual(['anthropic.messages'])
  })

  it('lists a protocol once, and nothing for a type that is gone', () => {
    expect(protocolColumns(type(['openai'], [ep('openai.chat', true), { ...ep('openai.chat', true), path: '/alias' }])).own).toHaveLength(1)
    expect(protocolColumns(null)).toEqual({ own: [], cross: [] })
    expect(protocolColumns({ platforms: [] } as unknown as AccountType)).toEqual({ own: [], cross: [] })
  })

  it('tells whether two types offer the same protocols', () => {
    expect(sameProtocolColumns(openai, { ...openai })).toBe(true)
    expect(sameProtocolColumns(openai, codex)).toBe(false)
    expect(sameProtocolColumns(null, undefined)).toBe(true)
  })
})

describe('own protocols of an account', () => {
  it('opens every own protocol with null and only the listed ones otherwise', () => {
    expect(openColumns(columns, null)).toHaveLength(4)
    expect(openColumns(columns, ['openai.chat']).map(c => c.key)).toEqual(['openai.chat'])
    expect(openColumns(columns, [])).toEqual([])
    expect(defaultProtocols(columns, null)).toEqual(['openai.chat', 'openai.responses', 'openai.responses_ws', 'openai.embeddings'])
    // A conversion is never a default, even when the saved list names it.
    expect(defaultProtocols(columns, ['openai.responses', 'anthropic.messages'])).toEqual(['openai.responses'])
  })

  it('ticks the ids of a column together, a half-ticked one first becoming whole', () => {
    const gemini = column('gemini.generate')
    expect(columnState([], gemini)).toBe('off')
    expect(columnState(['gemini.generate'], gemini)).toBe('partial')
    expect(toggleColumn(['openai.chat', 'gemini.generate'], gemini)).toEqual(['openai.chat', 'gemini.generate', 'gemini.stream_generate'])
    expect(columnState(['gemini.stream_generate', 'gemini.generate'], gemini)).toBe('on')
    expect(toggleColumn(['openai.chat', 'gemini.generate', 'gemini.stream_generate'], gemini)).toEqual(['openai.chat'])
  })
})

describe('rules: the protocols of chosen models', () => {
  const saved = [
    { models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] },
    { models: ['gpt-5.6', 'gpt-5.6-mini'], protocols: ['openai.chat', 'openai.responses', 'anthropic.messages'] },
    { models: ['*'], protocols: ['openai.chat'] }
  ]

  it('expands the saved entries to one rule per model and merges them back', () => {
    const rules = rulesByModel(saved)
    expect(rules).toEqual({
      'claude-sonnet-4-5': ['anthropic.messages'],
      'gpt-5.6': ['openai.chat', 'openai.responses', 'anthropic.messages'],
      'gpt-5.6-mini': ['openai.chat', 'openai.responses', 'anthropic.messages'],
      '*': ['openai.chat']
    })
    expect(modelProtocolRules(rules, columns)).toEqual(saved)
    expect(rulesByModel(null)).toEqual({})
    // A model listed twice keeps its first entry.
    expect(rulesByModel([{ models: ['a'], protocols: ['openai.chat'] }, { models: ['a', 'b'], protocols: ['openai.responses'] }])).toEqual({ a: ['openai.chat'], b: ['openai.responses'] })
  })

  it('saves the models ticked alike as one entry, protocols in column order, the other models last and on their own', () => {
    const rules: ModelRules = {
      '*': ['openai.responses', 'openai.chat'],
      a: ['anthropic.messages', 'openai.chat'],
      b: ['openai.embeddings'],
      c: ['openai.chat', 'anthropic.messages'],
      // The same protocols as the other models: still not merged with them.
      d: ['openai.chat', 'openai.responses']
    }
    expect(modelProtocolRules(rules, columns)).toEqual([
      { models: ['a', 'c'], protocols: ['openai.chat', 'anthropic.messages'] },
      { models: ['b'], protocols: ['openai.embeddings'] },
      { models: ['d'], protocols: ['openai.chat', 'openai.responses'] },
      { models: ['*'], protocols: ['openai.chat', 'openai.responses'] }
    ])
    expect(modelProtocolRules({}, columns)).toEqual([])
  })

  it('writes both ids of the Gemini column and keeps an id no column has, last', () => {
    const rules = toggleCell({ a: ['zz.gone', 'openai.chat'] }, 'a', column('gemini.generate'), [])
    expect(modelProtocolRules(rules, columns)).toEqual([{ models: ['a'], protocols: ['openai.chat', 'gemini.generate', 'gemini.stream_generate', 'zz.gone'] }])
  })

  it('makes a default row explicit from the default protocols when a cell is ticked, and default again when its rule is dropped', () => {
    const defaults = defaultProtocols(columns, ['openai.chat', 'openai.responses'])
    // A conversion ticked on a default row: the open own protocols stay.
    let rules = toggleCell({}, 'gpt-5.6', column('anthropic.messages'), defaults)
    expect(rules).toEqual({ 'gpt-5.6': ['openai.chat', 'openai.responses', 'anthropic.messages'] })
    // An own protocol unticked on a default row.
    rules = toggleCell(rules, REST, column('openai.responses'), defaults)
    expect(rules[REST]).toEqual(['openai.chat'])
    // An explicit row goes by its own protocols, not the defaults.
    rules = toggleCell(rules, 'gpt-5.6', column('openai.chat'), defaults)
    expect(rules['gpt-5.6']).toEqual(['openai.responses', 'anthropic.messages'])
    expect(withoutModels(rules, ['gpt-5.6'])).toEqual({ '*': ['openai.chat'] })
    expect(withoutModels(rules, Object.keys(rules))).toEqual({})
  })

  it('gives the selected models one protocol set', () => {
    expect(withModels({ a: ['openai.chat'] }, ['a', 'b'], ['anthropic.messages'])).toEqual({ a: ['anthropic.messages'], b: ['anthropic.messages'] })
  })

  it('rows the matrix by the account models, then the models that only have a rule', () => {
    expect(matrixModels(['gpt-5.6', 'gpt-5.6-mini'], { extra: ['openai.chat'], 'gpt-5.6': [], '*': [] })).toEqual(['gpt-5.6', 'gpt-5.6-mini', 'extra'])
    expect(matrixModels([], {})).toEqual([])
  })

  it('serves a model without a rule on the protocols of the other models once they have a rule, else on the default', () => {
    const defaults = defaultProtocols(columns, null)
    expect(protocolsOf({}, 'a', defaults)).toEqual(defaults)
    expect(protocolsOf({ a: ['openai.chat'], '*': ['anthropic.messages'] }, 'a', defaults)).toEqual(['openai.chat'])
    expect(protocolsOf({ '*': ['openai.chat', 'anthropic.messages'] }, 'a', defaults)).toEqual(['openai.chat', 'anthropic.messages'])
    // A cell ticked on such a row starts from what the row is served on.
    expect(toggleCell({ '*': ['openai.chat', 'anthropic.messages'] }, 'a', column('openai.chat'), defaults).a).toEqual(['anthropic.messages'])
  })
})

describe('cards of the list view', () => {
  const rules: ModelRules = { a: ['openai.chat'], b: ['openai.chat'], c: ['anthropic.messages'], '*': ['openai.responses'] }

  it('makes a card per entry and the rules back from the cards', () => {
    const cards = ruleCards(rules, columns)
    expect(cards.map(c => [c.id, c.models])).toEqual([[1, ['a', 'b']], [2, ['c']], [3, ['*']]])
    expect(cardRules(cards)).toEqual(rules)
    expect(ruleCards({}, columns, 7)).toEqual([])
    expect(ruleCards({ a: [] }, columns, 7)[0].id).toBe(7)
  })

  it('moves a model into the card it is added to, and drops a card left without a model', () => {
    const cards = ruleCards(rules, columns)
    // c joins the first card and leaves its own, which goes; b leaves; "*" cannot be added to a list.
    const next = withCardModels(cards, 1, ['a', 'c', '*'])
    expect(next.map(c => [c.id, c.models, c.protocols])).toEqual([[1, ['a', 'c'], ['openai.chat']], [3, ['*'], ['openai.responses']]])
    expect(withCardModels(cards, 2, []).map(c => c.id)).toEqual([1, 3])
  })

  it('keeps two cards apart when their protocols become the same: only the saved entries join', () => {
    const cards = ruleCards(rules, columns).map(c => (c.id === 2 ? { ...c, protocols: ['openai.chat'] } : c))
    expect(cards.map(c => c.id)).toEqual([1, 2, 3])
    expect(modelProtocolRules(cardRules(cards), columns)).toEqual([{ models: ['a', 'b', 'c'], protocols: ['openai.chat'] }, { models: ['*'], protocols: ['openai.responses'] }])
  })

  it('adds a card with a new id before the card of the other models, taking its models out of the others', () => {
    const next = withNewCard(ruleCards(rules, columns), ['b', 'd'], ['openai.embeddings'])
    expect(next.map(c => [c.id, c.models])).toEqual([[1, ['a']], [2, ['c']], [4, ['b', 'd']], [3, ['*']]])
    expect(withNewCard([], ['*'], ['openai.chat'])).toEqual([{ id: 1, models: ['*'], protocols: ['openai.chat'] }])
  })
})

describe('checks of the binding', () => {
  const codes = (protocols: string[] | null, rules: ModelRules, c = columns) => bindingIssues(protocols, rules, c).map(i => [i.level, i.code, i.model, i.column?.key ?? i.protocol].filter(x => x !== undefined))

  it('finds nothing wrong with the defaults', () => {
    expect(codes(null, {})).toEqual([])
    expect(codes(['openai.chat'], { a: ['openai.chat'] })).toEqual([])
  })

  it('refuses a rule without a protocol and an account that opens nothing', () => {
    expect(codes(null, { a: [], '*': [] })).toEqual([['error', 'empty_rule', 'a'], ['error', 'empty_rule', '*']])
    expect(codes([], {})).toEqual([['error', 'no_protocol']])
    // A rule naming a closed own protocol opens nothing either.
    expect(codes([], { a: ['openai.chat'] })).toEqual([['error', 'no_protocol'], ['warning', 'closed_protocol', 'a', 'openai.chat']])
    // Every own protocol closed is fine once a model is converted.
    expect(codes([], { a: ['anthropic.messages'] })).toEqual([])
  })

  it('warns about an own protocol the account does not open, a protocol the type cannot serve and a conversion for every other model', () => {
    expect(codes(['openai.chat'], { a: ['openai.chat', 'openai.embeddings'] })).toEqual([['warning', 'closed_protocol', 'a', 'openai.embeddings']])
    expect(codes(null, { a: ['openai.chat', 'volcengine.images'] })).toEqual([['warning', 'unknown_protocol', 'a', 'volcengine.images']])
    expect(codes(null, { '*': ['openai.chat', 'anthropic.messages'], a: ['anthropic.messages'] })).toEqual([['warning', 'rest_conversion', '*']])
  })

  it('finds the own protocols every account opens', () => {
    expect(sharedOwnProtocols([null, undefined])).toBeNull()
    expect(sharedOwnProtocols([null, ['openai.chat', 'openai.responses'], ['openai.responses', 'openai.embeddings']])).toEqual(['openai.responses'])
    expect(sharedOwnProtocols([['openai.chat'], []])).toEqual([])
  })

  it('finds the ids under protocols that are no own protocol of the type, which the own protocols field shows, not the issues', () => {
    expect(unknownOwnProtocols(null, columns)).toEqual([])
    expect(unknownOwnProtocols(['openai.chat', 'openai.legacy', 'anthropic.messages'], columns)).toEqual(['openai.legacy', 'anthropic.messages'])
    expect(codes(['openai.chat', 'openai.legacy'], {})).toEqual([])
  })

  it('notes a conversion through two converters, errors first', () => {
    const c = protocolColumns(claude)
    expect(codes(null, { a: ['gemini.generate', 'gemini.stream_generate'], b: [] }, c)).toEqual([['error', 'empty_rule', 'b'], ['info', 'two_hop', 'a', 'gemini.generate']])
    expect(codes(null, { a: ['openai.chat'] }, c)).toEqual([])
  })
})

describe('summaries of the binding', () => {
  it('counts the own protocols open, the models with a rule and those with a conversion', () => {
    expect(bindingSummary(null, {}, columns)).toEqual({ open: 4, total: 4, limited: 0, converting: 0 })
    const rules = { 'claude-sonnet-4-5': ['anthropic.messages'], 'text-embedding-3-large': ['openai.embeddings'] }
    expect(bindingSummary(['openai.chat', 'openai.embeddings'], rules, columns)).toEqual({ open: 2, total: 4, limited: 2, converting: 1 })
  })

  it('says whether a request can be converted or adapted at all', () => {
    expect(conversionInUse(null, {}, columns)).toBe(false)
    expect(conversionInUse(null, { a: ['openai.chat'] }, columns)).toBe(false)
    expect(conversionInUse(null, { a: ['anthropic.messages'] }, columns)).toBe(true)
    // Codex adapts chat to Responses while chat is open.
    const c = protocolColumns(codex)
    expect(conversionInUse(null, {}, c)).toBe(true)
    expect(conversionInUse(['openai.responses'], {}, c)).toBe(false)
  })
})

describe('the JSON view', () => {
  it('prints both fields, one entry a line, and reads them back', () => {
    const binding = { protocols: ['openai.chat'], model_protocols: [{ models: ['a', 'b'], protocols: ['openai.chat', 'anthropic.messages'] }, { models: ['*'], protocols: ['openai.chat'] }] }
    const text = bindingJSON(binding)
    expect(text).toBe(
      '{\n  "protocols": ["openai.chat"],\n  "model_protocols": [\n    {"models": ["a", "b"], "protocols": ["openai.chat", "anthropic.messages"]},\n    {"models": ["*"], "protocols": ["openai.chat"]}\n  ]\n}'
    )
    expect(parseBinding(text)).toEqual(binding)
    expect(bindingJSON({ protocols: null, model_protocols: [] })).toBe('{\n  "protocols": null,\n  "model_protocols": []\n}')
    // An absent field is its default; an empty box is both defaults.
    expect(parseBinding('{"model_protocols": []}')).toEqual({ protocols: null, model_protocols: [] })
    expect(parseBinding('  ')).toEqual({ protocols: null, model_protocols: [] })
  })

  it('says what is wrong with a text it cannot take', () => {
    expect(parseBinding('{')).toEqual({ problem: 'json' })
    expect(parseBinding('[]')).toEqual({ problem: 'shape' })
    expect(parseBinding('{"protocols": "openai.chat"}')).toEqual({ problem: 'shape' })
    expect(parseBinding('{"model_protocols": [{"models": [], "protocols": ["openai.chat"]}]}')).toEqual({ problem: 'shape' })
    expect(parseBinding('{"model_protocols": [{"models": ["*", "a"], "protocols": ["openai.chat"]}]}')).toEqual({ problem: 'star' })
    expect(parseBinding('{"model_protocols": [{"models": ["a"], "protocols": ["openai.chat"]}, {"models": ["b", "a"], "protocols": []}]}')).toEqual({ problem: 'duplicate', model: 'a' })
  })
})

describe('comparing saved values (bulk edit)', () => {
  it('compares the own protocols as a set, null apart from an empty list', () => {
    expect(sameProtocols(null, undefined)).toBe(true)
    expect(sameProtocols(null, [])).toBe(false)
    expect(sameProtocols(['a', 'b'], ['b', 'a'])).toBe(true)
    expect(sameProtocols(['a'], ['a', 'b'])).toBe(false)
  })

  it('compares the rules by what each model gets, however the entries are split', () => {
    const one = [{ models: ['a', 'b'], protocols: ['x', 'y'] }]
    expect(sameModelProtocols(one, [{ models: ['b'], protocols: ['y', 'x'] }, { models: ['a'], protocols: ['x', 'y'] }])).toBe(true)
    expect(sameModelProtocols(one, [{ models: ['a'], protocols: ['x', 'y'] }])).toBe(false)
    expect(sameModelProtocols(one, [{ models: ['a', 'b'], protocols: ['x'] }])).toBe(false)
    expect(sameModelProtocols(undefined, [])).toBe(true)
  })
})

describe('the platforms an account serves (account list)', () => {
  const marks = (a: Parameters<typeof platformMarks>[0], at = openai) => platformMarks(a, at).map(m => [m.platform, m.converted, m.columns.map(c => c.key), m.total, m.models])

  it('shows the platforms of the type with every own protocol by default, and no conversion', () => {
    expect(marks({})).toEqual([['openai', false, ['openai.chat', 'openai.responses', 'openai.responses_ws', 'openai.embeddings'], 4, []]])
  })

  it('counts the own protocols a bound account opens', () => {
    expect(marks({ protocols: ['openai.chat'] })).toEqual([['openai', false, ['openai.chat'], 4, []]])
    expect(marks({ protocols: [], model_protocols: [{ models: ['a'], protocols: ['anthropic.messages'] }] })).toEqual([
      ['openai', false, [], 4, []],
      ['anthropic', true, ['anthropic.messages'], 0, ['a']]
    ])
  })

  it('adds the platforms some model is converted for, with those models', () => {
    const a = { model_protocols: [{ models: ['claude-sonnet-4-5', 'gpt-5.6'], protocols: ['openai.chat', 'anthropic.messages'] }, { models: ['*'], protocols: ['openai.chat', 'gemini.generate', 'gemini.stream_generate'] }, { models: ['m'], protocols: ['openai.chat'] }] }
    expect(marks(a).slice(1)).toEqual([
      ['anthropic', true, ['anthropic.messages'], 0, ['claude-sonnet-4-5', 'gpt-5.6']],
      ['gemini', true, ['gemini.generate'], 0, ['*']]
    ])
  })

  it('leaves out a platform that is not available now and marks nothing for a type that is gone', () => {
    const at = { ...openai, platforms: [...openai.platforms, { id: 'foo', label: { en: 'Foo' }, builtin: false, available: false }] } as AccountType
    expect(marks({}, at).map(m => m[0])).toEqual(['openai'])
    expect(platformMarks({}, undefined)).toEqual([])
  })
})
