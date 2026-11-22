import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import SBadge from './SBadge.vue'
import SSwitch from './SSwitch.vue'
import SCheckbox from './SCheckbox.vue'
import SField from './SField.vue'
import SSettingRow from './SSettingRow.vue'
import SInput from './SInput.vue'
import STextarea from './STextarea.vue'
import SSelect from './SSelect.vue'
import SSegmented from './SSegmented.vue'
import SModal from './SModal.vue'
import SDrawer from './SDrawer.vue'
import SDropdown from './SDropdown.vue'
import STabs from './STabs.vue'
import STagInput from './STagInput.vue'
import SRadioGroup from './SRadioGroup.vue'
import SPagination from './SPagination.vue'
import SDisclosure from './SDisclosure.vue'
import SButton from './SButton.vue'
import SIcon from './SIcon.vue'
import { openLayerCount } from './overlay'

enableAutoUnmount(afterEach)
afterEach(() => {
  document.body.innerHTML = ''
})

describe('SBadge', () => {
  it('maps purple to the accent colours and supports xs', () => {
    const w = mount(SBadge, { props: { tone: 'purple', size: 'xs' }, slots: { default: 'x' } })
    expect(w.classes()).toEqual(expect.arrayContaining(['badge', 'badge-purple', 'badge-xs']))
    expect(mount(SBadge, { props: { tone: 'accent' } }).classes()).toContain('badge-accent')
  })
})

describe('SIcon', () => {
  it('defaults to h-4 w-4 unless the class sizes it, and is decorative without a label', () => {
    expect(mount(SIcon, { props: { name: 'check' } }).classes()).toEqual(['h-4', 'w-4'])
    const sized = mount(SIcon, { props: { name: 'check' }, attrs: { class: 'h-6 w-6' } })
    expect(sized.classes()).toEqual(['h-6', 'w-6'])
    expect(sized.attributes('aria-hidden')).toBe('true')
    const named = mount(SIcon, { props: { name: 'check', label: 'Done' } })
    expect(named.attributes('role')).toBe('img')
    expect(named.attributes('aria-label')).toBe('Done')
  })
})

describe('SSwitch', () => {
  it('renders a checkbox switch with attrs on the input and toggles on click without attaching', async () => {
    const w = mount(SSwitch, { props: { modelValue: false, label: 'On' }, attrs: { id: 'sw', 'data-testid': 'sw', title: 'T', class: 'ml-2' } })
    const input = w.get('input')
    expect(input.attributes('type')).toBe('checkbox')
    expect(input.attributes('role')).toBe('switch')
    expect(input.classes()).toContain('switch')
    expect(input.attributes('aria-checked')).toBe('false')
    expect(input.attributes('id')).toBe('sw')
    expect(input.attributes('data-testid')).toBe('sw')
    expect(input.attributes('title')).toBe('T')
    expect(w.classes()).toContain('ml-2')
    expect(w.attributes('data-testid')).toBeUndefined()
    await input.trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([[true]])
    await w.setProps({ modelValue: true })
    expect(input.attributes('aria-checked')).toBe('true')
  })

  it('emits once per click when attached (click then change)', async () => {
    const w = mount(SSwitch, { props: { modelValue: false }, attachTo: document.body })
    ;(w.get('input').element as HTMLInputElement).click()
    await flushPromises()
    expect(w.emitted('update:modelValue')).toEqual([[true]])
  })

  it('does not toggle when disabled and keeps the DOM in sync when the parent refuses', async () => {
    const w = mount(SSwitch, { props: { modelValue: false, disabled: true } })
    await w.get('input').trigger('click')
    expect(w.emitted('update:modelValue')).toBeUndefined()
    const c = mount(SSwitch, { props: { modelValue: false }, attachTo: document.body })
    ;(c.get('input').element as HTMLInputElement).click()
    await flushPromises()
    expect((c.get('input').element as HTMLInputElement).checked).toBe(false)
  })

  it('takes the id of its SSettingRow so the row label points at it', async () => {
    const w = mount(SSettingRow, { props: { label: 'Compat', description: 'Short' }, slots: { default: () => h(SSwitch, { modelValue: true }) } })
    await flushPromises()
    const input = w.get('input')
    const label = w.get('label')
    expect(label.attributes('for')).toBe(input.attributes('id'))
    expect(input.attributes('aria-describedby')).toContain(w.get('p.text-xs').attributes('id'))
  })
})

