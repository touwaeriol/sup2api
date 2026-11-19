<script setup lang="ts">
// Step 2 of "new account" and the edit form (wireframe A.4, CONTRACTS §18.4):
// connection (basic info, proxy, plugin credentials) / protocols and models
// (own protocols, model list, mapping, protocols per model:
// DESIGN-2026-11-17-PROTOCOL-BINDING §5.1) / scheduling and limits. A new
// account starts from the plugin's default models and mapping (§41); so does
// an existing account saved without either (see shouldPrefillOnEdit).
// Every core field follows the account type's common controls (hidden, fixed,
// a default, a required proxy: commonControls.ts); plugin sections declared
// for the type (account.editor.section) come after the core ones.
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SBadge, SButton, SCard, SChoiceCards, SField, SHint, SIcon, SInput, SLink, SSegmented, SSelect, SSettingRow, SSettingsGroup, SSpinner, SSwitch, toast } from '@sub2api/ui'
import type { Account, AccountType, ConversionLossPolicy, EnvironmentValues, ProxyProbeResult } from '@/api/types'
import EnvironmentEditor from '@/components/environment/EnvironmentEditor.vue'
import PluginIframe from '@/components/plugin/PluginIframe.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import PluginComponent from '@/components/plugin/PluginComponent.vue'
import { forAccountType, slotKey } from '@/components/plugin/slots'
import GroupPicker from '@/components/GroupPicker.vue'
import ProxyPicker from '@/components/ProxyPicker.vue'
import { lt } from '@/i18n'
import { assetURL, usePluginStore } from '@/stores/plugins'
import { useEnvironmentLayer, useGroupsWith, useProxiesLookup } from '@/composables/lookups'
import { ACCOUNT_ACTIONS, useScope } from '@/composables/useScope'
import { useAccountCaps } from '@/composables/useAccountCaps'
import { fieldErrors, notifyError } from '@/utils/errors'
import { looksLikeProxyURL, parseProxyURL } from '@/utils/proxyUrl'
import { groupPolicies, policyLabel, policyOptions, policySourceHint } from '@/utils/conversion'
import AccountEditorNav from './AccountEditorNav.vue'
import ParamOverrideField from './paramOverride/ParamOverrideField.vue'
import { operationCount, type Override as ParamOverride } from './paramOverride/model'
import HeaderOverrideField from './headerOverride/HeaderOverrideField.vue'
import { entryCount, type HeaderOverride } from './headerOverride/model'
import AccountModelsCard from './AccountModelsCard.vue'
import FetchedModelsModal from './FetchedModelsModal.vue'
import { sameCreationGroup } from './accountTypeChoices'
import { accountControl, newAccountValues, sameControlValue, savedAccountValues, useCommonControls, type CommonControlName, type ControlValues } from './commonControls'
import HeaderPassthroughEditor from './controls/HeaderPassthroughEditor.vue'
import RateLimitEditor from './controls/RateLimitEditor.vue'
import ModelLimitsEditor from './ModelLimitsEditor.vue'
import AccountUsageLimitCard from './AccountUsageLimitCard.vue'
import { useUsageLimitEdit } from './useUsageLimitEdit'
import { toCards, type ModelLimitCard } from './modelLimits'
import { SPM_WINDOW_DEFAULT } from './spmWindow'
import { newAccountConcurrency } from './accountLimits'
import { shouldPrefillOnEdit } from './modelDefaults'
import { useModelEdit } from './useModelEdit'
import { useAccountSave } from './useAccountSave'
import { CONNECTION_FIELDS, isAdvancedField, isProtocolField, isSchedulingField, LIMIT_ROWS, mappingErrorText, modelsErrorText, pluginSection, protocolsErrorTexts, SCHEDULING_ROWS, SECTION_CONTROLS, splitFieldErrors, type EditorSection } from './editorErrors'
import ModelProtocolsCard from './protocols/ModelProtocolsCard.vue'
import OwnProtocolsField from './protocols/OwnProtocolsField.vue'
import { sameProtocolColumns } from './protocols/protocolBinding'
import { useProtocolLabels } from './protocols/protocolLabels'
import { useProtocolEdit } from './protocols/useProtocolEdit'

const props = defineProps<{ accountType: AccountType | null; accountTypeOptions?: AccountType[]; account?: Account | null }>()
const emit = defineEmits<{ (e: 'change-type', at: AccountType): void; (e: 'saved', a: Account): void; (e: 'cancel'): void; (e: 'back'): void; (e: 'test', a: Account): void }>()
const { t } = useI18n()
const plugins = usePluginStore()
const scope = useScope()

const editing = computed(() => !!props.account?.id)
const caps = useAccountCaps(editing)

const mode = computed(() => props.accountType?.form.mode)
// Row-level rights (CONTRACTS §21): editing an account needs a scope of
// account:update that reaches it.
const canEditAccount = computed(() => editing.value && scope.can(props.account, ACCOUNT_ACTIONS.update))
const canTestAccount = computed(() => editing.value && scope.can(props.account, ACCOUNT_ACTIONS.test))
const authOptions = computed(() => (props.accountTypeOptions || []).map(at => ({ value: at.type, label: lt(at.auth_method_label) || at.type, disabled: !caps.creatable(at) })))
/** Guarded settings stay at the official values for this caller (the server enforces it). */
const settingsGuarded = computed(() => !!props.accountType?.guarded_settings?.length && !caps.customSettings.value)
function changeAuth(value: unknown) {
  const at = props.accountTypeOptions?.find(at => at.type === value)
  if (!editing.value && at && at.type !== props.accountType?.type) emit('change-type', at)
}

