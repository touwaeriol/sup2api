import { describe, expect, it } from 'vitest'
import { fakeHost, mounted, type M } from './testHost'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ApiError, type ApiClient } from '@sub2api/host'
import SSecretInput from '../SSecretInput.vue'
import SSettingRow from '../SSettingRow.vue'
import SSettingsGroup from '../SSettingsGroup.vue'
import SInput from '../SInput.vue'
import { confirmState, settleConfirm, toastState } from '../feedback'
import { createMemoryHistory, createRouter, RouterView, useRoute } from 'vue-router'
import { usePluginSettings, type PluginSettingsForm } from './pluginSettings'
import SSettingsPage from './SSettingsPage.vue'
import { SECRET_MASK } from './rules'

/** A fake host.api for GET / PUT /plugins/demo/settings. */
function settingsApi(view: M, put: (body: M) => Promise<unknown> = async () => null, secrets: string[] = ['password']) {
  const calls: M[] = []
  let reads = 0
  const api = {
    get: async (path: string) => {
      expect(path).toBe('/plugins/demo/settings')
      reads++
      return JSON.parse(JSON.stringify(view))
    },
    put: async (path: string, body: M) => {
      expect(path).toBe('/plugins/demo/settings')
      calls.push(body)
      const out = await put(body)
      // Stored; secrets read back masked.
      view.values = Object.fromEntries(Object.entries(body.values).map(([k, v]) => [k, secrets.includes(k) && v ? SECRET_MASK : v]))
      return out
    }
  } as unknown as ApiClient
  return { api, calls, reads: () => reads }
}

function settingsPage(api: ApiClient, defaults: M = { port: 587, from_name: 'Sub2API' }) {
  let settings!: PluginSettingsForm<M>
  const w = mount(
    defineComponent({
      setup() {
        settings = usePluginSettings<M>({ pluginKey: 'demo', api, defaults, rules: { host: { max: 253 }, port: { required: true, type: 'integer', min: 1, max: 65535 } } })
        return () =>
          h(SSettingsPage, { settings, title: 'Mailer' }, () =>
            h(SSettingsGroup, { title: 'SMTP' }, () => [
              h(SSettingRow, { prop: 'host', label: 'Host' }, () => h(SInput, settings.bind('host'))),
              h(SSettingRow, { prop: 'port', label: 'Port' }, () => h(SInput, { type: 'number', ...settings.bind('port') })),
              h(SSettingRow, { prop: 'password', label: 'Password' }, () => h(SSecretInput, { ...settings.bind('password'), clearable: true }))
            ])
          )
      }
    }),
    { attachTo: document.body }
  )
  mounted.push(w)
  return { w, settings: () => settings }
}

