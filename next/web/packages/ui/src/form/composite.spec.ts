import { describe, expect, it } from 'vitest'
import { mounted, editor, type M } from './testHost'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import { ApiError, type ApiClient } from '@sub2api/host'
import SSecretInput from '../SSecretInput.vue'
import SUrlPresetInput from '../SUrlPresetInput.vue'
import SResourcePage from '../SResourcePage.vue'
import SInput from '../SInput.vue'
import SField from '../SField.vue'
import { toastState } from '../feedback'
import { nativeAccountFormProps, useCredentialForm } from './credential'
import SFormSection from './SFormSection.vue'
import SCredentialForm from './SCredentialForm.vue'
import { SECRET_MASK } from './rules'

describe('SSecretInput', () => {
  it('shows a stored secret as an empty box, replaces it on input and keeps it when cleared', async () => {
    const value = ref<string | undefined>(SECRET_MASK)
    const w = mount(() => h(SSecretInput, { modelValue: value.value, 'onUpdate:modelValue': (v: any) => (value.value = v), placeholder: 'sk-…' }))
    const input = w.find('input')
    expect(input.element.value).toBe('')
    expect(input.attributes('placeholder')).toBe(SECRET_MASK)
    expect(input.attributes('autocomplete')).toBe('new-password')
    expect(input.attributes('type')).toBe('password')
    expect(w.text()).toContain('A value is stored. Leave empty to keep it.')
    await input.setValue('new-secret')
    expect(value.value).toBe('new-secret')
    await nextTick()
    expect(w.find('input').attributes('placeholder')).toBe('sk-…')
    await w.find('input').setValue('')
    expect(value.value).toBe(SECRET_MASK)
    // Another account: nothing is stored, clearing sends ''.
    value.value = undefined
    await nextTick()
    await w.find('input').setValue('x')
    await w.find('input').setValue('')
    expect(value.value).toBe('')
  })
})

describe('SSecretInput plain', () => {
  it('types a sensitive address in a text box, masked and kept like a secret while stored', async () => {
    const value = ref<string | undefined>(SECRET_MASK)
    const w = mount(() => h(SSecretInput, { modelValue: value.value, 'onUpdate:modelValue': (v: any) => (value.value = v), plain: true, clearable: true }))
    const input = w.find('input')
    expect(input.attributes('type')).toBe('text')
    expect(input.element.value).toBe('')
    expect(input.attributes('placeholder')).toBe(SECRET_MASK)
    expect(w.find('button[aria-pressed]').exists()).toBe(false)
    await input.setValue('https://hooks.example/x')
    expect(value.value).toBe('https://hooks.example/x')
    await w.find('input').setValue('')
    expect(value.value).toBe(SECRET_MASK)
    await w.find('[data-testid="secret-clear"]').trigger('click')
    expect(value.value).toBe('')
  })
})

describe('SSecretInput clearable', () => {
  function secret(props: M) {
    const value = ref<string | undefined>(props.modelValue)
    const w = mount(() => h(SSecretInput, { ...props, modelValue: value.value, 'onUpdate:modelValue': (v: string) => (value.value = v) }))
    return { w, value }
  }
  it('clears an optional stored secret with its button, then forgets it', async () => {
    const { w, value } = secret({ modelValue: SECRET_MASK, clearable: true })
    expect(w.find('[data-testid="secret-clear"]').text()).toBe('Clear the saved value')
    await w.find('[data-testid="secret-clear"]').trigger('click')
    expect(value.value).toBe('')
    await nextTick()
    expect(w.find('[data-testid="secret-clear"]').exists()).toBe(false)
    expect(w.text()).not.toContain('A value is stored')
    await w.find('input').setValue('x')
    await w.find('input').setValue('')
    expect(value.value).toBe('')
  })
  it('without the button clearing the box keeps the stored value, as before', async () => {
    const { w, value } = secret({ modelValue: SECRET_MASK, clearable: true })
    await w.find('input').setValue('typed')
    await w.find('input').setValue('')
    expect(value.value).toBe(SECRET_MASK)
  })
  it('no button for a secret that is not clearable, or with nothing stored', () => {
    expect(secret({ modelValue: SECRET_MASK }).w.find('[data-testid="secret-clear"]').exists()).toBe(false)
    expect(secret({ modelValue: undefined, clearable: true }).w.find('[data-testid="secret-clear"]').exists()).toBe(false)
  })
})