const basic = reactive({
  name: '',
  group_ids: [] as number[],
  proxy_id: null as number | null,
  priority: 10,
  weight: 1,
  max_concurrency: 0,
  schedulable: true,
  auto_disable: true,
  rpm_limit: 0,
  tpm_limit: 0,
  spm_limit: 0,
  spm_window_seconds: SPM_WINDOW_DEFAULT as number | null,
  environment: {} as EnvironmentValues,
  header_passthrough: [] as string[],
  header_override: null as HeaderOverride | null,
  param_override: null as ParamOverride | null,
  conversion_loss_policy: null as ConversionLossPolicy | null
})
/** 指定模型限速 (CONTRACTS §68), edited as cards and saved as one entry per list. */
const modelLimitCards = ref<ModelLimitCard[]>([])
const credentials = ref<Record<string, unknown>>({})
const errors = ref<Record<string, string>>({})
const credErrors = ref<Record<string, string>>({})
const saving = ref(false)

const iframe = ref<InstanceType<typeof PluginIframe>>()
const nativeRef = ref<{ validate?: () => boolean | Promise<boolean> }>()
const uiPlugin = computed(() => (props.accountType ? plugins.plugin(props.accountType.plugin_key) : undefined))
const iframeSrc = computed(() => (uiPlugin.value && props.accountType?.form.page ? assetURL(uiPlugin.value, props.accountType.form.page) : ''))
// The plugin list is still loading: show a spinner, not "form unavailable"
// (a native form waits for its plugin's module too, in PluginComponent).
const pluginFormWaiting = computed(() => !plugins.listed)

// ---------------------------------------------------------------- common controls (docs/COMMON-CONTROLS.md §2)

/** Hidden, fixed (with the reason) or required, per core field. */
const controls = useCommonControls(() => props.accountType, () => props.account)
const anyVisible = (names: readonly CommonControlName[]) => names.some(n => controls.value[n].visible)

const autoDisableEnabled = ref<boolean | null>(null)
const autoDisablePolicyFailed = ref(false)
let autoDisablePolicyRequest = 0
const autoDisableHint = computed(() => {
  if (autoDisableEnabled.value === false) return t('accounts.editorUi.autoDisableGlobalOff')
  if (controls.value.auto_disable.reason) return controls.value.auto_disable.reason
  if (autoDisableEnabled.value === true) return t('accounts.editorUi.autoDisableHint')
  return autoDisablePolicyFailed.value ? t('accounts.editorUi.autoDisableUnavailable') : t('common.loading')
})

async function loadAutoDisablePolicy() {
  const request = ++autoDisablePolicyRequest
  autoDisableEnabled.value = null
  autoDisablePolicyFailed.value = false
  try {
    const policy = await api.get<{ enabled: boolean }>('/accounts/auto-disable')
    if (request === autoDisablePolicyRequest) autoDisableEnabled.value = policy.enabled
  } catch {
    if (request === autoDisablePolicyRequest) autoDisablePolicyFailed.value = true
  }
}

watch(() => [props.account, props.accountType], loadAutoDisablePolicy, { immediate: true })
onMounted(() => window.addEventListener('focus', loadAutoDisablePolicy))
onBeforeUnmount(() => {
  autoDisablePolicyRequest++
  window.removeEventListener('focus', loadAutoDisablePolicy)
})

// ---------------------------------------------------------------- sections

const activeSection = ref<EditorSection>('connection')
// Usage limit (CONTRACTS §73): loaded when the scheduling section is shown, saved after the account.
const usageLimit = useUsageLimitEdit(() => props.account?.id, () => activeSection.value === 'scheduling')
/** The environment card shows only when the caller may read the declared variables (403 for some roles) and the account layer has any. */
const showEnvironment = useEnvironmentLayer('account')
const { limit: usageLimitModel, status: usageLimitStatus, loadError: usageLimitLoadError, errors: usageLimitErrors } = usageLimit
// The editor and each section start at the top: the modal body keeps the
// previous scroll offset otherwise (the type picker's, the last section's).
// The form's scroll-mt-4 keeps the body's top padding in view. Post-flush, so
// save() still scrolls to an invalid field.
const formEl = ref<HTMLFormElement>()
const toTop = () => formEl.value?.scrollIntoView({ block: 'start' })
onMounted(toTop)
watch(activeSection, toTop, { flush: 'post' })

const editorModels = useModelEdit(() => props.accountType)
const {
  models, mapping, prefilled, defaultModels, modelsTextMode,
  modelsError, mappingError, syncModelsText, clearPrefill, modelOptions
} = editorModels
/** Models a model list can hold: those the account serves, or every known model when it serves all. */
const modelLimitOptions = computed(() => (models.value.length ? models.value : [...new Set([...defaultModels.value, ...modelOptions.value])]))
const mergeModels = (ids: string[]) => editorModels.mergeModels(ids)
/** Models and mappings of the section summary (the cards render their own counts). */
const mappingCount = computed(() => Object.keys(mapping.value).length)

// Protocol binding (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1): the own protocols the account opens, the protocols of chosen models.
const protocolEdit = useProtocolEdit(() => props.accountType, () => models.value)
const protocolLabels = useProtocolLabels()
const ownProtocolsTitle = computed(() => {
  const names = protocolLabels.platformsName(props.accountType?.platforms ?? [])
  return names ? t('accounts.protocols.own.titleOf', { platforms: names }) : t('accounts.protocols.own.title')
})
/** "Protocols 4/4 · 2 models limited · 1 converted", ahead of the models of the section summary. */
const protocolSummary = computed(() => {
  const { open, total, limited, converting } = protocolEdit.summary.value
  return [
    t('accounts.protocols.summary.open', { open, total }),
    ...(limited ? [t('accounts.protocols.summary.limited', { n: limited })] : []),
    converting ? t('accounts.protocols.summary.converting', { n: converting }) : t('accounts.protocols.summary.noConversion')
  ]
})

