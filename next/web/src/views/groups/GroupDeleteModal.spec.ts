import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { i18n } from '@/i18n'
import type { Group } from '@/api/types'
import GroupDeleteModal from './GroupDeleteModal.vue'

const { del, get } = vi.hoisted(() => ({ del: vi.fn(), get: vi.fn() }))
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { del, get } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => true }) }))
const groups = ref<Partial<Group>[]>([])
vi.mock('@/composables/lookups', () => ({ useGroupsLookup: () => ({ groups, ready: Promise.resolve() }) }))
enableAutoUnmount(afterEach)

const doomed = { id: 3, name: 'old', account_count: 2 } as Group
function render(open = true) {
  i18n.global.locale.value = 'zh'
  return mount(GroupDeleteModal, { props: { open, group: doomed, keyCount: 4 }, global: { plugins: [i18n], stubs: { Teleport: true } } })
}

describe('group delete with API keys', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    del.mockResolvedValue(null)
    groups.value = [{ id: 1, name: 'default', status: 'active' }, doomed]
    // The lookup the target is picked from.
    get.mockResolvedValue({ items: [{ id: 1, name: 'default', status: 'active' }, { id: 3, name: 'old', status: 'active' }], more: false })
  })

  it('moves the keys to the picked group (never the group itself)', async () => {
    const w = render()
    await flushPromises()
    expect(w.text()).toContain('该分组下还有 4 个 API Key')
    expect(w.text()).toContain('2 个账号只会移出分组')
    expect(w.get('[data-testid="group-delete-pricing"]').text()).toBe('该分组的模型价格，以及各用户组在它上面的开通和倍率，会一并删除。')
    await w.get('[data-testid="group-delete-confirm"]').trigger('click')
    expect(del).not.toHaveBeenCalled()
    expect(w.text()).toContain('必填')
    await w.get('[data-testid="group-delete-target"] input').trigger('focus')
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/lookup/groups', expect.anything())
    const rows = w.findAll('[role="option"]')
    expect(rows.map(o => o.text())).toEqual([expect.stringContaining('default')])
    await rows[0].trigger('click')
    await w.get('[data-testid="group-delete-confirm"]').trigger('click')
    await flushPromises()
    expect(del).toHaveBeenCalledWith('/groups/3', { move_keys_to: 1 })
    expect(w.emitted('deleted')).toHaveLength(1)
  })

  it('unbinds the keys when chosen, or when there is no other group', async () => {
    groups.value = [doomed]
    const w = render()
    await flushPromises()
    expect(w.find('[data-testid="group-delete-target"]').exists()).toBe(false)
    expect(w.text()).toContain('Key 保留但暂时不能调用')
    await w.get('[data-testid="group-delete-confirm"]').trigger('click')
    await flushPromises()
    expect(del).toHaveBeenCalledWith('/groups/3', undefined)
  })
})
