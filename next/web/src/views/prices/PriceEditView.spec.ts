import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { i18n } from '@/i18n'
import type { Price } from '@/api/types'
import { SPagination, SSegmented } from '@sub2api/ui'
import ExprGuide from './ExprGuide.vue'
import PriceEditView from './PriceEditView.vue'
import PriceExprEditor from './PriceExprEditor.vue'
import PriceModelList from './PriceModelList.vue'
import VisualPriceEditor from './VisualPriceEditor.vue'
import { priceListState } from './priceListState'
import { singleTier } from './priceExpr'

const price = (id: number, note = ''): Price => ({
  id,
  model: `model-${id}`,
  note,
  mode: 'visual',
  config: singleTier({ p: 3, c: 15 }),
  expression: 'tier("base", p*3 + c*15)',
  expr_version: 1,
  expr_hash: 'abc',
  source: 'manual',
  enabled: true
})
/** Price 3 has a named rule, which no expression holds. */
const withRule = (): Price => ({
  ...price(3),
  config: { ...singleTier({ p: 3, c: 15 }), rules: [{ name: 'hd', multiplier: 2, conds: [{ source: 'param', key: 'quality', op: 'eq', value: 'hd' }] }] },
  expression: '(tier("base", p*3 + c*15)) * (param("quality") == "hd" ? 2 : 1)'
})
const api = vi.hoisted(() => ({
  get: vi.fn(async (path: string) => (path.endsWith('/3') ? withRule() : price(Number(path.split('/').pop())))),
  post: vi.fn(async () => ({ ok: true, errors: [], warnings: [] })),
  patch: vi.fn(async (path: string, body: Partial<Price>) => ({ ...price(Number(path.split('/').pop())), ...body }))
}))
vi.mock('@sub2api/host', async importOriginal => {
  const real = await importOriginal<typeof import('@sub2api/host')>()
  return { ...real, api: { ...real.api, ...api } }
})
const list = vi.hoisted(() => ({ reload: vi.fn() }))
vi.mock('@/composables/useList', async importOriginal => ({
  ...(await importOriginal<object>()),
  useList: (_path: string, init: Record<string, unknown>) => ({
    items: ref([]),
    loading: ref(false),
    error: ref(null),
    page: ref(1),
    pageSize: ref(20),
    total: ref(0),
    filters: reactive({ ...init }),
    reload: list.reload
  })
}))
const leave = vi.hoisted(() => ({ discard: false, asked: [] as boolean[] }))
vi.mock('@/composables/confirmDiscard', () => ({
  confirmDiscard: async (dirty: boolean) => (leave.asked.push(dirty), !dirty || leave.discard)
}))
const toasts = vi.hoisted(() => [] as string[])
vi.mock('@sub2api/ui', async importOriginal => {
  const real = await importOriginal<typeof import('@sub2api/ui')>()
  return { ...real, toast: (text: string) => toasts.push(text) }
})
const can = vi.hoisted(() => ({ manage: true }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (perm: string) => can.manage || perm.endsWith(':read') }) }))
enableAutoUnmount(afterEach)
afterEach(() => {
  Object.assign(leave, { discard: false, asked: [] })
  toasts.length = 0
  Object.assign(priceListState, { q: '', mode: '', page: 1 })
  can.manage = true
})

async function render(path = '/prices/1') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/prices/:id', component: PriceEditView },
      { path: '/prices', component: { template: '<div />' } }
    ]
  })
  i18n.global.locale.value = 'zh'
  await router.push(path)
  const w = mount(RouterView, { global: { plugins: [i18n, router], stubs: { PriceEstimator: true, Teleport: true } } })
  await flushPromises()
  return { w, router }
}

