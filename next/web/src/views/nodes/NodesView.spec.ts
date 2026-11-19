import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import NodesView from './NodesView.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), list: vi.fn() }))
const refresh = vi.hoisted(() => ({ load: (() => undefined) as () => unknown }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
vi.mock('@sub2api/ui', async orig => ({ ...(await orig<object>()), useAutoRefresh: (load: () => unknown) => { refresh.load = load } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => p === 'system:update:read' }) }))
enableAutoUnmount(afterEach)

const heartbeat = new Date().toISOString()
const nodes = ['node-1', 'node-2'].map((node_id, i) => ({ node_id, boot_id: `boot-${i}`, addr: '127.0.0.1', host_version: '0.1.290', started_at: heartbeat, last_heartbeat: heartbeat, plugins: {} }))
let shells: () => Promise<unknown>

beforeEach(() => {
  i18n.global.locale.value = 'zh'
  shells = () => Promise.resolve({ nodes: [{ node_id: 'node-1', gateway_version: '0.1.135' }, { node_id: 'node-2', gateway_version: '' }] })
  api.list.mockReset().mockResolvedValue({ items: nodes })
  api.get.mockReset().mockImplementation((path: string) => (path === '/system/upgrades' ? shells() : Promise.reject(new Error(path))))
})

async function render() {
  const w = mount(NodesView, { global: { plugins: [i18n], stubs: { TopologyGraph: true } } })
  await flushPromises()
  return w
}
const upgradeCalls = () => api.get.mock.calls.filter(([path]) => path === '/system/upgrades').length

describe('nodes view', () => {
  it('shows what the plugin sandbox of each node enforces', async () => {
    const partial = { wrapped: true, landlock: true, landlock_abi: 2, active: ['fs'], missing: ['fs_truncate', 'tcp', 'scope', 'unix', 'udp'] }
    api.list.mockResolvedValue({ items: [{ ...nodes[0], plugin_sandbox: partial }, nodes[1]] })
    const w = await render()
    const cells = w.findAll('[data-testid="node-sandbox"]')
    expect(cells.map(c => c.text())).toEqual(['缺 5 层', '—'])
    expect(cells[0].attributes('title')).toContain('路径型 unix socket（Linux 7.1）')
  })

  it('counts gateway versions without the nodes that report none', async () => {
    const w = await render()
    expect(w.text()).toContain('核心 1 · 网关 1')
    expect(w.text()).not.toContain('各节点版本不一致')
    expect(w.findAll('[data-testid="node-gateway-version"]').map(c => c.text())).toEqual(['0.1.135', '未知'])
  })

  it('hides the gateway column when its versions cannot be read and asks again only on a manual refresh', async () => {
    shells = () => Promise.reject(new Error('This node is not managed by the gateway'))
    const w = await render()
    expect(w.text()).not.toContain('网关版本')
    expect(w.find('[data-testid="node-gateway-version"]').exists()).toBe(false)
    await refresh.load()
    expect(upgradeCalls()).toBe(1)

    shells = () => Promise.resolve({ nodes: [{ node_id: 'node-1', gateway_version: '0.1.135' }] })
    await w.get('button').trigger('click')
    await flushPromises()
    expect(upgradeCalls()).toBe(2)
    expect(w.text()).toContain('网关版本')
  })

  it('keeps the last gateway versions through a later failure', async () => {
    const w = await render()
    shells = () => Promise.reject(new Error('gateway restarting'))
    await refresh.load()
    await flushPromises()
    expect(upgradeCalls()).toBe(2)
    expect(w.findAll('[data-testid="node-gateway-version"]').map(c => c.text())).toEqual(['0.1.135', '未知'])
  })
})
