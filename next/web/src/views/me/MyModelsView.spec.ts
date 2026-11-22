import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import MyModelsView from './MyModelsView.vue'
import { requestTier, singleTier } from '@/views/prices/priceExpr'

const get = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api: { get } }))
enableAutoUnmount(afterEach)

const groups = [
  { id: 1, name: 'default', rate_multiplier: '1', effective_rate: '0.9', rate_source: 'group', exclusive: false },
  { id: 2, name: 'vip', rate_multiplier: '1.2', effective_rate: '1.5', rate_source: 'segment', exclusive: true }
]
const [basic, vip] = groups.map(({ id, name }) => ({ id, name }))
const prices = [
  { model: 'tokens', mode: 'visual', config: singleTier({ p: 3, c: 15, cr: 0.3, cc: 3.75 }), expression: '', groups: [basic, vip] },
  { model: 'one-shot', mode: 'visual', config: requestTier(0.2), expression: '', groups: [basic] },
  { model: 'tiered', mode: 'visual', config: { tiers: [{ name: 'a', when: { var: 'len', op: '<=', value: 200000 }, p: 1, c: 2 }, { name: 'b', p: 3, c: 4 }], rules: [] }, expression: '', groups: [basic] },
  { model: 'custom', mode: 'expression', config: {}, expression: 'p * 2 + unknown(1)', groups: [] }
]
const everyGroup = { group: null, rate_multiplier: '1', prices }
const inGroup = (row: (typeof prices)[number], rate: string, rate_source: string) => ({ ...row, groups: undefined, source: 'group', rate, rate_source })
const inVip = { group: vip, rate_multiplier: '1.5', rate_source: 'segment', prices: [inGroup(prices[0], '1.5', 'segment'), inGroup({ ...prices[0], model: 'cheaper' }, '0.5', 'segment_model')] }

/** The prices are asked for a tick after the choice: only the latest choice counts. */
async function settle() {
  await flushPromises()
  await new Promise((r) => setTimeout(r))
  await flushPromises()
}
async function render(path = '/me/models') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/me/models', component: MyModelsView }] })
  await router.push(path)
  const w = mount(MyModelsView, { global: { plugins: [i18n, router] } })
  await settle()
  return { w, router }
}
type View = Awaited<ReturnType<typeof render>>['w']
const row = (w: View, model: string) => w.findAll('tr').find((r) => r.text().includes(model))!
const unit = (w: View) => w.get('[data-testid="models-unit"]').text()

