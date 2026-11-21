import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { i18n } from '@/i18n'
import type { Price } from '@/api/types'
import { SPagination } from '@sub2api/ui'
import ExprGuide from './ExprGuide.vue'
import PriceEditView from './PriceEditView.vue'
import PriceModelList from './PriceModelList.vue'
import { priceListState } from './priceListState'
import { singleTier } from './priceExpr'

const price = (id: number, note = ''): Price => ({
  id,
  model: `model-${id}`,
  note,
  mode: 'per_token',
  config: singleTier({ p: 3, c: 15 }),
  expression: 'tier("base", p*3 + c*15)',
  expr_version: 1,
  expr_hash: 'abc',
  source: 'manual',
  enabled: true
})
const api = vi.hoisted(() => ({
  get: vi.fn(async (path: string) => price(Number(path.split('/').pop()))),
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
const can = vi.hoisted(() => ({ manage: true }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (perm: string) => can.manage || perm.endsWith(':read') }) }))
enableAutoUnmount(afterEach)
afterEach(() => {
  Object.assign(leave, { discard: false, asked: [] })
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
})
