// Save pipeline of the account editor (CONTRACTS §16, §18, §21.4, §41): the
// editor's tabbed form (useTabbedForm) gets one part per core section, each
// with its checks, its share of the request body, the server errors it
// claims (editorErrors SECTION_FIELDS) and whether it shows an error now;
// Save checks every part, sends one POST / PATCH and opens the first section
// with a problem. The usage limit is saved on its own endpoint afterwards.
import { nextTick, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, isApiError } from '@sub2api/host'
import { toast, type TabbedForm } from '@sub2api/ui'
import type { Account, AccountType, ConversionLossPolicy, EnvironmentValues } from '@/api/types'
import { useProxiesLookup } from '@/composables/lookups'
import { errorMessage, fieldErrors, notifyError } from '@/utils/errors'
import { accountControl, withoutFixed } from './commonControls'
import { accountParts } from './accountParts'
import type { useModelEdit } from './useModelEdit'
import type { ModelLimitCard } from './modelLimits'
import type { ProtocolEditor } from './protocols/useProtocolEdit'
import type { AutoDisablePolicy } from './useAutoDisablePolicy'
import type { UsageLimitEditor } from './useUsageLimitEdit'

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
  /** The editor's tabbed form: the parts are registered on it. */
  form: TabbedForm
  /** The account being edited (null for a new one). */
  account: () => Account | null | undefined
  accountType: () => AccountType | null
  basic: AccountBasics
  /** 指定模型限速 cards (CONTRACTS §68). */
  modelLimits: Ref<ModelLimitCard[]>
  errors: Ref<Record<string, string>>
  credErrors: Ref<Record<string, string>>
  /** The model list / mapping editor (useModelEdit). */
  models: ModelEditor
  /** The protocol cards (useProtocolEdit): `protocols` and `model_protocols`. */
  protocols: ProtocolEditor
  autoDisable: AutoDisablePolicy
  /** The proxy_url to send instead of proxy_id ('' sends proxy_id). */
  proxyUrlToSend: () => string
  /** The plugin form's credentials; null when it refuses them (it shows why). */
  collectCredentials(): Promise<Record<string, unknown> | null>
  showCredentialErrors(cred: Record<string, string>): void
  /** The usage limit card (CONTRACTS §73), saved on its own endpoint once the account is. */
  usageLimit: UsageLimitEditor
  saved(a: Account): void
}

/**
 * Native constraints (an input's min, max…) are checked first, so the first
 * invalid field's section is opened before the browser tries to focus it in
 * a hidden one. Whether one was found.
 */
async function revealInvalidField(form: HTMLFormElement, current: Ref<string>): Promise<boolean> {
  type Validated = HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement
  const invalid = Array.from(form.elements as HTMLCollectionOf<Validated>).find((el) => el.willValidate && !el.validity.valid)
  if (!invalid) return false
  const tab = invalid.closest<HTMLElement>('[role="tabpanel"][data-tab]')?.dataset.tab
  if (tab) current.value = tab
  await nextTick()
  if (invalid.isConnected) invalid.reportValidity()
  return true
}

/** Words the field errors whose codes the console knows, before the parts take them. */
function withFieldTexts(e: unknown): unknown {
  if (isApiError(e)) Object.assign(e.fields, fieldErrors(e, undefined, SCOPE_FIELD_CODES))
  return e
}

type T = ReturnType<typeof useI18n>['t']
const editing = (deps: AccountSaveDeps) => !!deps.account()?.id
/** What a save sent and got back. */
interface Sent {
  account: Account
  /** A pasted proxy URL was sent (the server may have created the proxy). */
  viaUrl: boolean
}

/** The merged body, completed and sent: auto_disable while the global switch is on, the type of a new account, without the fixed fields. */
async function send(deps: AccountSaveDeps, body: Record<string, unknown>): Promise<Sent> {
  await deps.autoDisable.load()
  if (deps.autoDisable.enabled.value === true) body.auto_disable = deps.basic.auto_disable
  // Fields the type fixes are left to the server (docs/COMMON-CONTROLS.md §1).
  const sent = withoutFixed(body, deps.accountType())
  // The account type is (plugin_key, type); accounts have no platform.
  const request = editing(deps) ? api.patch<Account>(`/accounts/${deps.account()!.id}`, sent) : api.post<Account>('/accounts', { ...sent, plugin_key: deps.accountType()!.plugin_key, type: deps.accountType()!.type })
  return { account: await request.catch((e) => Promise.reject(withFieldTexts(e))), viaUrl: !!sent.proxy_url }
}

/** After the account is saved: the toasts, the usage limit on its own endpoint, then `saved`. */
async function afterSave(deps: AccountSaveDeps, t: T, { account, viaUrl }: Sent) {
  toast(t(editing(deps) ? 'common.updated' : 'common.created'), 'success')
  if (viaUrl) {
    // The proxy picker cache may now miss the (re)used proxy.
    useProxiesLookup(true)
    if (account?.proxy_created) toast(t('accounts.proxyAutoCreated'), 'info')
  }
  if (account?.id && accountControl(deps.accountType(), 'usage_limit').configurable) {
    // The account is saved either way: a refused limit is reported, not retried
    // (re-submitting a new account would create it twice).
    await deps.usageLimit.save(account).catch((e) => toast(t('accounts.usageLimit.saveFailed', { error: errorMessage(e) }), 'error'))
  }
  deps.saved(account)
}

export function useAccountSave(deps: AccountSaveDeps) {
  const { t } = useI18n()
  for (const part of accountParts(deps, t)) deps.form.register(part)

  async function save(event: Event) {
    deps.errors.value = {}
    deps.credErrors.value = {}
    if (await revealInvalidField(event.currentTarget as HTMLFormElement, deps.form.current)) return
    let result: Sent | false
    try {
      result = await deps.form.submit((body) => send(deps, body))
    } catch (e) {
      return notifyError(e)
    }
    if (result) await afterSave(deps, t, result)
  }

  return { save }
}
