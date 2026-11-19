import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import UsageDetail from './UsageDetail.vue'
import type { UsageRow } from './usage'
import type { UsageView } from './view'

const get = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/host', () => ({ api: { get } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => false, hasAction: () => false }) }))
vi.mock('@/views/accounts/accountTypes', () => ({ useAccountTypes: () => ({ load: vi.fn() }) }))
vi.mock('@/composables/platforms', () => ({ usePlatforms: () => ({ load: vi.fn(), label: (id: string) => id }) }))
enableAutoUnmount(afterEach)

const row = {
  id: 1, request_id: 'fixture', model: 'fixture-model', success: false, status_code: 502,
  input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0,
  total_cost: '0', billing_status: 'failed', latency_ms: 0,
  billing_detail: { inputs: { billing_error: 'helper response usage is incomplete', replacement: [
    { Kind: 'helper_round', Tokens: { Input: 24, Output: 91, CacheRead: 0, CacheCreation: 0, CacheCreation1h: 1647 } },
    { Kind: 'helper_round', Tokens: { Input: 17 } }
  ] } }
} as unknown as UsageRow

function render(value = row, path?: string, view: UsageView = 'all') {
  i18n.global.locale.value = 'zh'
  return mount(UsageDetail, { props: { row: value, path, view }, global: {
    plugins: [i18n], stubs: { ExprHistoryModal: true, BillingBreakdown: true }
  } })
}

describe('call chain', () => {
  const steps = [{ account_id: 16, status: 502, error_type: 'upstream_error', ms: 640, next: 'switch' }, { account_id: 12, status: 200, ms: 2950 }]
  const retried = { ...row, attempts: 2, attempt_trail: { v: 1, steps } } as unknown as UsageRow

  it('names the accounts the detailed record names, before the scheduling trace', async () => {
    const named = steps.map((s, i) => ({ ...s, account_name: ['relay-1', 'claude-main'][i] }))
    get.mockResolvedValueOnce({ ...retried, attempt_trail: { v: 1, steps: named }, anomalies: { scheduling: JSON.stringify({ v: 1, outcome: 'picked', skipped: [] }) } })
    const wrapper = render(retried, '/usage/1')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="attempt-account"]').map(a => a.text())).toEqual(['#16 relay-1', '#12 claude-main'])
    const html = wrapper.html()
    expect(html.indexOf('data-testid="usage-attempts"')).toBeLessThan(html.indexOf('data-testid="usage-scheduling"'))
  })

  it('leaves the accounts out of the caller\'s own records', () => {
    const wrapper = render(retried, undefined, 'self')
    expect(wrapper.findAll('[data-testid="attempt-step"]')).toHaveLength(2)
    expect(wrapper.find('[data-testid="attempt-account"]').exists()).toBe(false)
  })
})

