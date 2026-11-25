import { ref, type Ref } from 'vue'
import { isApiError, useList, type ConfirmOptions } from '@sub2api/host'
import { useForm, type Rules, type TableColumn } from '@sub2api/ui'
import { errorMessage, usePaymentHost } from '../host'

// Building blocks of the admin pages: a paged list of a plugin route, its
// columns, a (confirmed) row action and the state of a form in a modal.

/** Paged list of a plugin route (filters reload, page 1). */
export function useAdminList<T>(path: string, filters: Record<string, unknown>) {
  return useList<T>(usePaymentHost().pluginApi, path, filters)
}

/** A one-line column labelled by the message `<prefix>.<key>`. */
export function columnOf(prefix: string) {
  const t = usePaymentHost().t
  return (key: string, extra: Partial<TableColumn> = {}): TableColumn => ({ key, label: t(`${prefix}.${key}`), nowrap: true, ...extra })
}

/** The row actions column, last in every table. */
export const actionsColumn = (): TableColumn => ({ key: 'actions', label: usePaymentHost().t('admin.actions'), align: 'right', nowrap: true })

/** Runs `run`, toasts `done` on success and the error otherwise; resolves true when it ran through. */
export async function runAction(run: () => Promise<unknown>, done: string): Promise<boolean> {
  const host = usePaymentHost()
  try {
    await run()
    host.toast(done, 'success')
    return true
  } catch (e) {
    host.toast(errorMessage(e, host.t('saveFailed')), 'error')
    return false
  }
}

/** Asks first, then runAction. */
export async function confirmRun(opts: ConfirmOptions, run: () => Promise<unknown>, done: string): Promise<boolean> {
  return (await usePaymentHost().confirm(opts)) && runAction(run, done)
}

/**
 * A form in a modal: the value, useForm over it, and submit(run) that
 * validates, runs and maps the server's field errors onto the fields (other
 * failures are toasted). submit resolves run's result, undefined when it did
 * not run through.
 */
export function useModalForm<M extends Record<string, unknown>>(init: () => M, rules: Rules<M> | (() => Rules<M>)) {
  const host = usePaymentHost()
  const model = ref(init()) as Ref<M>
  const serverErrors = ref<Record<string, string>>({})
  const saving = ref(false)
  const form = useForm<M>({ model: () => model.value, update: (v) => (model.value = v), rules, serverErrors: () => serverErrors.value })

  function reset(next: M = init()) {
    model.value = next
    serverErrors.value = {}
    form.clearValidate()
  }

  async function submit<R>(run: (value: M) => Promise<R>): Promise<R | undefined> {
    if (saving.value || !(await form.validate())) return undefined
    saving.value = true
    try {
      return await run(form.value())
    } catch (e) {
      if (isApiError(e) && Object.keys(e.fields).length) serverErrors.value = e.fields
      else host.toast(errorMessage(e, host.t('saveFailed')), 'error')
      return undefined
    } finally {
      saving.value = false
    }
  }

  return { model, form, saving, reset, submit }
}
