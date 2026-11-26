import type { Ref } from 'vue'
import { fieldErrors, notifyError, type FieldCodeMessages } from '@/utils/errors'

/**
 * Shows a failed form submission the same way everywhere: server field errors
 * (invalid_argument with `fields`) go to `errors` and are shown next to the
 * fields only; any other error is shown as a toast. Returns true when field
 * errors were found. `codes` translates the field error codes the form knows;
 * `strip` keeps only the fields under that prefix, named without it (fieldErrors).
 */
export function applyFormError(e: unknown, errors: Ref<Record<string, string>>, codes?: FieldCodeMessages, strip?: string): boolean {
  errors.value = fieldErrors(e, strip, codes)
  if (Object.keys(errors.value).length) return true
  notifyError(e)
  return false
}
