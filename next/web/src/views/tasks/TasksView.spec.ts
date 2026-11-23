import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import TasksView from './TasksView.vue'
import type { TasksOverview } from './tasks'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), toast: vi.fn(), run: true, manage: true }))
vi.mock('@sub2api/host', async (importOriginal) => ({ ...await importOriginal<typeof import('@sub2api/host')>(), api: { get: mocks.get, post: mocks.post, patch: mocks.patch } }))
vi.mock('@sub2api/ui', async (original) => ({ ...await original<object>(), toast: (...a: unknown[]) => mocks.toast(...a) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string) => p === 'task:run' ? mocks.run : p === 'task:manage' ? mocks.manage : true }) }))

const overview = (): TasksOverview => ({
  summary: { total: 3, running: 0, failing: 1, failed_24h: 2, leader: 'node-a' },
  cluster: [
    { name: 'core.account.usage_limits', title: { en: 'Usage limits', zh: '账号用量上限' }, source: 'core', plugin_key: null, schedule: '@every 1m0s', timeout_ms: 50000, max_attempts: 1, enabled: true, registered: true, running: false, lease_node: null,
      last: { node: 'node-a', status: 'succeeded', duration_ms: 95, count: 3 }, last_success_at: '2026-11-03T00:00:00Z', consecutive_failures: 0, next_run_at: '2026-11-03T00:01:00Z',
      recent: ['succeeded', 'skipped'], stats_24h: { runs: 1440, failed: 0, skipped: 2, avg_ms: 95, max_ms: 820 }, queued: 0 },
    { name: 'core.account.credential_refresh', title: { en: 'Credential refresh', zh: '刷新账号凭证' }, source: 'core', plugin_key: null, schedule: '@every 1m0s', timeout_ms: 50000, max_attempts: 1, enabled: true, registered: true, running: false, lease_node: null,
      last: { node: 'node-b', status: 'failed', duration_ms: 120, count: 0, error: 'upstream down' }, last_success_at: null, consecutive_failures: 2, next_run_at: null,
      recent: ['failed', 'failed'], stats_24h: { runs: 10, failed: 2, skipped: 0, avg_ms: 120, max_ms: 200 }, queued: 0 },
    { name: 'plugin.dynweight.evaluate', title: { en: 'Dynamic weight · evaluate', zh: '动态权重 · evaluate' }, source: 'plugin_manifest', plugin_key: 'dynweight', schedule: '@every 10s', timeout_ms: 60000, max_attempts: 1, enabled: true, registered: true, running: false, lease_node: null,
      last: null, last_success_at: null, consecutive_failures: 0, next_run_at: null, recent: [], stats_24h: { runs: 0, failed: 0, skipped: 0, avg_ms: 0, max_ms: 0 }, queued: 0 }
  ],
  nodes: [
    { node_id: 'node-a', alive: true, tasks: [{ name: 'core.authz.version_poll', title: { en: 'Permission version', zh: '刷新权限版本' }, schedule: '@every 30s', timeout_ms: 10000, require_work: false, running: false, last_finished_at: '2026-11-03T00:00:00Z', duration_ms: 3, status: 'succeeded', count: 0, consecutive_failures: 0, skipped: 0, recent: ['succeeded'], failed_24h: 0 }] },
    { node_id: 'node-b', alive: false, tasks: [] }
  ]
})

