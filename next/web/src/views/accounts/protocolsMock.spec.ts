// @vitest-environment node
// The protocol binding in the dev mock holds to what the server answers
// (DESIGN-2026-11-17-PROTOCOL-BINDING §4): the two account fields, the
// read-only served_protocols of an account and of a group, the endpoints of
// an account type, and the field errors.
import { beforeAll, describe, expect, it } from 'vitest'
import type { AccountServedProtocol, AccountType } from '@/api/types'
import { mockServer, type MockServer } from '@/testing/mockServer'
import { protocolColumns } from './protocols/protocolBinding'

let call: MockServer['call'], fields: MockServer['fields']
let admin = ''
beforeAll(async () => {
  let signIn: MockServer['signIn']
  ;({ call, signIn, fields } = await mockServer('core', 'accounts', 'resources'))
  admin = await signIn('admin@example.com')
})

const data = async (method: string, url: string, body?: unknown) => (await call(method, url, body, admin)).body.data
const served = async (id: number) => ((await data('GET', `/accounts/${id}`)).served_protocols as AccountServedProtocol[]).map(s => [s.protocol, s.via, s.models])

describe('protocol binding in the mock', () => {
  it('lists the endpoints of a type: native, adapted within its platform, converted from the others, count_tokens estimated', async () => {
    const types = (await data('GET', '/account-types')) as AccountType[]
    const of = (plugin: string, type: string) => protocolColumns(types.find(t => t.plugin_key === plugin && t.type === type))
    const openai = of('openai', 'apikey')
    expect(openai.own.map(c => [c.key, c.via])).toEqual([['openai.chat', 'native'], ['openai.embeddings', 'native'], ['openai.responses', 'native']])
    expect(openai.cross.map(c => [c.ids, c.grade])).toEqual([[['anthropic.messages'], 'fair'], [['gemini.generate', 'gemini.stream_generate'], 'fair']])
    // Codex serves Responses and adapts chat to it; it has no embeddings.
    expect(of('codex', 'codex_oauth').own.map(c => [c.key, c.via])).toEqual([['openai.responses', 'native'], ['openai.chat', 'adapted']])
    // Anthropic reaches Gemini in two hops.
    expect(of('anthropic', 'apikey').cross.find(c => c.key === 'gemini.generate')?.hops).toEqual(['gemini.generate', 'openai.chat', 'anthropic.messages'])
    const local = types.find(t => t.plugin_key === 'openai' && t.type === 'apikey')!.endpoints.filter(e => e.grade === 'local').map(e => e.protocol)
    expect(local.sort()).toEqual(['anthropic.count_tokens', 'gemini.count_tokens'])
  })

  it('answers both fields on the list and served_protocols on a single account only', async () => {
    const rows = (await data('GET', '/accounts?page_size=200')).data as Array<Record<string, unknown>>
    const row = rows.find(a => a.id === 19)!
    expect(row.protocols).toBeNull()
    expect(row.model_protocols).toEqual([{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages'] }, { models: ['text-embedding-3-large'], protocols: ['openai.embeddings'] }])
    expect(rows.every(a => !('served_protocols' in a))).toBe(true)
    expect(rows.find(a => a.id === 20)).toMatchObject({ protocols: null, model_protocols: [] })
    // openai-main: every own protocol for the models without a rule, Messages for the one converted model (and its count_tokens).
    expect(await served(19)).toEqual([
      ['openai.chat', 'native', null],
      ['openai.embeddings', 'native', null],
      ['openai.responses', 'native', null],
      ['anthropic.messages', 'converted', ['claude-sonnet-4-5']],
      // count_tokens follows Messages.
      ['anthropic.count_tokens', 'converted', ['claude-sonnet-4-5']]
    ])
    // demo-token opens chat only.
    expect(await served(18)).toEqual([['openai.chat', 'native', null]])
  })

  it('counts for a group the member accounts serving each protocol', async () => {
    const g = await data('GET', '/groups/2')
    const count = Object.fromEntries((g.served_protocols as Array<{ protocol: string; accounts: number }>).map(s => [s.protocol, s.accounts]))
    // vip: claude-main, claude-key-1, claude-key-3 natively and openai-main through its one converted model.
    expect(count['anthropic.messages']).toBe(4)
    expect(count['openai.chat']).toBe(1)
    expect(count['gemini.generate']).toBe(1)
    expect((await data('GET', '/groups?page_size=50')).data.every((x: object) => !('served_protocols' in x))).toBe(true)
  })

  it('replaces the fields on save and refuses what the type cannot serve', async () => {
    const patch = (body: unknown) => call('PATCH', '/accounts/20', body, admin)
    const ok = await patch({ protocols: ['gemini.generate', 'gemini.stream_generate'], model_protocols: [{ models: ['*'], protocols: ['gemini.generate', 'openai.chat'] }] })
    expect(ok.body.data).toMatchObject({ protocols: ['gemini.generate', 'gemini.stream_generate'], model_protocols: [{ models: ['*'], protocols: ['gemini.generate', 'openai.chat'] }] })
    // The entry for the other models decides for every model: generate and the chat conversion, not stream_generate.
    expect(await served(20)).toEqual([['gemini.count_tokens', 'native', null], ['gemini.generate', 'native', null], ['openai.chat', 'converted', null]])

    // The paths and codes of CONTRACTS §110.6.
    const errors = (a: Awaited<ReturnType<typeof patch>>) => a.body.error?.details?.fields?.map((f: { field: string; code: string }) => `${f.field}: ${f.code}`)
    expect(errors(await patch({ protocols: ['gemini.generate', 'openai.chat', 'gemini.count_tokens', 'zz.gone'] }))).toEqual(['protocols[1]: cross_platform', 'protocols[2]: invalid', 'protocols[3]: unsupported'])
    const bad = await patch({ model_protocols: [{ models: ['a', '*'], protocols: [] }, { models: ['a'], protocols: ['zz.gone', 'gemini.count_tokens'] }, { models: [], protocols: ['openai.chat'] }] })
    expect(errors(bad)).toEqual([
      'model_protocols[0].models: invalid',
      'model_protocols[0].protocols: required',
      'model_protocols[1].models[0]: duplicate',
      'model_protocols[1].protocols[0]: unsupported',
      'model_protocols[1].protocols[1]: invalid',
      'model_protocols[2].models: required'
    ])
    // Every own protocol closed: fine with a conversion, refused without (the rules the request leaves out count).
    expect(fields(await patch({ protocols: [], model_protocols: [] }))).toEqual(['protocols'])
    expect((await patch({ protocols: [] })).status).toBe(200)
    expect(fields(await patch({ model_protocols: [] }))).toEqual(['protocols'])
    expect((await patch({ protocols: null, model_protocols: [] })).body.data).toMatchObject({ protocols: null, model_protocols: [] })
  })

  it('lists for an account with a model list only the protocols one of its models is served on', async () => {
    // video-1 serves one model: bound to another protocol by name, no own protocol is left open "for the other models".
    const type = ((await data('GET', '/account-types')) as AccountType[]).find(t => t.plugin_key === '_videolabs__videogen' && t.type === 'video_key')!
    const [own] = protocolColumns(type).own
    const before = await served(17)
    expect(before.every(([, , models]) => models === null)).toBe(true)
    const ok = await call('PATCH', '/accounts/17', { model_protocols: [{ models: ['myvideo-pro'], protocols: [own.key] }] }, admin)
    expect(ok.status).toBe(200)
    expect(await served(17)).toEqual([[own.key, 'native', ['myvideo-pro']]])
    await call('PATCH', '/accounts/17', { model_protocols: [] }, admin)
  })

  it('answers 503 for the binding of an account whose plugin is not enabled', async () => {
    // legacy-vendor: its plugin was uninstalled. As the core, which does not know what the type serves (CONTRACTS §110.6).
    const refused = await call('PATCH', '/accounts/15', { protocols: null }, admin)
    expect([refused.status, refused.body.error?.code]).toEqual([503, 'plugin_unavailable'])
    expect((await call('PATCH', '/accounts/15', { priority: 9 }, admin)).status).toBe(200)
  })
})