/** Summary parts of the shown controls (falsy parts dropped). */
const summaryOf = (parts: Array<[CommonControlName, string | number | false]>) => parts.filter(([n, v]) => v && controls.value[n].visible).map(([, v]) => v)

/** The advanced section's summary: header passthrough, header override, parameter override, the account's own conversion loss policy and environment values. */
const forwardingSummary = computed(() => {
  const rules = basic.header_passthrough
  const headers = entryCount(basic.header_override)
  const overrides = operationCount(basic.param_override)
  const custom = Object.keys(basic.environment).length
  const policy = basic.conversion_loss_policy
  const policyPart = policy ? [t('accounts.forwarding.conversion', { policy: policyLabel(t, policy) })] : []
  if (!summaryOf([['header_passthrough', rules.length], ['header_override', headers], ['param_override', overrides], ['environment', custom]]).length && !policy) return t('accounts.forwarding.allDefault')
  return [
    ...summaryOf([
      ['header_passthrough', rules.includes('*') ? t('accounts.forwarding.passAll') : rules.length ? t('accounts.forwarding.passRules', { n: rules.length }) : t('accounts.forwarding.passNone')],
      ['header_override', headers && t('accounts.forwarding.headerOverride', { n: headers })],
      ['param_override', overrides && t('accounts.forwarding.override', { n: overrides })]
    ]),
    ...policyPart,
    ...summaryOf([['environment', custom ? t('accounts.forwarding.envCustom', { n: custom }) : t('accounts.forwarding.envFollow')]])
  ].join(' · ')
})

// Conversion loss policy (CONVERT-MATRIX §6.3): null inherits each group's, the hint shows what applies.
const groupList = useGroupsWith(() => basic.group_ids)
const conversionOptions = computed(() => policyOptions(t, t('accounts.conversion.inherit')))
const conversionHint = computed(() => policySourceHint(t, basic.conversion_loss_policy, groupPolicies(basic.group_ids, groupList.value)))
/** Without a model converted or a protocol adapted nothing can be lost: the policy is greyed (an account whose type is gone keeps it editable). */
const conversionUsed = computed(() => !props.accountType || protocolEdit.conversionUsed.value)

/** Plugin sections declared for this account type (docs/COMMON-CONTROLS.md §4). */
const pluginSections = computed(() => plugins.slotEntries('account.editor.section').filter(e => forAccountType(e, props.accountType)))

const editorSections = computed(() => [
  { key: 'connection' as EditorSection, icon: 'key', label: t('accounts.editorUi.connection'), summary: basic.name.trim() || t('accounts.editorUi.unnamed') },
  {
    key: 'models' as const,
    icon: 'cpu',
    label: t('accounts.editorUi.models'),
    summary: [
      ...(props.accountType ? protocolSummary.value : []),
      models.value.length ? t('accounts.editorUi.modelsSummary', { n: models.value.length }) : t('accounts.editorUi.allModels'),
      ...summaryOf([['model_mapping', mappingCount.value && t('accounts.editorUi.mappingSummary', { n: mappingCount.value })]])
    ].join(' · ')
  },
  {
    key: 'scheduling' as const,
    icon: 'bolt',
    label: t('accounts.editorUi.scheduling'),
    summary: summaryOf([
      ['priority', t('accounts.editorUi.summaryPriority', { n: basic.priority })],
      ['weight', t('accounts.editorUi.summaryWeight', { n: basic.weight })],
      ['max_concurrency', t('accounts.editorUi.summaryConcurrency', { n: basic.max_concurrency || '∞' })],
      ['model_limits', modelLimitCards.value.length && t('accounts.modelLimits.summary', { n: modelLimitCards.value.length })],
      ['usage_limit', usageLimit.summary.value]
    ]).join(' · ')
  },
  {
    key: 'advanced' as const,
    icon: 'code',
    label: t('accounts.editorUi.advanced'),
    summary: forwardingSummary.value
  },
  ...pluginSections.value.map(e => ({
    key: pluginSection(slotKey(e)),
    icon: e.icon || 'puzzle',
    label: lt(e.label) || e.name,
    summary: editing.value ? lt(plugins.plugin(e.pluginKey)?.name) || e.pluginKey : t('accounts.editorUi.pluginSectionAfterSave')
  }))
].filter(s => !SECTION_CONTROLS[s.key]?.length || anyVisible(SECTION_CONTROLS[s.key]!)))
/** Priority and weight rows of the scheduling group. */
const schedulingFields = computed(() =>
  [
    { key: 'priority' as const, label: t('accounts.priority'), hint: t('accounts.priorityHint'), min: 0, max: 1_000_000 },
    { key: 'weight' as const, label: t('accounts.weight'), hint: t('accounts.weightHint'), min: 1, max: 1000 }
  ].filter(f => controls.value[f.key].visible)
)
/** Server field errors of the model list / mapping, shown under the editor. */
const serverModelsError = computed(() => modelsErrorText(errors.value))
const serverMappingError = computed(() => mappingErrorText(errors.value))
const serverProtocolErrors = computed(() => protocolsErrorTexts(errors.value, protocolEdit.protocols.value))
/** Sections holding an error, marked in the navigation. */
const sectionErrors = computed<Partial<Record<EditorSection, boolean>>>(() => ({
  connection: CONNECTION_FIELDS.some((k) => errors.value[k]) || Object.keys(credErrors.value).length > 0,
  models: !!(modelsError.value || serverModelsError.value || mappingError.value || serverMappingError.value) || Object.keys(errors.value).some(isProtocolField) || protocolEdit.issues.value.some(i => i.level === 'error'),
  scheduling: Object.keys(errors.value).some(isSchedulingField) || Object.keys(usageLimitErrors.value).length > 0,
  advanced: Object.keys(errors.value).some(isAdvancedField)
}))

