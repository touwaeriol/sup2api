<script setup lang="ts">
// Step 2 of "new account" and the edit form (wireframe A.4, CONTRACTS §18.4),
// a tabbed form with the sections on the side (STabbedForm layout "side"):
// connection (basic info, proxy, plugin credentials) / protocols and models
// (own protocols, model list, mapping, protocols per model:
// DESIGN-2026-11-17-PROTOCOL-BINDING §5.1) / scheduling and limits / advanced,
// then the plugin sections declared for the type (account.editor.section).
// Every section keeps its edits until Save sends them in one request
// (useAccountSave); a section with a problem is marked and opened. A new
// account starts from the plugin's default models and mapping (§41); so does
// an existing account saved without either (see shouldPrefillOnEdit). Every
// core field follows the account type's common controls (hidden, fixed, a
// default, a required proxy: commonControls.ts).
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SCard, SField, SHint, SIcon, SInput, SLink, STabbedForm, toast, useTabbedForm } from '@sub2api/ui'
import type { Account, AccountType, ConversionLossPolicy, EnvironmentValues } from '@/api/types'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { slotKey } from '@/components/plugin/slots'
import GroupPicker from '@/components/GroupPicker.vue'
import { ACCOUNT_ACTIONS, useScope } from '@/composables/useScope'
import { useAccountCaps } from '@/composables/useAccountCaps'
import { useAbortScope } from '@/composables/useAbortScope'
import { fieldErrors, notifyError } from '@/utils/errors'
import type { Override as ParamOverride } from './paramOverride/model'
import type { HeaderOverride } from './headerOverride/model'
import AccountCredentialsCard from './AccountCredentialsCard.vue'
import AccountForwardingPane from './AccountForwardingPane.vue'
import AccountModelsCard from './AccountModelsCard.vue'
import AccountProxyField from './AccountProxyField.vue'
import AccountSchedulingPane from './AccountSchedulingPane.vue'
import AccountTypeCard from './AccountTypeCard.vue'
import FetchedModelsModal from './FetchedModelsModal.vue'
import { sameCreationGroup } from './accountTypeChoices'
import { accountControl, newAccountValues, sameControlValue, savedAccountValues, useCommonControls, type CommonControlName, type ControlValues } from './commonControls'
import { useUsageLimitEdit } from './useUsageLimitEdit'
import { toCards, type ModelLimitCard } from './modelLimits'
import { SPM_WINDOW_DEFAULT } from './spmWindow'
import { newAccountConcurrency } from './accountLimits'
import { shouldPrefillOnEdit } from './modelDefaults'
import { useModelEdit } from './useModelEdit'
import { useAccountSave } from './useAccountSave'
import { useAutoDisablePolicy } from './useAutoDisablePolicy'
import { useEditorSections } from './useEditorSections'
import { useProxyChoice } from './useProxyChoice'
import { pluginSection, protocolsErrorTexts, splitFieldErrors } from './editorErrors'
import ModelProtocolsCard from './protocols/ModelProtocolsCard.vue'
import OwnProtocolsField from './protocols/OwnProtocolsField.vue'
import { sameProtocolColumns } from './protocols/protocolBinding'
import { useProtocolLabels } from './protocols/protocolLabels'
import { useProtocolEdit } from './protocols/useProtocolEdit'

const props = defineProps<{ accountType: AccountType | null; accountTypeOptions?: AccountType[]; account?: Account | null }>()
const emit = defineEmits<{ (e: 'change-type', at: AccountType): void; (e: 'saved', a: Account): void; (e: 'cancel'): void; (e: 'back'): void; (e: 'test', a: Account): void }>()
const { t } = useI18n()
const scope = useScope()

const editing = computed(() => !!props.account?.id)
const caps = useAccountCaps(editing)

// Row-level rights (CONTRACTS §21): editing an account needs a scope of
// account:update that reaches it.
const canEditAccount = computed(() => editing.value && scope.can(props.account, ACCOUNT_ACTIONS.update))
const canTestAccount = computed(() => editing.value && scope.can(props.account, ACCOUNT_ACTIONS.test))

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

const credentialsCard = ref<InstanceType<typeof AccountCredentialsCard>>()

/** Hidden, fixed (with the reason) or required, per core field (docs/COMMON-CONTROLS.md §2). */
const controls = useCommonControls(() => props.accountType, () => props.account)
const autoDisable = useAutoDisablePolicy(() => [props.account, props.accountType])

// ---------------------------------------------------------------- sections

const tab = ref('connection')
// Usage limit (CONTRACTS §73): loaded when the scheduling section is shown, saved after the account.
const usageLimit = useUsageLimitEdit(() => props.account?.id, () => tab.value === 'scheduling')
// The editor and each section start at the top: the modal body keeps the
// previous scroll offset otherwise (the type picker's, the last section's).
// The form's scroll-mt-4 keeps the body's top padding in view. Post-flush, so
// save() still scrolls to an invalid field.
const formEl = ref<HTMLFormElement>()
const toTop = () => formEl.value?.scrollIntoView({ block: 'start' })
onMounted(toTop)
watch(tab, toTop, { flush: 'post' })

