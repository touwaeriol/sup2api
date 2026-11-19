import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import { useForm, type FormApi, type UseFormOptions } from './useForm'
import { SECRET_MASK, type Rules } from './rules'
import { toFieldPath as toPath } from '@sub2api/host'
import { expandPath, overlaps, setPath } from './path'
import SForm from './SForm.vue'
import SField from '../SField.vue'
import SSettingRow from '../SSettingRow.vue'
import SInput from '../SInput.vue'
import SFormModal from '../SFormModal.vue'

type M = Record<string, any>

function setup(rules: Rules<M>, init: M = {}, extra: Partial<UseFormOptions<M>> = {}) {
  const model = ref<M>(init)
  let form!: FormApi<M>
  const w = mount(
    defineComponent({
      setup() {
        form = useForm<M>({ model: () => model.value, update: (v) => (model.value = v), rules, ...extra })
        return () => null
      }
    })
  )
  return { model, form, w }
}

/** The message of `rules` for `value` after validate(). */
async function check(rule: Rules<M>[string], value: unknown): Promise<string | undefined> {
  const { form } = setup({ x: rule }, { x: value })
  await form.validate()
  return form.error('x')
}

describe('built-in rules', () => {
  it('required: empty values fail, a stored secret and false pass', async () => {
    for (const v of [undefined, null, '', []]) expect(await check({ required: true }, v)).toBe('This field is required')
    expect(await check({ required: true }, SECRET_MASK)).toBeUndefined()
    expect(await check({ required: true }, false)).toBeUndefined()
    expect(await check({ required: true }, 0)).toBeUndefined()
  })
  it('whitespace: spaces only fail', async () => {
    expect(await check({ required: true, whitespace: true }, '   ')).toBe('Cannot be only spaces')
    expect(await check({ whitespace: true }, ' a ')).toBeUndefined()
  })
  it('an empty optional value skips the built-in checks', async () => {
    expect(await check({ min: 8, pattern: /^x+$/, type: 'url', enum: ['a'] }, '')).toBeUndefined()
    expect(await check({ min: 8 }, undefined)).toBeUndefined()
  })
  it('type', async () => {
    expect(await check({ type: 'integer' }, 1.5)).toBe('Must be an integer')
    expect(await check({ type: 'integer' }, 2)).toBeUndefined()
    expect(await check({ type: 'number' }, 'x')).toBe('Must be a number')
    expect(await check({ type: 'url' }, 'ftp://x')).toBe('Must be an http(s) URL')
    expect(await check({ type: 'url' }, 'https://x.y/z')).toBeUndefined()
    expect(await check({ type: 'email' }, 'a@b')).toBe('Must be an email address')
    expect(await check({ type: 'date-time' }, '2026-11-19 10:00')).toBe('Must be a date and time')
    expect(await check({ type: 'date-time' }, '2026-11-19T10:00:00Z')).toBeUndefined()
    expect(await check({ type: 'boolean' }, 'yes')).toBe('Invalid value')
    expect(await check({ type: 'array' }, [1])).toBeUndefined()
    expect(await check({ type: 'object' }, [1])).toBe('Invalid value')
  })
  it('min / max / len by the kind of value; len wins', async () => {
    expect(await check({ min: 3 }, 'ab')).toBe('At least 3 characters')
    expect(await check({ max: 2 }, 'abc')).toBe('At most 2 characters')
    // Code points, as JSON Schema and Go count.
    expect(await check({ max: 2 }, '😀😀')).toBeUndefined()
    expect(await check({ min: 2 }, [1])).toBe('At least 2 items')
    expect(await check({ max: 1 }, [1, 2])).toBe('At most 1 items')
    expect(await check({ min: 1 }, 0)).toBe('Must be ≥ 1')
    expect(await check({ max: 65535 }, 70000)).toBe('Must be ≤ 65535')
    expect(await check({ len: 2, min: 5 }, 'ab')).toBeUndefined()
    expect(await check({ len: 2 }, 'abc')).toBe('Must be exactly 2 characters')
    expect(await check({ len: 2 }, [1])).toBe('Must have exactly 2 items')
    expect(await check({ len: 3 }, 4)).toBe('Must be 3')
  })
  it('pattern (strings only; a stored secret is never matched), enum', async () => {
    expect(await check({ pattern: /^[!-~]+$/ }, 'a b')).toBe('Invalid format')
    expect(await check({ pattern: /^\d+$/g }, '12')).toBeUndefined()
    expect(await check({ pattern: /^\d+$/g }, '12')).toBeUndefined()
    expect(await check({ pattern: /^\d+$/ }, 12)).toBeUndefined()
    expect(await check({ pattern: /^x$/, min: 20 }, SECRET_MASK)).toBeUndefined()
    expect(await check({ enum: ['a', 'b'] }, 'c')).toBe('Not one of the allowed values')
    expect(await check({ enum: [0, false] }, false)).toBeUndefined()
  })
  it('message overrides the built-in text; the first failing rule wins', async () => {
    expect(await check({ min: 3, message: 'too short' }, 'a')).toBe('too short')
    expect(await check({ min: 3, message: (c) => `${c.path} short` }, 'a')).toBe('x short')
    expect(await check([{ required: true, message: 'A' }, { min: 9, message: 'B' }], 'abc')).toBe('B')
    expect(await check([{ min: 2, message: 'A' }, { min: 9, message: 'B' }], 'a')).toBe('A')
  })
  it('validator: sync / async, runs on empty values, rejection is a failure', async () => {
    expect(await check({ validator: (v) => (v === 'ok' ? true : 'bad') }, 'no')).toBe('bad')
    expect(await check({ validator: async (v) => (v === 'ok' ? undefined : 'bad') }, 'ok')).toBeUndefined()
    expect(await check({ validator: (v) => (v ? true : 'need one') }, '')).toBe('need one')
    expect(await check({ validator: () => Promise.reject(new Error('taken')) }, 'a')).toBe('taken')
    expect(await check({ validator: () => Promise.reject(new Error('taken')), message: 'nope' }, 'a')).toBe('nope')
    expect(await check({ validator: () => 'never' }, SECRET_MASK)).toBeUndefined()
  })
  it('the validator sees the path, the whole model and the frame label', async () => {
    const seen: unknown[] = []
    const { form } = setup({ b: { validator: (v, c) => void seen.push(v, c.path, c.model.a, c.label) } }, { a: 1, b: 2 })
    form.claim('b', () => 'Bee')
    await form.validate()
    expect(seen).toEqual([2, 'b', 1, 'Bee'])
  })
})

