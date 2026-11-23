import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, reactive } from 'vue'
import { SSpinner } from '@sub2api/ui'
import { i18n } from '@/i18n'
import PluginComponent from './PluginComponent.vue'

// The plugin store as PluginComponent reads it; refresh() is the console's
// reload of the plugin list and the native modules not loaded.
const store = vi.hoisted(() => ({
  state: null as unknown as { listed: boolean; pending: Record<string, boolean>; errors: Record<string, string>; mismatch: Record<string, 'plugin' | 'core'>; components: Record<string, unknown> },
  refresh: vi.fn()
}))
vi.mock('@/stores/plugins', () => ({
  usePluginStore: () => ({
    get listed() { return store.state.listed },
    get pending() { return store.state.pending },
    get errors() { return store.state.errors },
    uiMismatch: (key: string) => store.state.mismatch[key] ?? null,
    component: (key: string, name?: string) => (key === 'mailer' && name ? store.state.components[name] : undefined),
    refresh: store.refresh
  })
}))
enableAutoUnmount(afterEach)

const Settings = defineComponent({ name: 'MailerSettings', props: ['who'], setup: (p) => () => h('p', { 'data-testid': 'mailer' }, `settings of ${p.who}`) })

function render() {
  return mount(PluginComponent, {
    props: { pluginKey: 'mailer', name: 'MailerSettings' },
    slots: { default: ({ component }: { component: unknown }) => h(component as never, { who: 'mailer' }) },
    global: { plugins: [i18n] }
  })
}

describe('PluginComponent', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'en'
    store.state = reactive({ listed: true, pending: {}, errors: {}, mismatch: {}, components: {} })
  })

  it('waits for the plugin list and the plugin module, then mounts the component through the slot', async () => {
    store.state.listed = false
    const w = render()
    expect(w.findComponent(SSpinner).exists()).toBe(true)
    store.state.listed = true
    store.state.pending.mailer = true
    await flushPromises()
    expect(w.findComponent(SSpinner).exists()).toBe(true)
    store.state.components.MailerSettings = Settings
    delete store.state.pending.mailer
    await flushPromises()
    expect(w.findComponent(SSpinner).exists()).toBe(false)
    expect(w.get('[data-testid="mailer"]').text()).toBe('settings of mailer')
  })

  it('says why the module failed to load and loads it again on Retry', async () => {
    store.state.errors.mailer = 'entry does not export register(host)'
    store.refresh.mockImplementation(async () => {
      delete store.state.errors.mailer
      store.state.components.MailerSettings = Settings
    })
    const w = render()
    expect(w.get('[data-testid="plugin-component-failed"]').text()).toContain('could not be loaded: entry does not export register(host)')
    await w.get('[data-testid="plugin-component-retry"]').trigger('click')
    await flushPromises()
    expect(store.refresh).toHaveBeenCalledTimes(1)
    expect(w.find('[data-testid="plugin-component-failed"]').exists()).toBe(false)
    expect(w.find('[data-testid="mailer"]').exists()).toBe(true)
  })

  it('asks to upgrade a plugin whose module was built for an older UI contract, without Retry', () => {
    store.state.errors.mailer = 'hostUICompat ^3.20 does not match host UI 4.2.0'
    store.state.mismatch.mailer = 'plugin'
    const w = render()
    expect(w.get('[data-testid="plugin-component-failed"]').text()).toBe("This plugin version's interface is no longer supported. Upgrade the plugin.")
    expect(w.find('[data-testid="plugin-component-retry"]').exists()).toBe(false)
  })

  it('asks to upgrade the core for a module built for a newer UI contract, without Retry', () => {
    store.state.errors.mailer = 'hostUICompat ^4.3 does not match host UI 4.2.0'
    store.state.mismatch.mailer = 'core'
    const w = render()
    expect(w.get('[data-testid="plugin-component-failed"]').text()).toBe("This plugin's interface needs a newer console. Upgrade the core.")
    expect(w.find('[data-testid="plugin-component-retry"]').exists()).toBe(false)
  })

  it('names a component the plugin did not register', () => {
    const w = render()
    expect(w.get('[data-testid="plugin-component-failed"]').text()).toContain('did not register component "MailerSettings"')
  })

  it('keeps a component that fails while rendering inside its box and mounts it again on Retry', async () => {
    let fail = true
    store.state.components.MailerSettings = defineComponent({
      setup: () => () => {
        if (fail) throw new Error('boom')
        return h('p', { 'data-testid': 'mailer' }, 'ok')
      }
    })
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    const w = render()
    await flushPromises()
    expect(w.get('[data-testid="plugin-component-failed"]').text()).toContain('The plugin UI failed: boom')
    fail = false
    await w.get('[data-testid="plugin-component-retry"]').trigger('click')
    await flushPromises()
    expect(store.refresh).not.toHaveBeenCalled()
    expect(w.get('[data-testid="mailer"]').text()).toBe('ok')
    errors.mockRestore()
  })
})