const editorModels = useModelEdit(() => props.accountType)
const { models, mapping, prefilled, defaultModels, modelsTextMode, syncModelsText, clearPrefill, modelOptions } = editorModels
/** Models a model list can hold: those the account serves, or every known model when it serves all. */
const modelLimitOptions = computed(() => (models.value.length ? models.value : [...new Set([...defaultModels.value, ...modelOptions.value])]))
const mergeModels = (ids: string[]) => editorModels.mergeModels(ids)

// Protocol binding (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1): the own protocols the account opens, the protocols of chosen models.
const protocolEdit = useProtocolEdit(() => props.accountType, () => models.value)
const protocolLabels = useProtocolLabels()
const ownProtocolsTitle = computed(() => {
  const names = protocolLabels.platformsName(props.accountType?.platforms ?? [])
  return names ? t('accounts.protocols.own.titleOf', { platforms: names }) : t('accounts.protocols.own.title')
})
const serverProtocolErrors = computed(() => protocolsErrorTexts(errors.value, protocolEdit.protocols.value))
/** Without a model converted or a protocol adapted nothing can be lost: the policy is greyed (an account whose type is gone keeps it editable). */
const conversionUsed = computed(() => !props.accountType || protocolEdit.conversionUsed.value)

const { tabs } = useEditorSections({
  basic, controls, accountType: () => props.accountType, editing, models: editorModels, protocols: protocolEdit, modelLimits: modelLimitCards, usageLimitSummary: usageLimit.summary
})
const tabbed = useTabbedForm(tab, () => tabs.value.map(s => s.key))
const saving = tabbed.saving

// ---------------------------------------------------------------- proxy (CONTRACTS §21.4)

const proxy = useProxyChoice({ basic, errors, canPasteUrl: caps.proxyByURL, accountType: () => props.accountType })

// ---------------------------------------------------------------- fetch models from the upstream (CONTRACTS §19)

const fetchOpen = ref(false)
const fetching = ref(false)
/** The model fetch in flight: no time limit, the user stops it (or closes the editor). */
const fetchRequests = useAbortScope()
const fetched = ref<{ models: string[]; skipped: number }>({ models: [], skipped: 0 })
const canFetch = computed(() => !!props.accountType && !props.account?.orphaned && (editing.value ? canTestAccount.value : caps.scope.value !== null && caps.creatable(props.accountType)))

/** Server field errors of the credentials: on the plugin form (AccountCredentialsCard). */
const showCredentialErrors = (cred: Record<string, string>) => (credErrors.value = cred)

/** Credential problems of a failed fetch land on the plugin form, like a failed save's. */
function showFetchError(e: unknown) {
  const fe = fieldErrors(e)
  if (Object.keys(fe).length) {
    const { cred } = splitFieldErrors(fe)
    showCredentialErrors(cred)
    if (Object.keys(cred).length || fe.proxy_url) tab.value = 'connection'
    if (fe.proxy_url) errors.value = { ...errors.value, proxy_url: fe.proxy_url }
  }
  notifyError(e)
}

async function fetchModels() {
  const at = props.accountType
  if (!at) return
  const creds = await collectCredentials()
  if (!creds) {
    tab.value = 'connection'
    return
  }
  fetching.value = true
  await fetchRequests.run(async ({ signal }) => {
    try {
      const r = editing.value
        ? await api.post<{ models: string[]; skipped: number }>(`/accounts/${props.account!.id}/models/fetch`, { credentials: creds }, { signal })
        : await api.post<{ models: string[]; skipped: number }>(`/account-types/${encodeURIComponent(at.plugin_key)}/${encodeURIComponent(at.type)}/models/fetch`, { credentials: creds, ...proxy.choice() }, { signal })
      fetched.value = { models: r.models || [], skipped: r.skipped || 0 }
      if (fetched.value.models.length) fetchOpen.value = true
      else toast(t('accounts.fetchEmpty'), 'warning')
    } catch (e) {
      if (!signal.aborted) showFetchError(e)
    } finally {
      fetching.value = false
    }
  })
}

/** Merges the ticked models into the list (existing entries are kept). */
function applyFetched(picked: string[]) {
  if (modelsTextMode.value && !syncModelsText()) return
  mergeModels(picked)
  fetchOpen.value = false
  toast(t('accounts.fetchApplied', { n: models.value.length }), 'success')
}

// ---------------------------------------------------------------- load

