import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, shallowMount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import SSelect from './SSelect.vue'
import type { SelectOption } from './types'
import { mountPinned } from './testViewport'

enableAutoUnmount(afterEach)
const options = [{ label: 'en-US · English', value: 'en-US' }, { label: 'zh-CN · 中文', value: 'zh-CN' }]

describe('searchable select', () => {
  it('filters case-insensitively without accepting arbitrary text', async () => {
    const w = mount(SSelect, { props: { modelValue: 'en-US', options, searchable: true } })
    const input = w.get('input[role="combobox"]')
    await input.trigger('focus')
    await input.setValue('ZH')
    expect(w.findAll('[role="option"]').map(o => o.text())).toEqual(['zh-CN · 中文'])
    expect(w.emitted('update:modelValue')).toBeUndefined()
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')).toEqual([['zh-CN']])
  })

  it('restores the selected label when a search is dismissed', async () => {
    const w = mount(SSelect, { props: { modelValue: 'en-US', options, searchable: true, noResults: 'No matches' } })
    const input = w.get('input')
    await input.trigger('focus')
    await input.setValue('not-a-locale')
    expect(w.text()).toContain('No matches')
    await input.trigger('keydown', { key: 'Escape' })
    expect((input.element as HTMLInputElement).value).toBe('en-US · English')
    expect(w.emitted('update:modelValue')).toBeUndefined()
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('supports keyboard navigation, default selection and disabled options', async () => {
    const w = mount(SSelect, { props: { modelValue: 'en-US', options: [...options, { label: 'Disabled', value: 'no', disabled: true }], searchable: true, placeholder: 'Default' } })
    const input = w.get('input')
    await input.trigger('focus')
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['zh-CN'])
    await input.trigger('focus')
    await w.get('[role="option"]').trigger('click')
    expect(w.emitted('update:modelValue')?.[1]).toEqual([null])
    await w.setProps({ disabled: true })
    expect(w.get('input').attributes('disabled')).toBeDefined()
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('keeps the native select behavior by default', async () => {
    const w = mount(SSelect, { props: { modelValue: 'en-US', options } })
    await w.get('select').setValue('1')
    expect(w.emitted('update:modelValue')).toEqual([['zh-CN']])
  })
})

describe('searchable select on phones', () => {
  const grouped: SelectOption[] = [{ label: 'Europe', value: null, options: [{ label: 'Paris', value: 'par' }, { label: 'Berlin', value: 'ber' }] }, { label: 'Tokyo', value: 'tyo' }]

  it('opens a bottom sheet with a search field and large checked rows', async () => {
    const picked: unknown[] = []
    const w = mountPinned(() =>
      h(SSelect, { modelValue: 'ber', options: grouped, searchable: true, 'onUpdate:modelValue': (v: unknown) => picked.push(v) })
    )
    const trigger = w.get('input')
    expect(trigger.attributes('readonly')).toBeDefined()
    await trigger.trigger('focus')
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    await trigger.trigger('click')
    await flushPromises()
    const sheet = document.querySelector<HTMLElement>('[role="dialog"]')!
    expect(sheet.className).toContain('h-full')
    const search = sheet.querySelector<HTMLInputElement>('input[role="combobox"]')!
    expect(document.activeElement).toBe(search)
    expect(sheet.querySelector('[role="presentation"]')!.textContent).toContain('Europe')
    const rows = Array.from(sheet.querySelectorAll<HTMLElement>('[role="option"]'))
    expect(rows.every((r) => r.classList.contains('min-h-11'))).toBe(true)
    expect(rows[1].querySelector('svg')).not.toBeNull()
    expect(rows[0].querySelector('svg')).toBeNull()
    search.value = 'tok'
    search.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(Array.from(sheet.querySelectorAll('[role="option"]')).map((o) => o.textContent?.trim())).toEqual(['Tokyo'])
    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await flushPromises()
    expect(picked).toEqual(['tyo'])
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  })

  it('leaves the native select alone', () => {
    const w = mountPinned(() => h(SSelect, { modelValue: 'en-US', options }))
    expect(w.find('select').exists()).toBe(true)
  })
})

describe('searchable select with rich options (UI 3.11)', () => {
  const people: SelectOption[] = [
    { label: 'ann@example.com', value: 7, hint: '#7', description: 'Ann' },
    { label: 'old@example.com', value: 9, hint: '#9', description: 'Former user', muted: true, tag: 'Disabled' }
  ]
  const rows = (w: ReturnType<typeof mount>) => w.findAll('[role="option"]')

  it('lays a row out as label, tag, hint and description, and filters on all three texts', async () => {
    const w = mount(SSelect, { props: { modelValue: null, options: [{ label: 'Name contains “x”', value: 'x', accent: true }, ...people], searchable: true } })
    const input = w.get('input')
    await input.trigger('focus')
    expect(rows(w)[1].text()).toBe('ann@example.com#7Ann')
    expect(rows(w)[2].text()).toBe('old@example.comDisabled#9Former user')
    expect(rows(w)[2].find('.text-fg-muted').exists()).toBe(true)
    expect(rows(w)[0].classes()).toContain('border-dashed')
    await input.setValue('#9')
    expect(rows(w).map((r) => r.text())).toEqual(['old@example.comDisabled#9Former user'])
    await input.setValue('ANN')
    expect(rows(w).map((r) => r.text())).toEqual(['ann@example.com#7Ann'])
    // Local matches are not marked: a select without the new props renders as before.
    expect(w.find('mark').exists()).toBe(false)
  })

  it('frames the field: icon, the selected hint and a clear button that emits null', async () => {
    const w = mount(SSelect, { props: { modelValue: 7, options: people, searchable: true, icon: 'user', clearable: true, tinted: true, placeholder: 'User' } })
    const frame = w.get('.input-group')
    expect(frame.classes()).toContain('bg-primary-50')
    expect((w.get('input').element as HTMLInputElement).value).toBe('ann@example.com')
    expect(frame.text()).toBe('#7')
    expect(frame.attributes('title')).toBe('ann@example.com #7')
    // The × clears, so the placeholder is no row of the list.
    await w.get('input').trigger('focus')
    expect(rows(w)).toHaveLength(2)
    expect(frame.text()).toBe('')
    await w.get('button[aria-label="Clear User"]').trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([[null]])
    expect(w.find('[role="listbox"]').exists()).toBe(false)
    await w.setProps({ modelValue: null })
    expect(w.find('button').exists()).toBe(false)
    expect(w.get('.input-group').classes()).not.toContain('bg-primary-50')
  })

  it('a press beside the text opens the list', async () => {
    const w = mount(SSelect, { props: { modelValue: 7, options: people, searchable: true, icon: 'user' }, attachTo: document.body })
    await w.get('.input-group svg').trigger('mousedown')
    expect(document.activeElement).toBe(w.get('input').element)
    expect(document.querySelector('[role="listbox"]')).not.toBeNull()
  })

  it('a clear from the keyboard leaves focus in the field, a clear by pointer where it was', async () => {
    const w = mount(SSelect, { props: { modelValue: 7, options: people, searchable: true, clearable: true }, attachTo: document.body })
    const clear = w.get<HTMLButtonElement>('button')
    // A press does not focus the button (its mousedown is prevented).
    await clear.trigger('click')
    expect(document.activeElement).toBe(document.body)
    // Tab reaches the button; without the value it is gone, so the field takes over.
    clear.element.focus()
    await clear.trigger('click')
    await w.setProps({ modelValue: null })
    expect(w.emitted('update:modelValue')).toEqual([[null], [null]])
    expect(document.activeElement).toBe(w.get('input').element)
  })

  it('keeps the bare field and the placeholder row without the framing props', async () => {
    const w = mount(SSelect, { props: { modelValue: 7, options: people, searchable: true, placeholder: 'Any' } })
    expect(w.find('.input-group').exists()).toBe(false)
    expect(w.get('input').classes()).toEqual(expect.arrayContaining(['input', 'pr-8']))
    await w.get('input').trigger('focus')
    expect(rows(w)[0].text()).toBe('Any')
  })
})

describe('searchable select with remote options (UI 3.11)', () => {
  const found: SelectOption[] = [{ label: 'claude-main', value: 12, hint: '#12' }, { label: 'claude-bak', value: 13, hint: '#13' }]

  it('emits search on opening and typing, shows the options as given and marks the typed text', async () => {
    const w = mount(SSelect, { props: { modelValue: null, options: [], searchable: true, remote: true, loading: true, noResults: 'Nothing' } })
    const input = w.get('input')
    await input.trigger('focus')
    expect(w.emitted('search')).toEqual([['']])
    expect(w.get('[role="listbox"]').text()).toBe('Searching…')
    await input.setValue('zzz')
    expect(w.emitted('search')).toEqual([[''], ['zzz']])
    // Nothing to pick yet: Enter waits for the results.
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')).toBeUndefined()

    await w.setProps({ options: found, loading: false, note: 'Only group A', footer: 'Showing the first 2' })
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['claude-main#12', 'claude-bak#13'])
    expect(w.get('[role="listbox"]').text()).toContain('Only group A')
    expect(w.get('[role="listbox"]').text()).toContain('Showing the first 2')
    expect(w.get('[role="listbox"]').classes()).toContain('w-80')
    // The first result is active once it arrives.
    expect(input.attributes('aria-activedescendant')).toBe(w.findAll('[role="option"]')[0].attributes('id'))
    await input.setValue('BAK')
    expect(w.findAll('[role="option"]')).toHaveLength(2)
    expect(w.findAll('mark').map((m) => m.text())).toEqual(['bak'])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')).toEqual([[12]])

    await w.setProps({ options: [], note: undefined, footer: undefined })
    await input.trigger('focus')
    expect(w.get('[role="listbox"]').text()).toBe('Nothing')
  })

  it('keeps the highlight when the same results are given again, and moves it to the first of other results', async () => {
    const w = mount(SSelect, { props: { modelValue: null, options: found, searchable: true, remote: true } })
    const input = w.get('input')
    const active = () => w.findAll('[role="option"]').findIndex((o) => o.attributes('id') === input.attributes('aria-activedescendant'))
    await input.trigger('focus')
    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(active()).toBe(1)
    // The caller rebuilt its list (a page refresh): the user's place holds.
    await w.setProps({ options: found.map((o) => ({ ...o })) })
    expect(active()).toBe(1)
    await w.setProps({ options: [{ label: 'claude-new', value: 14 }, ...found] })
    expect(active()).toBe(0)
  })

  // Console specs shallow-mount pages with SSelect unstubbed: its rows must not live in a child component.
  it('renders rich rows and marks under shallowMount', async () => {
    const Host = defineComponent({ render: () => h('div', [h(SSelect, { modelValue: null, options: found, searchable: true, remote: true })]) })
    const w = shallowMount(Host, { global: { stubs: { SSelect: false } } })
    await w.get('input').trigger('focus')
    await w.get('input').setValue('main')
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['claude-main#12', 'claude-bak#13'])
    expect(w.get('mark').text()).toBe('main')
  })

  it('shows a value the results do not list from `selected`', async () => {
    const w = mount(SSelect, { props: { modelValue: 99, options: found, searchable: true, remote: true, clearable: true, selected: { value: 99, label: 'gone-account', hint: '#99' } } })
    expect((w.get('input').element as HTMLInputElement).value).toBe('gone-account')
    expect(w.get('.input-group').text()).toBe('#99')
    // A stale `selected` (another value) is not shown for this one.
    await w.setProps({ modelValue: 12 })
    expect((w.get('input').element as HTMLInputElement).value).toBe('claude-main')
  })

  it.each([{}, { icon: 'account' }])('hands native input and focus listeners through beside its own (%o)', async (framing) => {
    const seen: string[] = []
    const w = mount(SSelect, {
      props: { modelValue: null, options: found, searchable: true, ...framing },
      attrs: { onInput: (e: Event) => seen.push(`input:${(e.target as HTMLInputElement).value}`), onFocus: () => seen.push('focus') }
    })
    await w.get('input').trigger('focus')
    await w.get('input').setValue('bak')
    expect(seen).toEqual(['focus', 'input:bak'])
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['claude-bak#13'])
  })

  it('searches from the sheet on phones', async () => {
    const searched: string[] = []
    const w = mountPinned(() => h(SSelect, { modelValue: null, options: found, searchable: true, remote: true, footer: 'More', onSearch: (q: string) => searched.push(q) }))
    await w.get('input').trigger('click')
    await flushPromises()
    const sheet = document.querySelector<HTMLElement>('[role="dialog"]')!
    const search = sheet.querySelector<HTMLInputElement>('input[role="combobox"]')!
    search.value = 'main'
    search.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(searched).toEqual(['', 'main'])
    expect(sheet.querySelectorAll('[role="option"]')).toHaveLength(2)
    expect(sheet.textContent).toContain('More')
  })
})

