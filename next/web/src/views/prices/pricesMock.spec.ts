// @vitest-environment node
// The price endpoints of the dev mock hold to the server: a per_token price's
// expression comes from its config, and every `cond ? <number> : 1` of an
// expression is traced as a surcharge rule.
import { beforeAll, describe, expect, it } from 'vitest'
import { mockServer, type MockServer } from '@/testing/mockServer'
import { singleTier, type TokenConfig } from './priceExpr'

let call: MockServer['call']
beforeAll(async () => {
  ;({ call } = await mockServer('core', 'accounts', 'resources', 'billing', 'pricing', 'lookup'))
})

const ruled: TokenConfig = {
  tiers: [{ name: 'base', max_len: null, unit: 'token', p: 3, c: 15 }],
  rules: [
    { name: 'fast', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] },
    {
      name: 'priority',
      multiplier: 3,
      conds: [
        { source: 'param', key: 'service_tier', op: 'eq', value: 'priority' },
        { source: 'usage', key: 'speed', op: 'exists' }
      ]
    }
  ]
}

describe('prices in the mock', () => {
  it('generate the expression of a per_token price from its config, whatever the client sent', async () => {
    const v = (await call('POST', '/prices/validate', { mode: 'per_token', config: singleTier({ p: 3, c: 15 }), expression: 'flat(99)' }))
      .body.data
    expect(v).toMatchObject({ ok: true, expression: 'tier("base", p*3 + c*15)' })
  })

  it('trace each surcharge of the generated expression, and multiply the matched ones', async () => {
    const usage = { p: 1_000_000, c: 0 }
    const preview = async (extra: object) =>
      (await call('POST', '/prices/preview', { mode: 'per_token', config: ruled, expression: '', usage, ...extra })).body.data
    const plain = await preview({})
    expect(plain.expression).toBe(
      '(tier("base", p*3 + c*15)) * (has(header("anthropic-beta"), "fast-mode") ? 2 : 1) * (param("service_tier") == "priority" && u("speed") != 0 ? 3 : 1)'
    )
    expect(plain.rules.map((r: { matched: boolean; multiplier: number }) => [r.matched, r.multiplier])).toEqual([
      [false, 2],
      [false, 3]
    ])
    expect(Number(plain.cost)).toBeCloseTo(3, 8)
    const both = await preview({
      headers: { 'Anthropic-Beta': 'fast-mode' },
      params: { service_tier: 'priority' },
      metrics: { speed: 'fast' }
    })
    expect(both.rules.every((r: { matched: boolean }) => r.matched)).toBe(true)
    expect(both.breakdown).toMatchObject({ subtotal: '3.00000000', rules_multiplier: 6 })
    expect(Number(both.cost)).toBeCloseTo(18, 8)
  })

  it('trace only conditions that read the request, and leave a rule not reached unmatched', async () => {
    const preview = async (expression: string, extra: object = {}) =>
      (await call('POST', '/prices/preview', { mode: 'expression', expression, usage: { p: 1_000_000 }, ...extra })).body.data
    const constant = await preview('tier("base", p*1) * (len > 0 ? 2 : 1)')
    expect(constant.rules).toEqual([])
    expect(Number(constant.cost)).toBeCloseTo(2, 8)
    // The rule sits in the branch not taken, and behind an && that fails first.
    const src =
      'len <= 10 ? tier("a", p*1) * (param("x") == "y" ? 3 : 1) : tier("b", p*1) * (header("h") == "" && (param("x") == "y" ? 5 : 1) > 0 ? 2 : 1)'
    const r = await preview(src, { params: { x: 'y' }, headers: { h: 'set' } })
    expect(r.rules.map((x: { multiplier: number; matched: boolean }) => [x.multiplier, x.matched])).toEqual([
      [3, false],
      [2, false],
      [5, false]
    ])
    expect(Number(r.cost)).toBeCloseTo(1, 8)
  })

  it('refuse a config the server would not generate an expression of', async () => {
    const validate = async (body: object) => (await call('POST', '/prices/validate', body)).body.data
    const bad = await validate({ mode: 'per_token', config: { ...ruled, rules: [{ name: '', multiplier: 2, conds: [{ source: 'header', key: 'bad name', op: 'exists' }] }] } })
    expect(bad).toMatchObject({ ok: false, errors: [{ code: 'invalid_config' }] })
    expect(await validate({ mode: 'per_request', config: { price: null } })).toMatchObject({ ok: false, errors: [{ code: 'invalid_config' }] })
    // The server sorts the tiers: a config listed out of order is fine.
    const unsorted = { tiers: [{ name: 'rest', max_len: null, p: 2, c: 2 }, { name: 'short', max_len: 1000, p: 1, c: 1 }], rules: [] }
    expect(await validate({ mode: 'per_token', config: unsorted })).toMatchObject({ ok: true })
    expect((await call('POST', '/prices/preview', { mode: 'per_request', config: {}, usage: {} })).status).toBe(400)
  })

  it('leave a custom price to a per-token price of one token tier without rules', async () => {
    const save = (model: string, config: unknown) =>
      call('PUT', '/groups/1/model-prices', { items: [{ model, rule: { kind: 'custom', mode: 'per_token', config } }] })
    expect((await save('gpt-4o', singleTier({ p: 1, c: 2 }))).status).toBe(200)
    expect((await save('gpt-4o', { tiers: [{ name: 'a', max_len: null, unit: 'request', price: 1 }], rules: [] })).status).toBe(400)
  })
})
