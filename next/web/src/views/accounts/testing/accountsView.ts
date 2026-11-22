// Shared by the specs of the accounts list (AccountsView.*.spec.ts): the
// mocked host, stores and lookups, the list the view reads, and how it is
// mounted. Import it before the view.
import { afterEach, vi } from 'vitest'
import { enableAutoUnmount, shallowMount, type DOMWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import type { Account } from '@/api/types'
import { i18n } from '@/i18n'
import { list } from './accountsState'
import AccountsView from '../AccountsView.vue'

// The factories run before this module's imports are bound: what they read at once comes by a dynamic import.
vi.mock('@sub2api/host', async (original) => {
  const { mocks } = await import('./accountsState')
  return { ...await original<typeof import('@sub2api/host')>(), api: { get: mocks.get, post: mocks.post, patch: mocks.patch, list: async () => ({ items: [] }) } }
})
vi.mock('@sub2api/ui', async (original) => {
  const { mocks } = await import('./accountsState')
  return { ...await original<typeof import('@sub2api/ui')>(), confirm: mocks.confirm, toast: mocks.toast }
})
vi.mock('@/utils/errors', async () => ({ notifyError: (await import('./accountsState')).mocks.notifyError, errorMessage: String }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => false, hasAction: () => false }) }))
vi.mock('@/stores/plugins', async () => {
  const { mocks } = await import('./accountsState')
  return { usePluginStore: () => ({ slotEntries: mocks.slotEntries, get accountActions() { return mocks.accountActions }, plugin: () => undefined, component: () => undefined }) }
})
vi.mock('@/composables/lookups', async (original) => {
  const { mocks } = await import('./accountsState')
  return { ...await original<object>(), useGroupsLookup: () => ({ groups: ref(mocks.groups) }), useProxiesLookup: () => ({ proxies: ref([]) }) }
})
vi.mock('@/composables/useScope', async (original) => {
  const { mocks } = await import('./accountsState')
  return { ...await original<typeof import('@/composables/useScope')>(), useScope: () => ({ level: () => mocks.scope, can: (_a: Account, action: string) => action === 'account:update' && mocks.canUpdate }) }
})
vi.mock('../accountTypes', async () => {
  const { mocks } = await import('./accountsState')
  return { typeKey: () => '', useAccountTypes: () => ({ load: vi.fn(), find: mocks.findType, grouped: ref([]), types: ref([]), pluginName: (key: string) => key, typeLabel: (_key: string, type: string) => type }) }
})
vi.mock('@/composables/useList', async (original) => ({ ...await original<object>(), useList: () => list }))

enableAutoUnmount(afterEach)

/** Toggles a switch: SSwitch is a checkbox (jsdom only activates connected ones on click), stubs are buttons. */
export const flip = (el: Omit<DOMWrapper<Element>, 'exists'>) => (el.element instanceof HTMLInputElement ? el.setValue(!el.element.checked) : el.trigger('click'))

export function render(stubs: Record<string, unknown> = {}) {
  return shallowMount(AccountsView, { global: { plugins: [i18n], stubs: {
    STable: { props: ['rows'], template: '<div><div v-for="row in rows" :key="row.id"><slot name="cell-status" :row="row" /><slot name="cell-actions" :row="row" /></div></div>' },
    SLink: { template: '<button><slot /></button>' },
    SBadge: { template: '<span><slot /></span>' },
    SHint: { template: '<span><slot /></span>' },
    STooltip: { props: ['content', 'disabled'], template: '<span :data-tip="disabled ? undefined : content"><slot /></span>' },
    SSwitch: { props: ['modelValue'], emits: ['update:modelValue'], template: '<button role="switch" :aria-checked="modelValue" @click="$emit(\'update:modelValue\', !modelValue)" />' },
    ...stubs
  } } })
}

/** The reason of the status badge: its tooltip (hover or keyboard focus), CONTRACTS §73.4. */
export const statusTip = (w: ReturnType<typeof render>) => w.get('[data-tip]').attributes('data-tip')
/** The list with its row buttons rendered (text and attributes), for the enable / disable button. */
export const renderRow = () => render({ SButton: { template: '<button><slot /></button>' } })
/** The ?tz= every request carrying usage sends. */
export const today = { query: { tz: Intl.DateTimeFormat().resolvedOptions().timeZone } }
