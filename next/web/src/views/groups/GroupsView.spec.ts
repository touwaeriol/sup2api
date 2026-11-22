import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import type { Group } from '@/api/types'
import { i18n } from '@/i18n'
import { mountPinned } from '@sub2api/ui/testing'
import GroupsView from './GroupsView.vue'
import GroupServedProtocols from './GroupServedProtocols.vue'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'

const { get, denied } = vi.hoisted(() => ({ get: vi.fn(), denied: new Set<string>() }))
vi.mock('@sub2api/host', () => ({ api: { get } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (key: string) => !denied.has(key), hasAction: (a: string) => [a, `${a}:own`].some((key) => !denied.has(key)) }) }))
vi.mock('@/composables/lookups', async original => ({ ...(await original<object>()), useGroupsLookup: vi.fn() }))
const group = (id: number, name: string, over: Partial<Group> = {}): Group => ({
  id,
  name,
  description: '',
  status: 'active',
  rate_multiplier: '1',
  visibility: 'public',
  model_allowlist: [],
  account_count: 3,
  api_key_count: 1,
  platforms: ['anthropic'],
  ...over
})
const items = ref<Group[]>([])
/** served_protocols of GET /groups/:id, by group id. */
const served: Record<number, Group['served_protocols']> = { 1: [{ protocol: 'anthropic.messages', accounts: 2 }, { protocol: 'openai.chat', accounts: 1 }] }
vi.mock('@/composables/useList', () => ({
  useList: () => ({ items, loading: ref(false), error: ref(null), page: ref(1), pageSize: ref(20), total: ref(2), reload: vi.fn() })
}))
enableAutoUnmount(afterEach)

const options = {
  global: {
    plugins: [i18n],
    stubs: {
      Teleport: true,
      ModelBadge: true,
      GroupServedProtocols: true,
      EnvironmentEditor: true,
      GroupModelPolicyEditor: true,
      ModelPriceTable: true,
      GroupDeleteModal: true,
      PriceSimulatorModal: true
    }
  }
}
const render = () => mount(GroupsView, options)
type View = ReturnType<typeof render>
const rows = (w: View) => w.findAll('tbody tr')
async function openDetail(w: View, index: number) {
  await rows(w)[index].get('[data-testid="group-name"]').trigger('click')
  await flushPromises()
  return w.get('[data-testid="group-detail"]')
}

