import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { PricingQuote } from '@/api/pricing'
import PriceQuoteBreakdown from './PriceQuoteBreakdown.vue'
import { singleTier } from '@/views/prices/priceExpr'

const price = { mode: 'per_token' as const, config: singleTier({ p: 3, c: 15 }), expression: '' }
const quote: PricingQuote = {
  segment: { id: 2, kind: 'named', name: 'VIP' },
  accessible: true,
  access_source: 'public',
  base: price,
  list_price: price,
  price_source: 'global',
  price_levels: { segment: null, group: null },
  rate: { value: '0.9', source: 'discount', group_default: '1', discount: '0.9' },
  rate_override: null,
  list_cost: '4.50000000',
  cost: '4.05000000'
}
const render = (q: Partial<PricingQuote> = {}) => {
  i18n.global.locale.value = 'zh'
  return mount(PriceQuoteBreakdown, { props: { quote: { ...quote, ...q }, groupName: 'default' }, global: { plugins: [i18n] } })
}

describe('price quote breakdown', () => {
  it('marks the level of each chain that applies and sums up what is paid', () => {
    const w = render()
    const applied = (chain: string) => w.get(`[data-testid="price-quote-${chain}"] [data-state="applies"]`).attributes('data-level')
    expect([applied('price'), applied('rate')]).toEqual(['global', 'discount'])
    expect(w.get('[data-testid="price-quote-cost"]').text()).toBe('$4.05')
  })

  it('shows the price a multiplier comes to, and says of a discount of 1 that there is none', () => {
    const w = render({ price_source: 'group', price_levels: { segment: null, group: { kind: 'multiplier', multiplier: '0.9' } }, rate: { value: '1', source: 'group', group_default: '1', discount: '1' } })
    const level = (chain: string, key: string) => w.get(`[data-testid="price-quote-${chain}"] [data-level="${key}"]`)
    expect(level('price', 'group').text()).toContain('全局 ×0.9 = $2.7 / $13.5')
    expect(level('rate', 'discount').text()).toContain('没有全场折扣')
    expect(level('rate', 'discount').attributes('data-state')).toBe('unset')
    expect(level('rate', 'segment').text()).toContain('未设置')
  })

  it('marks a rule that is not in effect as the model price table does', () => {
    const stale = { kind: 'custom', mode: 'per_request', config: { price: 0.002 }, inactive: true } as const
    const w = render({ price_levels: { segment: null, group: stale } })
    const group = w.get('[data-testid="price-quote-price"] [data-level="group"]')
    expect(group.attributes('data-state')).toBe('shadowed')
    expect(group.get('[data-testid="price-quote-inactive"]').text()).toBe('规则未生效')
    expect(group.get('[data-testid="price-quote-inactive"]').attributes('title')).toContain('计费方式已变')
    expect(w.findAll('[data-testid="price-quote-inactive"]')).toHaveLength(1)
  })

  it('shows a multiplier over an expression it cannot break down as the rule alone, the list price with the multiplier beside it', () => {
    const opaque = { mode: 'expression' as const, config: null, expression: 'p * 2 + unknown(1)' }
    const w = render({ base: opaque, list_price: { ...opaque, multiplier: '0.9' }, price_source: 'group', price_levels: { segment: null, group: { kind: 'multiplier', multiplier: '0.9' } } })
    expect(w.get('[data-testid="price-quote-price"] [data-level="group"]').text()).toContain('全局 ×0.9')
    expect(w.get('[data-testid="price-quote-price"] [data-level="group"]').text()).not.toContain('=')
    expect(w.get('[data-testid="price-quote-price"] [data-level="global"]').text()).toContain('表达式计费')
  })

  it('notes under a tiered list price that the first tier is shown', () => {
    const tiered = { mode: 'per_token' as const, config: { tiers: [{ name: 'a', max_len: 200000, p: 3, c: 15 }, { name: 'b', max_len: null, p: 6, c: 22.5 }], rules: [] }, expression: '' }
    expect(render({ base: tiered, list_price: tiered }).get('[data-testid="price-quote-price-note"]').text()).toContain('分段计价')
    expect(render().find('[data-testid="price-quote-price-note"]').exists()).toBe(false)
  })

  it('says next to the access of a segment without the grant that the price is the one once granted', () => {
    const w = render({ accessible: false, access_source: 'none' })
    expect(w.get('[data-testid="price-quote-denied"]').text()).toBe('用不了这个私有分组，下面是授权后的价格。')
    expect(w.get('[data-testid="price-quote-paid"]').text()).toContain('授权后的价格')
  })

  it('says that a model without a global price cannot be billed, in place of a sum of zero', () => {
    const w = render({ base: null, list_price: null, list_cost: '0', cost: '0' })
    expect(w.get('[data-testid="price-quote-unpriced"]').text()).toContain('这个模型还没有全局价，无法计费')
    expect(w.find('[data-testid="price-quote-paid"]').exists()).toBe(false)
  })
})
