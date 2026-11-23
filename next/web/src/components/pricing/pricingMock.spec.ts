// @vitest-environment node
// The group pricing endpoints of the dev mock hold to what the server answers
// (CONTRACTS §97.6): the shape of a list price, who may read what, and the
// errors.
import { beforeAll, describe, expect, it } from 'vitest'
import { mockServer, type MockServer } from '@/testing/mockServer'
import { requestTier } from '@/views/prices/priceExpr'

let call: MockServer['call'], signIn: MockServer['signIn'], fields: MockServer['fields']
beforeAll(async () => {
  ;({ call, signIn, fields } = await mockServer('core', 'accounts', 'resources', 'billing', 'pricing', 'lookup'))
})

describe('the model prices and model rates of a group in the mock', () => {
  const row = async (gid: number, model: string) => (await call('GET', `/groups/${gid}/model-prices`)).body.data.models.find((m: { model: string }) => m.model === model)

  it('lists a model price as a price of its own and a model rate beside it', async () => {
    expect(await row(1, 'gpt-4o')).toMatchObject({ price: null, rate: '1.2' })
    const search = await row(1, 'web-search')
    expect(search.price).toMatchObject({ mode: 'visual', config: requestTier(0.008) })
    expect(search.rate).toBeNull()
  })

  it('quotes the most specific rate alone: the user group’s for the model over every other', async () => {
    const quote = (await call('POST', '/pricing/quote', { segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: 1_000_000, c: 0 } })).body.data
    expect(quote.rate).toMatchObject({ value: '0.85', source: 'segment_model' })
    expect(quote.rate_levels).toEqual({ segment_model: '0.85', segment: null, group_model: '1.2', group: '1' })
    expect(quote.price_levels).toEqual({ group: null })
    expect(Number(quote.cost)).toBeCloseTo(Number(quote.list_cost) * 0.85, 8)
    const plain = (await call('POST', '/pricing/quote', { segment_id: 1, group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 0 } })).body.data
    expect(plain.rate).toMatchObject({ value: '1.2', source: 'group_model' })
  })
})

describe('the quote in the mock', () => {
  it('takes exactly one of a user and a user group, as a 400 on user_id', async () => {
    const both = await call('POST', '/pricing/quote', { user_id: 1, segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 1 } })
    const neither = await call('POST', '/pricing/quote', { group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 1 } })
    for (const a of [both, neither]) {
      expect(a.status).toBe(400)
      expect(a.body.error.code).toBe('invalid_argument')
      expect(fields(a)).toEqual(['user_id'])
    }
  })

  it('takes 0 to 10^12 tokens of each kind, naming each count out of bounds as usage.<variable>', async () => {
    const a = await call('POST', '/pricing/quote', { segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: -1_000_000, c: 0, cr: 1e12 + 1 } })
    expect(a.status).toBe(400)
    expect(a.body.error.code).toBe('invalid_argument')
    expect(fields(a)).toEqual(['usage.p', 'usage.cr'])
    expect((await call('POST', '/pricing/quote', { segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: 1e12, c: 0 } })).status).toBe(200)
    const unbound = await call('POST', '/pricing/quote', { segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: '1', c: 0 } })
    expect([unbound.status, fields(unbound)]).toEqual([400, undefined])
  })

  it('needs group:read, and user:read or segment:read for whom it names, besides price:read', async () => {
    const pricer = await signIn('pricer@example.com')
    const user = await call('POST', '/pricing/quote', { user_id: 104, group_id: 2, model: 'claude-sonnet-4-5', usage: { p: 1, c: 1 } }, pricer)
    expect([user.status, user.body.error.details]).toEqual([403, { permission: 'user:read' }])
    // Both named: each is checked before the request is found wrong.
    const both = await call('POST', '/pricing/quote', { user_id: 104, segment_id: 11, group_id: 2, model: 'claude-sonnet-4-5', usage: { p: 1, c: 1 } }, pricer)
    expect([both.status, both.body.error.details]).toEqual([403, { permission: 'user:read' }])
    const readonly = await signIn('readonly@example.com')
    const segment = await call('POST', '/pricing/quote', { segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 1 } }, readonly)
    expect([segment.status, segment.body.error.details]).toEqual([403, { permission: 'price:read' }])
  })

  it('says which of a user and a user group it cannot find', async () => {
    const user = await call('POST', '/pricing/quote', { user_id: 99999, group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 1 } })
    const segment = await call('POST', '/pricing/quote', { segment_id: 99999, group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 1 } })
    expect([user.status, user.body.error.message, segment.status, segment.body.error.message]).toEqual([404, 'user not found', 404, 'user group not found'])
  })
})