describe('groups page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    denied.clear()
    i18n.global.locale.value = 'zh'
    items.value = [group(1, 'default', { platforms: ['anthropic', 'openai'] }), group(2, 'trial', { account_count: 0, platforms: [] })]
    // GET /groups/:id adds what the member accounts serve (served_protocols), which the list rows do not carry.
    get.mockImplementation(async (path: string) => {
      if (path.endsWith('/models')) return { models: [], unrestricted_accounts: 0 }
      const g = items.value.find(g => path === `/groups/${g.id}`)
      return g && { ...g, served_protocols: served[g.id] ?? [] }
    })
  })

  it('lists the groups without the platforms they serve', () => {
    const w = render()
    expect(w.findAll('thead th').map(th => th.text())).toEqual(['名称', '谁能用', '分组倍率', '模型价格', '模型名单', '账号数', 'Key 数', '状态', '操作'])
    expect(rows(w).map(r => r.get('[data-testid="group-name"]').text())).toEqual(['default', 'trial'])
    expect(w.find('[data-testid="platform-badge"], [data-platform]').exists()).toBe(false)
  })

  it('says who can use a group: everyone, the user groups a private one is granted to, or nobody yet', () => {
    const named = (id: number, name: string) => ({ id, kind: 'named' as const, name })
    items.value = [
      group(1, 'open'),
      group(2, 'vip', { visibility: 'restricted', access: { segments: [named(2, 'VIP'), named(3, 'Corp'), named(4, 'Staff')], personal_count: 2 } }),
      group(3, 'closed', { visibility: 'restricted', access: { segments: [], personal_count: 0 } })
    ]
    const access = rows(render()).map(r => r.get('[data-testid="group-access"]'))
    expect(access[0].text()).toBe('所有人')
    expect(access[1].findAll('[data-testid="segment-badge"]').map(b => b.text())).toEqual(['VIP', 'Corp'])
    expect(access[1].text()).toContain('+1')
    expect(access[1].get('[data-testid="group-access-personal"]').text()).toBe('2 位专享用户')
    expect(access[2].text()).toBe('还没开通给任何人')
  })

  it('sums up the model pricing of a group and opens its editor on that tab', async () => {
    items.value = [group(1, 'default', { model_count: 7 }), group(2, 'vip', { price_rule_count: 2, model_count: 3 })]
    const w = render()
    const [plain, priced] = rows(w)
    expect(plain.get('[data-testid="group-pricing-summary"]').text()).toBe('全部用原价')
    expect(plain.get('[data-testid="group-model-count"]').text()).toBe('提供 7 个模型')
    const summary = priced.get('[data-testid="group-pricing-summary"]')
    expect(summary.text()).toBe('2 个模型改过价')
    await summary.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('编辑分组 vip')
    expect(w.get('[role="tab"][aria-selected="true"]').text()).toBe('价格')
  })

  it('shows the model prices read-only to a caller who may not manage groups', async () => {
    denied.add('group:manage')
    items.value = [group(2, 'vip', { visibility: 'restricted', price_rule_count: 1 })]
    const w = render()
    expect(w.findAll('thead th').map(th => th.text())).not.toContain('操作')
    await rows(w)[0].get('[data-testid="group-pricing-summary"]').trigger('click')
    await flushPromises()
    const shown = () => w.find('[data-testid="group-pricing-view"]').exists()
    expect(shown()).toBe(true)
    expect(w.text()).toContain('vip 的价格')
    expect(w.text()).not.toContain('编辑分组')
    await w.get('[data-testid="group-pricing-view-close"]').trigger('click')
    await flushPromises()
    expect(shown()).toBe(false)

    await openDetail(w, 0)
    await w.get('[data-testid="group-detail-pricing"]').trigger('click')
    await flushPromises()
    expect(shown()).toBe(true)
  })

  it('lets a caller who may simulate prices but not manage groups simulate one in a group, from its detail', async () => {
    denied.add('group:manage')
    const w = render()
    await openDetail(w, 1)
    await w.get('[data-testid="group-detail-simulate"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="group-detail"]').exists()).toBe(false)
    expect(w.getComponent(PriceSimulatorModal).props()).toMatchObject({ open: true, preset: { groupId: 2 } })

    // Without price:read there is no quote to ask for (CONTRACTS §97.6): no entry.
    denied.add('price:read')
    const blind = render()
    await openDetail(blind, 0)
    expect(blind.find('[data-testid="group-detail-simulate"]').exists()).toBe(false)
    expect(blind.findComponent(PriceSimulatorModal).exists()).toBe(false)
  })

  it('warns beside the account count of a group without accounts, as an icon with the sentence behind it', () => {
    const [served, empty] = rows(render())
    expect(served.find('[data-testid="group-no-accounts"]').exists()).toBe(false)
    const count = empty.findAll('td')[5]
    expect(count.get('[data-testid="group-no-accounts"]').attributes('aria-label')).toBe('暂无账号，无法提供服务。')
    expect(count.text()).toBe('0')
  })

  it('shows the count and its warning on the cards of a phone too', () => {
    const w = mountPinned(GroupsView, options)
    expect(w.find('table').exists()).toBe(false)
    expect(w.findAll('[data-testid="group-no-accounts"]')).toHaveLength(1)
    expect(w.text()).toContain('账号数')
  })

  it('does not show the platforms served in the edit form', async () => {
    const w = render()
    await rows(w)[0]
      .findAll('button')
      .find(b => b.text() === '编辑')!
      .trigger('click')
    await flushPromises()
    expect(w.text()).toContain('编辑分组 default')
    const form = w.get('form')
    expect(form.text()).not.toContain('平台')
    expect(form.find('[data-testid="platform-badge"], [data-platform]').exists()).toBe(false)
  })

  it('shows in the detail the protocols the member accounts serve, as the server reports them, not the platforms', async () => {
    const w = render()
    const detail = await openDetail(w, 0)
    expect(get).toHaveBeenCalledWith('/groups/1')
    expect(detail.text()).not.toContain('平台')
    expect(detail.get('[data-testid="group-access"]').text()).toBe('所有人')
    expect(detail.get('[data-testid="group-pricing-summary"]').text()).toBe('全部用原价')
    expect(detail.find('[data-testid="group-no-accounts"]').exists()).toBe(false)
    expect(w.getComponent(GroupServedProtocols).props('served')).toEqual(served[1])
  })

  it('shows the warning and no endpoints in the detail of a group without accounts', async () => {
    const w = render()
    const detail = await openDetail(w, 1)
    expect(detail.get('[data-testid="group-no-accounts"]').attributes('aria-label')).toBe('暂无账号，无法提供服务。')
    expect(w.findComponent(GroupServedProtocols).exists()).toBe(false)
  })

  it('hands over an empty list when the accounts of the group serve nothing, and shows nothing until the detail is loaded', async () => {
    items.value = [group(3, 'orphans', { platforms: [] })]
    const w = render()
    await openDetail(w, 0)
    expect(w.getComponent(GroupServedProtocols).props('served')).toEqual([])
    get.mockRejectedValue(new Error('offline'))
    const offline = render()
    await openDetail(offline, 0)
    expect(offline.findComponent(GroupServedProtocols).exists()).toBe(false)
  })
})
