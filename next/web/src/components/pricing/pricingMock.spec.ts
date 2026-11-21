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

describe('the list price of a multiplier rule in the mock', () => {
  it('is the global price as it is with the multiplier beside it, in the model prices of a group', async () => {
    const { body } = await call('GET', '/groups/1/model-prices')
    const row = body.data.models.find((m: { model: string }) => m.model === 'gpt-4o')
    expect(row.group_rule).toEqual({ kind: 'multiplier', multiplier: '1.2' })
    expect(row.list_price).toEqual({ ...row.base, multiplier: '1.2' })
  })

  it('is the same in the caller’s own prices of a group and in a quote', async () => {
    const mine = (await call('GET', '/me/prices?group_id=1')).body.data
    const own = mine.prices.find((p: { model: string }) => p.model === 'gpt-4o')
    const base = (await call('GET', '/groups/1/model-prices')).body.data.models.find((m: { model: string }) => m.model === 'gpt-4o').base
    expect(own).toMatchObject({ ...base, multiplier: '1.2', source: 'group' })

    const quote = (await call('POST', '/pricing/quote', { user_id: 1, group_id: 1, model: 'gpt-4o', usage: { p: 1_000_000, c: 0, cc1h: 0 } })).body.data
    expect(quote.list_price).toEqual({ ...base, multiplier: '1.2' })
    expect(quote.price_levels).toEqual({ group: { kind: 'multiplier', multiplier: '1.2' } })
    expect(Number(quote.list_cost)).toBeCloseTo(Number(base.config.tiers[0].p) * 1.2, 8)
  })

  it('keeps a custom price a price of its own, without a multiplier', async () => {
    const row = (await call('GET', '/groups/1/model-prices')).body.data.models.find((m: { model: string }) => m.model === 'web-search')
    expect(row.list_price).toMatchObject({ mode: 'visual', config: requestTier(0.008) })
    expect(row.list_price.multiplier).toBeUndefined()
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

  it('counts and lists every model price of a group, on models it provides or not', async () => {
    const group = async () => (await call('GET', '/groups?page_size=200')).body.data.data.find((g: { id: number }) => g.id === 1)
    const listed = async () => (await call('GET', '/groups/1/model-prices')).body.data.models as Array<{ model: string; offered: boolean; group_rule: unknown }>
    expect((await group()).price_rule_count).toBe(3)

    // The group stops providing gpt-4o and gpt-4o-mini: the rule on gpt-4o stays, counted and listed as not provided; gpt-4o-mini goes.
    await call('PATCH', '/groups/1', { model_filter_mode: 'blacklist', model_allowlist: ['gpt-4o', 'gpt-4o-mini'] })
    expect((await group()).price_rule_count).toBe(3)
    const after = await listed()
    expect(after.find((m) => m.model === 'gpt-4o')).toMatchObject({ offered: false, group_rule: { kind: 'multiplier', multiplier: '1.2' } })
    expect(after.some((m) => m.model === 'gpt-4o-mini')).toBe(false)
    expect(after.filter((m) => m.group_rule).length).toBe(3)

    await call('PATCH', '/groups/1', { model_allowlist: [] })
    expect((await listed()).find((m) => m.model === 'gpt-4o')?.offered).toBe(true)
  })

  it('prices a model the group does not provide, as long as it has an original price', async () => {
    const catalog = (await call('GET', '/pricing/catalog')).body.data as Array<{ model: string }>
    const priced = catalog.map((c) => c.model)
    const provided = ((await call('GET', '/groups/2/model-prices')).body.data.models as Array<{ model: string }>).map((m) => m.model)
    const model = priced.find((m) => !provided.includes(m))!
    expect(model).toBeDefined()
    const put = await call('PUT', '/groups/2/model-prices', { items: [{ model, rule: { kind: 'custom', mode: 'expression', expression: 'p * 2' } }] })
    expect(put.status).toBe(200)
    expect(put.body.data.models.find((m: { model: string }) => m.model === model)).toMatchObject({ offered: false, group_rule: { kind: 'custom', mode: 'expression', expression: 'p * 2' }, source: 'group' })
    const unpriced = await call('PUT', '/groups/2/model-prices', { items: [{ model: 'no-such-model', rule: { kind: 'multiplier', multiplier: '2' } }] })
    expect([unpriced.status, fields(unpriced)]).toEqual([400, ['items.0']])
    await call('PUT', '/groups/2/model-prices', { items: [{ model, rule: null }] })
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