describe('SUrlPresetInput', () => {
  it('is a URL box with suggestions; empty emits undefined', async () => {
    const out: unknown[] = []
    const w = mount(SUrlPresetInput, { props: { modelValue: '', presets: ['https://api.a.com', 'https://b'], 'onUpdate:modelValue': (v: unknown) => out.push(v) }, attachTo: document.body })
    expect(w.find('input').attributes('placeholder')).toBe('https://api.a.com')
    expect(w.findAll('datalist option').map((o) => o.attributes('value'))).toEqual(['https://api.a.com', 'https://b'])
    await w.find('input').setValue('https://x')
    await w.find('input').setValue('')
    expect(out).toEqual(['https://x', undefined])
    await w.find('button').trigger('click')
    const item = [...document.querySelectorAll('[role="menuitem"]')].find((e) => e.textContent?.includes('https://b')) as HTMLElement
    item.click()
    expect(out.at(-1)).toBe('https://b')
    w.unmount()
  })
  it('offers only the allowed values, keeps a stored one outside them, and locks a single one', async () => {
    const w = mount(SUrlPresetInput, { props: { modelValue: 'https://admin-set', allowed: ['https://official'] } })
    expect(w.findAll('option').map((o) => o.text())).toEqual(['https://admin-set', 'https://official'])
    expect(w.find('select').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('Set by an administrator')
    const two = mount(SUrlPresetInput, { props: { modelValue: undefined, allowed: ['https://a', 'https://b'], placeholder: 'pick' } })
    expect(two.find('select').attributes('disabled')).toBeUndefined()
    expect(two.findAll('option').map((o) => o.text())).toEqual(['pick', 'https://a', 'https://b'])
    expect(two.text()).not.toContain('Set by an administrator')
  })
})

describe('useCredentialForm / SCredentialForm', () => {
  const Form = defineComponent({
    props: nativeAccountFormProps,
    emits: ['update:modelValue'],
    setup(props, { emit, expose }) {
      const form = useCredentialForm(props, (_e, v) => emit('update:modelValue', v), {
        defaults: { region: 'us-east-1' },
        rules: { access_key_id: { required: true, pattern: /^[A-Z0-9]{16,128}$/ } }
      })
      expose({ validate: form.validate, creating: form.creating })
      return () =>
        h(SCredentialForm, { form }, () =>
          h(SField, { prop: 'access_key_id', label: 'Key id' }, () => h(SInput, { modelValue: form.get('access_key_id'), 'onUpdate:modelValue': (v: any) => form.set('access_key_id', v) }))
        )
    }
  })

  it('fills defaults when creating only, and lists server errors no field shows', async () => {
    const created = editor(Form, { errors: { region: 'unknown region', access_key_id: 'rejected' } })
    await flushPromises()
    expect(created.credentials.value).toEqual({ region: 'us-east-1' })
    expect(created.w.find('[data-testid="credential-form-errors"]').text()).toBe('unknown region')
    expect(created.w.text()).toContain('rejected')
    // Inside the editor's <form>: no nested form.
    expect(document.querySelectorAll('form form').length).toBe(0)
    const edited = editor(Form, { mode: 'edit', modelValue: { access_key_id: 'AKIAABCDEFGHIJKLMNOP', token_for: 'kept' } })
    await flushPromises()
    expect(edited.credentials.value).toEqual({ access_key_id: 'AKIAABCDEFGHIJKLMNOP', token_for: 'kept' })
    expect(await edited.validate()).toBe(true)
  })

  it('nativeAccountFormProps: the §23.5 props, a new account by default', () => {
    let seen!: Record<string, unknown>
    const Probe = defineComponent({ props: nativeAccountFormProps, setup: (props) => () => ((seen = { ...props }), h('i')) })
    mounted.push(mount(Probe))
    expect(seen).toEqual({ modelValue: {}, mode: 'create', account: null, accountType: null, errors: undefined, proxyTicket: undefined })
  })
})

describe('SFormSection', () => {
  /** A form of three fields, b and c in a section. */
  function sectioned(section: Record<string, unknown>, modelValue: M = {}, errors?: Record<string, string>) {
    const Form = defineComponent({
      props: nativeAccountFormProps,
      emits: ['update:modelValue'],
      setup(props, { emit, expose }) {
        const form = useCredentialForm(props, (_e, v) => emit('update:modelValue', v), { rules: { b: { max: 3 }, c: { required: true } } })
        expose({ validate: form.validate })
        const input = (k: string) => h(SField, { prop: k, label: k.toUpperCase() }, () => h(SInput, form.bind(k)))
        return () => h(SCredentialForm, { form }, () => [input('a'), h(SFormSection, { title: 'More', fields: ['b', 'c'], ...section }, () => [input('b'), input('c')])])
      }
    })
    return editor(Form, { mode: 'edit', modelValue, errors })
  }
  const labels = (w: VueWrapper) => w.findAll('label').map((l) => l.text())

  it('a title with its help above the fields, a rule on top', async () => {
    const { w } = sectioned({ help: 'Only when needed' })
    await flushPromises()
    const section = w.get('[data-testid="form-section"]')
    expect(section.get('h4').text()).toBe('More')
    expect(section.get('p').text()).toBe('Only when needed')
    expect(section.classes()).toContain('s-form-section')
    expect(labels(w)).toEqual(['A', 'B', 'C*'])
  })

  it('collapsed: closed while empty, open with a value or an error, then as the user left it', async () => {
    const empty = sectioned({ collapsed: true })
    await flushPromises()
    expect(labels(empty.w)).toEqual(['A'])
    expect(empty.w.text()).toContain('More')
    await empty.w.get('[data-testid="form-section"] button').trigger('click')
    expect(labels(empty.w)).toEqual(['A', 'B', 'C*'])
    await empty.w.get('[data-testid="form-section"] button').trigger('click')
    expect(labels(empty.w)).toEqual(['A'])

    const valued = sectioned({ collapsed: true }, { c: 'x' })
    await flushPromises()
    expect(labels(valued.w)).toEqual(['A', 'B', 'C*'])

    const refused = sectioned({ collapsed: true }, {}, { b: 'not allowed' })
    await flushPromises()
    expect(refused.w.text()).toContain('not allowed')
    await refused.w.get('[data-testid="form-section"] button').trigger('click')
    expect(labels(refused.w)).toEqual(['A'])
  })

  it('collapsed: opens by itself when validate() finds an error inside', async () => {
    const { w, validate } = sectioned({ collapsed: true })
    await flushPromises()
    expect(labels(w)).toEqual(['A'])
    expect(await validate()).toBe(false)
    await flushPromises()
    expect(labels(w)).toEqual(['A', 'B', 'C*'])
  })
})

// The anthropic account form written with createApiKeyForm (DESIGN §5.10):
//   api_key required, any non-blank string (DESIGN-2026-11-20-KEY-AND-BASE-URL-RULES §5.1);
//   base_url as the plugins' apikey.NormalizeBaseURL takes it (trimmed; http(s), any scheme case, a host,
//   no userinfo, whitespace, query or fragment) with the preset https://api.anthropic.com; no defaults.
describe('SResourcePage', () => {
  function fakeList(total = 45) {
    const queries: M[] = []
    const posts: M[] = []
    const client = {
      list: async (path: string, q: M) => {
        expect(path).toBe('/assets')
        queries.push(q)
        return { items: [{ id: q.page * 100, name: `asset ${q.q ?? ''}` }], page: { page: q.page, page_size: q.page_size, total } }
      },
      post: async (_p: string, body: M) => posts.push(body)
    } as unknown as ApiClient
    return { client, queries, posts }
  }

  it('lists, searches by the named parameter, pages, and renders cell slots', async () => {
    const { client, queries } = fakeList()
    const w = mount(SResourcePage, {
      props: { client, path: '/assets', columns: [{ key: 'id', label: 'ID' }, { key: 'name', label: 'Name' }], search: 'q', rowKey: 'id' },
      slots: { 'cell-name': ({ row }: any) => h('b', row.name) },
      attachTo: document.body
    })
    await flushPromises()
    expect(queries).toEqual([{ page: 1, page_size: 20, q: '' }])
    expect(w.find('b').text()).toBe('asset')
    await w.find('input[type="search"]').setValue('cat')
    await new Promise((r) => setTimeout(r, 300))
    await flushPromises()
    expect(queries.at(-1)).toEqual({ page: 1, page_size: 20, q: 'cat' })
    await w.findAll('button').find((b) => b.text() === '2')!.trigger('click')
    await flushPromises()
    expect(queries.at(-1)).toMatchObject({ page: 2 })
    expect(w.find('[data-testid="resource-create"]').exists()).toBe(false)
    const before = queries.length
    await w.get('[data-testid="resource-refresh"]').trigger('click')
    await flushPromises()
    expect(queries.length).toBe(before + 1)
    expect(queries.at(-1)).toEqual({ page: 2, page_size: 20, q: 'cat' })
    w.unmount()
  })

  it('a create that throws keeps the dialog open and says why', async () => {
    const { client } = fakeList()
    const w = mount(SResourcePage, {
      props: { client, path: '/assets', columns: [{ key: 'id', label: 'ID' }], canCreate: true, submit: () => Promise.reject(new ApiError(409, { code: 'conflict', message: 'name taken' })) },
      slots: { create: () => h('p', 'form') },
      attachTo: document.body
    })
    mounted.push(w)
    await flushPromises()
    await w.find('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    ;([...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Save') as HTMLButtonElement).click()
    await flushPromises()
    expect(toastState.items.map((t) => t.message)).toEqual(['name taken'])
    expect(document.body.textContent).toContain('form')
  })

  it('creates through the dialog, then reloads page 1 and emits created', async () => {
    const { client, queries, posts } = fakeList()
    const name = ref('')
    let accept = false
    const submit = async () => {
      if (!accept) return false
      await client.post('/assets', { name: name.value })
    }
    const created: unknown[] = []
    const w = mount(SResourcePage, {
      props: { client, path: '/assets', columns: [{ key: 'id', label: 'ID' }], canCreate: true, createLabel: 'New asset', submit, onCreated: () => created.push(1) },
      slots: { create: () => h(SInput, { modelValue: name.value, 'onUpdate:modelValue': (v: any) => (name.value = v) }) },
      attachTo: document.body
    })
    await flushPromises()
    await w.findAll('button').find((b) => b.text() === '2')!.trigger('click')
    await flushPromises()
    await w.find('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    const save = () => [...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Save') as HTMLButtonElement
    save().click()
    await flushPromises()
    expect(created).toEqual([])
    expect(document.body.textContent).toContain('New asset')
    accept = true
    name.value = 'logo'
    save().click()
    await flushPromises()
    expect(posts).toEqual([{ name: 'logo' }])
    expect(created).toEqual([1])
    expect(queries.at(-1)).toMatchObject({ page: 1 })
    w.unmount()
  })
})
