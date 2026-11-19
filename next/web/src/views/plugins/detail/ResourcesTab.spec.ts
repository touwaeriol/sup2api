import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import ResourcesTab from './ResourcesTab.vue'
import type { PluginDetail } from '../pluginUtil'

const mocks = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('@sub2api/host', async (importOriginal) => ({ ...await importOriginal<typeof import('@sub2api/host')>(), api: { put: mocks.put } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))

async function render(memory: { override: number; effective: number }, accountTypes: unknown[] = []) {
  const detail = {
    key: 'demo',
    manifest: { account_types: accountTypes },
    resources: {
      requested: { memory_mb: 128, cpu: 0.5 },
      overrides: { memory_mb: memory.override },
      effective: { memory_mb: memory.effective, cpu: 0.5 }
    }
  } as unknown as PluginDetail
  const wrapper = mount(ResourcesTab, { props: { detail }, global: { plugins: [i18n] } })
  await flushPromises()
  return wrapper
}

describe('plugin resources tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
  })

  it('shows memory as unlimited by default: the manifest figure is a suggestion, not prefilled', async () => {
    const w = await render({ override: 0, effective: 0 })
    const input = w.get<HTMLInputElement>('#plugin-resource-memory_mb')
    expect(input.element.value).toBe('')
    expect(input.attributes('placeholder')).toBe('不限')
    expect(w.text()).toContain('插件建议 128 MB，这不是限制。当前限制：不限。')
    expect(w.text()).not.toContain('平台插件')
    // Other limits still start from the manifest request.
    expect(w.get<HTMLInputElement>('#plugin-resource-cpu').element.value).toBe('0.5')
  })

  it('shows the operator limit, and the sizing hint for a platform plugin', async () => {
    const w = await render({ override: 512, effective: 512 }, [{ id: 'apikey', platforms: ['video'] }])
    expect(w.get<HTMLInputElement>('#plugin-resource-memory_mb').element.value).toBe('512')
    expect(w.text()).toContain('当前限制：512 MB')
    expect(w.text()).toContain('平台插件每个并发的大请求约需 3 × 请求体上限 + 64 MB。')
  })

  it('saves an empty memory as no limit', async () => {
    mocks.put.mockResolvedValue({})
    const w = await render({ override: 0, effective: 0 })
    await w.findAll('button').find((b) => b.text() === i18n.global.t('common.save'))!.trigger('click')
    await flushPromises()
    expect(mocks.put.mock.calls[0][1].memory_mb).toBeNull()
  })
})