describe('usePluginSettings / SSettingsPage', () => {
  it('loads, fills defaults into the baseline, tracks unsaved changes, saves the whole value', async () => {
    fakeHost(['plugin:manage'])
    const { api, calls } = settingsApi({ values: { host: 'smtp.example.com', password: SECRET_MASK, extra: { keep: 1 } } })
    const { w, settings } = settingsPage(api)
    await flushPromises()
    expect(settings().get('port')).toBe(587)
    expect(settings().dirty.value).toBe(false)
    expect(w.find('[data-testid="settings-save"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="plugin-settings-dirty"]').exists()).toBe(false)
    await w.find('input').setValue('mail.example.com')
    await flushPromises()
    expect(settings().dirty.value).toBe(true)
    expect(w.find('[data-testid="plugin-settings-dirty"]').text()).toBe('You have unsaved changes')
    await w.find('[data-testid="settings-save"]').trigger('click')
    await flushPromises()
    expect(calls).toEqual([{ values: { host: 'mail.example.com', password: SECRET_MASK, extra: { keep: 1 }, port: 587, from_name: 'Sub2API' } }])
    expect(toastState.items.map((t) => t.message)).toEqual(['Saved'])
    expect(settings().dirty.value).toBe(false)
  })

  it('after a save, reset goes back to the saved value, and the settings are read again without unmounting the form', async () => {
    fakeHost(['plugin:manage'])
    const { api, reads } = settingsApi({ values: { host: 'a' } })
    const { w, settings } = settingsPage(api)
    await flushPromises()
    const input = w.find('input').element
    settings().set('host', 'b')
    expect(await settings().save()).toBe(true)
    expect(reads()).toBe(2)
    expect(w.find('input').element).toBe(input)
    settings().set('host', 'c')
    await flushPromises()
    expect(settings().dirty.value).toBe(true)
    settings().reset()
    await flushPromises()
    expect(settings().get('host')).toBe('b')
    expect(settings().dirty.value).toBe(false)
  })

  it('a failed re-read after a save still resets to the saved value', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a', port: 25 } })
    const { settings } = settingsPage(api)
    await flushPromises()
    api.get = () => Promise.reject(new TypeError('Failed to fetch'))
    settings().set('host', 'b')
    expect(await settings().save()).toBe(true)
    settings().set('host', 'c')
    await flushPromises()
    settings().reset()
    await flushPromises()
    expect(settings().get('host')).toBe('b')
    expect(settings().dirty.value).toBe(false)
  })

  it('disables the controls while saving', async () => {
    fakeHost(['plugin:manage'])
    let finish!: () => void
    const { api } = settingsApi({ values: { host: 'a', port: 25 } }, () => new Promise<void>((r) => (finish = r)))
    const { w, settings } = settingsPage(api)
    await flushPromises()
    settings().set('host', 'b')
    const saving = settings().save()
    await flushPromises()
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    finish()
    expect(await saving).toBe(true)
    await flushPromises()
    expect(w.find('fieldset').attributes('disabled')).toBeUndefined()
  })

  it('a secret typed in comes back masked after the save', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a' } })
    const { w, settings } = settingsPage(api)
    await flushPromises()
    await w.find('input[type="password"]').setValue('new-secret')
    expect(await settings().save()).toBe(true)
    await flushPromises()
    expect(settings().get('password')).toBe(SECRET_MASK)
    expect((w.find('input[type="password"]').element as HTMLInputElement).value).toBe('')
    expect(settings().dirty.value).toBe(false)
  })

  it('Enter saves only with changes, validating once', async () => {
    fakeHost(['plugin:manage'])
    const { api, calls } = settingsApi({ values: { host: 'a' } })
    let checks = 0
    const { w, settings } = (() => {
      let settings!: PluginSettingsForm<M>
      const w = mount(
        defineComponent({
          setup() {
            settings = usePluginSettings<M>({ pluginKey: 'demo', api, rules: { host: { validator: () => void checks++ } } })
            return () => h(SSettingsPage, { settings }, () => h(SSettingRow, { prop: 'host', label: 'Host' }, () => h(SInput, settings.bind('host'))))
          }
        }),
        { attachTo: document.body }
      )
      mounted.push(w)
      return { w, settings: () => settings }
    })()
    await flushPromises()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(calls).toEqual([])
    settings().set('host', 'b')
    await flushPromises()
    checks = 0
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(calls).toEqual([{ values: { host: 'b' } }])
    expect(checks).toBe(1)
  })

  it('a failed save is worded as the console words it; a cleared default stays cleared', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a', port: 25 } }, () => Promise.reject(new TypeError('Failed to fetch')))
    const { settings } = settingsPage(api, { from_name: 'Sub2API' })
    await flushPromises()
    settings().set('from_name', undefined)
    await flushPromises()
    expect(settings().get('from_name')).toBeUndefined()
    expect(await settings().save()).toBe(false)
    expect(toastState.items.map((t) => t.message)).toEqual(['auth.errors.network'])
  })

  it('reset goes back to the loaded value', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a' } })
    const { w, settings } = settingsPage(api)
    await flushPromises()
    await w.find('input').setValue('b')
    await flushPromises()
    await w.find('[data-testid="settings-reset"]').trigger('click')
    await flushPromises()
    expect(settings().get('host')).toBe('a')
    expect(settings().dirty.value).toBe(false)
  })

  it('local rules block the save; server field errors show at their rows, reenter in its own words, root ones as a toast', async () => {
    const refusal = new ApiError(400, {
      code: 'invalid_argument',
      message: 'invalid settings',
      details: { fields: [{ field: 'values/password', code: 'reenter', message: 're-enter this secret to change settings' }, { field: 'values/host', code: 'invalid', message: 'unknown host' }, { field: 'values', code: 'invalid', message: 'whole object bad' }] }
    })
    const { api, calls } = settingsApi({ values: { host: 'a', port: 25, password: SECRET_MASK }, permission: 'plugin.demo:settings' }, () => Promise.reject(refusal))
    fakeHost(['plugin.demo:settings'])
    const { w, settings } = settingsPage(api)
    await flushPromises()
    expect(settings().canEdit.value).toBe(true)
    settings().set('port', 70000)
    expect(await settings().save()).toBe(false)
    expect(calls).toEqual([])
    expect(w.text()).toContain('Must be ≤ 65535')
    settings().set('port', 25)
    settings().set('host', 'b')
    await flushPromises()
    expect(await settings().save()).toBe(false)
    await flushPromises()
    expect(calls.length).toBe(1)
    expect(w.text()).toContain('Re-enter this secret to change other settings')
    expect(w.text()).toContain('unknown host')
    expect(toastState.items.map((t) => t.message)).toEqual(['whole object bad'])
    await w.find('input').setValue('c')
    await flushPromises()
    expect(w.text()).not.toContain('unknown host')
  })

  it('without edit rights: controls disabled, no save bar', async () => {
    const { api } = settingsApi({ values: { host: 'a' }, permission: 'plugin.demo:settings' })
    const { w, settings } = settingsPage(api)
    await flushPromises()
    expect(settings().canEdit.value).toBe(false)
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="settings-save-bar"]').exists()).toBe(false)
  })

  it('a failed load offers a retry', async () => {
    fakeHost(['plugin:manage'])
    let fail = true
    const api = { get: async () => (fail ? Promise.reject(new Error('boom')) : { values: {} }), put: async () => null } as unknown as ApiClient
    const { w } = settingsPage(api)
    await flushPromises()
    expect(w.text()).toContain('boom')
    fail = false
    await w.findAll('button').find((b) => b.text() === 'Retry')!.trigger('click')
    await flushPromises()
    expect(w.text()).not.toContain('boom')
    expect(w.find('fieldset').exists()).toBe(true)
  })

  it('asks before leaving the page with unsaved changes', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a' } })
    const { w } = settingsPage(api)
    await flushPromises()
    const ev = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
    await w.find('input').setValue('b')
    await flushPromises()
    const dirty = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(dirty)
    expect(dirty.defaultPrevented).toBe(true)
  })
})

