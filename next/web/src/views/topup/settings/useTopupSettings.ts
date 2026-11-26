import { computed, ref, shallowRef } from 'vue'
import { toast, type PluginSettingsForm, type Rules } from '@sub2api/ui'
import { useModalForm } from '@/composables/useModalForm'
import { i18n } from '@/i18n'
import { topupApi } from '../api'
import type { SettingsView } from '../topup'
import { DEFAULT_MODEL, toModel, toValues, type SettingsModel } from './settingsForm'

/** The top-up settings form (what SSettingsPage drives). */
export type TopupSettingsForm = PluginSettingsForm<SettingsModel>

/** The view read last, the form over its values (server errors under "values.") and the "unchanged" baseline. */
function settingsState(rules: () => Rules<SettingsModel>) {
  const view = shallowRef<SettingsView | null>(null)
  const edit = useModalForm<SettingsModel>(DEFAULT_MODEL, rules, { strip: 'values.' })
  const baseline = ref('')

  /** Shows a view read or saved: its values become the baseline. */
  function show(v: SettingsView) {
    view.value = v
    edit.reset(toModel(v.values))
    baseline.value = JSON.stringify(edit.model.value)
  }
  const dirty = computed(() => !!baseline.value && JSON.stringify(edit.model.value) !== baseline.value)
  return { view, edit, show, dirty }
}

type State = ReturnType<typeof settingsState>

/** Reading the settings; a failure is shown in place of the form (SSettingsPage). */
function settingsLoad(s: State) {
  const loading = ref(false)
  const loadError = ref<unknown>(null)
  async function reload() {
    loading.value = true
    loadError.value = null
    try {
      s.show(await topupApi.settings())
    } catch (e) {
      loadError.value = e
    } finally {
      loading.value = false
    }
  }
  return { loading, loadError, reload }
}

/** Saving the values; the stored view answered becomes the new baseline. */
function settingsSave(s: State) {
  async function save(o: { validated?: boolean } = {}): Promise<boolean> {
    const saved = await s.edit.submit(values => topupApi.saveSettings(toValues(values)), o)
    if (!saved) return false
    s.show(saved)
    toast(i18n.global.t('common.saved'), 'success')
    return true
  }
  return { saving: s.edit.saving, save }
}

/**
 * GET / PUT /topup/settings as a form for SSettingsPage: the values (money as
 * numbers while edited, decimal strings on the wire), the server's field
 * errors at their fields ("values.epay.key" → "epay.key"), and what the view
 * holds beside them (callback addresses, currencies). The page is only
 * reachable with topup:manage, so the caller may edit. Saving answers the
 * stored view (secrets back as "******"), shown as the new baseline.
 */
export function useTopupSettings(rules: () => Rules<SettingsModel>) {
  const s = settingsState(rules)
  const load = settingsLoad(s)
  void load.reload()
  const settings: TopupSettingsForm = Object.assign(s.edit.form, {
    ...load,
    ...settingsSave(s),
    dirty: s.dirty,
    canEdit: computed(() => true),
    reset: () => s.view.value && s.show(s.view.value)
  })
  return {
    settings,
    notifyUrls: computed(() => s.view.value?.notify_urls ?? { epay: '', stripe: '' }),
    currencies: computed(() => s.view.value?.currencies ?? [])
  }
}
