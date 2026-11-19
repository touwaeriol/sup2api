import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import UsageSchedulingTrace from './UsageSchedulingTrace.vue'
import { schedulingTrace, type UsageRow } from './usage'

enableAutoUnmount(afterEach)

function rowWith(scheduling: unknown): UsageRow {
  return {
    id: 1, request_id: 'fixture', model: 'fixture-model', success: false, status_code: 529, error_type: 'no_account',
    created_at: '2026-11-11T08:00:00Z', anomalies: scheduling === undefined ? {} : { scheduling: typeof scheduling === 'string' ? scheduling : JSON.stringify(scheduling) }
  } as unknown as UsageRow
}

function render(scheduling: unknown, locale: 'zh' | 'en' = 'zh') {
  i18n.global.locale.value = locale
  return mount(UsageSchedulingTrace, { props: { row: rowWith(scheduling) }, global: { plugins: [i18n] } })
}

const none = {
  v: 1, outcome: 'none', candidates: 2, eligible: 0,
  counts: { referenced_model_not_served: 2, not_schedulable: 1 },
  skipped: [
    { account_id: 124, account_name: 'relay-a', reason: 'referenced_model_not_served', detail: 'fixture-fallback' },
    { account_id: 129, reason: 'referenced_model_not_served', detail: 'fixture-fallback' },
    { account_id: 127, account_name: 'relay-b', reason: 'not_schedulable', detail: 'disabled' }
  ],
  omitted: 2
}

const open = (w: ReturnType<typeof render>) => w.get('button[aria-expanded]').attributes('aria-expanded')

describe('UsageSchedulingTrace', () => {
  it('opens a request no account served, with each account and its reason', () => {
    const w = render(none)
    expect(open(w)).toBe('true')
    expect(w.get('[data-testid="scheduling-outcome"]').text()).toBe('无账号可用')
    const skips = w.findAll('[data-testid="scheduling-skip"]')
    expect(skips).toHaveLength(3)
    expect(skips[0].text()).toContain('#124 relay-a')
    expect(skips[0].get('[data-testid="scheduling-reason"]').text()).toBe('账号模型列表不含请求引用的模型：fixture-fallback')
    expect(skips[1].text()).toContain('#129')
    expect(skips[2].get('[data-testid="scheduling-reason"]').text()).toBe('账号当前不参与调度：已停用')
    expect(w.get('[data-testid="scheduling-counts"]').text()).toContain('× 2')
    expect(w.text()).toContain('另有 2 个账号未列出')
  })

  it('stays closed when an account served, and shows an unknown code as recorded', () => {
    const w = render({ v: 1, outcome: 'picked', candidates: 3, eligible: 3, counts: { future_reason: 1 }, skipped: [{ account_id: 1, reason: 'future_reason', detail: 'x' }] }, 'en')
    expect(open(w)).toBe('false')
    expect(schedulingTrace(rowWith({ outcome: 'picked', skipped: [{ account_id: 1, reason: 'future_reason', detail: 'x' }] }))?.skipped[0]).toEqual({ account_id: 1, reason: 'future_reason', detail: 'x' })
  })

  it('words the three reasons of the protocol binding, with the protocol or the model they are about', () => {
    // DESIGN-2026-11-17-PROTOCOL-BINDING §3.4: the account closes the protocol, the model is not converted, the model is served on other protocols.
    const trace = {
      v: 1, outcome: 'none', candidates: 3, eligible: 0,
      counts: { protocol_closed: 1, conversion_not_enabled: 1, model_protocol_closed: 1 },
      skipped: [
        { account_id: 1, account_name: 'compat-chat-only', reason: 'protocol_closed', detail: 'openai.responses' },
        { account_id: 2, account_name: 'openai-main', reason: 'conversion_not_enabled', detail: 'gpt-5.6' },
        { account_id: 3, account_name: 'openai-cc', reason: 'model_protocol_closed', detail: 'text-embedding-3-large' }
      ]
    }
    const reasons = (locale: 'zh' | 'en') => render(trace, locale).findAll('[data-testid="scheduling-reason"]').map(r => r.text())
    expect(reasons('zh')).toEqual(['账号未开放该协议：openai.responses', '模型未开启协议转换：gpt-5.6', '模型在该协议下不提供：text-embedding-3-large'])
    expect(reasons('en')).toEqual([
      'Account does not open this protocol: openai.responses',
      'Conversion is not turned on for the model: gpt-5.6',
      'Model is not served on this protocol: text-embedding-3-large'
    ])
  })

  it('names no account for a caller without account:read', () => {
    const w = render({ v: 1, outcome: 'busy', candidates: 1, eligible: 1, counts: { rate_limited: 1 }, skipped: [{ reason: 'rate_limited' }] }, 'en')
    expect(w.get('[data-testid="scheduling-hidden-account"]').text()).toBe('No permission to view accounts')
    expect(w.get('[data-testid="scheduling-reason"]').text()).toBe('Rate limited')
  })

  it('renders nothing without a well-formed trace', () => {
    for (const value of [undefined, '{broken', '[]', { candidates: 1 }]) {
      expect(render(value).find('[data-testid="usage-scheduling"]').exists()).toBe(false)
    }
  })
})