describe('SCheckbox', () => {
  it('toggles on click without attaching, sizes the box and forwards aria-label', async () => {
    const w = mount(SCheckbox, { props: { modelValue: true, size: 'md', ariaLabel: 'Pick' } })
    const input = w.get('input')
    expect(input.classes()).toContain('checkbox-md')
    expect(input.attributes('aria-label')).toBe('Pick')
    await input.trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([[false]])
  })
})

describe('SField context', () => {
  it('links label, hint and error to SInput, STextarea and SSelect', async () => {
    for (const control of [h(SInput, { modelValue: '' }), h(STextarea, { modelValue: '' }), h(SSelect, { modelValue: null, options: [] })]) {
      const w = mount(SField, { props: { label: 'Name', hint: 'Help' }, slots: { default: () => control } })
      await flushPromises()
      const el = w.get('input, textarea, select')
      expect(w.get('label').attributes('for')).toBe(el.attributes('id'))
      const hint = w.get('.input-hint')
      expect(el.attributes('aria-describedby')).toBe(hint.attributes('id'))
      expect(el.attributes('aria-invalid')).toBeUndefined()
      await w.setProps({ error: 'Bad' })
      expect(el.attributes('aria-invalid')).toBe('true')
      expect(el.classes()).toContain('input-error')
      expect(el.attributes('aria-describedby')).toBe(w.get('.input-error-text').attributes('id'))
    }
  })

  it('keeps an explicit control id and lets `for` name it', () => {
    const w = mount(SField, { props: { label: 'Name', for: 'mine' }, slots: { default: () => h(SInput, { modelValue: '', id: 'mine' }) } })
    expect(w.get('label').attributes('for')).toBe('mine')
    expect(w.get('input').attributes('id')).toBe('mine')
  })

  it('applies size sm to controls and labels group controls', () => {
    const w = mount(SField, {
      props: { label: 'Mode', size: 'sm' },
      slots: { default: () => [h(SInput, { modelValue: '' }), h(SSegmented, { modelValue: 'a', options: [{ value: 'a', label: 'A' }] })] }
    })
    expect(w.get('input.input').classes()).toContain('input-sm')
    expect(w.get('[role="radiogroup"]').attributes('aria-labelledby')).toBe(w.get('label').attributes('id'))
  })
})

describe('SInput adornments', () => {
  it('renders suffix, clears and reveals passwords', async () => {
    const w = mount(SInput, { props: { modelValue: 'secret', type: 'password', clearable: true, revealable: true, suffix: 'ms' }, attrs: { 'data-testid': 'in', class: 'w-40' } })
    expect(w.classes()).toEqual(expect.arrayContaining(['input', 'input-group', 'w-40']))
    expect(w.get('input').attributes('data-testid')).toBe('in')
    expect(w.text()).toContain('ms')
    await w.get('button[aria-label="Show password"]').trigger('click')
    expect(w.get('input').attributes('type')).toBe('text')
    await w.get('button[aria-label="Clear"]').trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['']])
  })
})

describe('STextarea', () => {
  it('passes rows through (height follows rows in CSS)', () => {
    expect(mount(STextarea, { props: { modelValue: '', rows: 7 } }).attributes('rows')).toBe('7')
  })
})

describe('SModal stack', () => {
  it('closes only the topmost modal on Escape, traps focus and restores it', async () => {
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    opener.focus()
    const Host = defineComponent({
      setup() {
        const a = ref(true)
        const b = ref(false)
        return { a, b }
      },
      render() {
        return [
          h(SModal, { open: this.a, title: 'A', 'onUpdate:open': (v: boolean) => (this.a = v) }, { default: () => h('input', { id: 'in-a' }) }),
          h(SModal, { open: this.b, title: 'B', 'onUpdate:open': (v: boolean) => (this.b = v) }, { default: () => h('input', { id: 'in-b' }) })
        ]
      }
    })
    const w = mount(Host, { attachTo: document.body })
    await flushPromises()
    expect(document.activeElement?.id).toBe('in-a')
    expect(document.body.style.overflow).toBe('hidden')
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.getAttribute('aria-labelledby')).toBeTruthy()
    expect(document.getElementById(dialog.getAttribute('aria-labelledby')!)?.textContent).toContain('A')
    ;(w.vm as any).b = true
    await flushPromises()
    expect(openLayerCount()).toBe(2)
    expect(document.activeElement?.id).toBe('in-b')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect((w.vm as any).b).toBe(false)
    expect((w.vm as any).a).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect((w.vm as any).a).toBe(false)
    expect(document.body.style.overflow).toBe('')
    expect(document.activeElement).toBe(opener)
  })

  it('persistent ignores Escape and the close button has type=button', async () => {
    const w = mount(SModal, { props: { open: true, title: 'P', persistent: true }, attachTo: document.body })
    await flushPromises()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(w.emitted('update:open')).toBeUndefined()
    const close = document.querySelector('[data-overlay-close]')!
    expect(close.getAttribute('type')).toBe('button')
    expect(close.getAttribute('aria-label')).toBe('Close')
  })
})

