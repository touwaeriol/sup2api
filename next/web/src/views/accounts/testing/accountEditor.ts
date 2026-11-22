// Shared by the specs of the account editor (AccountEditor.*.spec.ts): the
// mocked host, stores and scope, the account type and the saved account they
// start from, and how the editor is mounted with its sections rendered.
// Import it before the editor.
import { afterEach, beforeEach, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { STabbedForm, type FormTab, type TabbedForm } from '@sub2api/ui'
import ProxyPicker from '@/components/ProxyPicker.vue'
import { ownership, perms, pluginStore } from './editorState'
import AccountEditor from '../AccountEditor.vue'

// The factories run before this module's imports are bound: what they read at once comes by a dynamic import.
vi.mock('@sub2api/host', async (original) => ({ ...await original<object>(), api: (await import('./editorState')).mocks }))
vi.mock('@sub2api/ui', async (original) => ({ ...await original<typeof import('@sub2api/ui')>(), toast: (await import('./editorState')).mocks.toast }))
vi.mock('@/stores/plugins', () => ({ usePluginStore: () => pluginStore, assetURL: () => '' }))
// Keys compare in their canonical spelling (CONTRACTS §21).
vi.mock('@/stores/auth', async () => {
  const { canonicalPermission } = await import('@/authz/implies')
  const canonical = (k: string) => canonicalPermission(k) ?? k
  return { useAuthStore: () => ({ has: (p: string | string[]) => [p].flat().some(k => !perms.denied.map(canonical).includes(canonical(k))), scopes: () => [] }) }
})
vi.mock('@/composables/useScope', async original => ({ ...await original<typeof import('@/composables/useScope')>(), useScope: () => ({ can: () => true, level: () => ownership.scope }) }))
vi.mock('@/composables/lookups', async (original) => ({ ...await original<object>(), useProxiesLookup: () => ({}) }))

enableAutoUnmount(afterEach)
// The plugin's native credentials form; each test starts with it registered.
export const KeyForm = { name: 'KeyForm', template: '<div />' }
beforeEach(() => {
  Object.assign(pluginStore, { listed: true, pending: {}, errors: {}, component: () => KeyForm })
})
export const accountType = { plugin_key: 'claude', type: 'apikey', creation_group: 'claude', form: { mode: 'native', component: 'KeyForm' }, platforms: [], default_models: [], default_model_mapping: {} } as unknown as AccountType

/** The editor's side tabs: their keys, labels and summaries, the one in front and the ones marked for an error. */
export const nav = (w: ReturnType<typeof shallowMount>) => w.getComponent(STabbedForm)
export const sections = (w: ReturnType<typeof shallowMount>) => nav(w).props('tabs') as FormTab[]
export const marked = (w: ReturnType<typeof shallowMount>) => (nav(w).props('form') as TabbedForm).errorTabs.value
export const sectionOf = (el: Element) => el.closest('[data-tab]')?.getAttribute('data-tab')

export function render(account?: Account, realSelect = false, type: AccountType = accountType) {
  return shallowMount(AccountEditor, { props: { accountType: type, account }, global: { plugins: [i18n], stubs: {
    // The tabs, their panels and the sections are rendered (not stubbed); the models card so its tag select is reachable.
    STabbedForm: false,
    STitleScope: false,
    AccountProxyField: false,
    AccountCredentialsCard: false,
    AccountSchedulingPane: false,
    AccountForwardingPane: false,
    AccountModelsCard: false,
    RateLimitEditor: false,
    SessionLimitInput: false,
    HeaderPassthroughEditor: false,
    SCard: { template: '<div><slot name="title" /><slot name="actions" /><slot /></div>' },
    SSettingRow: { template: '<div><slot /></div>' },
    SSettingsGroup: { template: '<div><slot /></div>' },
    SField: { props: ['error'], template: '<div><slot /><p v-if="error">{{ error }}</p><slot v-else name="hint" /></div>' },
    SHint: { template: '<p><slot /></p>' },
    SAlert: { props: ['title'], template: '<div><b v-if="title">{{ title }}</b><slot /></div>' },
    SSegmented: false,
    SSelect: realSelect ? false : { props: ['modelValue', 'options', 'disabled', 'searchable'], emits: ['update:modelValue'], template: '<input :value="modelValue" :disabled="disabled" @input="$emit(\'update:modelValue\', $event.target.value)" />' },
    // Like SInput, type="number" emits a number (null when empty).
    SInput: { props: ['modelValue', 'type'], emits: ['update:modelValue'], template: '<input :type="type" :value="modelValue" @input="$emit(\'update:modelValue\', type === \'number\' ? ($event.target.value === \'\' ? null : Number($event.target.value)) : $event.target.value)" />' },
    // Mounts a native form the way plugin pages do: rendered for real.
    PluginComponent: false,
    // A native form whose expose (validate) the editor calls.
    ValidatingForm: false,
  } } })
}

export function savedAccount(): Account {
  return { id: 1, name: 'saved', proxy_id: 10, settings: {}, credentials: {}, group_ids: [], models: [], model_mapping: {} } as unknown as Account
}

export async function chooseProxy(w: ReturnType<typeof render>, id: number | null) {
  w.getComponent(ProxyPicker).vm.$emit('update:modelValue', id)
  await flushPromises()
}

