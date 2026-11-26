import { ref, type Ref } from 'vue'
import { useForm, type Rules } from '@sub2api/ui'
import { applyFormError } from '@/utils/formErrors'

type FormRules<M> = Rules<M> | (() => Rules<M>)

interface ModalFormOptions {
  /** The prefix the server puts before this form's field paths ("values." for a settings view). */
  strip?: string
}

/**
 * A form that is submitted as a whole (a modal's SFormModal + SForm, or a
 * settings page): the value, useForm over it, and submit(run), which
 * validates (unless the caller already did), runs, and shows a refusal the
 * console way (field errors at their fields, anything else as a toast).
 * submit resolves run's result, undefined when it did not run through.
 * reset() starts over from `init()` or the value given (call it when the
 * modal opens or the stored value arrives).
 */
export function useModalForm<M extends Record<string, unknown>>(init: () => M, rules: FormRules<M>, opts: ModalFormOptions = {}) {
  const model = ref(init()) as Ref<M>
  const serverErrors = ref<Record<string, string>>({})
  const saving = ref(false)
  const form = useForm<M>({ model: () => model.value, update: v => (model.value = v), rules, serverErrors: () => serverErrors.value })

  function reset(value: M = init()) {
    model.value = value
    serverErrors.value = {}
    form.clearValidate()
  }

  async function submit<R>(run: (value: M) => Promise<R>, o: { validated?: boolean } = {}): Promise<R | undefined> {
    if (saving.value || (!o.validated && !(await form.validate()))) return undefined
    saving.value = true
    try {
      return await run(form.value())
    } catch (e) {
      applyFormError(e, serverErrors, undefined, opts.strip)
      return undefined
    } finally {
      saving.value = false
    }
  }

  return { model, form, saving, reset, submit }
}