describe('the price editor', () => {
  it('asks before another model or page drops unsaved changes', async () => {
    const { w, router } = await render()
    await router.push('/prices/2')
    await flushPromises()
    expect([router.currentRoute.value.path, leave.asked]).toEqual(['/prices/2', [false]])

    await w.get('input[placeholder="可选"]').setValue('edited')
    await router.push('/prices/1')
    expect([router.currentRoute.value.path, leave.asked]).toEqual(['/prices/2', [false, true]])
    leave.discard = true
    await router.push('/prices')
    expect(router.currentRoute.value.path).toBe('/prices')
  })

  it('takes a saved price as unchanged, and refreshes the list beside it', async () => {
    const { w, router } = await render()
    await w.get('input[placeholder="可选"]').setValue('edited')
    await w.get('[data-testid="price-save"]').trigger('click')
    await flushPromises()
    expect(api.patch).toHaveBeenCalledWith('/prices/1', expect.objectContaining({ note: 'edited' }))
    expect(list.reload).toHaveBeenCalled()
    await router.push('/prices')
    expect([router.currentRoute.value.path, leave.asked]).toEqual(['/prices', [false]])
  })

  it('keeps the search, filter and page of the list beside it across the page built anew', async () => {
    const { router } = await render()
    const opts = { props: { current: 1, canManage: true }, global: { plugins: [i18n, router] } }
    const first = mount(PriceModelList, opts)
    await first.get('input').setValue('claude')
    first.getComponent(SPagination).vm.$emit('update:page', 3)
    await flushPromises()
    first.unmount()
    const again = mount(PriceModelList, opts)
    expect((again.get('input').element as HTMLInputElement).value).toBe('claude')
    expect(again.getComponent(SPagination).props('page')).toBe(3)
  })

  it('read-only, still leads back to the list, and its guide inserts nothing', async () => {
    can.manage = false
    const { w, router } = await render()
    await w.findAll('button').find(b => b.text() === '返回列表')!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/prices')
    const guide = mount(ExprGuide, { props: { readonly: true }, global: { plugins: [i18n] } })
    expect(guide.findAll('button').map(b => b.text())).not.toContain('插入')
    expect(guide.findAll('button').map(b => b.text())).not.toContain('填入')
  })

  it('has two modes, visual and expression, and switches between them through the generated expression', async () => {
    const { w } = await render()
    const mode = w.getComponent(SSegmented)
    expect((mode.props('options') as Array<{ label: string }>).map(o => o.label)).toEqual(['可视化', '表达式'])
    const to = async (m: string) => {
      mode.vm.$emit('update:modelValue', m)
      await flushPromises()
    }
    await to('expression')
    const expr = w.getComponent(PriceExprEditor)
    expect(expr.props('modelValue')).toBe('tier("base", p*3 + c*15)')
    // Whitespace aside, the generated expression comes back as the tiers it was generated from.
    expr.vm.$emit('update:modelValue', 'tier("base",\n  p*4 + c*15)')
    await flushPromises()
    await to('visual')
    expect(w.getComponent(VisualPriceEditor).props('modelValue').tiers).toEqual([{ name: 'base', unit: 'token', p: 4, c: 15 }])
    // An expression written by hand is dropped: the visual config stays.
    await to('expression')
    w.getComponent(PriceExprEditor).vm.$emit('update:modelValue', 'tier("base", 4*p + c*15)')
    await flushPromises()
    await to('visual')
    expect(w.getComponent(VisualPriceEditor).props('modelValue').tiers).toEqual([{ name: 'base', unit: 'token', p: 4, c: 15 }])
    expect(toasts).toEqual(['这条表达式不是可视化生成的，无法转成可视化，已丢弃'])
  })

  it('takes a price that went to the expression mode and back unchanged as unchanged', async () => {
    const { w, router } = await render('/prices/3')
    const mode = w.getComponent(SSegmented)
    for (const m of ['expression', 'visual']) {
      mode.vm.$emit('update:modelValue', m)
      await flushPromises()
    }
    expect(w.getComponent(VisualPriceEditor).props('modelValue').rules[0].name).toBe('hd')
    await router.push('/prices')
    expect([router.currentRoute.value.path, leave.asked]).toEqual(['/prices', [false]])
  })
})
