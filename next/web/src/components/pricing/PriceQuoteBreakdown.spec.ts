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
  rate: { value: '0.9', source: 'segment', group_default: '1' },
  rate_levels: { segment_model: null, segment: '0.9', group_model: '1.2', group: '1' },
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
    expect([applied('price'), applied('rate')]).toEqual(['global', 'segment'])
    expect(w.get('[data-testid="price-quote-cost"]').text()).toBe('$4.05')
  })

  it('lists the four rates, the most specific first: the one that applies, those under it greyed, the unset ones said so', () => {
    const w = render({ rate: { value: '0.85', source: 'segment_model', group_default: '1' }, rate_levels: { segment_model: '0.85', segment: null, group_model: '1.2', group: '1' } })
    const levels = w.findAll('[data-testid="price-quote-rate"] [data-level]').map((l) => [l.attributes('data-level'), l.attributes('data-state')])
    expect(levels).toEqual([['segment_model', 'applies'], ['segment', 'unset'], ['group_model', 'shadowed'], ['group', 'shadowed']])
    const level = (key: string) => w.get(`[data-testid="price-quote-rate"] [data-level="${key}"]`).text()
    expect(level('segment_model')).toContain('用户分组的模型倍率（VIP）')
    expect(level('segment')).toContain('未设置')
    expect(level('group_model')).toContain('账号分组的模型倍率（default）')
    expect(level('group')).toContain('基本倍率（default）')
  })

  it('notes under a tiered list price that the fallback tier is shown', () => {
    const tiered = { mode: 'visual' as const, config: { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }, expression: '' }
    expect(render({ base: tiered, list_price: tiered }).get('[data-testid="price-quote-price-note"]').text()).toContain('分段计价')
    expect(render().find('[data-testid="price-quote-price-note"]').exists()).toBe(false)
  })

  it('shows a custom group price with the price it is', () => {
    const w = render({ price_source: 'group', list_price: { mode: 'visual', config: singleTier({ p: 1, c: 8 }), expression: '' }, price_levels: { group: { mode: 'visual', config: singleTier({ p: 1, c: 8 }) } } })
    expect(w.get('[data-testid="price-quote-price"] [data-level="group"]').text()).toContain('自定义 = $1 / $8')
  })

  it('says next to the access of a segment without the grant that the price is the one once opened', () => {
    const w = render({ accessible: false, access_source: 'none' })
    expect(w.get('[data-testid="price-quote-access"]').text()).toContain('私有账号分组，未开通')
    expect(w.get('[data-testid="price-quote-denied"]').text()).toBe('用不了这个私有账号分组，下面是开通后的价格。')
    expect(w.get('[data-testid="price-quote-paid"]').text()).toContain('开通后的价格')
  })

  it('says that a disabled group cannot be used by anyone', () => {
    const w = render({ accessible: false, access_source: 'disabled' })
    expect(w.get('[data-testid="price-quote-access"]').text()).toContain('账号分组已停用')
    expect(w.get('[data-testid="price-quote-denied"]').text()).toContain('谁都用不了')
    expect(w.get('[data-testid="price-quote-paid"]').text()).toContain('启用后的价格')
  })

  it('says that a model without an original price cannot be billed, in place of a sum of zero', () => {
    const w = render({ base: null, list_price: null, list_cost: '0', cost: '0' })
    expect(w.get('[data-testid="price-quote-unpriced"]').text()).toContain('这个模型还没有原价，无法计费')
    expect(w.find('[data-testid="price-quote-paid"]').exists()).toBe(false)
  })
})
