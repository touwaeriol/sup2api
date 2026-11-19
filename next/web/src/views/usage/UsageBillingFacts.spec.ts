import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { UsagePricing } from '@/api/pricing'
import UsageBillingFacts from './UsageBillingFacts.vue'
import type { UsageRow } from './usage'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => false }) }))
vi.mock('@/composables/platforms', () => ({ usePlatforms: () => ({ load: vi.fn(), label: (id: string) => id }) }))
enableAutoUnmount(afterEach)

/** A billed record: 0.00576 at the global price, then what group pricing made of it. */
function record(detail: { rate: string; total: string; pricing?: UsagePricing; list_cost?: string }): UsageRow {
  const { rate, total, pricing, list_cost } = detail
  return {
    id: 1, request_id: 'fixture', model: 'gpt-4o-mini', success: true, status_code: 200, billing_status: 'billed', latency_ms: 0,
    input_tokens: 12000, output_tokens: 1800, cache_read_tokens: 0, cache_creation_tokens: 0, total_cost: total, rate_multiplier: rate,
    billing_detail: {
      breakdown: { items: [], subtotal: '0.00576', base: '0.00576', rules_multiplier: 1 },
      cost: '0.00576', rate_multiplier: rate, total_cost: total, ...(list_cost ? { list_cost } : {}), ...(pricing ? { inputs: { pricing } } : {})
    }
  } as unknown as UsageRow
}

function render(u: UsageRow, locale: 'zh' | 'en' = 'zh') {
  i18n.global.locale.value = locale
  const w = mount(UsageBillingFacts, { props: { u }, global: { plugins: [i18n], stubs: { ExprHistoryModal: true } } })
  const text = (id: string) => w.find(`[data-testid="${id}"]`).text().replace(/\s+/g, ' ')
  return { w, text, has: (id: string) => w.find(`[data-testid="${id}"]`).exists() }
}

describe('group pricing of a usage record', () => {
  it('reads as before at the global price and the default rate of the group', () => {
    const { w, has } = render(record({ rate: '1.5', total: '0.00864' }))
    expect(has('usage-list-price')).toBe(false)
    expect(has('usage-list-cost')).toBe(false)
    expect(has('rate-source')).toBe(false)
    expect(w.text().replace(/\s+/g, ' ')).toContain('基础费用 $0.00576 × 条件总倍率 1 × 分组倍率 1.5 = $0.00864')
  })

  it('shows the multiplier rule that gave the list price, the list cost and where the rate comes from', () => {
    const pricing: UsagePricing = { segment_id: 2, rate_source: 'discount', price_source: 'segment', scale: '0.8' }
    const { w, text } = render(record({ rate: '0.9', total: '0.0041472', list_cost: '0.004608', pricing }))
    expect(text('usage-list-price')).toMatch(/^专属价 ?全局 ×0\.8$/)
    expect(text('usage-list-cost')).toBe('$0.004608')
    expect(text('rate-source')).toBe('(分组默认 × 全场折扣)')
    expect(w.text().replace(/\s+/g, ' ')).toContain('× 条件总倍率 1 × 标价倍数 0.8 × 分组倍率 0.9 (分组默认 × 全场折扣) = $0.004147')
  })

  it('names a custom price, which is no multiplier of the base cost', () => {
    const pricing: UsagePricing = { segment_id: 2, rate_source: 'segment', price_source: 'group', base: { expression: 'tier("base", p*1 + c*5)', expr_hash: 'abc' } }
    const { w, text } = render(record({ rate: '0.7', total: '0.004032', list_cost: '0.00576', pricing }))
    expect(text('usage-list-price')).toMatch(/^分组内定价 ?自定义价格$/)
    expect(text('usage-list-cost')).toBe('$0.00576')
    expect(text('rate-source')).toBe('(专属倍率)')
    expect(w.text()).not.toContain('标价倍数')
  })

  it('says only where the rate comes from when no rule gave the list price', () => {
    for (const price_source of [undefined, 'global'] as const) {
      const { has, text } = render(record({ rate: '0.7', total: '0.004032', pricing: { segment_id: 2, rate_source: 'segment', price_source } }), 'en')
      expect(has('usage-list-price')).toBe(false)
      expect(has('usage-list-cost')).toBe(false)
      expect(text('rate-source')).toBe('(Own rate)')
    }
  })

  it('names no source of the rate when the record has none', () => {
    const { has, text } = render(record({ rate: '1', total: '0.006912', list_cost: '0.006912', pricing: { price_source: 'group', scale: '1.2' } }), 'en')
    expect(has('rate-source')).toBe(false)
    expect(text('usage-list-price')).toMatch(/^Group pricing ?Global ×1\.2$/)
    expect(text('usage-list-cost')).toBe('$0.006912')
  })
})

