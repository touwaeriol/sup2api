// @vitest-environment node
// The writes of group pricing in the dev mock answer as the server does
// (CONTRACTS §97.6): every problem of every field and item in one 400 with
// the server's codes, a required rate, prices and rates on any model name
// (a global price is not needed), the removal of a user group from an account
// group, the 409 of a user group the caller is in, and the caller's rate
// per model.
import { beforeAll, describe, expect, it } from 'vitest'
import { mockServer, type Answer, type MockServer } from '@/testing/mockServer'

let call: MockServer['call'], signIn: MockServer['signIn']
beforeAll(async () => {
  ;({ call, signIn } = await mockServer('core', 'accounts', 'resources', 'billing', 'pricing', 'lookup'))
})

/** The field errors of a 400 as `field code`. */
const problems = (a: Answer) => (a.body.error?.details?.fields ?? []).map((f: { field: string; code: string }) => `${f.field} ${f.code}`)

describe('the checks of a group write in the mock', () => {
  it('names every wrong field of the group with every wrong item, by the server codes', async () => {
    const a = await call('POST', '/groups', { visibility: 'x', rate_multiplier: 1.2, model_rates: [{ model: 'gpt-4o' }], segment_rates: [{ segment_id: 999, rate: '0.5' }, { segment_id: 2 }] })
    expect(problems(a)).toEqual(['name required', 'visibility invalid', 'rate_multiplier invalid', 'model_rates.0.rate required', 'segment_rates.0.segment_id not_found', 'segment_rates.1.rate required'])
  })

  it('takes any model named, and a rate written as a string only', async () => {
    const a = await call('PATCH', '/groups/1', { model_rates: [{ model: 'no-such-model', rate: 'x' }, { model: 'gpt-4o', rate: 0.5 }, { model: ' ', rate: '1' }], model_prices: [{ rule: null }] })
    expect(problems(a)).toEqual(['model_prices.0.model required', 'model_rates.0.rate invalid', 'model_rates.1.rate invalid', 'model_rates.2.model required'])
  })

  it('prices a model without a global price by the group alone, and lists models with only a rate', async () => {
    const rule = { mode: 'expression', expression: 'tier("base", p*2)' }
    const a = await call('PATCH', '/groups/1', { model_prices: [{ model: ' claude-opus-x-test ', rule }], model_rates: [{ model: 'only-rate-x', rate: '0.5' }] })
    expect(a.status).toBe(200)
    const rows = (await call('GET', '/groups/1/model-prices')).body.data.models as Array<{ model: string; base: unknown; price: unknown; rate: string | null }>
    expect(rows.find((r) => r.model === 'claude-opus-x-test')).toMatchObject({ base: null, price: { mode: 'expression' }, rate: null })
    expect(rows.find((r) => r.model === 'only-rate-x')).toMatchObject({ base: null, price: null, rate: '0.5' })
    expect((await call('PATCH', '/groups/1', { model_prices: [{ model: 'claude-opus-x-test', rule: null }], model_rates: [{ model: 'only-rate-x', rate: null }] })).status).toBe(200)
  })

  it('takes a name another group has as a duplicate', async () => {
    expect(problems(await call('PATCH', '/groups/1', { name: ' vip ' }))).toEqual(['name duplicate'])
  })
})

describe('the model rates of a user group in the mock', () => {
  it('take any model named, and a rate', async () => {
    const a = await call('PATCH', '/segments/2', { model_rates: [{ group_id: 1, model: ' ', rate: '1' }, { group_id: 1, model: 'no-such-model', rate: '1' }, { group_id: 99, model: 'gpt-4o' }] })
    expect(problems(a)).toEqual(['model_rates.0.model required', 'model_rates.2.group_id not_found', 'model_rates.2.rate required'])
  })

  it('list a personal user group that has only model rates in the account group, which Remove takes away', async () => {
    const segments = async () => ((await call('GET', '/groups/1/segments')).body.data as Array<{ segment: { id: number }; model_rates: unknown[] }>).map((r) => r.segment.id)
    expect(await segments()).not.toContain(11)
    expect((await call('PATCH', '/segments/11', { model_rates: [{ group_id: 1, model: 'gpt-4o', rate: '0.5' }] })).status).toBe(200)
    expect(await segments()).toContain(11)
    expect((await call('PATCH', '/groups/1', { segment_rates: [{ segment_id: 11, remove: true }] })).status).toBe(200)
    expect(await segments()).not.toContain(11)
  })
})

describe('the model rates of user groups set from an account group in the mock', () => {
  it('take a user group that exists, any model named and a rate, and show on the user group', async () => {
    const a = await call('PATCH', '/groups/1', { segment_model_rates: [{ segment_id: 999, model: 'gpt-4o', rate: '1' }, { segment_id: 3, model: 'no-such-model', rate: '1' }, { segment_id: 3, model: 'gpt-4o' }] })
    expect(problems(a)).toEqual(['segment_model_rates.0.segment_id not_found', 'segment_model_rates.2.rate required'])
    expect((await call('PATCH', '/groups/1', { segment_model_rates: [{ segment_id: 3, model: ' gpt-4o ', rate: '0.6' }] })).status).toBe(200)
    const row = ((await call('GET', '/segments/3/groups')).body.data as Array<{ group: { id: number }; model_rates: unknown[] }>).find((r) => r.group.id === 1)
    expect(row?.model_rates).toEqual([{ model: 'gpt-4o', rate: '0.6' }])
    expect((await call('PATCH', '/groups/1', { segment_model_rates: [{ segment_id: 3, model: 'gpt-4o', rate: null }] })).status).toBe(200)
  })
})

describe('the user groups a caller is in, in the mock', () => {
  it('are not his to change the terms of, unless he may act on himself', async () => {
    await call('POST', '/segments/2/members', { user_ids: [9] })
    const pricer = await signIn('pricer@example.com')
    const a = await call('PATCH', '/groups/2', { segment_rates: [{ segment_id: 2, rate: '0.1' }] }, pricer)
    expect([a.status, a.body.error.code]).toEqual([409, 'conflict'])
    const b = await call('PATCH', '/groups/2', { segment_model_rates: [{ segment_id: 2, model: 'gpt-4o', rate: '0.1' }] }, pricer)
    expect([b.status, b.body.error.code]).toEqual([409, 'conflict'])
    expect((await call('PATCH', '/groups/2', { segment_rates: [{ segment_id: 3, rate: null }] }, pricer)).status).toBe(200)
    await call('POST', '/segments/1/members', { user_ids: [9] })
  })
})

describe('the caller’s prices in one group in the mock', () => {
  it('carry the rate of each model, of the four levels, and where it comes from', async () => {
    const answer = (await call('GET', '/me/prices?group_id=1')).body.data
    const row = (model: string) => answer.prices.find((p: { model: string }) => p.model === model)
    expect(answer.rate_multiplier).toBe('1')
    expect(row('gpt-4o')).toMatchObject({ rate: '0.85', rate_source: 'segment_model' })
    expect(row('gpt-4o-mini')).toMatchObject({ rate: '1', rate_source: 'group' })
    expect((await call('GET', '/me/prices')).body.data.prices[0].rate).toBeUndefined()
  })
})
