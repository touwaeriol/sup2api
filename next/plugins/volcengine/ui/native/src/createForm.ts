import { ref } from 'vue'
import { isApiError } from '@sub2api/host'
import { useForm, type Rules } from '@sub2api/ui'
import { useVolcHost } from './host'

type Model = Record<string, unknown>

/**
 * The state of a create dialog posting to a plugin route: the form, and the
 * submit SResourcePage runs from the dialog's Save. Local rules first; the
 * route's field errors (invalid_argument with details.fields) are shown at
 * their fields and keep the dialog open; any other failure is thrown, so the
 * page reports it and the dialog stays open as well. Used inside the dialog's
 * slot, so a dialog opened again starts empty.
 */
export function useCreateForm(path: string, rules: Rules<Model>, defaults?: Model) {
  const value = ref<Model>({})
  const serverErrors = ref<Record<string, string>>({})
  const form = useForm<Model>({ model: () => value.value, update: (v) => (value.value = v), rules, defaults, serverErrors: () => serverErrors.value })

  async function submit(): Promise<boolean> {
    if (!(await form.validate())) return false
    try {
      await useVolcHost().pluginApi.post(path, form.value())
      return true
    } catch (e) {
      if (!isApiError(e) || !Object.keys(e.fields).length) throw e
      serverErrors.value = e.fields
      return false
    }
  }
  return { form, submit }
}