async function render(query: Record<string, string> = {}) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/system/tasks', component: TasksView }] })
  await router.push({ path: '/system/tasks', query })
  const wrapper = mount(TasksView, { global: { plugins: [i18n, router] }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

describe('scheduled tasks page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.run = true
    mocks.manage = true
    mocks.patch.mockResolvedValue({ name: 'plugin.dynweight.evaluate', enabled: false })
    i18n.global.locale.value = 'zh'
    mocks.get.mockImplementation(async (path: string) => {
      if (path === '/system/tasks') return overview()
      return { name: 'x', scope: 'cluster', schedule: '', timeout_ms: 1000, cluster: overview().cluster[0] }
    })
    mocks.post.mockResolvedValue({ submitted: true })
  })

  it('shows the overview and the cluster tasks, failing ones first', async () => {
    const wrapper = await render()
    expect(mocks.get).toHaveBeenCalledWith('/system/tasks')
    expect(wrapper.text()).toContain('当前主节点')
    expect(wrapper.text()).toContain('node-a')
    const rows = wrapper.get('[data-testid="tasks-cluster"]').findAll('tbody tr')
    expect(rows[0].text()).toContain('刷新账号凭证')
    expect(rows[0].text()).toContain('失败')
    expect(rows[1].text()).toContain('账号用量上限')
    expect(rows[1].text()).toContain('每 1 分钟')
    expect(rows[0].get('[data-testid="task-failures"]').classes()).toContain('text-danger-600')
    const squares = rows[1].get('[data-testid="task-recent"]').findAll('[data-status]')
    expect(squares.map((s) => s.attributes('data-status'))).toEqual(['succeeded', 'skipped'])
    wrapper.unmount()
  })

  it('runs a cluster task now', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="task-run-core.account.usage_limits"]').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/system/tasks/core.account.usage_limits/run', {})
    expect(wrapper.get('[data-testid="task-run-core.account.usage_limits"]').text()).toBe('已提交')
    wrapper.unmount()
  })

  it('switches a plugin task off; core tasks have no switch', async () => {
    const wrapper = await render()
    expect(wrapper.find('[data-testid="task-enabled-core.account.usage_limits"]').exists()).toBe(false)
    const sw = wrapper.get('[data-testid="task-enabled-plugin.dynweight.evaluate"]')
    await sw.setValue(false)
    await flushPromises()
    expect(mocks.patch).toHaveBeenCalledWith('/system/tasks/plugin.dynweight.evaluate', { enabled: false })
    expect(mocks.toast).toHaveBeenCalledWith(expect.stringContaining('已停用'), 'success')
    wrapper.unmount()
  })

  it('has no switch without task:manage', async () => {
    mocks.manage = false
    const wrapper = await render()
    expect(wrapper.find('[data-testid="task-enabled-plugin.dynweight.evaluate"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('hides the run buttons without task:run', async () => {
    mocks.run = false
    const wrapper = await render()
    expect(wrapper.find('[data-testid="task-run-core.account.usage_limits"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('translates a refused run', async () => {
    mocks.post.mockRejectedValue(new ApiError(409, { code: 'node_offline', message: 'the node is not online' }))
    const wrapper = await render()
    await wrapper.get('[data-testid="task-run-core.account.usage_limits"]').trigger('click')
    await flushPromises()
    expect(mocks.toast).toHaveBeenCalledWith('这个节点不在线', 'error')
    wrapper.unmount()
  })

  it('shows node tasks as a task × node matrix with offline nodes dimmed', async () => {
    const wrapper = await render({ tab: 'nodes' })
    const table = wrapper.get('[data-testid="tasks-nodes"]')
    expect(table.text()).toContain('刷新权限版本')
    expect(table.text()).toContain('每 30 秒')
    expect(table.find('[data-testid="task-cell-core.authz.version_poll-node-a"]').exists()).toBe(true)
    expect(table.text()).toContain('离线')
    wrapper.unmount()
  })

  it('reloads the tasks from the refresh button beside the interval picker', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="auto-refresh-now"]').trigger('click')
    await flushPromises()
    expect(mocks.get.mock.calls.filter(([path]) => path === '/system/tasks')).toHaveLength(2)
    wrapper.unmount()
  })

  it('shows empty tables without tasks', async () => {
    mocks.get.mockResolvedValue({ summary: { total: 0, running: 0, failing: 0, failed_24h: 0, leader: '' }, cluster: [], nodes: [] })
    const wrapper = await render()
    expect(wrapper.text()).toContain('没有全集群任务')
    expect(wrapper.text()).toContain('无')
    wrapper.unmount()
  })
})