describe('models and prices', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
    get.mockReset()
    get.mockImplementation(async (path: string, q: Record<string, string>) => {
      if (path === '/me/groups') return groups
      return q.group_id === '2' ? inVip : everyGroup
    })
  })

  it('shows each kind of price', async () => {
    const { w } = await render()
    expect(row(w, 'tokens').text()).toContain('$3')
    expect(row(w, 'tokens').text()).toContain('$3.75')
    expect(row(w, 'one-shot').text()).toContain('$0.2 / 次')
    expect(row(w, 'tiered').text()).toContain('显示兜底档位')
    expect(row(w, 'tiered').text()).toContain('$4')
    expect(row(w, 'custom').text()).toContain('按规则计费')
  })

  it('marks each model with its vendor, in the vendor colour', async () => {
    const named = [{ ...prices[0], model: 'claude-sonnet-4-5' }, { ...prices[1], model: 'seedance-pro' }, prices[3]]
    get.mockImplementation(async (path: string) => (path === '/me/groups' ? groups : { ...everyGroup, prices: named }))
    const { w } = await render()
    const badges = w.findAll('[data-testid="model-badge"]')
    expect(badges.map((b) => b.get('.truncate').text())).toEqual(['claude-sonnet-4-5', 'seedance-pro', 'custom'])
    expect(badges.map((b) => b.get('[data-brand]').attributes('data-brand'))).toEqual(['anthropic', 'doubao', 'custom'])
    expect(badges.every((b) => b.classes().includes('brand-tint'))).toBe(true)
  })

  it('opens on every group: the original prices, and which of the groups provide each model', async () => {
    const { w, router } = await render()
    expect(get).toHaveBeenLastCalledWith('/me/prices', {})
    expect(w.get('[data-testid="models-group"]').findAll('option').map((o) => o.text())).toEqual(['全部账号分组 · 原价', 'default · ×0.9', 'vip · ×1.5 · 专属'])
    expect(w.find('[data-testid="models-base-note"]').exists()).toBe(true)
    expect(unit(w)).toContain('原价，未乘倍率')
    expect(row(w, 'tokens').findAll('[data-testid="models-row-group"]').map((c) => c.text())).toEqual(['default', 'vip'])
    expect(row(w, 'custom').text()).toContain('暂无账号分组提供')

    // A group of a model opens what the caller pays there.
    await row(w, 'tokens').findAll('[data-testid="models-row-group"] button')[1].trigger('click')
    await settle()
    expect(router.currentRoute.value.query).toEqual({ group: '2' })
    expect(get).toHaveBeenLastCalledWith('/me/prices', { group_id: '2' })
  })

  it('multiplies each model by the rate of the caller for it in the chosen group, and says where it comes from', async () => {
    const { w, router } = await render()
    await w.get('[data-testid="models-group"]').setValue('2')
    await settle()
    expect(router.currentRoute.value.query).toEqual({ group: '2' })
    expect(get).toHaveBeenLastCalledWith('/me/prices', { group_id: '2' })
    expect(row(w, 'tokens').text()).toContain('$4.5')
    expect(row(w, 'tokens').get('[data-testid="models-row-rate"]').text()).toBe('×1.5')
    expect(row(w, 'tokens').get('[data-testid="models-row-rate-source"]').text()).toBe('你的用户分组在该账号分组的倍率')
    expect(row(w, 'cheaper').text()).toContain('$1.5')
    expect(row(w, 'cheaper').get('[data-testid="models-row-rate-source"]').text()).toBe('你的用户分组给这个模型单独设的倍率')
    expect(unit(w)).toContain('已乘你在该账号分组每个模型的倍率')
  })

  it('shows one group without the base price note and the groups, and searches', async () => {
    const { w, router } = await render('/me/models?group=2')
    expect(w.text()).not.toContain('one-shot')
    // One group: no base price note, no column of groups.
    expect(w.find('[data-testid="models-base-note"]').exists()).toBe(false)
    expect(w.find('[data-testid="models-row-group"]').exists()).toBe(false)

    await w.get('[data-testid="models-search"]').setValue('nothing')
    expect(w.text()).toContain('这个账号分组没有可用模型')

    await w.get('[data-testid="models-group"]').setValue('0')
    await settle()
    expect(router.currentRoute.value.query).toEqual({})
    expect(get).toHaveBeenLastCalledWith('/me/prices', {})
  })

  it('works the caller’s rate into a price it cannot break down, as into the others', async () => {
    const rated = { group: vip, rate_multiplier: '1', rate_source: 'group', prices: [inGroup(prices[3], '1.5', 'group_model')] }
    get.mockImplementation(async (path: string) => (path === '/me/groups' ? groups : rated))
    const { w } = await render('/me/models?group=2')
    expect(row(w, 'custom').get('[data-testid="models-opaque"]').text()).toBe('按规则计费 ×1.5')
  })

  it('opens the group named in the address', async () => {
    const { w } = await render('/me/models?group=2')
    expect(get.mock.calls.filter(([path]) => path === '/me/prices')).toEqual([['/me/prices', { group_id: '2' }]])
    expect((w.get('[data-testid="models-group"]').element as HTMLSelectElement).selectedIndex).toBe(2)
    expect(row(w, 'tokens').text()).toContain('$4.5')
  })

  it('falls back to every group, with a note, when the address names a group the caller cannot use', async () => {
    get.mockImplementation(async (path: string, q: Record<string, string>) => {
      if (path === '/me/groups') return groups
      if (q.group_id === '999') throw new ApiError(404, { code: 'not_found', message: 'group not found' })
      return q.group_id === '2' ? inVip : everyGroup
    })
    const { w, router } = await render('/me/models?group=999')
    expect(router.currentRoute.value.query).toEqual({})
    // Every group is asked for once, by the choice going back to it.
    expect(get.mock.calls.filter(([path]) => path === '/me/prices')).toEqual([
      ['/me/prices', { group_id: '999' }],
      ['/me/prices', {}]
    ])
    expect(w.get('[data-testid="models-group-unavailable"]').text()).toBe('链接里的账号分组不对你开放，已改为显示全部账号分组。')
    expect(w.find('[role="alert"]').exists()).toBe(false)
    expect((w.get('[data-testid="models-group"]').element as HTMLSelectElement).selectedIndex).toBe(0)
    expect(row(w, 'one-shot').exists()).toBe(true)

    // The note goes once a group is chosen.
    await w.get('[data-testid="models-group"]').setValue('2')
    await settle()
    expect(w.find('[data-testid="models-group-unavailable"]').exists()).toBe(false)
  })
})
