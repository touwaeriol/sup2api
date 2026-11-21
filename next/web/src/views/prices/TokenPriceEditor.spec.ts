import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { SDropdown, SSelect } from '@sub2api/ui'
import { i18n } from '@/i18n'
import TokenPriceEditor from './TokenPriceEditor.vue'
import PriceRuleGroups from './PriceRuleGroups.vue'
import PriceTierCard from './PriceTierCard.vue'
import { compactTokenConfig, tokenExpr, typedValue, valueText, type FieldCond, type MarkupRule, type TokenConfig } from './priceExpr'
import { addTier, editableTokenConfig, setOp } from './tokenPrice'

enableAutoUnmount(afterEach)

function render(start: TokenConfig, disabled = false) {
  i18n.global.locale.value = 'zh'
  const model = ref(start)
  const w = mount(TokenPriceEditor, {
    props: { modelValue: model.value, disabled, 'onUpdate:modelValue': (v: TokenConfig) => (model.value = v) },
    global: { plugins: [i18n] }
  })
  const tiers = () => w.findAll('[data-tier]')
  return { w, model, tiers }
}

const tierOf = (name: string, max_len: number) => ({ name, max_len, p: 1, c: 1 })
const claude = (): TokenConfig =>
  editableTokenConfig({ tiers: [{ name: 'base', max_len: null, p: 3, c: 15, cr: 0.3, cc: 3.75, cc1h: 6 }], rules: [] })

describe('the per-token editor', () => {
  it('shows a single tier for every request, its prices in one row', () => {
    const { tiers } = render(claude())
    expect(tiers()).toHaveLength(1)
    expect(tiers()[0].text()).toContain('始终匹配')
    expect(
      tiers()[0]
        .findAll('input[type="number"][data-testid^="tier-"]')
        .map(i => i.attributes('data-testid'))
    ).toEqual(['p', 'c', 'cr', 'cc', 'cc1h'].map(k => `tier-${k}`))
    expect(tiers()[0].text()).toContain('缓存写 1 小时: $6')
  })

  it('loads tiers in the order they match, the last one the rest, and a time window in full', () => {
    const c = editableTokenConfig({
      tiers: [
        { name: 'rest', max_len: null, p: 2, c: 2 },
        { name: 'short', max_len: 1000, p: 1, c: 1 }
      ],
      rules: [{ name: 'night', multiplier: 0.5, conds: [{ source: 'time' }, { source: 'param', key: 'n', op: 'eq', value: '5' }] }]
    })
    expect(c.tiers.map(t => [t.name, t.max_len, t.unit])).toEqual([
      ['short', 1000, 'token'],
      ['rest', null, 'token']
    ])
    expect(c.rules[0].conds).toEqual([
      { source: 'time', tz: 'UTC', from_hour: 0, to_hour: 24 },
      { source: 'param', key: 'n', op: 'eq', value: '"5"' }
    ])
    // A string that reads as a number stays a string through a save.
    expect(compactTokenConfig(c).rules[0].conds[1]).toMatchObject({ value: '5' })
    expect(
      editableTokenConfig({
        tiers: [
          { name: 'a', max_len: 10 },
          { name: 'b', max_len: 20 }
        ]
      }).tiers.map(t => t.max_len)
    ).toEqual([10, null])
  })

  it('names an added tier by the first tier_N not taken', () => {
    const tiers = editableTokenConfig({
      tiers: [
        { name: 'tier_2', max_len: 100 },
        { name: 'base', max_len: null }
      ]
    }).tiers
    addTier(tiers)
    expect(tiers.map(t => t.name)).toEqual(['tier_2', 'tier_3', 'base'])
  })

  it('adds a bounded tier before the rest and takes one away again', async () => {
    const { w, model, tiers } = render(claude())
    await w.get('[data-testid="token-add-tier"]').trigger('click')
    expect(model.value.tiers.map(t => [t.name, t.max_len])).toEqual([
      ['tier_2', 200000],
      ['base', null]
    ])
    expect(tiers()[0].text()).toContain('len ≤ 200,000')
    expect(tiers()[1].text()).toContain('兜底档位')
    expect(tiers()[1].text()).toContain('len > 200,000')
    w.getComponent(PriceTierCard).findComponent(SDropdown).vm.$emit('select', 'remove')
    await nextTick()
    expect(model.value.tiers.map(t => [t.name, t.max_len])).toEqual([['base', null]])
  })

  it('charges a price whose box is ticked: unticking 1 h cache writes drops cc1h, ticking images keeps img', async () => {
    const { w, model } = render(claude())
    const box = (label: string) => w.findAll('label').find(l => l.text() === label)!.get('input[type="checkbox"]')
    await box('缓存写 1 小时').setValue(false)
    expect('cc1h' in model.value.tiers[0]).toBe(false)
    expect(w.get('[data-testid="tier-cc1h"]').attributes('disabled')).toBeDefined()
    // Input and output are always charged.
    await box('输入').setValue(false)
    expect(model.value.tiers[0].p).toBe(3)
    await w.findAll('button').find(b => b.text() === '媒体定价')!.trigger('click')
    expect(w.get('[data-testid="tier-img"]').attributes('placeholder')).toBe('不勾选按输入价')
    await box('图片输入').setValue(true)
    expect(model.value.tiers[0]).toMatchObject({ img: null })
    expect('img_o' in model.value.tiers[0]).toBe(false)
  })

  it('prices a tier per request', async () => {
    const { w, model } = render(claude())
    await w.getComponent(SSelect).vm.$emit('update:modelValue', 'request')
    await nextTick()
    expect(w.find('[data-testid="tier-p"]').exists()).toBe(false)
    await w.get('[data-tier="0"] input[aria-label="每次价格"]').setValue('0.05')
    expect(tokenExpr(model.value)).toBe('tier("base", flat(0.05))')
  })

  it('read-only, opens every card and its media prices, and changes nothing', async () => {
    const c = editableTokenConfig({
      tiers: [tierOf('short', 1000), { name: 'rest', max_len: null, p: 2, c: 2, img: 4 }],
      rules: [{ name: 'r', multiplier: 2, conds: [{ source: 'time', tz: 'UTC', from_hour: 22, to_hour: 0 }] }]
    })
    const { w, tiers } = render(c, true)
    await tiers()[1].get('[data-testid="tier-toggle"]').trigger('click')
    expect(tiers()[1].get('[data-testid="tier-img"]').attributes('disabled')).toBeDefined()
    await tiers()[1].findAll('button').find(b => b.text() === '媒体定价')!.trigger('click')
    expect(tiers()[1].find('[data-testid="tier-img"]').exists()).toBe(false)
    expect(tiers()[1].findAll('input').every(i => i.attributes('disabled') !== undefined)).toBe(true)
    expect(w.get('[data-rule="0"]').findAll('input').every(i => i.attributes('disabled') !== undefined)).toBe(true)
    expect(w.findAll('[data-rule="0"] button').filter(b => b.attributes('disabled') === undefined).map(b => b.text())).toEqual([])
    expect(w.find('[data-testid="token-add-tier"]').exists()).toBe(false)
    expect(w.find('[data-testid="token-add-rule"]').exists()).toBe(false)
    // A window may end at 0, which the list offers.
    expect(w.findAllComponents(SSelect).find(s => s.props('ariaLabel') === '结束')!.props('options')).toContainEqual({ value: 0, label: '0:00' })
  })

  it('keeps a card’s state with its tier when one before it goes, and opens the media prices of a template that has them', async () => {
    const { w, model, tiers } = render(editableTokenConfig({ tiers: [tierOf('a', 1000), tierOf('b', 2000), { name: 'c', max_len: null, p: 1, c: 1 }] }))
    await tiers()[2].get('[data-testid="tier-toggle"]').trigger('click')
    expect(tiers()[2].find('[data-testid="tier-img"]').exists()).toBe(false)
    await tiers()[2].findAll('button').find(b => b.text() === '媒体定价')!.trigger('click')
    w.findAllComponents(PriceTierCard)[1].findComponent(SDropdown).vm.$emit('select', 'remove')
    await nextTick()
    expect(model.value.tiers.map(t => t.name)).toEqual(['a', 'c'])
    await tiers()[1].get('[data-testid="tier-toggle"]').trigger('click')
    expect(tiers()[1].find('[data-testid="tier-img"]').exists()).toBe(true)
    await tiers()[0].get('[data-testid="tier-toggle"]').trigger('click')
    expect(tiers()[0].find('[data-testid="tier-img"]').exists()).toBe(false)

    await w.get('[data-preset="image"]').trigger('click')
    expect(tiers()[0].find('[data-testid="tier-img"]').exists()).toBe(true)
  })

  it('starts from a template', async () => {
    const { w, model } = render(claude())
    await w.get('[data-preset="three"]').trigger('click')
    expect(model.value.tiers.map(t => t.max_len)).toEqual([32000, 128000, null])
  })

  it('adds a rule group of AND conditions', async () => {
    const { w, model } = render(claude())
    await w.get('[data-testid="token-add-rule"]').trigger('click')
    expect(model.value.rules).toEqual([{ name: '规则组 1', multiplier: 1, conds: [{ source: 'param', key: '', op: 'eq', value: '' }] }])
    await w.findAll('[data-rule="0"] button').find(b => b.text().includes('新增时间条件'))!.trigger('click')
    expect(model.value.rules[0].conds[1]).toEqual({ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 })
  })
})