// ---------------------------------------------------------------- proxy: pick an existing one | paste a URL (CONTRACTS §21.4)
// "url" sends proxy_url instead of proxy_id; the server parses it, reuses a
// matching proxy the caller can see or creates one, and answers proxy_created.
// A pasted one can also be tested and saved first (probeProxyUrl).
type ProxyMode = 'existing' | 'url'
const proxyMode = ref<ProxyMode>('existing')
const proxyUrl = ref('')
// Pasting a URL may create a proxy: only with a proxy management key.
const proxyModeOptions = computed(() => [
  { value: 'existing', label: t('accounts.proxyPickExisting') },
  ...(caps.proxyByURL.value ? [{ value: 'url', label: t('accounts.proxyPasteUrl') }] : [])
])
// The paste option withdrawn (permissions reloaded): back to picking one.
watch(caps.proxyByURL, (ok) => {
  if (!ok && proxyMode.value === 'url') setProxyMode('existing')
})
/** The proxy_url that will be sent, or '' (existing mode / empty input). */
const proxyUrlToSend = computed(() => (proxyMode.value === 'url' ? proxyUrl.value.trim() : ''))
/**
 * The form's proxy choice as the core takes it for an account not saved yet
 * (models/fetch, proxy-ticket): a pasted proxy_url is only parsed and used,
 * no proxy is looked up or created (CONTRACTS §21.2); proxy_id null is none.
 */
const proxyChoice = () => (proxyUrlToSend.value ? { proxy_url: proxyUrlToSend.value } : { proxy_id: basic.proxy_id })

/**
 * The native form's `proxyTicket` (CONTRACTS §23.5, §117): a proxy ticket of
 * the form's current proxy choice, taken by an authorization flow for every
 * call it makes to its plugin before the account exists. A refused choice is
 * shown under the proxy field too, and the error goes back to the form.
 */
async function proxyTicket(): Promise<string> {
  const at = props.accountType!
  try {
    const r = await api.post<{ ticket: string }>(`/account-types/${encodeURIComponent(at.plugin_key)}/${encodeURIComponent(at.type)}/proxy-ticket`, proxyChoice())
    return r.ticket
  } catch (e) {
    const fe = fieldErrors(e)
    const field = fe.proxy_url ? 'proxy_url' : fe.proxy_id ? 'proxy_id' : ''
    if (field) errors.value = { ...errors.value, [field]: fe[field] }
    throw e
  }
}
const proxyError = computed(() => (proxyMode.value === 'url' ? errors.value.proxy_url : errors.value.proxy_id))
/** Shape hint only (the server validates): shown while the pasted text does not look like scheme://host:port. */
const proxyUrlHint = computed(() => {
  const raw = proxyUrl.value.trim()
  if (!raw) return ''
  const r = parseProxyURL(raw)
  if (r.ok) return ''
  // host:port:user:pass and user:pass@host:port have no scheme: the test detects it.
  if (r.error === 'scheme') return looksLikeProxyURL(raw) ? t('accounts.proxyUrlSchemeHint') : t('accounts.proxyUrlProbeHint')
  if (r.error === 'port') return t('accounts.proxyUrlPortHint')
  if (r.error === 'extra') return t('accounts.proxyUrlExtraHint')
  return t('accounts.proxyUrlShapeHint')
})
/** Last successful test of a pasted proxy, shown under the proxy field. */
const probed = ref<{ protocol: string; ip: string; id: number; created: boolean } | null>(null)
const probing = ref(false)
const showProbed = computed(() => !!probed.value && proxyMode.value === 'existing' && basic.proxy_id === probed.value.id)

function clearErrors(...keys: string[]) {
  if (keys.some((k) => errors.value[k])) errors.value = Object.fromEntries(Object.entries(errors.value).filter(([k]) => !keys.includes(k)))
}
function setProxyMode(m: string) {
  proxyMode.value = m as ProxyMode
  clearErrors('proxy_url', 'proxy_id')
}

/**
 * POST /proxies/probe {save}: tries the pasted proxy (socks5, http, then https
 * without a scheme), finds or creates the proxy that works and selects it.
 */
async function probeProxyUrl() {
  const raw = proxyUrl.value.trim()
  if (!raw || probing.value) return
  clearErrors('proxy_url')
  probed.value = null
  probing.value = true
  try {
    const r = await api.post<ProxyProbeResult>('/proxies/probe', { proxy_url: raw, save: true })
    if (!r.ok || !r.proxy_id) {
      const tried = r.attempts.map(a => `${a.protocol}: ${a.message || a.status || t('proxies.failed')}`).join('; ')
      errors.value = { ...errors.value, proxy_url: t('proxies.probeFailed', { error: tried }) }
      return
    }
    await useProxiesLookup(true).ready
    probed.value = { protocol: (r.protocol || '').toUpperCase(), ip: r.ip || '', id: r.proxy_id, created: r.proxy_created }
    proxyUrl.value = ''
    proxyMode.value = 'existing'
    basic.proxy_id = r.proxy_id
  } catch (e) {
    const fe = fieldErrors(e)
    if (fe.proxy_url) errors.value = { ...errors.value, proxy_url: fe.proxy_url }
    else notifyError(e)
  } finally {
    probing.value = false
  }
}