describe('SDrawer', () => {
  it('opens as a dialog and closes on Escape', async () => {
    const w = mount(SDrawer, { props: { open: true, title: 'Details' }, slots: { default: '<button id="d-in">x</button>' }, attachTo: document.body })
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.activeElement?.id).toBe('d-in')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(w.emitted('update:open')).toEqual([[false]])
  })
})

describe('SDropdown', () => {
  const actions = [
    { key: 'edit', label: 'Edit', icon: 'edit' },
    { key: 'off', label: 'Off', disabled: true },
    { key: 'del', label: 'Delete', danger: true, divider: true }
  ]
  it('renders menu items as buttons with keyboard navigation', async () => {
    const w = mount(SDropdown, { props: { actions }, attachTo: document.body })
    const trigger = w.get('button')
    expect(trigger.attributes('aria-label')).toBe('More actions')
    expect(trigger.attributes('aria-haspopup')).toBe('menu')
    await trigger.trigger('click')
    await flushPromises()
    expect(trigger.attributes('aria-expanded')).toBe('true')
    const items = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
    expect(items.map((b) => b.textContent?.trim())).toEqual(['Edit', 'Off', 'Delete'])
    expect(items[1].disabled).toBe(true)
    expect(document.activeElement).toBe(items[0])
    const menu = document.querySelector('[role="menu"]')!
    menu.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    expect(document.activeElement).toBe(items[2])
    menu.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }))
    expect(document.activeElement).toBe(items[0])
    items[2].click()
    await flushPromises()
    expect(w.emitted('select')).toEqual([['del']])
    expect(document.querySelector('[role="menu"]')).toBeNull()
  })

  it('closes on Escape and returns focus to the trigger', async () => {
    const w = mount(SDropdown, { props: { actions }, attachTo: document.body })
    await w.get('button').trigger('click')
    await flushPromises()
    document.querySelector('[role="menu"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(w.get('button').element)
  })

  it('supports a custom trigger slot', async () => {
    const w = mount(SDropdown, {
      props: { actions },
      slots: { trigger: `<template #trigger="{ toggle, attrs }"><button class="mine" v-bind="attrs" @click="toggle">Go</button></template>` },
      attachTo: document.body
    })
    await w.get('button.mine').trigger('click')
    await flushPromises()
    expect(w.get('button.mine').attributes('aria-expanded')).toBe('true')
  })
})

describe('STabs keyboard navigation', () => {
  it('moves with arrow keys and uses a roving tabindex', async () => {
    const tabs = [{ key: 'a', label: 'A' }, { key: 'b', label: 'B', disabled: true }, { key: 'c', label: 'C' }]
    const w = mount(STabs, { props: { tabs, modelValue: 'a', idPrefix: 't' }, attachTo: document.body })
    const buttons = w.findAll('[role="tab"]')
    expect(buttons.map((b) => b.attributes('tabindex'))).toEqual(['0', '-1', '-1'])
    expect(buttons.every((b) => b.attributes('type') === 'button')).toBe(true)
    expect(buttons[0].attributes('aria-controls')).toBe('t-panel-a')
    ;(buttons[0].element as HTMLElement).focus()
    await buttons[0].trigger('keydown', { key: 'ArrowRight' })
    expect(w.emitted('update:modelValue')).toEqual([['c']])
  })
})

