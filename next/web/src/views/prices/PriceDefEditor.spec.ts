import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { SSegmented } from '@sub2api/ui'
import { i18n } from '@/i18n'
import PriceDefEditor from './PriceDefEditor.vue'
import PriceExprEditor from './PriceExprEditor.vue'
import VisualPriceEditor from './VisualPriceEditor.vue'
import { priceDraftOf, pricePayload, type PriceDraft } from './priceDraft'
import { setPriceOn } from './visualPrice'

const toasts = vi.hoisted(() => [] as string[])
vi.mock('@sub2api/ui', async importOriginal => {
  const real = await importOriginal<typeof import('@sub2api/ui')>()
  return { ...real, toast: (text: string) => toasts.push(text) }
})
enableAutoUnmount(afterEach)
afterEach(() => {
  toasts.length = 0
})

/** Two tiers (the first at night) and a named rule. */
const start = () =>
  priceDraftOf({
    mode: 'visual',
    config: {
      tiers: [
        { name: 'night', when: { var: 'hour', tz: 'Asia/Shanghai', op: '<', value: 6 }, unit: 'token', p: 1, c: 2 },
        { name: 'base', unit: 'token', p: 3, c: 15 }
      ],
      rules: [{ name: 'hd', multiplier: 2, conds: [{ source: 'param', key: 'quality', op: 'eq', value: 'hd' }] }]
    }
  })

function render(draft: PriceDraft, props: { issues?: string[]; readonly?: boolean } = {}) {
  i18n.global.locale.value = 'zh'
  const model = ref(draft)
  const w = mount(PriceDefEditor, {
    props: { modelValue: model.value, issues: [], ...props, 'onUpdate:modelValue': (v: PriceDraft) => (model.value = v) },
    global: { plugins: [i18n], stubs: { Teleport: true } }
  })
  return { w, draft: model }
}

const to = async (w: VueWrapper, mode: string) => {
  w.getComponent(SSegmented).vm.$emit('update:modelValue', mode)
  await flushPromises()
}
const write = async (w: VueWrapper, src: string) => {
  w.getComponent(PriceExprEditor).vm.$emit('update:modelValue', src)
  await flushPromises()
}
const opened = (w: VueWrapper) => w.findAll('[data-testid="tier-toggle"]').map(b => b.attributes('aria-expanded'))

describe('the price definition editor', () => {
  it('switches into the expression mode with the expression the visual config generates', async () => {
    const { w, draft } = render(start())
    const generated = pricePayload(draft.value).expression
    await to(w, 'expression')
    expect(draft.value.mode).toBe('expression')
    expect(w.getComponent(PriceExprEditor).props('modelValue')).toBe(generated)
    expect(w.findComponent(VisualPriceEditor).exists()).toBe(false)
  })

  it('stays visual while the price has a local problem, saying the first', async () => {
    const { w, draft } = render(start(), { issues: ['档位 "base" 的按次价未填写', 'x'] })
    await to(w, 'expression')
    expect(toasts).toEqual(['档位 "base" 的按次价未填写'])
    expect(draft.value).toMatchObject({ mode: 'visual', expression: '' })
  })

  it('comes back to the visual config it left while the expression is the one generated then, whitespace aside', async () => {
    const { w, draft } = render(start())
    const config = draft.value.config
    setPriceOn(config.tiers[1], 'cc1h', true)
    await w.findAll('[data-testid="tier-toggle"]')[1].trigger('click')
    expect(opened(w)).toEqual(['false', 'true'])
    const saved = JSON.stringify(pricePayload(draft.value))

    await to(w, 'expression')
    await write(w, draft.value.expression.replace(' ? ', '\n  ? '))
    await to(w, 'visual')
    // The same objects: the rule keeps its name, the ticked price its key, the cards their state.
    expect(draft.value.config).toBe(config)
    expect(config.rules[0].name).toBe('hd')
    expect(config.tiers[1]).toHaveProperty('cc1h', null)
    expect(opened(w)).toEqual(['false', 'true'])
    expect(JSON.stringify(pricePayload(draft.value))).toBe(saved)
    expect(toasts).toEqual([])
  })

  it('reads an edited expression back into a visual config, its rules unnamed', async () => {
    const { w, draft } = render(start())
    const config = draft.value.config
    await to(w, 'expression')
    await write(w, draft.value.expression.replace('? 2 : 1', '? 3 : 1'))
    await to(w, 'visual')
    expect(draft.value.mode).toBe('visual')
    expect(draft.value.config).not.toBe(config)
    expect(draft.value.config.rules[0]).toMatchObject({ name: '', multiplier: 3 })
  })

  it('stays in the expression mode for an expression the generator did not write, and keeps the visual config for a blank one', async () => {
    const { w, draft } = render(start())
    const config = draft.value.config
    await to(w, 'expression')
    await write(w, 'tier("base", 4*p + c*15)')
    await to(w, 'visual')
    expect(toasts).toEqual(['这条表达式不是可视化生成的，无法转成可视化'])
    expect(draft.value.mode).toBe('expression')
    await write(w, '  ')
    await to(w, 'visual')
    expect(draft.value).toMatchObject({ mode: 'visual', config })
  })

  it('forgets the visual config it left when another draft is bound', async () => {
    const { w, draft } = render(start())
    await to(w, 'expression')
    const other = reactive(priceDraftOf({ mode: 'expression', config: {}, expression: draft.value.expression }))
    await w.setProps({ modelValue: other })
    await to(w, 'visual')
    expect(other.config.rules[0].name).toBe('')
  })

  it('read-only, neither switches nor edits, and still opens the guide', async () => {
    const { w } = render(priceDraftOf({ mode: 'expression', config: {}, expression: 'p * 2' }), { readonly: true })
    expect(w.getComponent(SSegmented).props('disabled')).toBe(true)
    expect(w.getComponent(PriceExprEditor).props('readonly')).toBe(true)
    await w.get('[data-testid="expr-guide-open"]').trigger('click')
    expect(w.emitted('guide')).toHaveLength(1)
  })
})
