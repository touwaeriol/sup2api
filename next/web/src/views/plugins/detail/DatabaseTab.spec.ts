import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import DatabaseTab from './DatabaseTab.vue'
import type { PluginDetail } from '../pluginUtil'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { get: mocks.get } }))
enableAutoUnmount(afterEach)

const DATABASE = { prefix: 'plg_guard__', role: 'plg713988_guard', access: [], migrations: [] }
const detail = (over: Partial<PluginDetail> = {}): PluginDetail => ({ key: 'guard', display_id: 'guard', name: 'Guard', status: 'enabled', active_version: '0.1.0', ...over })
const render = (d = detail()) => mount(DatabaseTab, { props: { detail: d }, global: { plugins: [i18n] } })

describe('plugin database tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
  })

  it('shows why it could not load and loads again on refresh', async () => {
    mocks.get.mockRejectedValueOnce(new Error('connection refused'))
    const w = render()
    await flushPromises()
    const error = w.get('[data-testid="plugin-database-error"]')
    expect(error.text()).toContain('无法读取插件的数据库信息。')
    expect(error.text()).toContain('connection refused')
    mocks.get.mockResolvedValueOnce(DATABASE)
    await error.get('button').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="plugin-database-error"]').exists()).toBe(false)
    expect(w.get('[data-testid="plugin-database"]').text()).toContain('plg713988_guard')
  })

  it('copies the prefix and the identity from aligned value rows, the hints below them', async () => {
    mocks.get.mockResolvedValue(DATABASE)
    const w = render()
    await flushPromises()
    const values = w.findAll('dd')
    expect(values.map((v) => v.text())).toEqual(['plg_guard__', 'plg713988_guard'])
    expect(values.every((v) => v.find('button').exists())).toBe(true)
  })

  it('loads again when the version the plugin runs changes', async () => {
    mocks.get.mockResolvedValue(DATABASE)
    const w = render()
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledTimes(1)
    await w.setProps({ detail: detail() })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledTimes(1)
    await w.setProps({ detail: detail({ active_version: '0.2.0' }) })
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledTimes(2)
    expect(mocks.get).toHaveBeenLastCalledWith('/plugins/guard/database')
  })

  it('shows the latest load when an earlier one answers after it', async () => {
    let answerOld!: (v: unknown) => void
    let failOld!: (e: unknown) => void
    mocks.get.mockReturnValueOnce(new Promise((resolve) => (answerOld = resolve)))
    const w = render()
    mocks.get.mockResolvedValueOnce({ ...DATABASE, role: 'plg713988_guard_new' })
    await w.setProps({ detail: detail({ desired_version: '0.2.0' }) })
    await flushPromises()
    answerOld({ ...DATABASE, role: 'plg713988_guard_old' })
    await flushPromises()
    expect(w.get('[data-testid="plugin-database"]').text()).toContain('plg713988_guard_new')
    expect(w.text()).not.toContain('plg713988_guard_old')

    // An earlier load failing late neither hides the data nor shows its error.
    mocks.get.mockReturnValueOnce(new Promise((_, reject) => (failOld = reject)))
    await w.get('[data-testid="plugin-database"] button').trigger('click')
    mocks.get.mockResolvedValueOnce({ ...DATABASE, role: 'plg713988_guard_newer' })
    await w.setProps({ detail: detail({ desired_version: '0.2.0', active_version: '0.2.0' }) })
    await flushPromises()
    failOld(new Error('stale failure'))
    await flushPromises()
    expect(w.get('[data-testid="plugin-database"]').text()).toContain('plg713988_guard_newer')
    expect(w.text()).not.toContain('stale failure')
    expect(w.get('[data-testid="plugin-database"] button').attributes('disabled')).toBeUndefined()
  })
})