describe('SSettingsPage route leave', () => {
  it('asks before leaving the route with unsaved changes', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a' } })
    let settings!: PluginSettingsForm<M>
    const Page = defineComponent({
      setup() {
        settings = usePluginSettings<M>({ pluginKey: 'demo', api, rules: {} })
        return () => h(SSettingsPage, { settings })
      }
    })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: Page }, { path: '/other', component: { render: () => h('p', 'other') } }] })
    await router.push('/')
    const w = mount(RouterView, { global: { plugins: [router] } })
    mounted.push(w)
    await flushPromises()
    settings.set('host', 'b')
    const leaving = router.push('/other')
    await flushPromises()
    expect(confirmState.current?.title).toBe('Discard unsaved changes?')
    settleConfirm(false)
    await leaving
    expect(router.currentRoute.value.path).toBe('/')
    const again = router.push('/other')
    await flushPromises()
    settleConfirm(true)
    await again
    expect(router.currentRoute.value.path).toBe('/other')
  })

  it('asks before another ?tab= of the same route unmounts it', async () => {
    fakeHost(['plugin:manage'])
    const { api } = settingsApi({ values: { host: 'a' } })
    let settings!: PluginSettingsForm<M>
    const Settings = defineComponent({
      setup() {
        settings = usePluginSettings<M>({ pluginKey: 'demo', api, rules: {} })
        return () => h(SSettingsPage, { settings })
      }
    })
    // Like the console's plugin detail page: the tab is the address's ?tab=.
    const Detail = defineComponent({
      setup() {
        const route = useRoute()
        return () => (route.query.tab === 'settings' ? h(Settings) : h('p', 'overview'))
      }
    })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: Detail }] })
    await router.push('/?tab=settings')
    const w = mount(RouterView, { global: { plugins: [router] } })
    mounted.push(w)
    await flushPromises()
    settings.set('host', 'b')
    const switching = router.replace({ query: { tab: 'overview' } })
    await flushPromises()
    expect(confirmState.current?.title).toBe('Discard unsaved changes?')
    settleConfirm(false)
    await switching
    expect(router.currentRoute.value.query.tab).toBe('settings')
    expect(w.find('[data-testid="plugin-settings"]').exists()).toBe(true)
    const again = router.replace({ query: { tab: 'overview' } })
    await flushPromises()
    settleConfirm(true)
    await again
    await flushPromises()
    expect(w.text()).toBe('overview')
  })
})
