import { describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { AccountType } from '@/api/types'

const types = vi.hoisted(() => ({ list: [] as unknown[] }))
vi.mock('./accountTypes', async original => ({
  ...(await original<typeof import('./accountTypes')>()),
  useAccountTypes: () => ({ grouped: ref([{ types: types.list }]), loaded: ref(true), load: () => undefined })
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (p: string | string[]) => [p].flat().includes('account:create:own'), hasAction: (a: string) => a === 'account:create', scopes: () => [] }) }))

import AccountTypePicker from './AccountTypePicker.vue'

describe('AccountTypePicker', () => {
  it('greys out a custom-upstream type for an own-level caller without account:relay', async () => {
    const base = { plugin_key: 'p', plugin_name: 'P', label: 'L', form: { mode: 'native', component: 'Form' }, platforms: [], endpoints: [], sensitive_fields: [] }
    types.list = [{ ...base, type: 'official', custom_upstream: false }, { ...base, type: 'relay', custom_upstream: true }] as unknown as AccountType[]
    const w = mount(AccountTypePicker, { global: { plugins: [i18n], stubs: { PluginAvatar: true, TrustBadge: true, AccountTypePlatforms: true, AccountTypeEndpoints: true } } })
    await flushPromises()
    const relay = w.get('[data-type="p/relay"]')
    expect(relay.attributes('aria-disabled')).toBe('true')
    expect(relay.find('[data-testid="type-needs-relay"]').exists()).toBe(true)
    await relay.trigger('click')
    expect(w.emitted('pick')).toBeUndefined()
    await w.get('[data-type="p/official"]').trigger('click')
    expect(w.emitted('pick')).toHaveLength(1)
  })

  it('opens a creation group on its first type the caller can create', async () => {
    const base = { plugin_key: 'q', plugin_name: 'Q', label: 'L', form: { mode: 'native', component: 'Form' }, platforms: [], endpoints: [], sensitive_fields: [], creation_group: 'g' }
    types.list = [{ ...base, type: 'custom', custom_upstream: true }, { ...base, type: 'official', custom_upstream: false }] as unknown as AccountType[]
    const w = mount(AccountTypePicker, { global: { plugins: [i18n], stubs: { PluginAvatar: true, TrustBadge: true, AccountTypePlatforms: true, AccountTypeEndpoints: true } } })
    await flushPromises()
    const card = w.get('[data-type="q/custom"]')
    expect(card.attributes('aria-disabled')).toBe('false')
    await card.trigger('click')
    expect((w.emitted('pick')![0][0] as AccountType).type).toBe('official')
  })
})
