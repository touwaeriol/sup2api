// The parts of the account editor's save (useTabbedForm), one per core
// section. Each checks what it holds before the request (every check runs,
// so each section shows what is wrong), adds its fields to the one request
// body, takes the server errors of those fields (SECTION_FIELDS) and marks
// its section while an error of it is shown.
import { ref } from 'vue'
import type { TabbedFormPart } from '@sub2api/ui'
import { sameSet } from './bulkEdit'
import { accountControl } from './commonControls'
import { isSectionField, SECTION_FIELDS, splitFieldErrors, type CoreSection } from './editorErrors'
import { fromCards, repeatedModel } from './modelLimits'
import { windowOf } from './spmWindow'
import type { AccountSaveDeps } from './useAccountSave'

type T = (key: string, values?: Record<string, unknown>) => string

/** The part's shared shape: its section, the fields it claims, errors merged into the editor's and the section marked while one of them shows. */
function sectionPart(d: AccountSaveDeps, tab: CoreSection, part: Omit<TabbedFormPart, 'tab'>): TabbedFormPart {
  const holds = isSectionField(tab)
  return {
    tab,
    fields: SECTION_FIELDS[tab],
    invalid: () => Object.keys(d.errors.value).some(holds),
    applyErrors: (e) => (d.errors.value = { ...d.errors.value, ...e }),
    ...part
  }
}

/** Puts errors found before the request into the editor's; whether there were none. */
function report(d: AccountSaveDeps, problems: Record<string, string>): boolean {
  d.errors.value = { ...d.errors.value, ...problems }
  return !Object.keys(problems).length
}

/** The type's credentials and protocols go only to an account whose type is still there. */
const typed = (d: AccountSaveDeps) => !!d.accountType() && !d.account()?.orphaned

/** What the connection section lacks before the request: a name, a proxy the type requires. */
function connectionProblems(d: AccountSaveDeps, t: T): Record<string, string> {
  const { basic } = d
  const proxyMissing = accountControl(d.accountType(), 'proxy').required && basic.proxy_id == null && !d.proxyUrlToSend()
  return { ...(basic.name.trim() ? {} : { name: t('ui.form.v.required') }), ...(proxyMissing ? { proxy_id: t('accounts.proxyRequired'), proxy_url: t('accounts.proxyRequired') } : {}) }
}

function connectionBody(d: AccountSaveDeps, credentials: Record<string, unknown> | null): Record<string, unknown> {
  const { basic } = d
  const out: Record<string, unknown> = { name: basic.name.trim() }
  // Groups only when changed: changing them needs its own permission (CONTRACTS §21.2).
  if (!sameSet(basic.group_ids, d.account()?.group_ids ?? [])) out.group_ids = basic.group_ids
  // Exactly one of proxy_id / proxy_url (CONTRACTS §21.4: both is a conflict).
  Object.assign(out, d.proxyUrlToSend() ? { proxy_url: d.proxyUrlToSend() } : { proxy_id: basic.proxy_id })
  if (typed(d)) out.credentials = credentials
  return out
}

/**
 * Name, groups, proxy and the plugin form's credentials (collected by the
 * check, sent by the body). Credentials the plugin form refused (its own
 * validation, which shows why) mark the section until the next save.
 */
function connectionPart(d: AccountSaveDeps, t: T): TabbedFormPart {
  let credentials: Record<string, unknown> | null = null
  const refused = ref(false)
  return sectionPart(d, 'connection', {
    async check() {
      const ok = report(d, connectionProblems(d, t))
      credentials = await d.collectCredentials()
      refused.value = credentials === null
      return ok && !refused.value
    },
    body: () => connectionBody(d, credentials),
    invalid: () => refused.value || Object.keys(d.errors.value).some(isSectionField('connection')) || Object.keys(d.credErrors.value).length > 0,
    applyErrors(e) {
      const { cred, base } = splitFieldErrors(e)
      d.errors.value = { ...d.errors.value, ...base }
      d.showCredentialErrors(cred)
    }
  })
}

/** The model list and mapping, and the protocols the account opens (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1). */
function modelsPart(d: AccountSaveDeps): TabbedFormPart {
  const { models, protocols } = d
  const protocolError = () => protocols.issues.value.some((i) => i.level === 'error')
  return sectionPart(d, 'models', {
    // Pending text-mode edits are committed (and validated) first; a rule without protocols, an account serving nothing, is shown in the protocol cards.
    check: () => models.commitPending() && protocols.commitPending() && !protocolError(),
    body: () => ({ ...models.payload(), ...(typed(d) ? protocols.binding() : {}) }),
    invalid: () => !!(models.modelsError.value || models.mappingError.value) || protocolError() || Object.keys(d.errors.value).some(isSectionField('models'))
  })
}

/** Scheduling and limits; auto_disable is added when the request is sent, after the global switch is read again. */
function schedulingPart(d: AccountSaveDeps, t: T): TabbedFormPart {
  const { basic } = d
  return sectionPart(d, 'scheduling', {
    check() {
      const repeated = repeatedModel(d.modelLimits.value)
      const limitsOk = report(d, repeated ? { model_limits: t('accounts.modelLimits.duplicate', { model: repeated }) } : {})
      return !d.usageLimit.invalid() && limitsOk
    },
    body: () => ({
      priority: Number(basic.priority),
      weight: Number(basic.weight),
      max_concurrency: Number(basic.max_concurrency),
      rpm_limit: Number(basic.rpm_limit) || 0,
      tpm_limit: Number(basic.tpm_limit) || 0,
      spm_limit: Number(basic.spm_limit) || 0,
      spm_window_seconds: windowOf(basic.spm_window_seconds),
      model_limits: fromCards(d.modelLimits.value),
      schedulable: basic.schedulable
    }),
    invalid: () => Object.keys(d.errors.value).some(isSectionField('scheduling')) || Object.keys(d.usageLimit.errors.value).length > 0
  })
}

/** Request forwarding. */
function advancedPart(d: AccountSaveDeps): TabbedFormPart {
  const { basic } = d
  return sectionPart(d, 'advanced', {
    body: () => ({
      environment: basic.environment,
      header_passthrough: basic.header_passthrough,
      header_override: basic.header_override,
      param_override: basic.param_override,
      conversion_loss_policy: basic.conversion_loss_policy
    })
  })
}

export const accountParts = (d: AccountSaveDeps, t: T): TabbedFormPart[] => [connectionPart(d, t), modelsPart(d), schedulingPart(d, t), advancedPart(d)]
