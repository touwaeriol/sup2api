import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { provideHost, type ApiClient, type HostContext } from '@sub2api/host'
import { SInput, SSettingRow, SSettingsGroup, SSettingsPage, usePluginSettings } from '@sub2api/ui'
import { i18n } from '@/i18n'
import SettingsTab from './SettingsTab.vue'

// Native settings without a page (ui.settings {mode: "native", component}):
// the settings tab mounts the component the plugin registered, written with
// the UI SDK the way a plugin's ui/native writes it; the component reads and
// saves the settings itself and decides who may edit them.
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => false, hasDynamic: () => false }) }))
const api = { get: vi.fn(), put: vi.fn() }
const MailerSettings = defineComponent({
  setup() {
    const settings = usePluginSettings({ pluginKey: 'mailer', api: api as unknown as ApiClient, rules: { host: { max: 253 } } })
    return () => h(SSettingsPage, { settings }, () => h(SSettingsGroup, { title: 'SMTP' }, () => h(SSettingRow, { prop: 'host', label: 'Host' }, () => h(SInput, settings.bind('host')))))
  }
})
vi.mock('@/stores/plugins', () => ({
  usePluginStore: () => ({ listed: true, pending: {}, errors: {}, component: (key: string, name?: string) => (key === 'mailer' && name === 'MailerSettings' ? MailerSettings : undefined), refresh: vi.fn() })
}))
enableAutoUnmount(afterEach)

function host(granted: string[]) {
  provideHost({
    permissions: { superuser: () => false, has: (k: string) => granted.includes(k), any: (...k: string[]) => k.some((x) => granted.includes(x)) },
    i18n: { t: (k: string) => k }
  } as unknown as HostContext)
}

async function render() {
  const w = mount(SettingsTab, {
    props: { pluginKey: 'mailer', settings: { mode: 'native', component: 'MailerSettings', values: {} } },
    global: { plugins: [i18n] }
  })
  await flushPromises()
  return w
}

describe('settings tab: native settings without a page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'en'
    api.get.mockResolvedValue({ values: { host: 'smtp.example.com' }, permission: 'plugin.mailer:settings:manage' })
  })

  it("mounts the plugin's settings component, with its own save bar and no console one", async () => {
    host(['plugin:manage'])
    const w = await render()
    expect(api.get).toHaveBeenCalledWith('/plugins/mailer/settings')
    expect(w.findAll('[data-testid="plugin-settings"]')).toHaveLength(1)
    expect((w.get('input').element as HTMLInputElement).value).toBe('smtp.example.com')
    expect(w.get('input').attributes('disabled')).toBeUndefined()
    expect(w.findAll('[data-testid="settings-save-bar"]')).toHaveLength(1)
  })

  it('is read-only for a caller who may not edit the settings', async () => {
    host(['plugin:read'])
    const w = await render()
    expect((w.get('input').element as HTMLInputElement).value).toBe('smtp.example.com')
    expect(w.get('fieldset').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="settings-save-bar"]').exists()).toBe(false)
  })

  it("lets holders of the plugin's settings permission edit", async () => {
    host(['plugin.mailer:settings:manage'])
    const w = await render()
    expect(w.get('fieldset').attributes('disabled')).toBeUndefined()
    expect(w.find('[data-testid="settings-save-bar"]').exists()).toBe(true)
  })
})
