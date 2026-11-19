// Native account forms (CONTRACTS §23.5) in a spec: mounted the way the
// account editor mounts them, typed into through their labels, and the
// api_key + base_url cases every API key form shares.
import { flushPromises, mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, ref, type Component, type Ref } from 'vue'
import type { AccountTypeView } from '@sub2api/host'
import { SECRET_MASK } from '../form/rules'
import { track } from './host'

type M = Record<string, any>

export interface MountedAccountForm {
  w: VueWrapper
  /** The credentials v-model, as the editor holds it. */
  credentials: Ref<M>
  /** The form's exposed validate(). */
  validate(): Promise<boolean>
  /** The credentials as the editor would save them (JSON). */
  saved(): M
}

/**
 * Mounts a native account form inside a <form>, as the account editor does:
 * v-model credentials and the §23.5 props. With `stored` it edits an account
 * holding those credentials (secrets as SECRET_MASK), else it creates one.
 * `form` is typed loosely: the plugin's component carries its own copy of
 * the Vue types. Call fakeHost() first (beforeEach): the forms read the
 * console through useHost().
 */
export function mountAccountForm(form: object, opts: { accountType: AccountTypeView; stored?: M; errors?: Record<string, string> }): MountedAccountForm {
  const credentials = ref<M>(opts.stored ? { ...opts.stored } : {})
  const inner = ref<{ validate: () => boolean | Promise<boolean> }>()
  const w = mount(
    defineComponent({
      setup: () => () =>
        h('form', [
          h(form as Component, {
            ref: inner,
            modelValue: credentials.value,
            'onUpdate:modelValue': (v: M) => (credentials.value = v),
            mode: opts.stored ? 'edit' : 'create',
            account: opts.stored ? { id: 1, type: opts.accountType.type, credentials: opts.stored } : null,
            accountType: opts.accountType,
            errors: opts.errors,
            proxyTicket: async () => 'ticket'
          })
        ])
    }),
    { attachTo: document.body }
  )
  track(w)
  return {
    w,
    credentials,
    validate: async () => inner.value!.validate(),
    saved: () => JSON.parse(JSON.stringify(credentials.value))
  }
}

/** The control an SField label names (the frame gives its first control the label's id); a required field's star is ignored. */
export function control(w: VueWrapper, label: string): Omit<DOMWrapper<Element>, 'exists'> {
  const l = w.findAll('label').find((x) => x.text().replace(/\*$/, '').trim() === label)
  if (!l) throw new Error(`no label "${label}" in ${w.findAll('label').map((x) => x.text())}`)
  return w.get(`[id="${l.attributes('for')}"]`)
}

/**
 * Types `input` (field -> text) into the controls, in order: `labels` names
 * each field's label. A select takes the text of the option to pick, a
 * datetime-local box its local value, '' clears a box. Each control is
 * blurred.
 */
export async function fillControls(w: VueWrapper, labels: Record<string, string>, input: Record<string, string>) {
  await flushPromises()
  for (const [field, value] of Object.entries(input)) {
    const label = labels[field]
    if (!label) throw new Error(`no label given for ${field}`)
    const el = control(w, label)
    if (el.element.tagName === 'SELECT') {
      const option = el.findAll('option').find((o) => o.text() === value)
      if (!option) throw new Error(`${field}: no option ${value}`)
      await el.setValue((option.element as HTMLOptionElement).value)
    } else {
      await el.setValue(value)
    }
    await el.trigger('blur')
    await flushPromises()
  }
}

/** One input of an account form and what it must make of it. */
export interface AccountFormCase {
  name: string
  /** Edit an account holding these credentials (create when absent). */
  stored?: M
  /** Typed into the controls (fillControls). */
  input?: Record<string, string>
  /** The credentials the editor saves. */
  out: M
  /** validate()'s verdict. */
  ok: boolean
}

/**
 * The api_key and base_url cases every API key form shares: `key` a valid
 * key, `url` a valid base URL, `required` whether base_url is, `fill` the
 * other fields of a valid account (the defaults a new one starts with).
 */
export function keyAndUrlCases(key: string, url: string, required: boolean, fill: M = {}): AccountFormCase[] {
  const typed = (k: string, out: M = { api_key: k }) => ({ input: { api_key: k, base_url: url }, out: { ...fill, base_url: url, ...out } })
  const withUrl = (u: string) => ({ input: { api_key: key, base_url: u }, out: { ...fill, api_key: key, base_url: u } })
  const stored = { ...fill, api_key: SECRET_MASK, base_url: url }
  return [
    { name: 'key only', input: { api_key: key }, out: { ...fill, api_key: key }, ok: !required },
    { name: 'key and base URL', ...withUrl(url), ok: true },
    { name: 'a cleared base URL is not sent', input: { api_key: key, base_url: '' }, out: { ...fill, api_key: key }, ok: !required },
    { name: 'nothing typed', out: { ...fill }, ok: false },
    { name: 'empty key', ...typed(''), ok: false },
    { name: 'blank key', ...typed('   '), ok: false },
    { name: 'key of 1 character', ...typed('k'), ok: true },
    { name: 'key of 4096 characters', ...typed('k'.repeat(4096)), ok: true },
    { name: 'key with a space', ...typed('sk-abc def'), ok: true },
    { name: 'key with a leading space', ...typed(' sk-abcdef'), ok: true },
    { name: 'key not ASCII', ...typed('sk-ключ-abcdef'), ok: true },
    { name: 'base URL with a query', ...withUrl('https://x.example.com/?a=1'), ok: false },
    { name: 'base URL with a fragment', ...withUrl('https://x.example.com/#a'), ok: false },
    { name: 'base URL with a space', ...withUrl('https://x.example.com/a b'), ok: false },
    { name: 'base URL not http', ...withUrl('ftp://x.example.com'), ok: false },
    { name: 'base URL without a host', ...withUrl('https://'), ok: false },
    { name: 'edit: untouched', stored, out: stored, ok: true },
    { name: 'edit: the emptied key box keeps the stored key', stored, input: { api_key: '' }, out: stored, ok: true },
    { name: 'edit: a new key replaces it', stored, input: { api_key: key }, out: { ...stored, api_key: key }, ok: true },
    { name: 'edit: a blank new key', stored, input: { api_key: '   ' }, out: { ...stored, api_key: '   ' }, ok: false },
    { name: 'edit: a stored base URL the rules refuse', stored: { ...stored, base_url: 'ftp://x.example.com' }, out: { ...stored, base_url: 'ftp://x.example.com' }, ok: false },
    { name: 'edit: a key the form does not know goes back as it is', stored: { ...stored, legacy: 'x' }, out: { ...stored, legacy: 'x' }, ok: true }
  ]
}
