import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { CustomPrice, PriceDef } from '@/api/pricing'
import { singleTier, type VisualConfig } from '@/views/prices/priceExpr'
import ModelPriceEditor from './ModelPriceEditor.vue'

i18n.global.locale.value = 'en'
enableAutoUnmount(afterEach)

const perToken: PriceDef = { mode: 'visual', config: singleTier({ p: 3, c: 15 }), expression: '' }
const tieredConfig: VisualConfig = { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] }
const tiered: PriceDef = { mode: 'visual', config: tieredConfig, expression: '' }
const expression: PriceDef = { mode: 'expression', config: null, expression: 'p * 3 + c * 15' }

function render(modelValue: CustomPrice | null, base: PriceDef | null = perToken, rate = '1.2') {
  const w = mount(ModelPriceEditor, { props: { modelValue, base, rate }, global: { plugins: [i18n], stubs: { Teleport: true } } })
  const emitted = () => w.emitted('update:modelValue')?.at(-1)?.[0] as CustomPrice | null | undefined
  return { w, emitted, kind: (value: string) => w.get(`[data-testid="model-price-editor-kind"] input[type="radio"][value="${value}"]`) }
}
const preview = (w: ReturnType<typeof render>['w']) => w.get('[data-testid="model-price-editor-preview"]').text()

describe('price rule editor', () => {
  it('offers to follow the original price or a price of its own, and says what is paid', () => {
    const { w, kind } = render(null)
    expect(w.get('[data-testid="model-price-editor-kind"]').text()).toBe('Follows the original price Custom price')
    expect((kind('none').element as HTMLInputElement).checked).toBe(true)
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(false)
    expect(preview(w)).toBe('Model price $3 / $15; at rate ×1.2 you pay $3.6 / $18')
  })

  it('starts a price of its own from the original price, tiers and all, in the price editor', async () => {
    const { w, emitted, kind } = render(null, tiered)
    await kind('custom').setValue(true)
    expect(emitted()).toMatchObject({ mode: 'visual', config: { tiers: [{ name: 'a', p: 3, c: 15 }, { name: 'b', p: 6, c: 22.5 }], rules: [] } })
    await w.setProps({ modelValue: emitted() as CustomPrice })
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(true)
    // The list shows the fallback tier.
    expect(preview(w)).toBe('Model price $6 / $22.5; at rate ×1.2 you pay $7.2 / $27')
  })

  it('sends every edit in the price editor as a whole rule', async () => {
    const { w, emitted, kind } = render(null, expression)
    await kind('custom').setValue(true)
    expect(emitted()).toEqual({ mode: 'expression', expression: 'p * 3 + c * 15' })
    await w.setProps({ modelValue: emitted() as CustomPrice })
    await w.get('[data-testid="expr-code"] textarea').setValue('p * 2')
    await flushPromises()
    expect(emitted()).toEqual({ mode: 'expression', expression: 'p * 2' })
    await w.setProps({ modelValue: emitted() as CustomPrice })
    expect(preview(w)).toBe('Model price Billed by expression; at rate ×1.2 you pay Billed by expression ×1.2')
  })

  it('closes the price editor when the rule is cleared from outside', async () => {
    const { w } = render({ mode: 'expression', expression: 'p * 2' })
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(true)
    await w.setProps({ modelValue: null })
    expect(w.find('[data-testid="price-def-editor"]').exists()).toBe(false)
  })

  it('says why a price of its own cannot be saved', () => {
    const { w } = render({ mode: 'expression', expression: ' ' })
    expect(w.get('[data-testid="model-price-editor-issue"]').text()).toBe(i18n.global.t('prices.local.emptyExpr'))
  })

  it('goes back to the original price', async () => {
    const { emitted, kind } = render({ mode: 'expression', expression: 'p * 2' })
    await kind('none').setValue(true)
    expect(emitted()).toBeNull()
  })

  it('starts a price of its own blank for a model without an original price', async () => {
    const { w, emitted, kind } = render(null, null)
    expect(w.get('[data-testid="model-price-editor-kind"]').text()).toBe('Not priced Custom price')
    expect(w.get('[data-testid="model-price-editor-no-base"]').text()).toBe(i18n.global.t('pricing.editor.noBase'))
    expect(w.find('[data-testid="model-price-editor-preview"]').exists()).toBe(false)
    await kind('custom').setValue(true)
    expect(emitted()).toMatchObject({ mode: 'visual', config: { tiers: [{ name: 'base', unit: 'token' }], rules: [] } })
    await w.setProps({ modelValue: emitted() as CustomPrice })
    // Nothing is copied: the price fields wait to be typed, and blank is not 0.
    const fields = () => w.findAll('[data-testid="price-def-editor"] input[type="number"]')
    expect(fields().map((f) => (f.element as HTMLInputElement).value).filter(Boolean)).toEqual([])
    expect(emitted()).toMatchObject({ config: { tiers: [{ p: null, c: null }] } })
    expect(w.get('[data-testid="model-price-editor-issue"]').text()).toBe('Tier "base" has a price not filled in: enter a price (0 for free), or untick it')
    expect(w.find('[data-testid="model-price-editor-preview"]').exists()).toBe(false)
    await fields()[0].setValue('3')
    await fields()[1].setValue('0')
    await flushPromises()
    await w.setProps({ modelValue: emitted() as CustomPrice })
    expect(w.find('[data-testid="model-price-editor-issue"]').exists()).toBe(false)
    expect(preview(w)).toBe('Model price $3 / $0; at rate ×1.2 you pay $3.6 / $0')
  })

  it('edits the saved price of a model without an original price, which stands alone', () => {
    const { w } = render({ mode: 'expression', expression: 'p * 2' }, null)
    expect(w.get('[data-testid="expr-code"] textarea').attributes('readonly')).toBeUndefined()
    expect(preview(w)).toBe('Model price Billed by expression; at rate ×1.2 you pay Billed by expression ×1.2')
  })
})