// ---------------------------------------------------------------- fetch models from the upstream (CONTRACTS §19)

const fetchOpen = ref(false)
const fetching = ref(false)
const fetched = ref<{ models: string[]; skipped: number }>({ models: [], skipped: 0 })
const canFetch = computed(() => !!props.accountType && !props.account?.orphaned && (editing.value ? canTestAccount.value : caps.scope.value !== null && caps.creatable(props.accountType)))

/** Server field errors of the credentials: on the plugin form (and its iframe). */
function showCredentialErrors(cred: Record<string, string>) {
  credErrors.value = cred
  if (mode.value === 'iframe' && Object.keys(cred).length) iframe.value?.setErrors(cred)
}

async function fetchModels() {
  const at = props.accountType
  if (!at) return
  const creds = await collectCredentials()
  if (!creds) {
    activeSection.value = 'connection'
    return
  }
  fetching.value = true
  try {
    const r = editing.value
      ? await api.post<{ models: string[]; skipped: number }>(`/accounts/${props.account!.id}/models/fetch`, { credentials: creds })
      : await api.post<{ models: string[]; skipped: number }>(`/account-types/${encodeURIComponent(at.plugin_key)}/${encodeURIComponent(at.type)}/models/fetch`, { credentials: creds, ...proxyChoice() })
    fetched.value = { models: r.models || [], skipped: r.skipped || 0 }
    if (fetched.value.models.length) fetchOpen.value = true
    else toast(t('accounts.fetchEmpty'), 'warning')
  } catch (e) {
    // Credential problems land on the plugin form, like a failed save.
    const fe = fieldErrors(e)
    if (Object.keys(fe).length) {
      const { cred } = splitFieldErrors(fe)
      showCredentialErrors(cred)
      if (Object.keys(cred).length || fe.proxy_url) activeSection.value = 'connection'
      if (fe.proxy_url) errors.value = { ...errors.value, proxy_url: fe.proxy_url }
    }
    notifyError(e)
  } finally {
    fetching.value = false
  }
}

/** Merges the ticked models into the list (existing entries are kept). */
function applyFetched(picked: string[]) {
  if (!syncModelsTextIfNeeded()) return
  mergeModels(picked)
  fetchOpen.value = false
  toast(t('accounts.fetchApplied', { n: models.value.length }), 'success')
}
const syncModelsTextIfNeeded = () => !modelsTextMode.value || syncModelsText()

// ---------------------------------------------------------------- load

/** Fills the form from the account (or a new one) and the account type. */
function fillForm(a: Account | null | undefined, at: AccountType | null) {
  basic.name = a?.name || ''
  basic.group_ids = [...(a?.group_ids || [])]
  basic.proxy_id = a?.proxy_id ?? null
  // Editing defaults to "pick existing" with the current proxy selected.
  proxyMode.value = 'existing'
  proxyUrl.value = ''
  basic.conversion_loss_policy = a?.conversion_loss_policy ?? null
  if (a) editorModels.setSaved(a.models, a.model_mapping)
  if (!a || shouldPrefillOnEdit(a, at)) editorModels.applyDefaults(at)
  // After the models: the matrix view needs the model list.
  protocolEdit.setSaved(a)
  applyValues(a ? savedAccountValues(a, coreValues()) : newAccountValues(at, coreValues(), ownCaller()))
}

/** Whether the caller creates accounts at a restricted scope (capped scheduling, CONTRACTS §15.9). */
const ownCaller = () => scope.level(ACCOUNT_ACTIONS.create) === 'own'
/** The core's values of the controls (before the plugin's defaults). */
const coreValues = (): ControlValues => ({
  priority: 10, weight: 1, max_concurrency: newAccountConcurrency(scope.level(ACCOUNT_ACTIONS.create)), schedulable: true, auto_disable: true,
  rpm_limit: 0, tpm_limit: 0, spm_limit: 0, spm_window_seconds: SPM_WINDOW_DEFAULT, environment: {}, header_passthrough: [], header_override: null, param_override: null, model_limits: []
})
/** Controls kept outside `basic`. */
const setControl: Partial<Record<CommonControlName, (v: any) => void>> = {
  model_limits: v => (modelLimitCards.value = toCards(v)),
  model_mapping: v => (mapping.value = v),
  usage_limit: v => usageLimit.prefill(v)
}
/** Puts control values (copies) into the form. */
function applyValues(values: ControlValues) {
  for (const [name, v] of Object.entries(values)) {
    const value = v == null ? v : JSON.parse(JSON.stringify(v))
    const set = setControl[name as CommonControlName]
    if (set) set(value)
    else if (name in basic) Object.assign(basic, { [name]: value })
  }
}

/** Switching the auth method of a new account: untouched defaults and fixed controls follow the type. */
function switchAuth(at: AccountType | null, previous: AccountType | null | undefined) {
  if (editorModels.sameDefaults(previous)) editorModels.applyDefaults(at)
  // What was ticked only carries over to a type offering the same protocols.
  if (!sameProtocolColumns(at, previous)) protocolEdit.setSaved(null)
  const before = newAccountValues(previous, coreValues(), ownCaller())
  const untouched = (name: string) => name in basic && sameControlValue(basic[name as keyof typeof basic], before[name as CommonControlName])
  const follows = ([name]: [string, unknown]) => !accountControl(at, name as CommonControlName).configurable || untouched(name)
  applyValues(Object.fromEntries(Object.entries(newAccountValues(at, coreValues(), ownCaller())).filter(follows)))
}