describe('what a caller without user:read sees in the mock', () => {
  let pricer = ''
  beforeAll(async () => {
    pricer = await signIn('pricer@example.com')
  })

  it('names a personal user group by its user id, without the user, and does not search users', async () => {
    const list = (await call('GET', '/segments?kind=personal', undefined, pricer)).body.data.data
    expect(list.map((s: { name: string }) => s.name)).toEqual(['#104', '#107'])
    expect(list.every((s: object) => !('user' in s))).toBe(true)
    expect((await call('GET', '/segments?kind=personal&q=user5', undefined, pricer)).body.data.data).toEqual([])

    const admin = (await call('GET', '/segments?kind=personal&q=user5')).body.data.data
    expect(admin.map((s: { name: string; user: { id: number } }) => [s.name, s.user.id])).toEqual([['user5@example.org', 104]])
  })

  it('masks it in the rows of a group and in a quote too', async () => {
    const rows = (await call('GET', '/groups/2/segments', undefined, pricer)).body.data
    expect(rows.filter((r: { segment: { kind: string } }) => r.segment.kind === 'personal').map((r: { segment: { name: string } }) => r.segment.name)).toEqual(['#104', '#107'])
    // A quote for the user himself takes user:read (CONTRACTS §97.6); one for his personal user group does not.
    const quote = (await call('POST', '/pricing/quote', { segment_id: 11, group_id: 2, model: 'claude-sonnet-4-5', usage: { p: 1, c: 1 } }, pricer)).body.data
    expect(quote.segment).toEqual({ id: 11, kind: 'personal', name: '#104', user_id: 104 })
  })

  it('may not list the members of a user group, nor put a new user in one', async () => {
    const members = await call('GET', '/segments/2/members', undefined, pricer)
    expect(members.status).toBe(403)
    expect(members.body.error.details).toEqual({ permission: 'user:read' })
    const created = await call('POST', '/users', { email: 'x@example.com', password: 'longenough', segment_id: 2 }, pricer)
    expect(created.status).toBe(403)
    expect(created.body.error.details).toEqual({ permission: 'user:create' })
  })
})

