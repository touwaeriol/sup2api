// @vitest-environment node
// The price endpoints of the dev mock hold to the server: a visual price's
// expression comes from its config, and every `cond ? <number> : 1` of an
// expression is traced as a surcharge rule.
import { beforeAll, describe, expect, it } from 'vitest'
import { mockServer, type MockServer } from '@/testing/mockServer'
import { requestTier, singleTier, type VisualConfig } from './priceExpr'

let call: MockServer['call']
beforeAll(async () => {
  ;({ call } = await mockServer('core', 'accounts', 'resources', 'billing', 'pricing', 'lookup'))
})

const ruled: VisualConfig = {
  tiers: [{ name: 'base', unit: 'token', p: 3, c: 15 }],
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
  it('generate the expression of a visual price from its config, whatever the client sent', async () => {
    const v = (await call('POST', '/prices/validate', { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: 'flat(99)' }))
      .body.data
    expect(v).toMatchObject({ ok: true, expression: 'tier("base", p*3 + c*15)' })
  })

  it('trace each surcharge of the generated expression, and multiply the matched ones', async () => {
    const usage = { p: 1_000_000, c: 0 }
    const preview = async (extra: object) =>
      (await call('POST', '/prices/preview', { mode: 'visual', config: ruled, expression: '', usage, ...extra })).body.data
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
    const bad = await validate({ mode: 'visual', config: { ...ruled, rules: [{ name: '', multiplier: 2, conds: [{ source: 'header', key: 'bad name', op: 'exists' }] }] } })
    expect(bad).toMatchObject({ ok: false, errors: [{ code: 'invalid_config' }] })
    expect(await validate({ mode: 'visual', config: requestTier(null) })).toMatchObject({ ok: false, errors: [{ code: 'invalid_config' }] })
    // Tiers match in the order listed: only the last one is the rest.
    const restFirst = { tiers: [{ name: 'rest', p: 2, c: 2 }, { name: 'short', when: { var: 'len', op: '<=', value: 1000 }, p: 1, c: 1 }], rules: [] }
    expect(await validate({ mode: 'visual', config: restFirst })).toMatchObject({ ok: false, errors: [{ code: 'invalid_config' }] })
    expect((await call('POST', '/prices/preview', { mode: 'visual', config: {}, usage: {} })).status).toBe(400)
  })

  it('hit the tier whose condition holds at the time simulated, and name it', async () => {
    const night: VisualConfig = {
      tiers: [
        {
          name: 'long_night',
          when: { and: [{ var: 'len', op: '>', value: 200000 }, { or: [{ var: 'hour', tz: 'Asia/Shanghai', op: '>=', value: 22 }, { var: 'hour', tz: 'Asia/Shanghai', op: '<', value: 6 }] }] },
          unit: 'token',
          p: 4,
          c: 18
        },
        { name: 'long', when: { var: 'len', op: '>', value: 200000 }, unit: 'token', p: 6, c: 22.5 },
        { name: 'base', unit: 'token', p: 3, c: 15 }
      ],
      rules: []
    }
    const tierAt = async (at: string, p: number) =>
      (await call('POST', '/prices/preview', { mode: 'visual', config: night, expression: '', usage: { p, c: 0 }, at })).body.data.tier
    expect(await tierAt('2026-01-01T15:30:00Z', 300000)).toBe('long_night') // 23:30 in Shanghai
    expect(await tierAt('2026-01-01T04:00:00Z', 300000)).toBe('long') // 12:00 in Shanghai
    expect(await tierAt('2026-01-01T15:30:00Z', 1000)).toBe('base')
  })

  it('leave a custom price to a visual price of one tier without rules, priced as the global one is', async () => {
    const save = (model: string, config: unknown) =>
      call('PUT', '/groups/1/model-prices', { items: [{ model, rule: { kind: 'custom', mode: 'visual', config } }] })
    expect((await save('gpt-4o', singleTier({ p: 1, c: 2 }))).status).toBe(200)
    expect((await save('gpt-4o', { tiers: [{ name: 'a', unit: 'request', price: 1 }], rules: [] })).status).toBe(400)
  })
})
