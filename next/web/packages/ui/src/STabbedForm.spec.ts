import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { defineComponent, effect, h, nextTick, ref } from 'vue'
import { ApiError } from '@sub2api/host'
import STabbedForm from './STabbedForm.vue'
import SSettingsGroup from './SSettingsGroup.vue'
import { errorsByPart, useTabbedForm, useTabbedFormPart, type TabbedForm, type TabbedFormPart } from './tabbedForm'
import { toastState } from './feedback'
import { provideViewport } from './viewport'
import type { FormTab } from './types'

enableAutoUnmount(afterEach)

const TABS: FormTab[] = [
  { key: 'basic', label: 'Basic', summary: 'Name and status' },
  { key: 'prices', label: 'Prices', summary: '2 models' },
  { key: 'rates', label: 'Rates' }
]
const KEYS = TABS.map((t) => t.key)

/** A tab's content: a part with an input; `ok` says whether its check passes. */
function pane(tab: string, field: string) {
  return defineComponent({
    props: { ok: { type: Boolean, default: true } },
    setup(props) {
      const value = ref('')
      const errors = ref<Record<string, string>>({})
      useTabbedFormPart({ tab, check: () => props.ok, body: () => (value.value ? { [field]: value.value } : {}), dirty: () => value.value !== '', fields: [field], applyErrors: (e) => (errors.value = e) })
      return () => h('div', [h('input', { 'data-testid': `input-${tab}`, value: value.value, onInput: (e: Event) => (value.value = (e.target as HTMLInputElement).value) }), h('p', { 'data-testid': `errors-${tab}` }, Object.keys(errors.value).join(','))])
    }
  })
}
const Basic = pane('basic', 'name')
const Prices = pane('prices', 'model_prices')
const Rates = pane('rates', 'segment_rates')

/** A dialog like the editors: the form, three panes, and what Save does. */
function mountEditor(layout: 'top' | 'side' = 'top', checks: Record<string, boolean> = {}) {
  let form!: TabbedForm
  const Host = defineComponent({
    setup() {
      const tab = ref('basic')
      form = useTabbedForm(tab, KEYS)
      return () =>
        h(STabbedForm, { tabs: TABS, modelValue: tab.value, 'onUpdate:modelValue': (v: string) => (tab.value = v), layout, form, idPrefix: 'f' }, {
          default: ({ tab: key }: { tab: string }) => h({ basic: Basic, prices: Prices, rates: Rates }[key]!, { ok: checks[key] ?? true })
        })
    }
  })
  return { w: mount(Host, { attachTo: document.body }), form: () => form }
}

describe('STabbedForm', () => {
  it('keeps every panel mounted and shows the one in front', async () => {
    const { w } = mountEditor()
    expect(w.findAll('[role="tabpanel"]')).toHaveLength(3)
    expect(w.get('[data-tab="basic"]').isVisible()).toBe(true)
    expect(w.get('[data-tab="prices"]').isVisible()).toBe(false)
    await w.get('#f-tab-prices').trigger('click')
    expect(w.get('[data-tab="prices"]').isVisible()).toBe(true)
    expect(w.get('#f-panel-prices').attributes('aria-labelledby')).toBe('f-tab-prices')
  })

  it('marks the tabs with unsaved changes', async () => {
    const { w, form } = mountEditor()
    await w.get('[data-testid="input-prices"]').setValue('x')
    expect(form().dirtyTabs.value).toEqual(['prices'])
    expect(form().anyDirty.value).toBe(true)
    expect(w.get('#f-tab-prices').find('[data-testid="tab-mark-unsaved"]').exists()).toBe(true)
    expect(w.get('#f-tab-basic').find('[data-testid="tab-mark-unsaved"]').exists()).toBe(false)
  })

  it('lays the tabs out on the side with numbers, summaries and marks', async () => {
    const { w, form } = mountEditor('side', { rates: false })
    const nav = w.get('[role="tablist"]')
    expect(nav.attributes('aria-orientation')).toBe('vertical')
    expect(w.get('#f-tab-basic').text()).toContain('1')
    expect(w.get('#f-tab-basic').text()).toContain('Name and status')
    await form().submit(async () => 'sent')
    expect(w.get('#f-tab-rates').find('[data-testid="tab-mark-error"]').exists()).toBe(true)
    expect(w.get('[data-tab="rates"]').isVisible()).toBe(true)
    await w.get('#f-tab-rates').trigger('keydown', { key: 'Home' })
    expect(w.get('[data-tab="basic"]').isVisible()).toBe(true)
  })

  it('says the side tabs run across on a phone, where they are a row', () => {
    const Phone = defineComponent({ setup: () => (provideViewport({ compact: true }), () => h(STabbedForm, { tabs: TABS, modelValue: 'rates', layout: 'side' }, { default: () => 'x' })) })
    expect(mount(Phone).get('[role="tablist"]').attributes('aria-orientation')).toBe('horizontal')
  })

  it('shows the icon of a side tab in a tile, the number leading its title', () => {
    const w = mount(STabbedForm, { props: { tabs: [{ ...TABS[0], icon: 'key' }, TABS[1]], modelValue: 'basic', layout: 'side', idPrefix: 'i' }, slots: { default: 'x' } })
    expect(w.get('#i-tab-basic').find('svg').exists()).toBe(true)
    expect(w.get('#i-tab-basic').text()).toMatch(/^1\s*Basic/)
    expect(w.get('#i-tab-prices').find('svg').exists()).toBe(false)
  })

  it('puts the before-nav slot above the side navigation', () => {
    const w = mount(STabbedForm, { props: { tabs: TABS, modelValue: 'basic', layout: 'side' }, slots: { 'before-nav': '<div data-testid="lead">Account type</div>', default: 'x' } })
    const lead = w.get('[data-testid="lead"]').element
    expect(lead.nextElementSibling?.getAttribute('role')).toBe('tablist')
  })

  it('hides a heading that repeats the title of its tab', () => {
    const w = mount(STabbedForm, { props: { tabs: TABS, modelValue: 'basic' }, slots: { default: () => h(SSettingsGroup, { title: 'Basic' }, () => 'content') } })
    expect(w.get('[data-tab="basic"]').find('h5').exists()).toBe(false)
    expect(w.get('[data-tab="prices"]').get('h5').text()).toBe('Basic')
    expect(w.get('[data-tab="basic"]').text()).toContain('content')
  })
})

