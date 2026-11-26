// usePluginSettings: a plugin's settings (GET / PUT /plugins/:key/settings,
// CONTRACTS §75) as a form: the loaded value with the defaults filled is the
// "unchanged" baseline; saving validates, PUTs the whole value, toasts, and
// reads the settings again (kept and new secrets come back as "******"),
// without switching to `loading`; on refusal it shows the field errors at
// their fields (errors of the whole object as a toast). Secrets come back as
// "******" and go back as is to keep them; a caller with only the plugin's
// settings permission who changes something while keeping a secret is
// asked to type it again (field code "reenter").
import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { errorMessage, isApiError, isSilentError, settingsFieldErrors, useHost, type ApiClient } from '@sub2api/host'
import { toast } from '../feedback'
import { useUiT } from '../i18n'
import { useForm, type FormApi } from './useForm'
import { fillMissing } from './path'
import type { Rules } from './rules'

type Model = Record<string, unknown>

/** GET /plugins/:key/settings (the fields this composable reads). */
interface SettingsView {
  values: Model | null
  /** RBAC key of manifest ui.settings.permission: its holders may edit too. */
  permission?: string
}

export interface PluginSettingsForm<M extends Model = Model> extends FormApi<M> {
  loading: Ref<boolean>
  saving: Ref<boolean>
  loadError: Ref<unknown>
  /** Differs from the loaded value (after defaults). */
  dirty: ComputedRef<boolean>
  /** plugin:manage, or the plugin's ui.settings.permission. */
  canEdit: ComputedRef<boolean>
  reload(): Promise<void>
  /** validate() (unless `validated`: the caller just ran it), then PUT and read again; true when saved. */
  save(opts?: { validated?: boolean }): Promise<boolean>
  /** Back to the loaded value. */
  reset(): void
}

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v ?? {}))

export function usePluginSettings<M extends Model = Model>(opts: {
  pluginKey: string
  /** host.api */
  api: ApiClient
  rules: Rules<M> | (() => Rules<M>)
  /** Filled into the absent keys of every value read (part of the "unchanged" baseline). */
  defaults?: Partial<M>
}): PluginSettingsForm<M> {
  const host = useHost()
  const t = useUiT()
  const path = `/plugins/${encodeURIComponent(opts.pluginKey)}/settings`
  const view = ref<SettingsView | null>(null)
  const values = ref({}) as Ref<M>
  const serverErrors = ref<Record<string, string>>({})
  const baseline = ref('')
  const loading = ref(false)
  const saving = ref(false)
  const loadError = ref<unknown>(null)
  const form = useForm<M>({ model: () => values.value, update: (v) => (values.value = v), rules: opts.rules, serverErrors: () => serverErrors.value })
  const notify = (e: unknown) => isSilentError(e) || toast(errorMessage(e, host.i18n.t), 'error')

  /** Shows the value read, defaults filled in: the "unchanged" baseline. */
  function show() {
    values.value = fillMissing(clone(view.value?.values ?? {}) as M, opts.defaults ?? {})
    serverErrors.value = {}
    form.clearValidate()
    baseline.value = JSON.stringify(values.value)
  }

  /** Reads the settings and shows them; `loading` is the caller's. */
  async function read() {
    view.value = await opts.api.get<SettingsView>(path)
    show()
  }

  async function reload() {
    loading.value = true
    loadError.value = null
    try {
      await read()
    } catch (e) {
      loadError.value = e
    } finally {
      loading.value = false
    }
  }

  async function save(o: { validated?: boolean } = {}): Promise<boolean> {
    if (!o.validated && !(await form.validate())) return false
    saving.value = true
    serverErrors.value = {}
    try {
      await opts.api.put(path, { values: values.value })
    } catch (e) {
      const fields = isApiError(e) && e.code === 'invalid_argument' ? Object.fromEntries(Object.entries(e.fields).map(([k, v]) => [k, e.fieldCodes[k] === 'reenter' ? t('ui.form.reenter') : v])) : {}
      const { fields: at, root } = settingsFieldErrors(fields)
      serverErrors.value = at
      // Errors of the whole object have no field to show at.
      if (root.length) toast(root.join('; '), 'error')
      else notify(e)
      saving.value = false
      return false
    }
    toast(t('ui.form.saved'), 'success')
    baseline.value = JSON.stringify(values.value)
    // What was stored, should the read below fail (reset goes back to it).
    view.value = { ...view.value, values: clone(values.value) }
    // The stored value as the core now shows it; the form stays mounted (no `loading`).
    await read().catch(notify)
    saving.value = false
    return true
  }

  void reload()
  return Object.assign(form, {
    loading,
    saving,
    loadError,
    dirty: computed(() => !!baseline.value && JSON.stringify(values.value) !== baseline.value),
    canEdit: computed(() => host.permissions.has('plugin:manage') || (!!view.value?.permission && host.permissions.has(view.value.permission))),
    reload,
    save,
    reset: show
  })
}