describe('a condition value', () => {
  it('is held as typed, so a decimal survives its keystrokes', async () => {
    const rule: MarkupRule = { name: 'r', multiplier: 2, conds: [{ source: 'param', key: 'n', op: 'gt', value: '' }] }
    const w = mount(PriceRuleGroups, { props: { modelValue: [rule] }, global: { plugins: [i18n] } })
    const input = w.get('[data-cond="0"] input[aria-label="值"]')
    for (const typed of ['0', '0.', '0.0', '0.05']) await input.setValue(typed)
    expect((input.element as HTMLInputElement).value).toBe('0.05')
    expect(rule.conds[0]).toMatchObject({ value: '0.05' })
    expect(compactTokenConfig({ tiers: [], rules: [rule] }).rules[0].conds[0]).toMatchObject({ value: 0.05 })
  })

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
  it('reads as text in double quotes, and a saved string that reads otherwise is shown quoted', () => {
    expect(typedValue(cond('param', 'eq'), '"3"')).toBe('3')
    expect(valueText(cond('param', 'eq'), '3')).toBe('"3"')
    expect(valueText(cond('param', 'eq'), 3)).toBe('3')
    expect(valueText(cond('header', 'eq'), '3')).toBe('3')
  })
  it('keeps its text when the operator changes, and is dropped for exists', () => {
    const c = cond('param', 'contains', '3')
    setOp(c, 'eq')
    expect(c.value).toBe('3')
    setOp(c, 'exists')
    expect('value' in c).toBe(false)
    setOp(c, 'eq')
    expect(c.value).toBe('')
  })
})
