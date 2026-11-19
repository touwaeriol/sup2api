// The form an SForm provides to the frames (SField, SSettingRow) inside it.
import { computed, inject, watch, type InjectionKey } from 'vue'
import type { FormApi } from './useForm'
import type { Trigger } from './rules'

export const FORM_KEY: InjectionKey<FormApi<any>> = Symbol('s-form')
/** Set inside an element that already is a <form> (SFormModal, SCredentialForm): SForm renders no second one. */
export const IN_FORM_KEY: InjectionKey<boolean> = Symbol('s-in-form')

/**
 * Form wiring of a frame with `prop`: its error and required star (an
 * explicit `error` / `required` wins), the claim of the path, and the blur
 * of its control. Without `prop` or outside an SForm everything is the
 * frame's own.
 */
export function useFormItem(props: { prop?: string; error?: string; required?: boolean; label?: string }) {
  const form = inject(FORM_KEY, null)
  watch(
    () => props.prop,
    (p, _, onCleanup) => {
      if (p && form) onCleanup(form.claim(p, () => props.label))
    },
    { immediate: true }
  )
  return {
    error: computed(() => props.error ?? (props.prop && form ? form.error(props.prop) : undefined)),
    required: computed(() => props.required ?? (!!props.prop && !!form && form.required(props.prop))),
    touch: (kind: Trigger) => {
      if (props.prop && form) form.touch(props.prop, kind)
    }
  }
}
