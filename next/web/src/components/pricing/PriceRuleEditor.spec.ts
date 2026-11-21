import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { PriceDef, PriceRule } from '@/api/pricing'
import { singleTier, type VisualConfig } from '@/views/prices/priceExpr'
import PriceRuleEditor from './PriceRuleEditor.vue'

i18n.global.locale.value = 'en'
enableAutoUnmount(afterEach)

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: '' }
const tieredConfig: VisualConfig = { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }
const tiered: PriceDef = { mode: 'visual', config: tieredConfig, expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'p * 3 + c * 15' }

function render(modelValue: PriceRule | null, base: PriceDef | null = perToken, rate = '1.2') {
  const w = mount(PriceRuleEditor, { props: { modelValue, base, rate }, global: { plugins: [i18n], stubs: { Teleport: true } } })
  const emitted = () => w.emitted('update:modelValue')?.at(-1)?.[0] as PriceRule | null | undefined
  return { w, emitted, kind: (value: string) => w.get(`[data-testid="price-rule-kind"] input[type="radio"][value="${value}"]`) }
}
const preview = (w: ReturnType<typeof render>['w']) => w.get('[data-testid="price-rule-preview"]').text()

describe('price rule editor', () => {
  it('offers the original price, the original × N and a price of its own, and says what is paid', () => {
    const { w, kind } = render(null)
    expect(w.get('[data-testid="price-rule-kind"]').text()).toBe('Original price Original × N Custom price')
    expect((kind('none').element as HTMLInputElement).checked).toBe(true)
    expect(w.find('[data-testid="price-rule-multiplier"]').exists()).toBe(false)
    expect(preview(w)).toBe('Model price $3 / $15; at rate ×1.2 you pay $3.6 / $18')
  })

  it('starts N at 1 and shows the price it comes to and what is paid at the rate', async () => {
    const { w, emitted, kind } = render(null)
    await kind('multiplier').setValue(true)
    expect(emitted()).toEqual({ kind: 'multiplier', multiplier: '1' })
    await w.setProps({ modelValue: { kind: 'multiplier', multiplier: '1.5' } })
    expect(preview(w)).toBe('Model price $4.5 / $22.5; at rate ×1.2 you pay $5.4 / $27')
    await w.get('input[data-testid="price-rule-multiplier"]').setValue('2')
    expect(emitted()).toEqual({ kind: 'multiplier', multiplier: '2' })
  })

  it('keeps N as typed while the rule reads the same number', async () => {
    const { w } = render({ kind: 'multiplier', multiplier: '1' })
    const input = () => w.get<HTMLInputElement>('input[data-testid="price-rule-multiplier"]')
    await input().setValue('1.0')
    // The table answers with the saved rule, which prices alike.
    await w.setProps({ modelValue: { kind: 'multiplier', multiplier: '1' } })
    expect(input().element.value).toBe('1.0')
  })

  it('says why N is not one', () => {
    const { w } = render({ kind: 'multiplier', multiplier: 'x' })
    expect(w.get('input[data-testid="price-rule-multiplier"]').attributes('aria-invalid')).toBe('true')
    expect(w.get('[data-testid="price-rule-issue"]').text()).toBe('Enter a number from 0 to below 1000000, up to 4 decimals')
    expect(w.find('[data-testid="price-rule-preview"]').exists()).toBe(false)
  })

  it('starts a price of its own from the original price, tiers and all, in the price editor', async () => {
    const { w, emitted, kind } = render(null, tiered)
    await kind('custom').setValue(true)
    expect(emitted()).toMatchObject({ kind: 'custom', mode: 'visual', config: { tiers: [{ name: 'a', p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] } })
    await w.setProps({ modelValue: emitted() as PriceRule })
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(true)
    // The list shows the fallback tier.
    expect(preview(w)).toBe('Model price $6 / $22.5; at rate ×1.2 you pay $7.2 / $27')
  })

  it('sends every edit in the price editor as a whole rule', async () => {
    const { w, emitted, kind } = render(null, expression)
    await kind('custom').setValue(true)
    expect(emitted()).toEqual({ kind: 'custom', mode: 'expression', expression: 'p * 3 + c * 15' })
    await w.setProps({ modelValue: emitted() as PriceRule })
    await w.get('[data-testid="expr-code"] textarea').setValue('p * 2')
    await flushPromises()
    expect(emitted()).toEqual({ kind: 'custom', mode: 'expression', expression: 'p * 2' })
    await w.setProps({ modelValue: emitted() as PriceRule })
    expect(preview(w)).toBe('Model price Billed by expression; at rate ×1.2 you pay Billed by expression ×1.2')
  })

  it('closes the price editor when the rule is cleared from outside', async () => {
    const { w } = render({ kind: 'custom', mode: 'expression', expression: 'p * 2' })
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(true)
    await w.setProps({ modelValue: null })
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(false)
  })

  it('says why a price of its own cannot be saved', () => {
    const { w } = render({ kind: 'custom', mode: 'expression', expression: ' ' })
    expect(w.get('[data-testid="price-rule-issue"]').text()).toBe(i18n.global.t('prices.local.emptyExpr'))
  })

  it('goes back to the original price', async () => {
    const { emitted, kind } = render({ kind: 'multiplier', multiplier: '2' })
    await kind('none').setValue(true)
    expect(emitted()).toBeNull()
  })

  it('takes no rule for a model without an original price', () => {
    const { w, kind } = render(null, null)
    expect(kind('multiplier').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="price-rule-no-base"]').exists()).toBe(true)
    expect(w.find('[data-testid="price-rule-preview"]').exists()).toBe(false)
  })
})
