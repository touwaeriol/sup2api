import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import UninstallModal from './UninstallModal.vue'

vi.mock('@sub2api/host', async (original) => ({ ...(await original<object>()), api: { del: vi.fn() } }))
enableAutoUnmount(afterEach)

const render = (hasDatabase: boolean) =>
  mount(UninstallModal, { props: { open: true, pluginKey: 'guard', displayId: 'guard', name: 'Guard', hasDatabase }, global: { plugins: [i18n], stubs: { teleport: true } } })

describe('uninstall dialog', () => {
  it('names the tables purging drops and says the database identity goes either way', () => {
    i18n.global.locale.value = 'zh'
    const text = render(true).get('[data-testid="uninstall-purge"]').text()
    expect(text).toContain('清除数据：删除插件的表（plg_guard__ 开头）和 KV 数据')
    expect(text).toContain('插件的数据库身份不论勾选与否都会删除，重新安装时重建。')
  })

  it('speaks of the KV data alone for a plugin without a database', () => {
    i18n.global.locale.value = 'zh'
    const text = render(false).get('[data-testid="uninstall-purge"]').text()
    expect(text).toContain('清除数据：删除插件的 KV 数据')
    expect(text).not.toContain('表')
    expect(text).not.toContain('数据库身份')
  })
})