/** Fills the form from the account (or a new one) and the account type. */
function fillForm(a: Account | null | undefined, at: AccountType | null) {
  basic.name = a?.name || ''
  basic.group_ids = [...(a?.group_ids || [])]
  basic.proxy_id = a?.proxy_id ?? null
  proxy.reset()
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
    tab.value = 'connection'
    errors.value = {}
    credErrors.value = {}
    tabbed.clearErrors()
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

/** The credentials to save, as the plugin's form gives them (the card stays mounted while its section is hidden). */
const collectCredentials = async () => (credentialsCard.value ? credentialsCard.value.collect() : credentials.value)

const { save } = useAccountSave({
  form: tabbed,
  account: () => props.account,
  accountType: () => props.accountType,
  basic,
  modelLimits: modelLimitCards,
  errors,
  credErrors,
  models: editorModels,
  protocols: protocolEdit,
  autoDisable,
  proxyUrlToSend: () => proxy.urlToSend.value,
  collectCredentials,
  showCredentialErrors,
  usageLimit,
  saved: (a) => emit('saved', a)
})
</script>

<template>
  <form ref="formEl" class="flex min-h-full scroll-mt-4 flex-col gap-5" novalidate @submit.prevent="save">
    <STabbedForm v-model="tab" :tabs="tabs" :form="tabbed" layout="side" id-prefix="account-editor" :aria-label="t('accounts.editorUi.navigation')" data-testid="account-editor-tabs">
      <template v-if="accountType" #before-nav>
        <AccountTypeCard :account-type="accountType" />
      </template>
      <template #default="{ tab: key }">
        <!-- ============================================================ connection -->
        <template v-if="key === 'connection'">
          <SCard :title="t('accounts.basic')" :subtitle="t('accounts.editorUi.basicHint')">
            <div class="grid gap-4 md:grid-cols-2">
              <SField :label="t('common.name')" :error="errors.name" required>
                <SInput v-model="basic.name" :maxlength="100" :placeholder="t('accounts.editorUi.namePlaceholder')" />
              </SField>
              <SField :label="t('accounts.groups')" :error="errors.group_ids" :hint="!caps.bindGroups.value ? t('accounts.groupsLocked') : basic.group_ids.length ? '' : t('accounts.editorUi.groupsHint')">
                <GroupPicker :model-value="basic.group_ids" multiple :disabled="!caps.bindGroups.value" @update:model-value="basic.group_ids = $event as number[]" />
              </SField>
              <AccountProxyField v-model="basic.proxy_id" :proxy="proxy" :required="controls.proxy.required" class="md:col-span-2" />
            </div>
          </SCard>

          <AccountCredentialsCard
            ref="credentialsCard"
            v-model="credentials"
            :account-type="accountType"
            :account-type-options="accountTypeOptions"
            :account="account"
            :errors="credErrors"
            :saving="saving"
            :proxy-ticket="proxy.ticket"
            @change-type="emit('change-type', $event)"
          />
        </template>

        <!-- ============================================================ models -->
        <template v-else-if="key === 'models'">
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
            @stop-fetch="fetchRequests.stopAll"
          />
          <ModelProtocolsCard v-if="accountType" :editor="protocolEdit" :models="models" :mapping="mapping" :options="modelLimitOptions" :errors="errors" :disabled="saving" />
        </template>

        <!-- ============================================================ scheduling and limits -->
        <AccountSchedulingPane
          v-else-if="key === 'scheduling'"
          v-model:model-limits="modelLimitCards"
          :basic="basic"
          :controls="controls"
          :errors="errors"
          :saving="saving"
          :auto-disable="autoDisable"
          :usage-limit="usageLimit"
          :account="account"
          :model-options="modelLimitOptions"
        />

        <!-- ============================================================ advanced -->
        <AccountForwardingPane v-else-if="key === 'advanced'" :basic="basic" :controls="controls" :errors="errors" :saving="saving" :models="models" :conversion-used="conversionUsed" />

        <!-- ============================================================ plugin sections (account.editor.section) -->
        <template v-else>
          <PluginSlot v-if="editing" name="account.editor.section" :only="x => pluginSection(slotKey(x)) === key" :props="{ account, accountType, readonly: !canEditAccount }" />
          <SHint v-else data-testid="plugin-section-after-save">{{ t('accounts.editorUi.pluginSectionAfterSave') }}</SHint>
        </template>
      </template>
    </STabbedForm>

    <FetchedModelsModal v-model:open="fetchOpen" :models="fetched.models" :skipped="fetched.skipped" :existing="models" @apply="applyFetched" />

    <div class="modal-sticky-footer flex flex-wrap items-center justify-end gap-2">
      <SButton v-if="!editing" class="mr-auto" variant="ghost" icon="arrow-left" @click="emit('back')">{{ t('common.previous') }}</SButton>
      <SButton v-if="canTestAccount" class="mr-auto" icon="play" @click="emit('test', account!)">{{ t('accounts.testConnection') }}</SButton>
      <SHint v-if="editing && prefilled" inline size="xs" tone="warning" class="inline-flex flex-wrap items-center gap-x-2" data-testid="footer-prefilled">
        <SIcon name="info" class="h-3.5 w-3.5" />{{ t('accounts.editorUi.prefilledFooter') }}
        <SLink v-if="tab !== 'models'" as="button" class="text-xs" @click="tab = 'models'">{{ t('accounts.editorUi.prefilledView') }}</SLink>
        <SLink as="button" class="text-xs" @click="clearPrefill">{{ t('accounts.editorUi.clearModels') }}</SLink>
      </SHint>
      <SButton @click="emit('cancel')">{{ t('common.cancel') }}</SButton>
      <SButton type="submit" variant="primary" :loading="saving" data-testid="account-save">{{ editing ? t('common.save') : t('accounts.editorUi.create') }}</SButton>
    </div>
  </form>
</template>
