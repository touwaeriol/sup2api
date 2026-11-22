// Spec helper of the form specs: the fake console a test grants permissions
// on, mounting a native account form, typing into a field, and the cleanup
// of what a test mounted. Importing it registers the
// hooks (the toasts and the grants reset before each test).
import { afterEach, beforeEach } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, ref, type Component } from 'vue'
import { provideHost, type HostContext } from '@sub2api/host'
import { toastState } from '../feedback'
import type { NativeAccountFormProps } from './credential'

export type M = Record<string, any>

/** A fake console: the permissions a test grants, everything else unused. */
export function fakeHost(granted: string[] = []) {
  provideHost({
    permissions: { superuser: () => false, has: (k: string) => granted.includes(k), any: (...k: string[]) => k.some((x) => granted.includes(x)) },
    // The console's translation: the key itself here.
    i18n: { t: (k: string) => k }
  } as unknown as HostContext)
}

/** What a test mounted: unmounted after it. */
export const mounted: VueWrapper[] = []

beforeEach(() => {
  toastState.items.splice(0)
  fakeHost()
})
afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount())
  document.body.innerHTML = ''
})

/** Mounts a native account form the way the account editor does: v-model credentials plus the §23.5 props. */
export function editor(form: Component, props: Partial<NativeAccountFormProps> & { modelValue?: M } = {}) {
  const credentials = ref<M>(props.modelValue ?? {})
  const errors = ref<Record<string, string> | undefined>(props.errors)
  const inner = ref<{ validate: () => Promise<boolean> }>()
  const w = mount(
    defineComponent({
      setup: () => () =>
        h('form', [
          h(form, {
            ref: inner,
            modelValue: credentials.value,
            'onUpdate:modelValue': (v: M) => (credentials.value = v),
            mode: props.mode ?? 'create',
            account: props.account ?? null,
            accountType: props.accountType ?? { plugin_key: 'anthropic', type: 'apikey' },
            errors: errors.value,
            proxyTicket: async () => 'ticket'
          })
        ])
    }),
    { attachTo: document.body }
  )
  mounted.push(w)
  return { w, credentials, errors, validate: () => inner.value!.validate() }
}

export async function typeInto(w: ReturnType<typeof mount>, selector: string, value: string) {
  const el = w.find(selector)
  await el.setValue(value)
  await el.trigger('blur')
  await flushPromises()
}