describe('useTabbedForm', () => {
  it('sends the parts merged in one request', async () => {
    const { w, form } = mountEditor()
    await w.get('[data-testid="input-basic"]').setValue('vip')
    await w.get('[data-testid="input-rates"]').setValue('[1]')
    const sent: unknown[] = []
    const answer = await form().submit(async (body) => (sent.push(body), 'ok'))
    expect(answer).toBe('ok')
    expect(sent).toEqual([{ name: 'vip', segment_rates: '[1]' }])
    expect(form().saving.value).toBe(false)
  })

  it('sends nothing while a check fails, and opens the first tab that failed', async () => {
    const { w, form } = mountEditor('top', { prices: false, rates: false })
    let sent = 0
    expect(await form().submit(async () => sent++)).toBe(false)
    expect(sent).toBe(0)
    expect(form().errorTabs.value).toEqual(['prices', 'rates'])
    expect(form().current.value).toBe('prices')
    await nextTick()
    expect(w.get('#f-tab-rates').find('[data-testid="tab-mark-error"]').exists()).toBe(true)
  })

  it('hands field errors to the parts that sent the fields and opens the first of their tabs', async () => {
    const { w, form } = mountEditor()
    const refusal = new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [
      { field: 'segment_rates.0.rate', code: 'invalid', message: 'bad rate' },
      { field: 'model_prices.1.rule', code: 'invalid', message: 'bad price' }
    ] } })
    expect(await form().submit(() => Promise.reject(refusal))).toBe(false)
    expect(form().errorTabs.value).toEqual(['prices', 'rates'])
    expect(form().current.value).toBe('prices')
    await nextTick()
    expect(w.get('[data-testid="errors-rates"]').text()).toBe('segment_rates.0.rate')
    expect(w.get('[data-testid="errors-prices"]').text()).toBe('model_prices.1.rule')
  })

  it('rejects with a failure no part claims, and clears the marks on the next attempt', async () => {
    const { form } = mountEditor()
    const other = new ApiError(409, { code: 'conflict', message: 'busy' })
    await expect(form().submit(() => Promise.reject(other))).rejects.toBe(other)
    expect(form().errorTabs.value).toEqual([])
    const orphan = new ApiError(400, { code: 'invalid_argument', message: 'x', details: { fields: [{ field: 'unknown', code: 'invalid', message: 'x' }] } })
    await expect(form().submit(() => Promise.reject(orphan))).rejects.toBe(orphan)
  })

  it('tells in a toast the field errors no part claims, beside those it hands out', async () => {
    const { w, form } = mountEditor()
    toastState.items.splice(0)
    const refusal = new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [
      { field: 'name', code: 'duplicate', message: 'taken' },
      { field: 'other.0', code: 'invalid', message: 'odd' }
    ] } })
    expect(await form().submit(() => Promise.reject(refusal))).toBe(false)
    await nextTick()
    expect(w.get('[data-testid="errors-basic"]').text()).toBe('name')
    expect(toastState.items.map((t) => [t.kind, t.message])).toEqual([['error', 'other.0: odd']])
  })

  it('claims a field by its prefix only', () => {
    const a: TabbedFormPart = { tab: 'a', body: () => ({}), fields: ['name'] }
    const b: TabbedFormPart = { tab: 'b', body: () => ({}), fields: ['model_rates'] }
    const out = errorsByPart([a, b], { name: 'x', 'name.first': 'y', names: 'z', 'model_rates.0.rate': 'w' })
    expect(errorsByPart([a], { 'name[0]': 'v' }).get(a)).toEqual({ 'name[0]': 'v' })
    expect(out.get(a)).toEqual({ name: 'x', 'name.first': 'y' })
    expect(out.get(b)).toEqual({ 'model_rates.0.rate': 'w' })
  })

  it('waits for a check that waits, and is saving meanwhile', async () => {
    const { form } = mountEditor()
    let pass!: (ok: boolean) => void
    form().register({ tab: 'rates', body: () => ({}), check: () => new Promise<boolean>((r) => (pass = r)) })
    const answer = form().submit(async () => 'sent')
    await nextTick()
    expect(form().saving.value).toBe(true)
    pass(false)
    expect(await answer).toBe(false)
    expect(form().saving.value).toBe(false)
    expect(form().current.value).toBe('rates')
  })

  it('marks the tab of an invalid part as its errors come and go, not by the last Save', async () => {
    const { w, form } = mountEditor()
    const wrong = ref(true)
    form().register({ tab: 'prices', body: () => ({}), check: () => !wrong.value, invalid: wrong })
    expect(form().errorTabs.value).toEqual(['prices'])
    expect(await form().submit(async () => 'sent')).toBe(false)
    expect(form().current.value).toBe('prices')
    wrong.value = false
    expect(form().errorTabs.value).toEqual([])
    await nextTick()
    expect(w.get('#f-tab-prices').find('[data-testid="tab-mark-error"]').exists()).toBe(false)
  })

  it('forgets the part of a component that is gone', async () => {
    let form!: TabbedForm
    const show = ref(true)
    mount(defineComponent({
      setup() {
        form = useTabbedForm(ref('basic'), KEYS)
        return () => (show.value ? h(Basic) : null)
      }
    }))
    expect(await form.submit(async (body) => body)).toEqual({})
    show.value = false
    await nextTick()
    const failing: TabbedFormPart = { tab: 'rates', body: () => ({}), check: () => false }
    const drop = form.register(failing)
    expect(await form.submit(async () => 'sent')).toBe(false)
    drop()
    expect(await form.submit(async () => 'sent')).toBe('sent')
  })

  it('holds the part of a component only while it is active', async () => {
    let form!: TabbedForm
    const active = ref(false)
    const Part = defineComponent({ setup: () => (useTabbedFormPart({ tab: 'rates', body: () => ({ segment_rates: [1] }), dirty: true }, active), () => null) })
    mount(defineComponent({ setup: () => ((form = useTabbedForm(ref('basic'), KEYS)), () => h(Part)) }))
    expect(await form.submit(async (body) => body)).toEqual({})
    active.value = true
    await nextTick()
    expect(form.dirtyTabs.value).toEqual(['rates'])
    expect(await form.submit(async (body) => body)).toEqual({ segment_rates: [1] })
    active.value = false
    await nextTick()
    expect(form.anyDirty.value).toBe(false)
    expect(await form.submit(async (body) => body)).toEqual({})
  })

  it('shows no tabs for a form of one tab, as STabs does', () => {
    const w = mount(STabbedForm, { props: { tabs: [TABS[0]], modelValue: 'basic', idPrefix: 'one' }, slots: { default: '<input data-testid="only" />' } })
    expect(w.find('[role="tablist"]').exists()).toBe(false)
    expect(w.find('[role="tabpanel"]').exists()).toBe(false)
    expect(w.get('[data-testid="only"]').isVisible()).toBe(true)
  })

  it('does not make what runs while a part comes or goes depend on the parts', () => {
    let form!: TabbedForm
    mount(defineComponent({ setup: () => ((form = useTabbedForm(ref('basic'), KEYS)), () => null) }))
    let runs = 0
    // A parent that mounts or unmounts a part while it renders would otherwise render again for it, and again.
    effect(() => {
      runs++
      form.register({ tab: 'basic', body: () => ({}) })()
    })
    form.register({ tab: 'rates', body: () => ({}) })
    expect(runs).toBe(1)
  })
})