describe('model availability trace', () => {
  const rejected = { ...row, status_code: 429, error_type: 'model_availability', billing_status: 'free', billing_detail: {},
    error_message: 'the requested model is unavailable for these accounts, please retry later' } as unknown as UsageRow
  const trace = JSON.stringify({ v: 1, outcome: 'blocked_all', candidates: 1, admitted: 0, counts: { 'unavailable:quota_rejected:7d_fable': 1 },
    samples: [{ account_id: 25, decision: 'block', reason: 'unavailable:quota_rejected:7d_fable' }] })

  it('shows the trace from the detailed record before the failure decisions and points the error at it', async () => {
    get.mockResolvedValueOnce({ ...rejected, anomalies: { availability: trace, failure_policy: '[{"action":"retry"}]' } })
    const wrapper = render(rejected, '/usage/1')
    expect(wrapper.find('[data-testid="usage-availability"]').exists()).toBe(false)
    await flushPromises()
    const card = wrapper.get('[data-testid="usage-availability"]')
    expect(card.text()).toContain('额度窗口 7d_fable 已耗尽')
    expect(wrapper.get('[data-testid="usage-availability-hint"]').text()).toContain('原因见下方')
    const html = wrapper.html()
    expect(html.indexOf('data-testid="usage-availability"')).toBeLessThan(html.indexOf('data-testid="failure-decisions"'))
  })

  it('shows neither the card nor the hint without a trace', () => {
    for (const anomalies of [undefined, {}, { availability: '{broken' }]) {
      const wrapper = render({ ...rejected, anomalies } as unknown as UsageRow)
      expect(wrapper.find('[data-testid="usage-availability"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="usage-availability-hint"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('the requested model is unavailable')
    }
  })

  it('does not point other errors at the trace', () => {
    const wrapper = render({ ...rejected, error_type: 'upstream_overloaded', anomalies: { availability: trace } } as unknown as UsageRow)
    expect(wrapper.find('[data-testid="usage-availability"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="usage-availability-hint"]').exists()).toBe(false)
  })
})

describe('vendor and platform marks', () => {
  const served = {
    ...row, success: true, status_code: 200, billing_status: 'billed', billing_detail: {}, model: 'gpt-4o', upstream_model: 'claude-sonnet-4-5',
    account_id: 9, account_name: 'Account nine', platform: 'openai', protocol: 'openai.chat', endpoint: '/v1/chat/completions', upstream_protocol: 'anthropic.messages',
    price: { id: 3, model: 'gpt-4o*', source: 'manual' }
  } as unknown as UsageRow
  const brands = (w: { findAll: ReturnType<typeof render>['findAll'] }) => w.findAll('[data-testid="model-badge"] [data-brand]').map(m => m.attributes('data-brand'))

  it('marks the requested, priced and mapped models by vendor, the account by the upstream platform and the endpoint by its own', () => {
    const w = render(served)
    expect(brands(w.get('[data-testid="usage-detail-summary"]'))).toEqual(['openai'])
    expect(w.get('[data-testid="usage-billing-facts"] [data-testid="model-badge"]').text()).toContain('gpt-4o*')
    const facts = w.get('[data-testid="usage-request-facts"]')
    expect(brands(facts)).toEqual(['anthropic'])
    const account = facts.get('[data-testid="detail-account"]')
    expect(account.text()).toContain('#9 Account nine')
    expect(account.get('[data-platform]').attributes('data-platform')).toBe('anthropic')
    expect(facts.get('[data-testid="platform-badge"]').attributes('data-platform')).toBe('openai')
  })

  it('repeats no upstream model equal to the requested one, guesses no platform the record does not name, and dashes a missing model', () => {
    const same = render({ ...served, upstream_model: 'gpt-4o', upstream_protocol: 'local' } as unknown as UsageRow)
    const facts = same.get('[data-testid="usage-request-facts"]')
    expect(brands(facts)).toEqual([])
    const account = facts.get('[data-testid="detail-account"]')
    expect(account.text()).toBe('#9 Account nine')
    expect(account.find('[data-platform]').exists()).toBe(false)
    const summary = render({ ...served, model: '' } as unknown as UsageRow).get('[data-testid="usage-detail-summary"]')
    expect(summary.find('[data-testid="model-badge"]').exists()).toBe(false)
    expect(summary.get('p').text()).toBe('—')
  })
})

describe('protocol conversion', () => {
  it('shows grade, steps, policy, losses by side and category, refused types and omitted paths', () => {
    const conversion = {
      v: 1, grade: 'fair', hops: ['anthropic.messages', 'openai.responses', 'openai.chat'], policy: 'safe', omitted: 3,
      lost: { other: ['top_k'], tool: ['tools[3]', '1:tools[0]'] }, response_lost: { reasoning: ['response.output[0].encrypted_content'] },
      rejected: [{ type: 'anthropic/apikey', policy: 'strict', category: 'tool', path: 'tools[1]' }, { type: 'x/y', error: 'previous_response_id is not supported' }]
    }
    const card = render({ ...row, conversion } as unknown as UsageRow).get('[data-testid="usage-conversion"]')
    expect(card.get('[data-testid="conversion-grade"]').text()).toBe('一般')
    expect(card.get('[data-testid="conversion-hops"]').text()).toBe('anthropic.messages → openai.responses → openai.chat')
    expect(card.get('[data-testid="conversion-policy"]').text()).toBe('安全')
    // categories in their fixed order, request and response apart
    expect(card.get('[data-testid="conversion-lost"]').text()).toMatch(/工具\s*tools\[3\]\s*1:tools\[0\]\s*其他\s*top_k/)
    expect(card.get('[data-testid="conversion-response-lost"]').text()).toContain('推理')
    expect(card.text()).toContain('第 2 跳（openai.responses → openai.chat）')
    expect(card.text()).toContain('3')
    const rejected = card.get('[data-testid="conversion-rejected"]').findAll('li').map(li => li.text())
    expect(rejected[0]).toMatch(/anthropic\/apikey\s*严格\s*工具\s*tools\[1\]/)
    expect(rejected[1]).toContain('previous_response_id is not supported')
  })

  it('marks a request every account type refused, and shows nothing without a record', () => {
    const refused = render({ ...row, conversion: { v: 1, rejected: [{ type: 'x/y', policy: 'strict', category: 'other', path: 'seed' }] } } as unknown as UsageRow)
    expect(refused.get('[data-testid="conversion-all-rejected"]').text()).toBe('全部拒绝')
    expect(render({ ...row, conversion: null } as unknown as UsageRow).find('[data-testid="usage-conversion"]').exists()).toBe(false)
  })
})

describe('known partial usage in failed request details', () => {
  it('shows client IP, client agent and upstream attempts from detail', async () => {
    get.mockResolvedValueOnce({ ...row, client_ip: '192.0.2.9', user_agent: 'fixture-client', attempts: 2, api_key_name: 'my-laptop', node_id: '' })
    const wrapper = render(row, '/me/usage/1')
    await flushPromises()
    expect(wrapper.get('[data-testid="detail-client-ip"]').text()).toBe('192.0.2.9')
    expect(wrapper.text()).toContain('fixture-client')
    expect(wrapper.text()).toContain('my-laptop')
    expect(wrapper.text()).toContain('上游尝试次数')
  })
  it('separates reported TTL facts from compatible billing for successful partial usage', () => {
    const value = { ...row, success: true, status_code: 200, cache_creation_tokens: 1219, cache_creation_1h_tokens: 1376,
      metrics: { cache_write_evidence: { version: 1, total: { state: 'value', value: 2595 }, explicit_5m: { state: 'value', value: 0 }, explicit_1h: { state: 'value', value: 1376 }, unclassified_tokens: 1219, completeness: 'partial', source: 'sse', pricing_policy: 'platform_default_cache_write_compat' } }
    } as unknown as UsageRow
    const before = JSON.stringify(value)
    const wrapper = render(value)
    const facts = wrapper.get('[data-testid="cache-write-facts"]')
    expect(facts.get('button[aria-expanded]').attributes('aria-expanded')).toBe('true')
    expect(facts.text()).toContain('已报告的 5 分钟写入')
    expect(facts.text()).toContain('TTL 未细分')
    expect(facts.text()).toContain('1,219')
    expect(facts.text()).toContain('平台默认缓存写入费率')
    expect(wrapper.text()).not.toContain('u("cache_write_evidence")')
    expect(JSON.stringify(value)).toBe(before)
  })
  it('keeps complete cache observations compact while allowing the full counters to be opened', async () => {
    const value = { ...row, success: true, status_code: 200, cache_creation_tokens: 1200,
      metrics: { cache_write_evidence: { version: 1, total: { state: 'value', value: 1200 }, explicit_5m: { state: 'value', value: 1200 }, explicit_1h: { state: 'value', value: 0 }, unclassified_tokens: 0, completeness: 'complete', source: 'sse', pricing_policy: 'platform_default_cache_write_compat' } }
    } as unknown as UsageRow
    const wrapper = render(value)
    const facts = wrapper.get('[data-testid="cache-write-facts"]')
    expect(facts.text()).toContain('已报告的 TTL 细分完整')
    expect(facts.find('dd').exists()).toBe(false)
    await facts.get('button[aria-expanded]').trigger('click')
    expect(facts.findAll('dd').map(node => node.text())).toEqual(['1,200', '1,200', '0', '0'])
    expect(facts.text()).toContain('平台默认缓存写入费率')
  })
  it('shows recorded rounds and 1h cache separately without inventing totals or missing counts', () => {
    const before = JSON.stringify(row)
    const wrapper = render()
    const section = wrapper.get('[data-testid="known-usage-rounds"]')
    expect(section.text()).toContain('不能作为本次请求总计')
    const rounds = section.findAll('[data-testid="known-usage-round"]')
    expect(rounds).toHaveLength(2)
    expect(rounds[0]!.findAll('dd').map(node => node.text())).toEqual(['24', '91', '0', '0', '1,647'])
    expect(rounds[1]!.findAll('dd').map(node => node.text())).toEqual(['17', '—', '—', '—', '—'])
    expect(section.text()).not.toContain('41')
    expect(wrapper.text()).toContain('结算失败')
    expect(JSON.stringify(row)).toBe(before)
  })

  it('loads partial evidence from the detailed record when the list lacks it', async () => {
    get.mockResolvedValueOnce(row)
    const wrapper = render({ ...row, billing_detail: {} }, '/me/usage/1')
    expect(wrapper.find('[data-testid="known-usage-rounds"]').exists()).toBe(false)
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/me/usage/1')
    expect(wrapper.findAll('[data-testid="known-usage-round"]')).toHaveLength(2)
  })

  it('keeps main and billed component cache evidence separate and does not expose debug fields', () => {
    const evidence = (n: number) => ({ cache_write_evidence: { version: 1, total: { state: 'value', value: n }, explicit_5m: { state: 'absent' }, explicit_1h: { state: 'absent' }, unclassified_tokens: n, completeness: 'unknown', source: 'iteration', pricing_policy: 'platform_default_cache_write_compat', private_debug: 'secret' } })
    const value = { ...row, success: true, status_code: 200, metrics: evidence(11), billing_detail: { inputs: {
      additional: [{ Metrics: evidence(22) }], replacement: [{ Metrics: evidence(33) }]
    } }, plugin_detail: { private_debug: 'secret' } } as unknown as UsageRow
    const wrapper = render(value)
    const parts = wrapper.findAll('[data-testid="component-cache-write-facts"]')
    expect(parts).toHaveLength(2)
    expect(parts[0]!.findAll('dd').map(node => node.text())).toEqual(['22', '未报告', '未报告', '22'])
    expect(parts[1]!.findAll('dd').map(node => node.text())).toEqual(['33', '未报告', '未报告', '33'])
    expect(wrapper.text()).not.toContain('secret')
    expect(wrapper.findAll('[data-testid="cache-write-facts"]')).toHaveLength(3)
  })

  it('keeps ordinary successful or nonzero top-level usage unchanged', () => {
    for (const value of [{ ...row, success: true }, { ...row, input_tokens: 12 }, { ...row, cache_creation_1h_tokens: 5 }]) {
      expect(render(value).find('[data-testid="known-usage-rounds"]').exists()).toBe(false)
    }
  })

  it('does not turn malformed or absent replacement facts into zero usage', () => {
    for (const replacement of [null, {}, [{ Tokens: null }], [{ Tokens: [] }], [{ Tokens: { Unknown: 24 } }], [{ Tokens: { Input: '24', Output: -1, CacheRead: Number.NaN } }]]) {
      const value = { ...row, billing_detail: { inputs: { replacement } } }
      expect(render(value).find('[data-testid="known-usage-rounds"]').exists()).toBe(false)
    }
  })
})

describe('unreported execution', () => {
  const unreported = { ...row, status_code: 200, error_type: 'usage_unreported', billing_status: 'free', billing_detail: {},
    error_message: 'plugin returned no usage' } as unknown as UsageRow

  it('says the plugin reported no usage, so the request was not billed (design §8.5)', () => {
    const wrapper = render(unreported)
    expect(wrapper.get('[data-testid="usage-unreported-hint"]').text()).toBe('插件没有返回这次请求的用量（或选用了核心的读数而读数为空），因此未计费。')
    expect(render({ ...unreported, error_type: 'upstream_error' } as UsageRow).find('[data-testid="usage-unreported-hint"]').exists()).toBe(false)
  })
})
