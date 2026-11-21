import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { PricingQuote } from '@/api/pricing'
import PriceQuoteBreakdown from './PriceQuoteBreakdown.vue'
import { singleTier } from '@/views/prices/priceExpr'

const price = { mode: 'visual' as const, config: singleTier({ p: 3, c: 15 }), expression: '' }
const quote: PricingQuote = {
  segment: { id: 2, kind: 'named', name: 'VIP' },
  accessible: true,
  access_source: 'public',
  base: price,
  list_price: price,
  price_source: 'global',
  price_levels: { group: null },
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
    const w = render({ price_source: 'group', price_levels: { group: { kind: 'multiplier', multiplier: '0.9' } }, rate: { value: '1', source: 'group', group_default: '1', discount: '1' } })
    const level = (chain: string, key: string) => w.get(`[data-testid="price-quote-${chain}"] [data-level="${key}"]`)
    expect(level('price', 'group').text()).toContain('原价 × 0.9 = $2.7 / $13.5')
    expect(level('rate', 'discount').text()).toContain('没有折扣')
    expect(level('rate', 'discount').attributes('data-state')).toBe('unset')
    expect(level('rate', 'segment').text()).toContain('未设置')
  })

  it('shows a multiplier over an expression it cannot break down as the rule alone, the list price with the multiplier beside it', () => {
    const opaque = { mode: 'expression' as const, config: null, expression: 'p * 2 + unknown(1)' }
    const w = render({ base: opaque, list_price: { ...opaque, multiplier: '0.9' }, price_source: 'group', price_levels: { group: { kind: 'multiplier', multiplier: '0.9' } } })
    expect(w.get('[data-testid="price-quote-price"] [data-level="group"]').text()).toContain('原价 × 0.9')
    expect(w.get('[data-testid="price-quote-price"] [data-level="group"]').text()).not.toContain('=')
    expect(w.get('[data-testid="price-quote-price"] [data-level="global"]').text()).toContain('表达式计费')
  })

  it('notes under a tiered list price that the fallback tier is shown', () => {
    const tiered = { mode: 'visual' as const, config: { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }, expression: '' }
    expect(render({ base: tiered, list_price: tiered }).get('[data-testid="price-quote-price-note"]').text()).toContain('分段计价')
    expect(render().find('[data-testid="price-quote-price-note"]').exists()).toBe(false)
  })

  it('shows a custom group price with the price it is', () => {
    const w = render({ price_source: 'group', list_price: { mode: 'visual', config: singleTier({ p: 1, c: 8 }), expression: '' }, price_levels: { group: { kind: 'custom', mode: 'visual', config: singleTier({ p: 1, c: 8 }) } } })
    expect(w.get('[data-testid="price-quote-price"] [data-level="group"]').text()).toContain('自定义 = $1 / $8')
  })

  it('says next to the access of a segment without the grant that the price is the one once opened', () => {
    const w = render({ accessible: false, access_source: 'none' })
    expect(w.get('[data-testid="price-quote-access"]').text()).toContain('私有分组，未开通')
    expect(w.get('[data-testid="price-quote-denied"]').text()).toBe('用不了这个私有分组，下面是开通后的价格。')
    expect(w.get('[data-testid="price-quote-paid"]').text()).toContain('开通后的价格')
  })

  it('says that a disabled group cannot be used by anyone', () => {
    const w = render({ accessible: false, access_source: 'disabled' })
    expect(w.get('[data-testid="price-quote-access"]').text()).toContain('分组已停用')
    expect(w.get('[data-testid="price-quote-denied"]').text()).toContain('谁都用不了')
    expect(w.get('[data-testid="price-quote-paid"]').text()).toContain('启用后的价格')
  })

  it('says that a model without an original price cannot be billed, in place of a sum of zero', () => {
    const w = render({ base: null, list_price: null, list_cost: '0', cost: '0' })
    expect(w.get('[data-testid="price-quote-unpriced"]').text()).toContain('这个模型还没有原价，无法计费')
    expect(w.find('[data-testid="price-quote-paid"]').exists()).toBe(false)
  })
})