describe('the rest of the group pricing mock', () => {
  it('lists who may use a private group only', async () => {
    const rows = (await call('GET', '/groups?page_size=200')).body.data.data
    const access = (id: number) => rows.find((g: { id: number }) => g.id === id).access
    expect(access(1)).toEqual({ segments: [], personal_count: 0 })
    expect(access(2).segments.map((s: { name: string }) => s.name)).toEqual(['VIP', '企业客户'])
    expect(access(2).personal_count).toBe(2)
  })

  it('counts and lists every model a group sets a price or a rate on, provided or not', async () => {
    const group = async () => (await call('GET', '/groups?page_size=200')).body.data.data.find((g: { id: number }) => g.id === 1)
    const listed = async () => (await call('GET', '/groups/1/model-prices')).body.data.models as Array<{ model: string; offered: boolean; price: unknown; rate: string | null }>
    expect((await group()).price_rule_count).toBe(3)

    // The group stops providing gpt-4o and gpt-4o-mini: the rate on gpt-4o stays, counted and listed as not provided; gpt-4o-mini goes.
    await call('PATCH', '/groups/1', { model_filter_mode: 'blacklist', model_allowlist: ['gpt-4o', 'gpt-4o-mini'] })
    expect((await group()).price_rule_count).toBe(3)
    const after = await listed()
    expect(after.find((m) => m.model === 'gpt-4o')).toMatchObject({ offered: false, rate: '1.2' })
    expect(after.some((m) => m.model === 'gpt-4o-mini')).toBe(false)
    expect(after.filter((m) => m.price || m.rate).length).toBe(3)

    await call('PATCH', '/groups/1', { model_allowlist: [] })
    expect((await listed()).find((m) => m.model === 'gpt-4o')?.offered).toBe(true)
  })

  it('saves a group with its prices and rates in one request, or nothing of it when one item is wrong', async () => {
    const catalog = (await call('GET', '/pricing/catalog')).body.data as Array<{ model: string }>
    const provided = ((await call('GET', '/groups/2/model-prices')).body.data.models as Array<{ model: string }>).map((m) => m.model)
    const model = catalog.map((c) => c.model).find((m) => !provided.includes(m))!
    const price = { mode: 'expression', expression: 'p * 2' }
    const wrong = await call('PATCH', '/groups/2', { name: 'vip2', model_prices: [{ model, rule: price }, { model: ' ', rule: price }], model_rates: [{ model, rate: '0.' }], segment_rates: [{ segment_id: 2, rate: '0.5' }] })
    expect([wrong.status, fields(wrong)]).toEqual([400, ['model_prices.1.model', 'model_rates.0.rate']])
    expect((await call('GET', '/groups/2')).body.data.name).toBe('vip')

    const saved = await call('PATCH', '/groups/2', { model_prices: [{ model, rule: price }], model_rates: [{ model, rate: '0.9' }], segment_rates: [{ segment_id: 2, granted: false, rate: null }] })
    expect(saved.status).toBe(200)
    const row = (await call('GET', '/groups/2/model-prices')).body.data.models.find((m: { model: string }) => m.model === model)
    expect(row).toMatchObject({ offered: false, price: { mode: 'expression', expression: 'p * 2' }, rate: '0.9' })
    const vip = (await call('GET', '/groups/2/segments')).body.data.find((r: { segment: { id: number } }) => r.segment.id === 2)
    expect(vip).toMatchObject({ granted: false, rate_override: null })
    await call('PATCH', '/groups/2', { model_prices: [{ model, rule: null }], model_rates: [{ model, rate: null }], segment_rates: [{ segment_id: 2, granted: true, rate: '0.7' }] })
  })

  it('creates a user group with its rates and model rates in one request, checked as one', async () => {
    const wrong = await call('POST', '/segments', { name: 'Gold', group_rates: [{ group_id: 99, rate: '1' }], model_rates: [{ group_id: 1, model: 'gpt-4o', rate: 'x' }] })
    expect([wrong.status, fields(wrong)]).toEqual([400, ['group_rates.0.group_id', 'model_rates.0.rate']])
    const created = await call('POST', '/segments', { name: 'Gold', group_rates: [{ group_id: 2, granted: true, rate: '0.6' }], model_rates: [{ group_id: 1, model: 'gpt-4o', rate: '0.5' }] })
    expect(created.status).toBe(200)
    const rows = (await call('GET', `/segments/${created.body.data.id}/groups`)).body.data as Array<{ group: { id: number }; granted: boolean; rate_override: string | null; model_rates: unknown[] }>
    expect(rows.find((r) => r.group.id === 2)).toMatchObject({ granted: true, rate_override: '0.6' })
    expect(rows.find((r) => r.group.id === 1)?.model_rates).toEqual([{ model: 'gpt-4o', rate: '0.5' }])
    await call('DELETE', `/segments/${created.body.data.id}`)
  })

  it('says a quote in a disabled group cannot be used by anyone', async () => {
    await call('PATCH', '/groups/1', { status: 'disabled' })
    const quote = (await call('POST', '/pricing/quote', { segment_id: 2, group_id: 1, model: 'gpt-4o', usage: { p: 1, c: 1 } })).body.data
    expect([quote.accessible, quote.access_source]).toEqual([false, 'disabled'])
    await call('PATCH', '/groups/1', { status: 'active' })
  })

  it('gives the caller’s groups the fields of the server, no more', async () => {
    const [first] = (await call('GET', '/me/groups')).body.data
    expect(Object.keys(first).sort()).toEqual(['description', 'effective_rate', 'exclusive', 'id', 'model_allowlist', 'model_filter_mode', 'name', 'platforms', 'rate_multiplier', 'rate_source', 'served_protocols'])
    // The protocols as the group's detail lists them, without the account counts.
    expect(first.served_protocols.length).toBeGreaterThan(0)
    for (const s of first.served_protocols) expect(Object.keys(s)).toEqual(['protocol'])
  })

  it('keeps the names of user groups unique without regard to case, and at most 100 characters', async () => {
    const taken = await call('POST', '/segments', { name: 'vip' })
    expect([taken.status, fields(taken)]).toEqual([400, ['name']])
    const long = await call('POST', '/segments', { name: 'x'.repeat(101) })
    expect([long.status, fields(long)]).toEqual([400, ['name']])
  })
})

describe('the groups a caller may use in the mock', () => {
  let pricer = ''
  beforeAll(async () => {
    pricer = await signIn('pricer@example.com')
  })
  const ids = (rows: Array<{ id: number }>) => rows.map((g) => g.id).sort((a, b) => a - b)

  it('are the caller’s in the lookup too, as in GET /me/groups', async () => {
    for (const token of [undefined, pricer]) {
      const mine = ids((await call('GET', '/me/groups', undefined, token)).body.data)
      expect(ids((await call('GET', '/lookup/groups?mine=true&limit=50', undefined, token)).body.data.items)).toEqual(mine)
    }
    const admin = ids((await call('GET', '/me/groups')).body.data)
    expect(ids((await call('GET', '/me/groups', undefined, pricer)).body.data)).not.toEqual(admin)
  })

  it('are the only groups the caller may put a key in', async () => {
    const mine = ids((await call('GET', '/me/groups', undefined, pricer)).body.data)
    const other = ids((await call('GET', '/me/groups')).body.data).find((id) => !mine.includes(id))
    const created = await call('POST', '/me/api-keys', { name: 'k', group_id: other }, pricer)
    expect([created.status, fields(created)]).toEqual([400, ['group_id']])
    expect((await call('POST', '/me/api-keys', { name: 'k', group_id: mine[0] }, pricer)).status).toBe(200)
  })
})