describe('STagInput', () => {
  it('splits pasted text on commas and new lines and disables remove buttons', async () => {
    const w = mount(STagInput, { props: { modelValue: ['a'] } })
    const input = w.get('input')
    await input.trigger('paste', { clipboardData: { getData: () => 'b, c\nd\na' } })
    expect(w.emitted('update:modelValue')).toEqual([[['a', 'b', 'c', 'd']]])
    expect(w.get('button').attributes('aria-label')).toBe('Remove a')
    await w.setProps({ disabled: true })
    expect(w.get('button').attributes('disabled')).toBeDefined()
  })
})

describe('SSelect searchable', () => {
  it('teleports the list to <body> in a live page and filters grouped options', async () => {
    const options = [{ label: 'Fruit', value: null, options: [{ label: 'Apple', value: 'a' }, { label: 'Banana', value: 'b' }] }, { label: 'Carrot', value: 'c' }]
    const w = mount(SSelect, { props: { modelValue: 'a', options, searchable: true }, attachTo: document.body })
    const input = w.get('input')
    await input.trigger('focus')
    await flushPromises()
    const list = document.body.querySelector(':scope > ul[role="listbox"]')
    expect(list).not.toBeNull()
    expect(Array.from(list!.querySelectorAll('[role="option"]')).map((o) => o.textContent?.trim())).toEqual(['Apple', 'Banana', 'Carrot'])
    expect(list!.textContent).toContain('Fruit')
    await input.setValue('zzz')
    expect(list!.textContent).toContain('No matches')
  })
})

describe('SRadioGroup', () => {
  it('shares a generated name and labels the group', () => {
    const w = mount(SRadioGroup, { props: { modelValue: 1, options: [{ value: 1, label: 'One' }, { value: 2, label: 'Two' }], ariaLabel: 'Count' } })
    const names = w.findAll('input').map((i) => i.attributes('name'))
    expect(names[0]).toBeTruthy()
    expect(new Set(names).size).toBe(1)
    expect(w.attributes('aria-label')).toBe('Count')
  })
})


describe('SPagination', () => {
  it('marks the current page and labels the arrows', () => {
    const w = mount(SPagination, { props: { page: 2, pageSize: 20, total: 100 } })
    expect(w.get('[aria-current="page"]').text()).toBe('2')
    expect(w.find('button[aria-label="Previous"]').exists()).toBe(true)
    expect(w.findAll('button').every((b) => b.attributes('type') === 'button')).toBe(true)
  })
})

describe('SDisclosure', () => {
  it('keeps section actions interactive without toggling the panel', async () => {
    const w = mount(SDisclosure, { props: { variant: 'section', label: 'Group' }, slots: { actions: '<button class="action">Set all</button>', default: '<p class="body">Children</p>' } })
    const toggle = w.get('button[aria-expanded]')
    expect(w.get('.action').element.closest('button[aria-expanded]')).toBeNull()
    await w.get('.action').trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(w.find('.body').exists()).toBe(true)
    await w.get('.action').trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
  })
  it('supports v-model:open and the section variant', async () => {
    const w = mount(SDisclosure, { props: { open: false, variant: 'section', label: 'Advanced' }, slots: { default: '<p class="body">x</p>' } })
    const b = w.get('button')
    expect(b.attributes('aria-controls')).toBeTruthy()
    await b.trigger('click')
    expect(w.emitted('update:open')).toEqual([[true]])
    expect(w.find('.body').exists()).toBe(false)
    await w.setProps({ open: true })
    expect(w.find('.body').exists()).toBe(true)
  })
})

describe('SButton', () => {
  it('icon-only buttons render the icon with an aria-label; disabled links lose href', () => {
    const w = mount(SButton, { props: { icon: 'trash', iconOnly: true, ariaLabel: 'Delete' } })
    expect(w.attributes('aria-label')).toBe('Delete')
    expect(w.find('svg').exists()).toBe(true)
    const link = mount(SButton, { props: { href: '/x', disabled: true } })
    expect(link.attributes('href')).toBeUndefined()
    expect(link.attributes('aria-disabled')).toBe('true')
  })
})

describe('controlled write-back', () => {
  it('SSegmented re-checks the current option when the parent refuses a change', async () => {
    const w = mount(SSegmented, { props: { modelValue: 'a', options: [{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }] } })
    const inputs = w.findAll('input')
    ;(inputs[1].element as HTMLInputElement).checked = true
    await inputs[1].trigger('change')
    await nextTick()
    expect((inputs[0].element as HTMLInputElement).checked).toBe(true)
    expect((inputs[1].element as HTMLInputElement).checked).toBe(false)
  })
})