watch(
  () => [props.account, props.accountType] as const,
  ([a, at], previous) => {
    activeSection.value = 'connection'
    errors.value = {}
    credErrors.value = {}
    editorModels.resetEditing()
    if (!a && !previous?.[0] && sameCreationGroup(at, previous?.[1])) switchAuth(at, previous?.[1])
    else fillForm(a, at)
    credentials.value = { ...(a?.credentials || {}) }
  },
  { immediate: true }
)

// Server-side credential errors are stale once the user edits the form.
watch(credentials, () => {
  if (Object.keys(credErrors.value).length) credErrors.value = {}
}, { deep: true })

// ---------------------------------------------------------------- save

async function collectCredentials(): Promise<Record<string, unknown> | null> {
  if (!props.accountType) return credentials.value
  if (mode.value === 'native') {
    const validate = nativeRef.value?.validate
    return typeof validate === 'function' && !(await validate()) ? null : credentials.value
  }
  if (mode.value !== 'iframe') return credentials.value
  if (!iframe.value?.isReady()) {
    toast(t('pluginHost.frameNotReady'), 'warning')
    return null
  }
  const v = await iframe.value.validate().catch(() => ({ ok: false }))
  return v?.ok ? (await iframe.value.getValue()) || {} : null
}

/** Checks done before the request; the section to show when one fails. */
// The save pipeline (checks, request body, POST/PATCH, error routing) lives in
// useAccountSave; the template and the wiring below stay here.
const { save } = useAccountSave({
  account: computed(() => props.account),
  accountType: computed(() => props.accountType),
  editing,
  basic,
  modelLimits: modelLimitCards,
  errors,
  credErrors,
  saving,
  activeSection,
  models: editorModels,
  protocols: protocolEdit,
  autoDisableEnabled,
  proxyUrlToSend,
  collectCredentials,
  showCredentialErrors,
  loadAutoDisablePolicy,
  usageLimit,
  saved: (a) => emit('saved', a)
})
</script>

