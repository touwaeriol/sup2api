import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { SDropdown, SSelect } from '@sub2api/ui'
import { i18n } from '@/i18n'
import TokenPriceEditor from './TokenPriceEditor.vue'
import { tokenExpr, type FieldCond, type TokenConfig } from './priceExpr'
import { editableTokenConfig, setOp, typedValue } from './tokenPrice'

enableAutoUnmount(afterEach)

function render(start: TokenConfig) {
  i18n.global.locale.value = 'zh'
  const model = ref(start)
  const w = mount(TokenPriceEditor, {
    props: { modelValue: model.value, 'onUpdate:modelValue': (v: TokenConfig) => (model.value = v) },
    global: { plugins: [i18n] }
  })
  const tiers = () => w.findAll('[data-tier]')
  return { w, model, tiers }
}

const claude = (): TokenConfig =>
  editableTokenConfig({ tiers: [{ name: 'base', max_len: null, p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 }], rules: [] })

describe('the per-token editor', () => {
  it('shows a single tier for every request, its prices in one row', () => {
    const { tiers } = render(claude())
    expect(tiers()).toHaveLength(1)
    expect(tiers()[0].text()).toContain('所有请求')
    expect(
      tiers()[0]
        .findAll('input[type="number"][data-testid^="tier-"]')
        .map(i => i.attributes('data-testid'))
    ).toEqual(['p', 'c', 'cr', 'cc', 'cc1h'].map(k => `tier-${k}`))
  })

  it('adds a bounded tier before the rest and takes one away again', async () => {
    const { w, model, tiers } = render(claude())
    await w.get('[data-testid="token-add-tier"]').trigger('click')
    expect(model.value.tiers.map(t => [t.name, t.max_len])).toEqual([
      ['tier_2', 200000],
      ['base', null]
    ])
    expect(tiers()[0].text()).toContain('len ≤')
    expect(tiers()[1].text()).toContain('len > 200,000')
    await tiers()[0].get('button[aria-label="删除档位"]').trigger('click')
    expect(model.value.tiers.map(t => [t.name, t.max_len])).toEqual([['base', null]])
  })

  it('splits cache writes by keeping cc1h, and prices images apart by keeping img and img_o', async () => {
    const { w, model } = render(claude())
    await w.get('[data-testid="tier-cache"] input[value="false"]').setValue(true)
    expect('cc1h' in model.value.tiers[0]).toBe(false)
    expect(w.find('[data-testid="tier-cc1h"]').exists()).toBe(false)
    await w.get('input[data-testid="tier-images"]').setValue(true)
    expect(model.value.tiers[0]).toMatchObject({ img: null, img_o: null })
    expect(w.get('[data-testid="tier-img"]').attributes('placeholder')).toBe('按输入价')
    await w.get('input[data-testid="tier-images"]').setValue(false)
    expect('img' in model.value.tiers[0] || 'img_o' in model.value.tiers[0]).toBe(false)
  })

  it('prices a tier per request', async () => {
    const { w, model } = render(claude())
    await w.getComponent(SSelect).vm.$emit('update:modelValue', 'request')
    await nextTick()
    expect(w.find('[data-testid="tier-p"]').exists()).toBe(false)
    await w.get('[data-tier="0"] input[type="number"]:not([aria-label])').setValue('0.05')
    expect(tokenExpr(model.value)).toBe('tier("base", flat(0.05))')
  })

  it('starts from a template', async () => {
    const { w, model } = render(claude())
    w.getComponent(SDropdown).vm.$emit('select', 'three')
    await nextTick()
    expect(model.value.tiers.map(t => t.max_len)).toEqual([32000, 128000, null])
  })

  it('adds a rule of AND conditions', async () => {
    const { w, model } = render(claude())
    expect(w.text()).toContain('没有加价规则')
    await w.get('[data-testid="token-add-rule"]').trigger('click')
    expect(model.value.rules).toEqual([{ name: '规则 1', multiplier: 2, conds: [{ source: 'param', key: '', op: 'eq', value: '' }] }])
    await w.get('[data-rule="0"]').findAll('button').at(-1)!.trigger('click')
    expect(
      w
        .get('[data-rule="0"]')
        .findAll('[data-cond]')
        .map(c => c.text().slice(0, 1))
    ).toEqual(['当', '且'])
  })
})

describe('a condition value', () => {
  const cond = (source: FieldCond['source'], op: FieldCond['op'], value?: FieldCond['value']): FieldCond => ({
    source,
    key: 'k',
    op,
    value
  })
  it('is typed as it reads: numbers and booleans for parameters and usage fields, strings for headers and contains', () => {
    expect(typedValue(cond('param', 'eq'), '3')).toBe(3)
    expect(typedValue(cond('usage', 'ne'), 'true')).toBe(true)
    expect(typedValue(cond('param', 'eq'), 'priority')).toBe('priority')
    expect(typedValue(cond('usage', 'gt'), '1.5')).toBe(1.5)
    expect(typedValue(cond('header', 'eq'), '3')).toBe('3')
    expect(typedValue(cond('param', 'contains'), '3')).toBe('3')
  })
  it('is retyped when the operator changes, and dropped for exists', () => {
    const c = cond('param', 'contains', '3')
    setOp(c, 'eq')
    expect(c.value).toBe(3)
    setOp(c, 'exists')
    expect('value' in c).toBe(false)
    setOp(c, 'eq')
    expect(c.value).toBe('')
  })
})