describe('creatable searchable select (UI 4.2)', () => {
  const models: SelectOption[] = [{ label: 'gpt-5', value: 'gpt-5' }, { label: 'gpt-5-mini', value: 'gpt-5-mini' }]
  const texts = (w: ReturnType<typeof mount>) => w.findAll('[role="option"]').map((r) => r.text())

  it('offers the typed text last while no option is it, and emits create with it trimmed', async () => {
    const w = mount(SSelect, { props: { modelValue: null, options: models, searchable: true, clearable: true, creatable: true } })
    const input = w.get('input')
    await input.trigger('focus')
    expect(texts(w)).toEqual(['gpt-5', 'gpt-5-mini'])
    await input.setValue('  gpt-5  ')
    // An option has that text: nothing to add.
    expect(texts(w)).toEqual(['gpt-5', 'gpt-5-mini'])
    await input.setValue(' gpt ')
    expect(texts(w)).toEqual(['gpt-5', 'gpt-5-mini', 'Add “gpt”'])
    const add = w.findAll('[role="option"]')[2]
    expect(add.classes()).toContain('border-dashed')
    expect(add.attributes('aria-selected')).toBe('false')
    // Enter picks the first match; the row adds only when picked.
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')).toEqual([['gpt-5']])
    await input.trigger('focus')
    await input.setValue(' claude-x ')
    expect(texts(w)).toEqual(['Add “claude-x”'])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('create')).toEqual([['claude-x']])
    expect(w.emitted('update:modelValue')).toHaveLength(1)
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('picks the option the typed text names exactly before the first match, and offers no row for a disabled one', async () => {
    const w = mount(SSelect, { props: { modelValue: null, options: [models[1], models[0], { label: 'o3', value: 'o3', disabled: true }], searchable: true, creatable: true } })
    const input = w.get('input')
    await input.trigger('focus')
    await input.setValue(' gpt-5 ')
    expect(texts(w)).toEqual(['gpt-5-mini', 'gpt-5'])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')).toEqual([['gpt-5']])
    await input.trigger('focus')
    await input.setValue('o3')
    expect(texts(w)).toEqual(['o3'])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('create')).toBeUndefined()
    expect(w.emitted('update:modelValue')).toHaveLength(1)
  })

  it('pins the add row over the footer, keeps it an option of the listbox, and scrolls the active option clear of it', async () => {
    const many = Array.from({ length: 12 }, (_, i) => ({ label: `gpt-${i}`, value: `gpt-${i}` }))
    const scrolled: { value: string; margin: string }[] = []
    const proto = Element.prototype as { scrollIntoView?: (this: HTMLElement) => void }
    const native = proto.scrollIntoView
    proto.scrollIntoView = function () {
      scrolled.push({ value: this.textContent ?? '', margin: this.style.scrollMarginBottom })
    }
    const height = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.hasAttribute('data-pinned') ? 64 : 0
    })
    const w = mount(SSelect, { props: { modelValue: null, options: many, searchable: true, creatable: true, footer: 'Type any name' } })
    const input = w.get('input')
    await input.trigger('focus')
    await input.setValue('gpt')
    const pin = w.get('[data-pinned]')
    expect(pin.classes()).toEqual(expect.arrayContaining(['sticky', 'bottom-0']))
    expect(pin.attributes('role')).toBe('none')
    expect(pin.get('[role="option"]').text()).toBe('Add “gpt”')
    expect(pin.text()).toContain('Type any name')
    expect(texts(w)).toHaveLength(13)
    await input.trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()
    expect(scrolled.at(-1)).toEqual({ value: 'gpt-1', margin: '64px' })
    await input.trigger('keydown', { key: 'End' })
    await flushPromises()
    const add = pin.get('[role="option"]')
    expect(input.attributes('aria-activedescendant')).toBe(add.attributes('id'))
    expect(add.classes()).toContain('bg-primary-50')
    expect(scrolled.at(-1)?.value).toBe('gpt-1')
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('create')).toEqual([['gpt']])
    proto.scrollIntoView = native
    height.mockRestore()
  })

  it('adds by a click and offers nothing for blank text or without creatable', async () => {
    const w = mount(SSelect, { props: { modelValue: null, options: models, searchable: true, creatable: true, noResults: 'None' } })
    const input = w.get('input')
    await input.trigger('focus')
    await input.setValue('   ')
    expect(texts(w)).toEqual(['gpt-5', 'gpt-5-mini'])
    await input.setValue('o3')
    await w.get('[role="option"]').trigger('click')
    expect(w.emitted('create')).toEqual([['o3']])
    await w.setProps({ creatable: false })
    await input.trigger('focus')
    await input.setValue('o3')
    expect(texts(w)).toEqual([])
    expect(w.text()).toContain('None')
  })
})
