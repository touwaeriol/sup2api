import { describe, expect, it } from 'vitest'
import type { AccountTestResult } from '@/api/types'
import { candidateModels, formatLatency, hasWarnings, lastTestOf, latencyLevel, mergeCandidates, prettyBody, summarize, withoutModels } from './accountTest'

describe('account test helpers', () => {
  it('offers the account model list when it has one', () => {
    expect(candidateModels({ models: ['a', 'b', 'a'], model_mapping: { x: 'a' } }, { default_models: ['d'] })).toEqual([
      { model: 'a', source: 'account' },
      { model: 'b', source: 'account' }
    ])
  })

  it('falls back to mapping request models + plugin defaults (deduplicated) for all-models accounts', () => {
    expect(candidateModels({ models: [], model_mapping: { x: 'y', d1: 'z' } }, { default_models: ['d1', 'd2'] })).toEqual([
      { model: 'x', source: 'mapping' },
      { model: 'd1', source: 'mapping' },
      { model: 'd2', source: 'default' }
    ])
    expect(candidateModels({ models: [], model_mapping: {} }, null)).toEqual([])
  })

  it('merges extra candidates without duplicates', () => {
    const r = mergeCandidates([{ model: 'a', source: 'account' }], ['a', 'b', '', 'b'], 'upstream')
    expect(r.added).toBe(1)
    expect(r.list).toEqual([{ model: 'a', source: 'account' }, { model: 'b', source: 'upstream' }])
  })

  it('bands and formats latencies', () => {
    expect([999, 1000, 1001, 3000, 3001, 5000, 5001].map(latencyLevel)).toEqual(['fast', 'fast', 'ok', 'ok', 'slow', 'slow', 'bad'])
    expect(formatLatency(480.4)).toBe('480 ms')
    expect(formatLatency(1234)).toBe('1.23 s')
    expect(formatLatency(undefined)).toBe('—')
  })

  it('summarizes row states', () => {
    expect(summarize([{ state: 'ok' }, { state: 'fail' }, { state: 'fail' }, { state: 'testing' }, { state: 'idle' }, { state: 'canceled' }])).toEqual({ total: 6, ok: 1, fail: 2, testing: 1, idle: 1, canceled: 1 })
  })

  it('removes failed models, null when nothing changes', () => {
    expect(withoutModels(['a', 'b', 'c'], ['b', 'x'])).toEqual(['a', 'c'])
    expect(withoutModels(['a'], ['x'])).toBeNull()
    expect(withoutModels(['a'], ['a'])).toEqual([])
  })

  it('derives last_test from a result', () => {
    const at = new Date('2026-10-05T00:00:00Z')
    const r = (o: Partial<AccountTestResult>): AccountTestResult => ({ ok: true, status: 200, latency_ms: 10, ...o })
    // A success records its warnings as the message, as the server does (CONTRACTS §122.5).
    expect(lastTestOf(r({ model: 'up', message: 'hi' }), 'req', at)).toMatchObject({ at: at.toISOString(), ok: true, status: 200, latency_ms: 10, model: 'up', message: '' })
    const warnings = [{ code: 'model_price_not_configured', message: 'no price' }, { code: 'x', message: 'other' }]
    expect(lastTestOf(r({ warnings }), 'req', at).message).toBe('no price\nother')
    expect(hasWarnings(r({ warnings }))).toBe(true)
    expect(hasWarnings(r({}))).toBe(false)
    expect(lastTestOf(r({ ok: false, reason: 'rate_limited' }), 'req', at)).toMatchObject({ ok: false, model: 'req', message: 'rate_limited' })
    expect(lastTestOf(r({ requested_model: 'rq' }), '', at).model).toBe('rq')
    expect(lastTestOf(r({ protocol: 'openai.responses' }), 'req', at).protocol).toBe('openai.responses')
    expect(lastTestOf(r({ ok: false, status: 0, error_code: 'proxy_unavailable', error_source: 'gateway' }), 'req', at)).toMatchObject({ status: 0, error_code: 'proxy_unavailable', error_source: 'gateway' })
  })

  it('pretty prints JSON bodies and keeps truncated ones verbatim', () => {
    expect(prettyBody('{"a":1}')).toBe('{\n  "a": 1\n}')
    expect(prettyBody('{"a":')).toBe('{"a":')
    expect(prettyBody(undefined)).toBe('')
  })
})
