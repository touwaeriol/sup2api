// Save pipeline of the account editor (CONTRACTS §16, §18, §21.4, §41):
// the pre-request checks (invalidSection), the request body (requestBody), the
// POST / PATCH (submit) and the error routing (showSaveError) in one place.
// AccountEditor keeps the template and only wires the state below.
import type { ComputedRef, Ref } from 'vue'
import { nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { toast } from '@sub2api/ui'
import type { Account, AccountType, CommonControlName, ConversionLossPolicy, EnvironmentValues } from '@/api/types'
import { useProxiesLookup } from '@/composables/lookups'
import { errorMessage, fieldErrors, notifyError } from '@/utils/errors'
import { accountControl, withoutFixed } from './commonControls'
import { sectionOfErrors, splitFieldErrors, type EditorSection } from './editorErrors'
import type { useModelEdit } from './useModelEdit'
import { fromCards, repeatedModel, type ModelLimitCard } from './modelLimits'
import { windowOf } from './spmWindow'
import { sameSet } from './bulkEdit'
import type { ProtocolEditor } from './protocols/useProtocolEdit'

/** Field error codes of a save worded by the console: a restricted caller's account out of its groups (CONTRACTS §21). */
const SCOPE_FIELD_CODES = { not_in_scope: 'accounts.scopeErrors.notInScope' }

/** Editable basics of an account (AccountEditor's `basic`). */
export interface AccountBasics {
  name: string
  group_ids: number[]
  proxy_id: number | null
  priority: number
  weight: number
  max_concurrency: number
  schedulable: boolean
  auto_disable: boolean
  rpm_limit: number
  tpm_limit: number
  spm_limit: number
  /** Window of spm_limit in seconds; null while its input is empty (the default). */
  spm_window_seconds: number | null
  /** The account layer of the environment variables (CONTRACTS §74.3). */
  environment: EnvironmentValues
  /** Header passthrough rules (CONTRACTS §81). */
  header_passthrough: string[]
  /** Request header override, new-api's header_override format (CONTRACTS §108); null clears it. */
  header_override: Record<string, string> | null
  /** Request parameter override, new-api's param_override format (CONTRACTS §84); null clears it. */
  param_override: Record<string, unknown> | null
  /** Loss policy of converted requests (CONVERT-MATRIX §2.4); null inherits the groups'. */
  conversion_loss_policy: ConversionLossPolicy | null
}

/** The account editor's model list / mapping editor (useModelEdit: its state and actions). */
export type ModelEditor = ReturnType<typeof useModelEdit>

export interface AccountSaveDeps {
  /** The account being edited (null for a new one). */
  account: ComputedRef<Account | null | undefined>
  accountType: ComputedRef<AccountType | null>
  editing: ComputedRef<boolean>
  basic: AccountBasics
  /** 指定模型限速 cards (CONTRACTS §68). */
  modelLimits: Ref<ModelLimitCard[]>
  errors: Ref<Record<string, string>>
  credErrors: Ref<Record<string, string>>
  saving: Ref<boolean>
  activeSection: Ref<EditorSection>
  /** The model list / mapping editor (useModelEdit). */
  models: ModelEditor
  /** The protocol cards (useProtocolEdit): `protocols` and `model_protocols`. */
  protocols: ProtocolEditor
  /** The global auto-disable switch (null while it loads). */
  autoDisableEnabled: Ref<boolean | null>
  /** The proxy_url to send instead of proxy_id ('' sends proxy_id). */
  proxyUrlToSend: ComputedRef<string>
  collectCredentials(): Promise<Record<string, unknown> | null>
  showCredentialErrors(cred: Record<string, string>): void
  loadAutoDisablePolicy(): Promise<void>
  /** The usage limit card (CONTRACTS §73), saved on its own endpoint once the account is. */
  usageLimit?: {
    /** Shows the card's errors and reports whether it blocks saving. */
    invalid(): boolean
    /** Saves the limit of the saved account when it changed. */
    save(a: Account): Promise<void>
  }
  saved(a: Account): void
}

export function useAccountSave(deps: AccountSaveDeps) {
  const { t } = useI18n()
  const { account, accountType, editing, basic, errors, credErrors, saving, activeSection, models, proxyUrlToSend } = deps
  const control = (name: CommonControlName) => accountControl(accountType.value, name)

  /** Checks done before the request; the section to show when one fails. */
  async function invalidSection(form: HTMLFormElement): Promise<EditorSection | 'native' | null> {
    // Native constraints are checked explicitly so the first invalid field can
    // be revealed before the browser tries to focus it in a hidden pane.
    const invalid = form.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input:invalid, select:invalid, textarea:invalid')
    if (invalid) {
      const section = invalid.closest<HTMLElement>('[data-editor-section]')?.dataset.editorSection
      if (section) activeSection.value = section as EditorSection
      await nextTick()
      if (invalid.isConnected) invalid.reportValidity()
      return 'native'
    }
    if (!basic.name.trim()) {
      errors.value = { name: t('ui.form.v.required') }
      return 'connection'
    }
    if (control('proxy').required && basic.proxy_id == null && !proxyUrlToSend.value) {
      const message = t('accounts.proxyRequired')
      errors.value = { proxy_id: message, proxy_url: message }
      return 'connection'
    }
    const repeated = repeatedModel(deps.modelLimits.value)
    if (repeated) {
      errors.value = { model_limits: t('accounts.modelLimits.duplicate', { model: repeated }) }
      return 'scheduling'
    }
    if (deps.usageLimit?.invalid()) return 'scheduling'
    // Pending text-mode edits are committed (and validated) before saving.
    if (!models.commitPending() || !deps.protocols.commitPending()) return 'models'
    // A rule without protocols, an account serving nothing: shown in the protocol cards.
    return deps.protocols.issues.value.some(i => i.level === 'error') ? 'models' : null
  }

  function requestBody(creds: Record<string, unknown>): Record<string, unknown> {
    const body: Record<string, unknown> = {
      name: basic.name.trim(),
      priority: Number(basic.priority),
      weight: Number(basic.weight),
      max_concurrency: Number(basic.max_concurrency),
      ...models.payload(),
      rpm_limit: Number(basic.rpm_limit) || 0,
      tpm_limit: Number(basic.tpm_limit) || 0,
      spm_limit: Number(basic.spm_limit) || 0,
      spm_window_seconds: windowOf(basic.spm_window_seconds),
      model_limits: fromCards(deps.modelLimits.value),
      environment: basic.environment,
      header_passthrough: basic.header_passthrough,
      header_override: basic.header_override,
      param_override: basic.param_override,
      conversion_loss_policy: basic.conversion_loss_policy,
      schedulable: basic.schedulable
    }
    // Groups only when changed: changing them needs its own permission (CONTRACTS §21.2).
    if (!sameSet(basic.group_ids, account.value?.group_ids ?? [])) body.group_ids = basic.group_ids
    if (deps.autoDisableEnabled.value === true) body.auto_disable = basic.auto_disable
    // Exactly one of proxy_id / proxy_url (CONTRACTS §21.4: both is a conflict).
    if (proxyUrlToSend.value) body.proxy_url = proxyUrlToSend.value
    else body.proxy_id = basic.proxy_id
    // The credentials and the protocols depend on the account type: an account whose type is gone keeps what it has.
    if (accountType.value && !account.value?.orphaned) Object.assign(body, { credentials: creds }, deps.protocols.binding())
    // Fields the type fixes are left to the server (docs/COMMON-CONTROLS.md §1).
    return withoutFixed(body, accountType.value)
  }

  async function submit(body: Record<string, unknown>): Promise<Account> {
    if (editing.value) return api.patch<Account>(`/accounts/${account.value!.id}`, body)
    // The account type is (plugin_key, type); accounts have no platform.
    body.plugin_key = accountType.value!.plugin_key
    body.type = accountType.value!.type
    return api.post<Account>('/accounts', body)
  }

  function showSaveError(e: unknown) {
    const all = fieldErrors(e, undefined, SCOPE_FIELD_CODES)
    const { cred, base } = splitFieldErrors(all)
    errors.value = base
    deps.showCredentialErrors(cred)
    const section = sectionOfErrors(base, cred)
    if (section) activeSection.value = section
    if (!Object.keys(all).length) notifyError(e)
  }

  async function save(event: Event) {
    errors.value = {}
    credErrors.value = {}
    const problem = await invalidSection(event.currentTarget as HTMLFormElement)
    if (problem && problem !== 'native') activeSection.value = problem
    if (problem) return
    const creds = await deps.collectCredentials()
    if (!creds) {
      activeSection.value = 'connection'
      return
    }
    saving.value = true
    await deps.loadAutoDisablePolicy()
    const body = requestBody(creds)
    try {
      const result = await submit(body)
      toast(t(editing.value ? 'common.updated' : 'common.created'), 'success')
      if (body.proxy_url) {
        // The proxy picker cache may now miss the (re)used proxy.
        useProxiesLookup(true)
        if (result?.proxy_created) toast(t('accounts.proxyAutoCreated'), 'info')
      }
      if (deps.usageLimit && result?.id && control('usage_limit').configurable) {
        // The account is saved either way: a refused limit is reported, not retried
        // (re-submitting a new account would create it twice).
        try {
          await deps.usageLimit.save(result)
        } catch (e) {
          toast(t('accounts.usageLimit.saveFailed', { error: errorMessage(e) }), 'error')
        }
      }
      deps.saved(result)
    } catch (e) {
      showSaveError(e)
    } finally {
      saving.value = false
    }
  }

  return { save }
}
