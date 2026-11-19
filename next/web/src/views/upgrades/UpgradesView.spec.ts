import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { i18n } from '@/i18n'
import { updateCheck } from '@/stores/__fixtures__/updateCheck'
import { useUpdatesStore } from '@/stores/updates'
import UpgradesView from './UpgradesView.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
const refresh = vi.hoisted(() => ({ load: (() => undefined) as () => unknown }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
vi.mock('@sub2api/ui', async orig => ({ ...(await orig<object>()), useAutoRefresh: (load: () => unknown) => { refresh.load = load } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
enableAutoUnmount(afterEach)

let status = 'running'
beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'zh'
  status = 'running'
  const plan = () => ({ id: 'p1', release_digest: 'd1', status, cursor: 0, nodes: [], created_at: '2026-11-16T08:00:00Z' })
  const answers: Record<string, () => unknown> = {
    '/system/releases': () => ({ releases: [] }),
    '/system/upgrades': () => ({ upgrades: [plan()], nodes: [], revision: 1, primary_node: 'n1' }),
    '/system/upgrades/p1': plan,
    '/system/upgrades/p1/events': () => ({ events: [] }),
    '/system/update-check': () => updateCheck()
  }
  api.get.mockReset().mockImplementation((path: string) => Promise.resolve(answers[path]()))
})

async function render() {
  mount(UpgradesView, { global: { plugins: [i18n], stubs: { TopologyGraph: true, ReleaseHistory: true, StepLanes: true } } })
  await flushPromises()
}
const checks = () => api.get.mock.calls.filter(([path]) => path === '/system/update-check').length

describe('upgrades view', () => {
  it('reads the update check again once the active plan ends', async () => {
    useUpdatesStore().result = updateCheck({ core: { has_update: true } })
    await render()
    await refresh.load()
    expect(checks()).toBe(0)
    status = 'completed'
    await refresh.load()
    await flushPromises()
    expect(checks()).toBe(1)
    expect(useUpdatesStore().available).toBe(false)
  })

  it('renders on a console opened over plain HTTP (no crypto.randomUUID)', async () => {
    vi.stubGlobal('crypto', { getRandomValues: crypto.getRandomValues.bind(crypto) })
    try {
      await render()
    } finally {
      vi.unstubAllGlobals()
    }
    expect(api.get).toHaveBeenCalledWith('/system/upgrades')
  })

  it('leaves an update check nobody loaded alone', async () => {
    await render()
    status = 'completed'
    await refresh.load()
    await flushPromises()
    expect(checks()).toBe(0)
  })
})
