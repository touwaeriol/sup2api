import { describe, expect, it } from 'vitest'
import { accountPlatform, attemptRuns, attemptTrail, knownUsageRounds, retryCount, type UsageRow } from './usage'

describe('accountPlatform', () => {
  it.each([
    ['anthropic.messages', 'anthropic'],
    ['openai.responses_ws', 'openai'],
    ['gemini.stream_generate', 'gemini'],
    ['volcengine.video_submit', 'volcengine'],
    // a protocol name may itself contain dots; the platform is what precedes the first
    ['acme.v2.chat', 'acme']
  ])('reads the platform off the upstream protocol %s', (upstream_protocol, platform) => {
    expect(accountPlatform({ upstream_protocol })).toBe(platform)
  })
  it.each([undefined, '', 'local', '.messages', 'anthropic.'])(
    'names no platform for %o, which is not an endpoint protocol',
    upstream_protocol => {
      expect(accountPlatform({ upstream_protocol })).toBe('')
    }
  )
})

describe('attemptTrail', () => {
  const trail = {
    v: 1,
    steps: [
      { account_id: 16, status: 502, error_type: 'upstream_error', ms: 640, message: 'connect error', next: 'same' },
      { account_id: 12, status: 200, ms: 2950 }
    ],
    omitted: 0
  }

  it('reads the steps as the API sends them, or as a JSON string', () => {
    const want = { steps: trail.steps }
    expect(attemptTrail({ attempt_trail: trail })).toEqual(want)
    expect(attemptTrail({ attempt_trail: JSON.stringify({ ...trail, omitted: 3 }) })).toEqual({ ...want, omitted: 3 })
  })

  it.each([undefined, null, '', '{broken', '[]', [], 7, { steps: 'x' }, { steps: [] }, { steps: [null, 'x', []] }])(
    'is null for a request sent once, a row from before the trail and a malformed trail (%o)',
    attempt_trail => {
      expect(attemptTrail({ attempt_trail })).toBeNull()
    }
  )

  it('keeps only well-typed fields, a known next action and at most ten steps', () => {
    const steps = Array.from({ length: 12 }, () => ({
      account_id: '9',
      account_ref: -1,
      status: '500',
      ms: -5,
      error_type: 3,
      message: 'x'.repeat(500),
      next: 'later'
    }))
    const read = attemptTrail({ attempt_trail: { steps } })!
    expect(read.steps).toHaveLength(10)
    expect(read.steps[0]).toEqual({ status: 0, ms: 0, message: 'x'.repeat(200) })
  })
})

describe('attemptRuns', () => {
  it('folds consecutive attempts on one account, by ID or by its stand-in number, not repeats further apart', () => {
    const runs = attemptRuns([
      { account_id: 16, status: 502, ms: 1 },
      { account_id: 16, status: 502, ms: 1 },
      { account_id: 12, status: 529, ms: 1 },
      { account_id: 16, status: 200, ms: 1 }
    ])
    expect(runs.map(r => [r.step.account_id, r.times])).toEqual([
      [16, 2],
      [12, 1],
      [16, 1]
    ])
    const refs = attemptRuns([
      { account_ref: 1, status: 502, ms: 1 },
      { account_ref: 1, status: 502, ms: 1 },
      { account_ref: 2, status: 200, ms: 1 }
    ])
    expect(refs.map(r => [r.step.account_ref, r.times])).toEqual([
      [1, 2],
      [2, 1]
    ])
  })

  it("does not fold the attempts of the caller's own records, which name no account", () => {
    expect(
      attemptRuns([
        { status: 502, ms: 1 },
        { status: 200, ms: 1 }
      ]).map(r => r.times)
    ).toEqual([1, 1])
  })
})

describe('retryCount', () => {
  it.each([
    [undefined, 0],
    [0, 0],
    [1, 0],
    [3, 2]
  ])('is %o attempts less the first: %o', (attempts, n) => {
    expect(retryCount({ attempts })).toBe(n)
  })
})

describe('knownUsageRounds', () => {
  it('reads the image tokens of a recorded round', () => {
    const row = { success: false, input_tokens: 0, output_tokens: 0,
      billing_detail: { inputs: { replacement: [{ Tokens: { Input: 40, Output: 520, CacheRead: 0, CacheCreation: 0, CacheCreation1h: 0, ImageInput: 30, ImageOutput: 515 } }] } } } as unknown as UsageRow
    expect(knownUsageRounds(row)).toEqual([{ round: 1, metrics: undefined,
      tokens: { input_tokens: 40, output_tokens: 520, cache_read_tokens: 0, cache_creation_tokens: 0, cache_creation_1h_tokens: 0, image_input_tokens: 30, image_output_tokens: 515 } }])
  })
})