// A Messages request that declared an advisor model, priced at the advisor model's own price.
describe('separately priced items of a usage record', () => {
  const tokens = (Input: number, Output: number) => ({ Input, Output, CacheRead: 0, CacheCreation: 0, CacheCreation1h: 0 })
  const advisorDetail = {
    breakdown: { items: [{ label: 'p', quantity: 3000, rate: 15, expr: 'p*15', cost: '0.045' }, { label: 'c', quantity: 400, rate: 75, expr: 'c*75', cost: '0.03' }],
      subtotal: '0.075', base: '0.075', rules_multiplier: '1' },
    rate_multiplier: '1', total_cost: '0.075'
  }
  function withItems(list: 'additional' | 'replacement', parts: Array<Record<string, unknown>>, inputs: Array<Record<string, unknown>>): UsageRow {
    const u = record({ rate: '1', total: '0.096' })
    u.billing_detail = { ...u.billing_detail, [list]: parts, inputs: { [list]: inputs } }
    return u
  }
  const advisor = () => withItems('additional',
    [{ kind: 'advisor', model: 'claude-opus-4-1', price_id: 5, detail: advisorDetail }],
    [{ Kind: 'advisor', Model: 'claude-opus-4-1', Tokens: tokens(3000, 400) }])
  /** The value of a token row of an item, by its label. */
  function tokenValue(item: { text(): string }, label: string) {
    return item.text().replace(/\s+/g, ' ').match(new RegExp(`${label} ?([\\d,]+)`))?.[1]
  }

  it('lists each additional item with its kind, model, tokens, own formula and amount', () => {
    const { w, has } = render(advisor())
    expect(has('usage-additional')).toBe(true)
    expect(has('usage-replacement')).toBe(false)
    const items = w.findAll('[data-testid="usage-additional-item"]')
    expect(items).toHaveLength(1)
    const item = items[0]
    expect(item.find('.ml-auto').text()).toBe('$0.075')
    expect(item.text()).toContain('顾问模型')
    expect(item.text()).toContain('claude-opus-4-1')
    expect(tokenValue(item, '输入')).toBe('3,000')
    expect(tokenValue(item, '输出')).toBe('400')
    const formula = item.find('[data-testid="usage-component-breakdown"]').text().replace(/\s+/g, ' ')
    expect(formula).toContain('3,000 × $15/M = $0.045')
    expect(formula).toContain('400 × $75/M = $0.03')
    expect(formula).toContain('基础费用 $0.075 × 条件总倍率 1 × 分组倍率 1 = $0.075')
  })

  it('keeps each item\'s tokens and amount together, a free one first and a priced one after', () => {
    const u = withItems('additional',
      [{ kind: 'advisor', model: 'claude-haiku-4-5', price_id: 2, free: true, billing_reason: 'cached_answer', detail: { total_cost: '0', rate_multiplier: '0', breakdown: { subtotal: '0', base: '0', rules_multiplier: '0' } } },
        { kind: 'advisor', model: 'claude-opus-4-1', price_id: 5, detail: advisorDetail }],
      [{ Kind: 'advisor', Model: 'claude-haiku-4-5', Tokens: tokens(120, 30) }, { Kind: 'advisor', Model: 'claude-opus-4-1', Tokens: tokens(3000, 400) }])
    const { w } = render(u)
    const [free, priced] = w.findAll('[data-testid="usage-additional-item"]')
    expect(free.text()).toContain('claude-haiku-4-5')
    expect(free.find('[data-testid="usage-component-free"]').text()).toBe('免费')
    expect(free.find('[data-testid="usage-component-reason"]').text()).toBe('cached_answer')
    expect(free.find('.ml-auto').text()).toBe('$0.00')
    expect(free.find('[data-testid="usage-component-breakdown"]').exists()).toBe(false)
    expect(tokenValue(free, '输入')).toBe('120')
    expect(tokenValue(free, '输出')).toBe('30')
    expect(priced.text()).toContain('claude-opus-4-1')
    expect(priced.find('[data-testid="usage-component-free"]').exists()).toBe(false)
    expect(priced.find('.ml-auto').text()).toBe('$0.075')
    expect(tokenValue(priced, '输入')).toBe('3,000')
    expect(tokenValue(priced, '输出')).toBe('400')
  })

  it('names replacement items by their attempt round, not by their kind', () => {
    const u = withItems('replacement',
      [{ kind: 'fallback', model: 'claude-sonnet-4-5', price_id: 1, billing_reason: 'completed_attempt', detail: { ...advisorDetail, total_cost: '0.01' } },
        { kind: 'fallback', model: 'claude-haiku-4-5', price_id: 2, billing_reason: 'completed_attempt', detail: { ...advisorDetail, total_cost: '0.02' } }],
      [{ Kind: 'fallback', Model: 'claude-sonnet-4-5', Tokens: tokens(1000, 200) }, { Kind: 'fallback', Model: 'claude-haiku-4-5', Tokens: tokens(500, 100) }])
    const { w, has, text } = render(u, 'en')
    expect(has('usage-additional')).toBe(false)
    expect(text('usage-replacement')).toContain('each attempt round priced on its own')
    const [first, second] = w.findAll('[data-testid="usage-replacement-item"]')
    expect(first.text()).toContain('Round 1')
    expect(first.text()).toContain('claude-sonnet-4-5')
    expect(first.find('.ml-auto').text()).toBe('$0.01')
    expect(second.text()).toContain('Round 2')
    expect(second.find('.ml-auto').text()).toBe('$0.02')
    expect(w.text()).not.toContain('fallback')
    expect(w.text()).not.toContain('completed_attempt')
  })

  it('shows nothing for a record without separately priced items, its formula ending in the record\'s amount', () => {
    const { has, text } = render(record({ rate: '1', total: '0.00576' }))
    expect(has('usage-additional')).toBe(false)
    expect(has('usage-replacement')).toBe(false)
    expect(has('usage-components-total')).toBe(false)
    expect(text('usage-primary-breakdown')).toContain('基础费用 $0.00576 × 条件总倍率 1 × 分组倍率 1 = $0.00576')
  })
})

