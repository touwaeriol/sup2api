import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { SDropdown } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { Price } from '@/api/types'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import PricesView from './PricesView.vue'
import { singleTier } from './priceExpr'

const price = (id: number, model: string, note = ''): Price => ({
  id,
  model,
  note,
  mode: 'per_token',
  config: singleTier({ p: 3, c: 15, cr: 0.3 }),
  expression: '',
  expr_version: 1,
  expr_hash: '',
  source: 'manual',
  enabled: true
})
const prices = [price(11, 'claude-sonnet-4-5', 'negotiated'), price(12, 'gpt-4o*'), price(13, 'house-model')]
vi.mock('@/composables/useList', async importOriginal => ({
  ...(await importOriginal<object>()),
  useList: (_path: string, init: Record<string, unknown>) => ({
    items: ref(prices),
    loading: ref(false),
    error: ref(null),
    page: ref(1),
    pageSize: ref(20),
    total: ref(prices.length),
    filters: reactive({ ...init }),
    reload: vi.fn()
  })
}))
const can = vi.hoisted(() => ({ manage: true, denied: new Set<string>() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (perm: string) => !can.denied.has(perm) && (can.manage || perm.endsWith(':read')) }) }))
enableAutoUnmount(afterEach)
afterEach(() => {
  can.manage = true
  can.denied.clear()
})

async function render() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/prices', component: PricesView },
      ...['/prices/:id', '/groups', '/segments'].map(path => ({ path, component: { template: '<div />' } }))
    ]
  })
  await router.push('/prices')
  i18n.global.locale.value = 'zh'
  return mount(PricesView, { global: { plugins: [i18n, router], stubs: { PriceSimulatorModal: true } } })
}

describe('model prices list', () => {
  it('links each model to its price, with the vendor mark and without a tint', async () => {
    const rows = (await render()).findAll('tbody tr')
    const links = rows.map(tr => tr.get('a'))
    expect(links.map(a => a.attributes('href'))).toEqual(['/prices/11', '/prices/12', '/prices/13'])
    const badges = links.map(a => a.get('[data-testid="model-badge"]'))
    expect(badges.map(b => b.get('.truncate').text())).toEqual(['claude-sonnet-4-5', 'gpt-4o*', 'house-model'])
    expect(badges.map(b => b.get('[data-brand]').attributes('data-brand'))).toEqual(['anthropic', 'openai', 'house-model'])
    expect(badges.some(b => b.classes().includes('brand-tint'))).toBe(false)
    expect(rows[0].text()).toContain('negotiated')
  })

  it('says that these are the base prices and links to where a group or a user group prices on its own', async () => {
    const note = (await render()).get('[data-testid="price-scope-note"]')
    expect(note.text()).toContain('实付 = 标价 × 倍率')
    expect(note.findAll('a').map(a => a.attributes('href'))).toEqual(['/groups', '/segments'])
  })

  it('opens the price simulator from the header, and on its model from the menu of a row', async () => {
    const w = await render()
    const simulator = () => w.getComponent(PriceSimulatorModal).props()
    expect(simulator().open).toBe(false)
    await w.get('[data-testid="price-simulate"]').trigger('click')
    expect(simulator()).toEqual({ open: true, preset: undefined })

    await w.findAllComponents(SDropdown)[1].vm.$emit('select', 'simulate')
    expect(simulator()).toEqual({ open: true, preset: { model: 'gpt-4o*' } })
  })

  it('lets a reader simulate a model, and only a manager delete its price', async () => {
    const actions = async () => (await render()).findComponent(SDropdown).props('actions').filter(a => !a.hidden).map(a => a.key)
    expect(await actions()).toEqual(['simulate', 'delete'])
    can.manage = false
    expect(await actions()).toEqual(['simulate'])
  })

  it('leaves the price simulator out for a caller who may not read groups', async () => {
    can.denied.add('group:read')
    const w = await render()
    expect(w.find('[data-testid="price-simulate"]').exists()).toBe(false)
    expect(w.findComponent(SDropdown).props('actions').filter(a => !a.hidden).map(a => a.key)).toEqual(['delete'])
    expect(w.findComponent(PriceSimulatorModal).exists()).toBe(false)
  })
})
