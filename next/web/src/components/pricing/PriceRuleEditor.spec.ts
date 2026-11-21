import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { PriceDef, PriceRule } from '@/api/pricing'
import { singleTier } from '@/views/prices/priceExpr'
import PriceRuleEditor from './PriceRuleEditor.vue'

i18n.global.locale.value = 'en'

const perToken: PriceDef = { mode: 'per_token', config: singleTier({ p: 3, c: 15, cr: 0.3, cc: 3.75 }), expression: '' }
const tiered: PriceDef = { mode: 'per_token', config: { tiers: [{ name: 'a', max_len: 200000, p: 3, c: 15 }, { name: 'b', max_len: null, p: 6, c: 22.5 }], rules: [] }, expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'len <= 200000 ? tier("a", p*3 + c*15) : tier("b", p*6 + c*22.5)' }

function render(modelValue: PriceRule | null, base: PriceDef | null = perToken) {
  const w = mount(PriceRuleEditor, { props: { modelValue, base, inheritLabel: 'Follows global' }, global: { plugins: [i18n] } })
  const emitted = () => w.emitted('update:modelValue')?.at(-1)?.[0]
  return { w, emitted, kind: (value: string) => w.get(`input[type="radio"][value="${value}"]`) }
}

describe('price rule editor', () => {
  it('offers the three ways to price, the inherited one under its given name', () => {
    const { w, kind } = render(null)
    expect(w.get('[data-testid="price-rule-kind"]').text()).toContain('Follows global')
    expect((kind('inherit').element as HTMLInputElement).checked).toBe(true)
    expect(w.find('[data-testid="price-rule-multiplier"]').exists()).toBe(false)
  })
  it('starts a multiplier at ×1 and shows the price after it', async () => {
    const { w, emitted, kind } = render(null)
    await kind('multiplier').setValue(true)
    expect(emitted()).toEqual({ kind: 'multiplier', multiplier: '1' })
    await w.setProps({ modelValue: { kind: 'multiplier', multiplier: '1.5' } })
    expect(w.get('[data-testid="price-rule-preview"]').text()).toContain('$4.5 / $22.5')
    await w.get('[data-testid="price-rule-multiplier"]').setValue('2')
    expect(emitted()).toEqual({ kind: 'multiplier', multiplier: '2' })
  })
  it('says so when the multiplier is not a rate', async () => {
    const { w } = render({ kind: 'multiplier', multiplier: 'x' })
    expect(w.get('[data-testid="price-rule-multiplier"]').attributes('aria-invalid')).toBe('true')
    expect(w.get('[data-testid="price-rule-preview"]').text()).toBe('Enter a number from 0 to below 1000000, up to 4 decimals')
  })
  it('starts a custom price from the global one and edits one price at a time', async () => {
    const { w, emitted, kind } = render(null)
    await kind('custom').setValue(true)
    const start: PriceRule = { kind: 'custom', mode: 'per_token', config: singleTier({ p: 3, c: 15, cr: 0.3, cc: 3.75 }) }
    expect(emitted()).toEqual(start)
    await w.setProps({ modelValue: start })
    await w.get('[data-testid="price-rule-c"]').setValue('8')
    expect(emitted()).toEqual({ ...start, config: singleTier({ p: 3, c: 8, cr: 0.3, cc: 3.75 }) })
    await w.get('[data-testid="price-rule-cr"]').setValue('')
    expect(emitted()).toEqual({ ...start, config: singleTier({ p: 3, c: 15, cc: 3.75 }) })
  })
  it('goes back to inheriting', async () => {
    const { emitted, kind } = render({ kind: 'multiplier', multiplier: '2' })
    await kind('inherit').setValue(true)
    expect(emitted()).toBeNull()
  })
  it('lets an expression price, or a per-token price with tiers, take a multiplier only', () => {
    for (const base of [expression, tiered]) {
      const { w, kind } = render(null, base)
      expect(kind('custom').attributes('disabled')).toBeDefined()
      expect(kind('multiplier').attributes('disabled')).toBeUndefined()
      expect(w.find('[data-testid="price-rule-multiplier-only"]').exists()).toBe(true)
    }
    expect(render(null).w.find('[data-testid="price-rule-multiplier-only"]').exists()).toBe(false)
  })
  it('takes no rule for a model without a global price', () => {
    const { w, kind } = render(null, null)
    expect(kind('multiplier').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="price-rule-no-base"]').exists()).toBe(true)
  })
})