// The top of billing_detail sums the primary usage and every item; primary_total_cost is the primary usage's own amount.
describe('primary usage and total of a record with items', () => {
  const advisorPart = { kind: 'advisor', model: 'claude-opus-4-1', price_id: 5, detail: { breakdown: { items: [], subtotal: '0.075', base: '0.075', rules_multiplier: '1' }, rate_multiplier: '1', total_cost: '0.075' } }
  function withAdvisor(primary?: string): UsageRow {
    const u = record({ rate: '1', total: '0.08076' })
    u.billing_detail = { ...u.billing_detail, ...(primary ? { primary_cost: primary, primary_total_cost: primary } : {}), additional: [advisorPart] }
    return u
  }

  it('ends the primary formula in its own amount and adds the items up to the total', () => {
    const { text } = render(withAdvisor('0.00576'))
    expect(text('usage-primary-breakdown')).toContain('基础费用 $0.00576 × 条件总倍率 1 × 分组倍率 1 = $0.00576')
    expect(text('usage-components-total')).toBe('主用量 $0.00576 + 顾问模型 $0.075 = 合计 $0.08076')
  })

  it('shows no primary formula when the rounds replaced the primary usage', () => {
    const u = record({ rate: '1', total: '0.03' })
    const round = (model: string, total: string) => ({ kind: 'fallback', model, price_id: 1, detail: { ...advisorPart.detail, total_cost: total } })
    u.billing_detail = { cost: '0.03', total_cost: '0.03', rate_multiplier: '1', breakdown: { vars: null, items: null, subtotal: '0', base: '0', rules_multiplier: '0' },
      replacement: [round('claude-sonnet-4-5', '0.01'), round('claude-haiku-4-5', '0.02')] }
    const { has, text, w } = render(u, 'en')
    expect(has('usage-primary-breakdown')).toBe(false)
    expect(w.text()).not.toContain('× combined rule multiplier 0')
    expect(text('usage-components-total')).toBe('Round 1 $0.01 + Round 2 $0.02 = Total $0.03')
  })

  it('leaves out the primary section of a replaced record without a ledger entry', () => {
    const u = record({ rate: '1', total: '0.03' })
    u.billing_detail = { cost: '0.03', total_cost: '0.03', rate_multiplier: '1',
      replacement: [{ kind: 'fallback', model: 'claude-sonnet-4-5', price_id: 1, detail: { ...advisorPart.detail, total_cost: '0.03' } }] }
    const { has } = render(u)
    expect(has('usage-primary')).toBe(false)
    expect(has('usage-replacement')).toBe(true)
  })

  it('leaves the primary amount out of a record settled before it was kept', () => {
    const { text } = render(withAdvisor())
    const primary = text('usage-primary-breakdown')
    expect(primary).toContain('基础费用 $0.00576 × 条件总倍率 1 × 分组倍率 1')
    expect(primary).not.toContain('=')
    expect(text('usage-components-total')).toBe('主用量 — + 顾问模型 $0.075 = 合计 $0.08076')
  })
})