<template>
  <form ref="formEl" class="flex min-h-full scroll-mt-4 flex-col gap-5" novalidate @submit.prevent="save">
    <div class="grid gap-5 md:grid-cols-[220px_minmax(0,1fr)]">
      <AccountEditorNav v-model="activeSection" :account-type="accountType" :sections="editorSections" :errors="sectionErrors" />

      <div class="min-w-0">
        <!-- ============================================================ connection -->
        <div v-show="activeSection === 'connection'" data-editor-section="connection" class="space-y-4">
          <SCard :title="t('accounts.basic')" :subtitle="t('accounts.editorUi.basicHint')">
            <div class="grid gap-4 md:grid-cols-2">
              <SField :label="t('common.name')" :error="errors.name" required>
                <SInput v-model="basic.name" :maxlength="100" :placeholder="t('accounts.editorUi.namePlaceholder')" />
              </SField>
              <SField :label="t('accounts.groups')" :error="errors.group_ids" :hint="!caps.bindGroups.value ? t('accounts.groupsLocked') : basic.group_ids.length ? '' : t('accounts.editorUi.groupsHint')">
                <GroupPicker :model-value="basic.group_ids" multiple :disabled="!caps.bindGroups.value" @update:model-value="basic.group_ids = $event as number[]" />
              </SField>
              <div class="md:col-span-2" data-testid="proxy-field">
                <div class="mb-1.5 flex flex-wrap items-center justify-between gap-2">
                  <span class="text-sm font-medium text-fg">{{ t('accounts.proxy') }}<span v-if="controls.proxy.required" class="ml-0.5 text-red-500" aria-hidden="true">*</span></span>
                  <SSegmented v-if="proxyModeOptions.length > 1" :model-value="proxyMode" :options="proxyModeOptions" size="sm" :aria-label="t('accounts.proxy')" data-testid="proxy-mode" @update:model-value="setProxyMode(String($event))" />
                </div>
                <SField :error="proxyError">
                  <ProxyPicker v-if="proxyMode === 'existing'" v-model="basic.proxy_id" />
                  <div v-else class="flex gap-2">
                    <SInput
                      v-model="proxyUrl"
                      class="flex-1"
                      mono
                      autocomplete="off"
                      spellcheck="false"
                      data-testid="proxy-url"
                      placeholder="socks5://user:pass@host:port"
                      @keydown.enter.prevent="probeProxyUrl"
                    />
                    <SButton :disabled="!proxyUrl.trim()" :loading="probing" data-testid="proxy-url-probe" @click="probeProxyUrl">
                      {{ probing ? t('proxies.probing') : t('proxies.probeProxy') }}
                    </SButton>
                  </div>
                  <template v-if="proxyMode === 'url'" #hint>
                    <SHint v-if="proxyUrlHint" inline size="xs" tone="warning" data-testid="proxy-url-hint">{{ proxyUrlHint }}</SHint>
                    <template v-else>{{ t('accounts.proxyUrlHint') }}</template>
                  </template>
                </SField>
                <SHint v-if="showProbed" tone="success" size="xs" class="mt-1" role="status" data-testid="proxy-probe-result">
                  {{ t(probed!.created ? 'accounts.proxyProbeCreated' : 'accounts.proxyProbeReused', { protocol: probed!.protocol, ip: probed!.ip, id: probed!.id }) }}
                </SHint>
              </div>
            </div>
          </SCard>

          <SCard :title="t('accounts.credentials')" :subtitle="accountType ? t('accounts.editorUi.credentialsHint', { plugin: lt(accountType.plugin_name) || accountType.plugin_key }) : ''">
            <template v-if="accountType?.auth_method_label && (editing || authOptions.length <= 1)" #actions>
              <SBadge tone="accent">{{ lt(accountType.auth_method_label) }}</SBadge>
            </template>

            <SField v-if="!editing && authOptions.length > 1" :label="t('accounts.editorUi.authMethod')" class="mb-5">
              <SChoiceCards :model-value="accountType?.type" :options="authOptions" :cols="2" :disabled="saving" data-testid="auth-method" @update:model-value="changeAuth" />
            </SField>

            <SHint v-if="settingsGuarded" size="xs" class="mb-3" data-testid="guarded-settings-hint">{{ t('accounts.guardedSettingsHint') }}</SHint>
            <SAlert v-if="account?.orphaned || !accountType" tone="warning">{{ t('accounts.orphanedEdit') }}</SAlert>
            <template v-else-if="mode === 'iframe'">
              <PluginIframe
                v-if="iframeSrc"
                ref="iframe"
                :plugin-key="accountType.plugin_key"
                :page="accountType.form.page"
                :src="iframeSrc"
                mode="account-form"
                :value="credentials"
                :min-height="200"
                @change="credentials = $event || {}"
              />
              <div v-else-if="pluginFormWaiting" class="flex justify-center py-6"><SSpinner /></div>
              <SHint v-else tone="danger">{{ t('accounts.formUnavailable') }}</SHint>
            </template>
            <template v-else-if="mode === 'native'">
              <PluginComponent v-slot="{ component }" :plugin-key="accountType.plugin_key" :name="accountType.form.component">
                <component :is="component" ref="nativeRef" v-model="credentials" :mode="editing ? 'edit' : 'create'" :account="account" :account-type="accountType" :errors="credErrors" :proxy-ticket="proxyTicket" />
              </PluginComponent>
            </template>
            <!-- An older package's form mode: the credentials are sent back unchanged. -->
            <SAlert v-else tone="warning" data-testid="form-unavailable-old">{{ t('accounts.formUnavailableOld') }}</SAlert>

            <div class="mt-4 space-y-3 empty:hidden">
              <PluginSlot name="account.form.widgets" :props="{ account, accountType, credentials }" />
            </div>
          </SCard>
        </div>

        <!-- ============================================================ models -->
        <div v-show="activeSection === 'models'" data-editor-section="models" class="space-y-4">
          <SCard v-if="accountType" :title="ownProtocolsTitle" :subtitle="t('accounts.protocols.own.subtitle')" data-testid="own-protocols-card">
            <OwnProtocolsField :editor="protocolEdit" :errors="serverProtocolErrors" :disabled="saving" />
            <SAlert tone="neutral" dense class="mt-3">{{ t('accounts.protocols.own.crossHint') }}</SAlert>
          </SCard>
          <AccountModelsCard
            :editor="editorModels"
            :editing="editing"
            :can-fetch="canFetch"
            :fetching="fetching"
            :errors="errors"
            :merge-models="mergeModels"
            :mapping-control="controls.model_mapping"
            @fetch="fetchModels"
          />
          <ModelProtocolsCard v-if="accountType" :editor="protocolEdit" :models="models" :mapping="mapping" :options="modelLimitOptions" :errors="errors" :disabled="saving" />
        </div>

        <!-- ============================================================ scheduling and limits -->
        <div v-show="activeSection === 'scheduling'" data-editor-section="scheduling" class="space-y-4">
          <SSettingsGroup v-if="anyVisible(SCHEDULING_ROWS)" :title="t('accounts.scheduling')" :description="t('accounts.editorUi.schedulingCardHint')" data-testid="account-scheduling">
            <SSettingRow v-for="f in schedulingFields" :key="f.key" :label="f.label" :description="controls[f.key].reason || f.hint" :error="errors[f.key]">
              <SInput v-model.number="basic[f.key]" type="number" :min="f.min" :max="f.max" :disabled="controls[f.key].disabled" class="w-full sm:w-40" :data-testid="`account-${f.key}`" />
            </SSettingRow>
            <SSettingRow v-if="controls.schedulable.visible" :label="t('accounts.schedulable')" :description="controls.schedulable.reason || t('accounts.editorUi.schedulableHint')">
              <SSwitch v-model="basic.schedulable" :disabled="controls.schedulable.disabled || saving" data-testid="account-scheduling-control" />
            </SSettingRow>
            <SSettingRow v-if="controls.auto_disable.visible" :label="t('accounts.autoDisable')" :description="autoDisableHint">
              <SSwitch v-model="basic.auto_disable" :disabled="autoDisableEnabled !== true || controls.auto_disable.disabled || saving" data-testid="account-auto-disable" />
            </SSettingRow>
          </SSettingsGroup>

          <SSettingsGroup v-if="anyVisible(LIMIT_ROWS)" :title="t('accounts.limits')" :description="t('accounts.editorUi.limitsHint')" data-testid="account-limits">
            <SSettingRow v-if="controls.max_concurrency.visible" :label="t('accounts.maxConcurrency')" :description="controls.max_concurrency.reason || t('accounts.zeroUnlimited')" :error="errors.max_concurrency">
              <SInput v-model.number="basic.max_concurrency" type="number" :min="0" :suffix="t('accounts.editorUi.unitConcurrency')" :disabled="controls.max_concurrency.disabled" class="w-full sm:w-48" data-testid="account-max_concurrency" />
            </SSettingRow>
            <RateLimitEditor :model-value="basic" :errors="errors" :controls="controls" @update:model-value="Object.assign(basic, $event)" />
          </SSettingsGroup>

          <AccountUsageLimitCard
            v-if="controls.usage_limit.visible"
            v-model="usageLimitModel"
            :status="usageLimitStatus"
            :load-error="usageLimitLoadError"
            :usage="account?.usage"
            :disabled="controls.usage_limit.disabled || saving"
            :reason="controls.usage_limit.reason"
            :errors="usageLimitErrors"
            @retry="usageLimit.load"
          />

          <SSettingsGroup v-if="controls.model_limits.visible" :title="t('accounts.modelLimits.title')" :description="controls.model_limits.reason || t('accounts.modelLimits.hint')" data-testid="account-model-limits">
            <div class="p-4">
              <ModelLimitsEditor v-model="modelLimitCards" :options="modelLimitOptions" :errors="errors" :disabled="controls.model_limits.disabled || saving" />
            </div>
          </SSettingsGroup>
        </div>

        <!-- ============================================================ advanced -->
        <div v-show="activeSection === 'advanced'" data-editor-section="advanced" class="space-y-4">
          <SSettingsGroup :title="t('accounts.forwarding.title')" :description="t('accounts.forwarding.hint')" data-testid="account-forwarding">
            <SSettingRow v-if="controls.header_passthrough.visible" :label="t('accounts.headerPassthrough.title')" :description="controls.header_passthrough.reason || t('accounts.headerPassthrough.hint')" :error="errors.header_passthrough" data-testid="account-header-passthrough">
              <HeaderPassthroughEditor v-model="basic.header_passthrough" :disabled="controls.header_passthrough.disabled || saving" class="w-full sm:w-96" data-testid="account-header-passthrough-rules" />
            </SSettingRow>
            <HeaderOverrideField v-if="controls.header_override.visible" v-model="basic.header_override" :error="errors.header_override" :disabled="controls.header_override.disabled || saving" :reason="controls.header_override.reason" />
            <ParamOverrideField v-if="controls.param_override.visible" v-model="basic.param_override" :models="models" :error="errors.param_override" :disabled="controls.param_override.disabled || saving" :reason="controls.param_override.reason" :header-override="basic.header_override" />
            <SSettingRow :label="t('accounts.conversion.title')" :description="t('accounts.conversion.hint')" :error="errors.conversion_loss_policy" data-testid="account-conversion-policy">
              <div class="w-full space-y-1 sm:w-64">
                <SSelect v-model="basic.conversion_loss_policy" :options="conversionOptions" :disabled="saving || !conversionUsed" data-testid="account-conversion-policy-select" />
                <SHint size="xs" data-testid="account-conversion-policy-hint">{{ conversionUsed ? conversionHint : t('accounts.protocols.conversionOff') }}</SHint>
              </div>
            </SSettingRow>
            <template v-if="showEnvironment && controls.environment.visible">
              <SHint v-if="controls.environment.reason" size="xs" class="py-3">{{ controls.environment.reason }}</SHint>
              <EnvironmentEditor v-model="basic.environment" layer="account" field="environment" :errors="errors" :disabled="controls.environment.disabled || saving" />
            </template>
          </SSettingsGroup>
        </div>

        <!-- ============================================================ plugin sections (account.editor.section) -->
        <div v-for="e in pluginSections" v-show="activeSection === pluginSection(slotKey(e))" :key="slotKey(e)" :data-editor-section="pluginSection(slotKey(e))" class="space-y-4">
          <PluginSlot v-if="editing" name="account.editor.section" :only="x => slotKey(x) === slotKey(e)" :props="{ account, accountType, readonly: !canEditAccount }" />
          <SHint v-else data-testid="plugin-section-after-save">{{ t('accounts.editorUi.pluginSectionAfterSave') }}</SHint>
        </div>
      </div>
    </div>

    <FetchedModelsModal v-model:open="fetchOpen" :models="fetched.models" :skipped="fetched.skipped" :existing="models" @apply="applyFetched" />

    <div class="modal-sticky-footer flex flex-wrap items-center justify-end gap-2">
      <SButton v-if="!editing" class="mr-auto" variant="ghost" icon="arrow-left" @click="emit('back')">{{ t('common.previous') }}</SButton>
      <SButton v-if="canTestAccount" class="mr-auto" icon="play" @click="emit('test', account!)">{{ t('accounts.testConnection') }}</SButton>
      <SHint v-if="editing && prefilled" inline size="xs" tone="warning" class="inline-flex flex-wrap items-center gap-x-2" data-testid="footer-prefilled">
        <SIcon name="info" class="h-3.5 w-3.5" />{{ t('accounts.editorUi.prefilledFooter') }}
        <SLink v-if="activeSection !== 'models'" as="button" class="text-xs" @click="activeSection = 'models'">{{ t('accounts.editorUi.prefilledView') }}</SLink>
        <SLink as="button" class="text-xs" @click="clearPrefill">{{ t('accounts.editorUi.clearModels') }}</SLink>
      </SHint>
      <SButton @click="emit('cancel')">{{ t('common.cancel') }}</SButton>
      <SButton type="submit" variant="primary" :loading="saving" data-testid="account-save">{{ editing ? t('common.save') : t('accounts.editorUi.create') }}</SButton>
    </div>
  </form>
</template>