describe('useForm', () => {
  it('validate() resolves a boolean, never rejects, and shows every failure', async () => {
    const { form } = setup({ a: { required: true }, b: { min: 2 } }, { b: 'x' })
    await expect(form.validate()).resolves.toBe(false)
    expect(form.errors).toEqual({ a: 'This field is required', b: 'At least 2 characters' })
    form.set('a', 'y')
    form.set('b', 'yy')
    await flushPromises()
    await expect(form.validate()).resolves.toBe(true)
    expect(form.errors).toEqual({})
  })

  it('is lazy until touched, then validates every change', async () => {
    const { form } = setup({ a: { min: 3 } })
    form.set('a', 'x')
    await flushPromises()
    expect(form.error('a')).toBeUndefined()
    form.touch('a', 'blur')
    await flushPromises()
    expect(form.error('a')).toBe('At least 3 characters')
    form.set('a', 'xyz')
    await flushPromises()
    expect(form.error('a')).toBeUndefined()
    form.set('a', 'x')
    await flushPromises()
    expect(form.error('a')).toBe('At least 3 characters')
  })

  it('validate() touches every field', async () => {
    const { form } = setup({ a: { min: 3 } }, { a: 'abcd' })
    await form.validate()
    form.set('a', 'x')
    await flushPromises()
    expect(form.error('a')).toBe('At least 3 characters')
  })

  it('a rule with trigger runs only on those events', async () => {
    const { form } = setup({ a: [{ min: 3, trigger: 'change' }, { max: 1, trigger: 'blur', message: 'blur rule' }] }, { a: 'abcd' })
    form.set('a', 'x')
    await flushPromises()
    expect(form.error('a')).toBe('At least 3 characters')
    form.set('a', 'abcd')
    await flushPromises()
    expect(form.error('a')).toBeUndefined()
    form.touch('a', 'blur')
    await flushPromises()
    expect(form.error('a')).toBe('blur rule')
  })

  it('drops a late async result (a newer run decides) and aborts the older run', async () => {
    const pending: Array<{ v: unknown; signal: AbortSignal; done: (m?: string) => void }> = []
    const { form } = setup({ a: { validator: (v, c) => new Promise<string | void>((done) => pending.push({ v, signal: c.signal, done })) } })
    form.touch('a', 'blur')
    form.set('a', 'second')
    const all = form.validate()
    expect(pending.map((p) => p.v)).toEqual([undefined, 'second', 'second'])
    expect(pending[0].signal.aborted).toBe(true)
    // The newest run answers first, the old ones late.
    pending[2].done()
    pending[0].done('stale')
    pending[1].done('stale too')
    await expect(all).resolves.toBe(true)
    expect(form.error('a')).toBeUndefined()
  })

  it('validate() waits for a newer run started meanwhile', async () => {
    const pending: Array<(m?: string) => void> = []
    const { form } = setup({ a: { validator: () => new Promise<string | void>((done) => pending.push(done)) } })
    const all = form.validate()
    form.set('a', 'x')
    pending[0]()
    pending[1]('bad')
    await expect(all).resolves.toBe(false)
    expect(form.error('a')).toBe('bad')
  })

  it('expands * over the current items', async () => {
    const { form } = setup({ 'items.*.url': { required: true, type: 'url' } }, { items: [{ url: 'https://a' }, { url: 'x' }, {}] })
    expect(await form.validate()).toBe(false)
    expect(form.errors).toEqual({ 'items.1.url': 'Must be an http(s) URL', 'items.2.url': 'This field is required' })
    form.set('items', [{ url: 'https://a' }])
    await flushPromises()
    expect(await form.validate()).toBe(true)
    expect(form.errors).toEqual({})
  })

  it('when: off rules do not run and their errors go', async () => {
    const { form } = setup({ url: { required: true, when: (m) => !!m.on } }, { on: true })
    expect(await form.validate()).toBe(false)
    expect(form.required('url')).toBe(true)
    form.set('on', false)
    await flushPromises()
    expect(form.error('url')).toBeUndefined()
    expect(form.required('url')).toBe(false)
    expect(await form.validate()).toBe(true)
  })

  it('validateField and clearValidate', async () => {
    const { form } = setup({ a: { required: true }, b: { required: true } })
    expect(await form.validateField('a')).toBe(false)
    expect(form.errors).toEqual({ a: 'This field is required' })
    await form.validate()
    form.clearValidate('a')
    expect(Object.keys(form.errors)).toEqual(['b'])
    form.clearValidate()
    expect(form.errors).toEqual({})
    // Cleared fields are lazy again.
    form.set('b', '')
    await flushPromises()
    expect(form.errors).toEqual({})
  })

  it('writes patches only: keys no rule or field knows go back unchanged', async () => {
    const hidden = { access_token: 'tok', expires_at: 123, token_for: 'sa', nested: { keep: true } }
    const { form, model } = setup({ project_id: { pattern: /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/ } }, { ...hidden, project_id: 'old-project' })
    form.set('project_id', 'new-project')
    form.set('nested.extra', 1)
    await nextTick()
    expect(model.value).toEqual({ ...hidden, nested: { keep: true, extra: 1 }, project_id: 'new-project' })
    expect(await form.validate()).toBe(true)
    expect(model.value).toEqual({ ...hidden, nested: { keep: true, extra: 1 }, project_id: 'new-project' })
  })

  it('writes in one tick build on each other; undefined removes the key', async () => {
    const { form, model } = setup({}, { a: 1, b: 2 })
    form.set('a', 10)
    form.set('b', undefined)
    form.set('c', 3)
    expect(form.get('a')).toBe(10)
    await nextTick()
    expect(model.value).toEqual({ a: 10, c: 3 })
    expect('b' in model.value).toBe(false)
  })

  it('a stored secret passes required and goes back as is', async () => {
    const { form, model } = setup({ api_key: { required: true, min: 8, pattern: /^[!-~]+$/ } }, { api_key: SECRET_MASK })
    expect(await form.validate()).toBe(true)
    expect(model.value.api_key).toBe(SECRET_MASK)
  })

  it('fills defaults into absent keys of each value handed in, never over present ones or what the user clears', async () => {
    const { form, model } = setup({}, { region: 'eu-west-1', nested: { a: 1 } }, { defaults: { region: 'us-east-1', prefix: 'global', nested: { a: 0, b: 2 } } })
    expect(model.value).toEqual({ region: 'eu-west-1', prefix: 'global', nested: { a: 1, b: 2 } })
    form.set('prefix', undefined)
    await flushPromises()
    expect(model.value.prefix).toBeUndefined()
    // A new value from the parent (another account type) is filled again.
    model.value = {}
    await flushPromises()
    expect(model.value).toEqual({ region: 'us-east-1', prefix: 'global', nested: { a: 0, b: 2 } })
  })

  it('merges server errors: local first, a write clears the path, a new set shows again', async () => {
    const server = ref<Record<string, string>>({ 'credentials.api_key': 'rejected upstream', 'values/notify/url': 'bad url', other: 'x' })
    const { form } = setup({ api_key: { min: 3 } }, { api_key: 'abcd' }, { serverErrors: () => server.value })
    expect(form.errors).toEqual({ api_key: 'rejected upstream', 'notify.url': 'bad url', other: 'x' })
    form.touch('api_key', 'blur')
    form.set('api_key', 'a')
    await flushPromises()
    expect(form.error('api_key')).toBe('At least 3 characters')
    form.set('api_key', 'abcd')
    await flushPromises()
    expect(form.error('api_key')).toBeUndefined()
    form.set('notify', { url: 'https://x' })
    expect(form.error('notify.url')).toBeUndefined()
    expect(form.error('other')).toBe('x')
    server.value = { 'credentials.api_key': 'again' }
    await nextTick()
    expect(form.error('api_key')).toBe('again')
  })

  it('a write drops the in-flight check of the old value (a blur-only async rule)', async () => {
    const pending: Array<(m?: string) => void> = []
    const { form } = setup({ a: { trigger: 'blur', validator: () => new Promise<string | void>((done) => pending.push(done)) } })
    form.touch('a', 'blur')
    form.set('a', 'fixed')
    pending[0]('old value is bad')
    await flushPromises()
    expect(form.error('a')).toBeUndefined()
  })

  it('the blur of a list frame checks its items, and touches them', async () => {
    const { form } = setup({ 'tags.*': { pattern: /^[a-z]+$/ } }, { tags: ['ok', 'Bad'] })
    form.touch('tags', 'blur')
    await flushPromises()
    expect(form.errors).toEqual({ 'tags.1': 'Invalid format' })
    form.set('tags', ['ok', 'fine', 'NO'])
    await flushPromises()
    expect(form.errors).toEqual({ 'tags.2': 'Invalid format' })
  })

  it('clearValidate of a path clears the paths inside it', async () => {
    const { form } = setup({ 'items.*.url': { required: true } }, { items: [{}, {}] })
    await form.validate()
    expect(Object.keys(form.errors)).toEqual(['items.0.url', 'items.1.url'])
    form.clearValidate('items')
    expect(form.errors).toEqual({})
  })

  it('bind gives the v-model of a path, unconverted', async () => {
    const { form, model } = setup({}, { a: 1 })
    const b = form.bind('a')
    expect(b.modelValue).toBe(1)
    b['onUpdate:modelValue'](null)
    await nextTick()
    expect(model.value).toEqual({ a: null })
  })

  it('bindNumber leaves a cleared number box out of the value', async () => {
    const { form, model } = setup({}, { port: 587, keep: 1 })
    const b = form.bindNumber('port')
    expect(b.modelValue).toBe(587)
    b['onUpdate:modelValue'](25)
    await nextTick()
    expect(model.value).toEqual({ port: 25, keep: 1 })
    form.bindNumber('port')['onUpdate:modelValue'](null)
    await nextTick()
    expect(model.value).toEqual({ keep: 1 })
    expect('port' in model.value).toBe(false)
    form.bindNumber('port')['onUpdate:modelValue'](0)
    await nextTick()
    expect(model.value).toEqual({ keep: 1, port: 0 })
  })

  it('aborts the in-flight checks when the form goes away', async () => {
    let signal!: AbortSignal
    const { form, w } = setup({ a: { validator: (_v, c) => ((signal = c.signal), new Promise<void>(() => {})) } })
    void form.validate()
    expect(signal.aborted).toBe(false)
    w.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('lists errors no frame claims', async () => {
    const { form } = setup({ a: { required: true } }, {}, { serverErrors: () => ({ b: 'server b', credentials: 'whole' }) })
    await form.validate()
    const release = form.claim('a')
    expect(form.unclaimed.value).toEqual(['server b', 'whole'])
    release()
    expect(form.unclaimed.value).toEqual(['server b', 'whole', 'This field is required'])
  })
})

describe('paths', () => {
  it('setPath copies the way and shares the rest', () => {
    const src = { a: { b: 1 }, keep: { x: 1 } }
    const out = setPath(src, 'a.b', 2)
    expect(out).toEqual({ a: { b: 2 }, keep: { x: 1 } })
    expect(out.keep).toBe(src.keep)
    expect(src.a.b).toBe(1)
    expect(setPath({}, 'list.0.url', 'u')).toEqual({ list: [{ url: 'u' }] })
  })
  it('overlaps: ancestors, descendants and * steps', () => {
    expect(overlaps('items', 'items.0.url')).toBe(true)
    expect(overlaps('items.0.url', 'items')).toBe(true)
    expect(overlaps('items.*.url', 'items.3.url')).toBe(true)
    expect(overlaps('items.*', 'items.3.url')).toBe(true)
    expect(overlaps('items.1', 'items.3.url')).toBe(false)
    expect(overlaps('item', 'items')).toBe(false)
  })
  it('toPath reads the server field forms', () => {
    expect(toPath('credentials.a.b')).toBe('a.b')
    expect(toPath('values/a/0/b')).toBe('a.0.b')
    expect(toPath('values.a.b')).toBe('a.b')
    expect(toPath('values/a~1b')).toBe('a/b')
    expect(toPath('values')).toBe('')
    expect(toPath('api_key')).toBe('api_key')
  })
  it('expandPath', () => {
    expect(expandPath('a.*.b', { a: [1, 2] })).toEqual(['a.0.b', 'a.1.b'])
    expect(expandPath('a.*.b', {})).toEqual([])
    expect(expandPath('x', {})).toEqual(['x'])
  })
})

describe('SForm with SField / SSettingRow prop', () => {
  function page(rules: Rules<M>, init: M = {}, onSubmit?: () => void) {
    const model = ref<M>(init)
    let form!: FormApi<M>
    const w = mount(
      defineComponent({
        setup() {
          form = useForm<M>({ model: () => model.value, update: (v) => (model.value = v), rules })
          return () =>
            h(SForm, { form, onSubmit }, () => [
              h(SField, { prop: 'name', label: 'Name' }, () => h(SInput, { modelValue: form.get('name'), 'onUpdate:modelValue': (v: unknown) => form.set('name', v) })),
              h(SSettingRow, { prop: 'port', label: 'Port' }, () => h(SInput, { type: 'number', modelValue: form.get('port'), 'onUpdate:modelValue': (v: unknown) => form.set('port', v) })),
              h(SField, { prop: 'note', label: 'Note', required: false, error: '' }, () => h(SInput, { modelValue: form.get('note') }))
            ])
        }
      }),
      { attachTo: document.body }
    )
    return { w, model, form: () => form }
  }

  it('stars required fields, validates on blur and then on input, shows the message', async () => {
    const { w } = page({ name: { required: true }, port: { min: 1, max: 65535 }, note: { required: true } })
    await flushPromises()
    const labels = w.findAll('label, p').filter((l) => l.text().startsWith('Name') || l.text().startsWith('Port') || l.text().startsWith('Note'))
    expect(labels.map((l) => l.text())).toEqual(['Name*', 'Port', 'Note'])
    const input = w.find('input')
    await input.setValue('a')
    await flushPromises()
    expect(w.text()).not.toContain('This field is required')
    await input.setValue('')
    await input.trigger('blur')
    await flushPromises()
    expect(w.text()).toContain('This field is required')
    expect(input.attributes('aria-invalid')).toBe('true')
    await input.setValue('b')
    await flushPromises()
    expect(w.text()).not.toContain('This field is required')
    w.unmount()
  })

  it('submits only after validate() passed; explicit error / required win', async () => {
    let submitted = 0
    const { w, form } = page({ port: { required: true, max: 10 }, note: { required: true } }, { port: 99 }, () => submitted++)
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(submitted).toBe(0)
    expect(w.text()).toContain('Must be ≤ 10')
    // The note frame passes error="" and required=false: the form's message is not shown there.
    expect(form().error('note')).toBe('This field is required')
    expect(w.findAll('.input-error-text').map((e) => e.text())).toEqual(['Must be ≤ 10'])
    form().set('port', 5)
    form().set('note', 'n')
    await flushPromises()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(submitted).toBe(1)
    w.unmount()
  })

  it('disables every control, and renders no second <form> inside SFormModal', async () => {
    const model = ref<M>({})
    const w = mount(
      defineComponent({
        setup() {
          const form = useForm<M>({ model: () => model.value, update: (v) => (model.value = v), rules: {} })
          return () => [h(SForm, { form, disabled: true }, () => h(SInput, { modelValue: '' })), h(SFormModal, { open: true, title: 'T' }, () => h(SForm, { form }, () => h(SInput, { modelValue: '' })))]
        }
      }),
      { attachTo: document.body }
    )
    await flushPromises()
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    expect(document.querySelectorAll('form form').length).toBe(0)
    w.unmount()
  })
})

describe('get() in a render', () => {
  it('a render that runs while a write is pending still follows later changes', async () => {
    const model = ref<M>({ on: true })
    let form!: FormApi<M>
    const w = mount(
      defineComponent({
        setup() {
          form = useForm<M>({ model: () => model.value, update: (v) => (model.value = v), rules: {} })
          return () => h('p', String(form.get('on')))
        }
      })
    )
    form.set('on', false)
    // The re-render runs before the draft is dropped (it reads the draft).
    await flushPromises()
    expect(w.text()).toBe('false')
    model.value = { on: true }
    await flushPromises()
    expect(w.text()).toBe('true')
    form.set('on', false)
    await flushPromises()
    expect(w.text()).toBe('false')
  })
})
