<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SChoiceCards, SField, SFormModal, SGrid, SHint, SInput, SSelect, SSettingsGroup, STabs, STextarea, toast, type ChoiceOption, type TabItem } from '@sub2api/ui'
import type { ConversionLossPolicy, EnvironmentValues, Group } from '@/api/types'
import { useStatusOptions } from '@/composables/crud'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { applyFormError } from '@/utils/formErrors'
import { policyOptions } from '@/utils/conversion'
import { ACCOUNT_ACTIONS } from '@/composables/useScope'
import { markUnsaved, useTabbedSave, type SaveStep } from '@/composables/useTabbedSave'
import type { DraftedSetting } from '@/components/pricing/accessRateDrafts'
import type { ModelOffer } from '@/components/pricing/offeredModels'
import { validRate } from '@/components/pricing/pricingFormat'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { latestOnly } from '@/composables/useLatestRequest'
import EnvironmentEditor from '@/components/environment/EnvironmentEditor.vue'
import GroupAccountsEditor from './GroupAccountsEditor.vue'
import GroupModelPolicyEditor from './GroupModelPolicyEditor.vue'
import GroupPricingEditor from './GroupPricingEditor.vue'
import GroupSegmentsEditor from './GroupSegmentsEditor.vue'
import { GROUP_EDIT_TABS, changedTabs, tabOfErrors, type GroupEditTab } from './groupEditTabs'

// Creates a group (`group` null) or edits one, in tabs. The form holds the
// group's own fields; its accounts, user groups and model prices are
// sub-editors with drafts of their own, saved in that order once the group
// itself is. Every tab can be filled before the group exists: the price table
// of a new group lists the models of the accounts picked, through the model
// policy being edited. Drafts that cannot be saved stop Save before anything
// is sent; a step that fails keeps the dialog open on its tab; a tab with
// unsaved changes is marked, and closing with one asks first. Only the Save
// button submits: Enter in a rate or a search field of a tab must not save
// the whole group.
const props = defineProps<{ open: boolean; group: Group | null; initialTab?: GroupEditTab }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved'): void }>()
const { t } = useI18n()
const auth = useAuthStore()
// Account membership is stored on accounts, so editing it needs account rights
// (all-level or own-level, CONTRACTS §21; the editor disables rows the caller cannot edit).
const canEditAccounts = computed(() => auth.hasAction(ACCOUNT_ACTIONS.read) && auth.hasAction(ACCOUNT_ACTIONS.update))
// A saved group answers with its own user groups (group:read); one not created yet has to list
// them all (GET /segments), which takes segment:read. Without it they are set once the group is saved.
const canSetSegments = computed(() => props.group !== null || auth.has('segment:read'))
const accountsEditor = ref<InstanceType<typeof GroupAccountsEditor>>()
const modelPolicyEditor = ref<InstanceType<typeof GroupModelPolicyEditor>>()
const segmentsEditor = ref<InstanceType<typeof GroupSegmentsEditor>>()
const pricingEditor = ref<InstanceType<typeof GroupPricingEditor>>()
interface ModelSource { models: string[]; unrestricted_accounts: number }
const modelSource = ref<ModelSource>({ models: [], unrestricted_accounts: 0 })
const selectedModelSource = ref<ModelSource | null>(null)
const modelsLoading = ref(false)
const modelLoadError = ref('')
const beginModels = latestOnly()
const currentModelSource = computed(() => canEditAccounts.value && auth.has('account:read') && selectedModelSource.value ? selectedModelSource.value : {
  models: [...new Set([...modelSource.value.models, ...(selectedModelSource.value?.models || [])])].sort(),
  unrestricted_accounts: modelSource.value.unrestricted_accounts
})
async function loadModels(gid: number | null) {
  const latest = beginModels()
  modelSource.value = { models: [], unrestricted_accounts: 0 }
  selectedModelSource.value = null
  modelLoadError.value = ''
  modelsLoading.value = !!gid
  if (!gid) return
  try {
    const result = await api.get<ModelSource>(`/groups/${gid}/models`)
    if (latest()) modelSource.value = result
  } catch (e) {
    if (latest()) modelLoadError.value = t('groups.modelsLoadFailed')
    notifyError(e)
  } finally { if (latest()) modelsLoading.value = false }
}

// ------------------------------------------------------------------ form

interface GroupForm {
  name: string
  description: string
  status: string
  rate_multiplier: string
  visibility: Group['visibility']
  model_allowlist: string[]
  model_filter_mode: 'whitelist' | 'blacklist'
  environment: EnvironmentValues
  conversion_loss_policy: ConversionLossPolicy | null
}
const blankForm = (): GroupForm => ({ name: '', description: '', status: 'active', rate_multiplier: '1', visibility: 'public', model_allowlist: [], model_filter_mode: 'blacklist', environment: {}, conversion_loss_policy: null })
const formOf = (g: Group): GroupForm => ({
  name: g.name,
  description: g.description || '',
  status: g.status,
  rate_multiplier: String(g.rate_multiplier ?? '1'),
  visibility: g.visibility,
  model_allowlist: [...(g.model_allowlist || [])],
  model_filter_mode: g.model_filter_mode || 'whitelist',
  environment: { ...g.environment },
  conversion_loss_policy: g.conversion_loss_policy ?? null
})

const errors = ref<Record<string, string>>({})
const form = reactive(blankForm())
/** The form as it was opened or last saved: what a changed tab is compared with. */
const opened = ref(blankForm())
/** The group a first save created: saving again after a later step failed must not create another. */
const created = ref<Group | null>(null)
const current = computed(() => created.value ?? props.group)
/** The rows of the user groups editor, for the paid prices of the price tab. */
const segmentRows = ref<DraftedSetting[]>([])
const tab = ref<GroupEditTab>('basic')

const visibilityOptions = computed<ChoiceOption[]>(() => [
  { value: 'public', icon: 'globe', label: t('groups.public'), description: t('groups.publicHint') },
  { value: 'restricted', icon: 'lock', label: t('groups.restricted'), description: t('groups.restrictedHint') }
])
const statusOptions = useStatusOptions()
// Conversion loss policy (CONVERT-MATRIX §6.3): null leaves it to the accounts, effectively allow.
const conversionOptions = computed(() => policyOptions(t, t('groups.conversionDefault')))

watch(
  () => props.open,
  (v) => {
    if (!v) return
    loadModels(props.group?.id ?? null)
    Object.assign(form, props.group ? formOf(props.group) : blankForm())
    opened.value = JSON.parse(JSON.stringify(form))
    created.value = null
    errors.value = {}
    segmentRows.value = []
    tab.value = props.initialTab ?? 'basic'
  },
  { immediate: true }
)

// ------------------------------------------------------------------ tabs

const dirtyTabs = computed<string[]>(() => {
  const editors: Partial<Record<GroupEditTab, boolean>> = { accounts: accountsEditor.value?.dirty, segments: segmentsEditor.value?.dirty, pricing: pricingEditor.value?.dirty }
  return [...changedTabs(form, opened.value), ...GROUP_EDIT_TABS.filter((key) => editors[key])]
})
const tabs = computed<TabItem[]>(() => {
  const hidden: Partial<Record<GroupEditTab, boolean>> = { accounts: !canEditAccounts.value, segments: !canSetSegments.value }
  const shown = GROUP_EDIT_TABS.filter((key) => !hidden[key])
  return markUnsaved(shown.map((key) => ({ key, label: t(`groups.tabs.${key}`) })), (key) => dirtyTabs.value.includes(key))
})

/** What a group not saved yet would provide, for its price table; a saved group's table lists what the server says. */
const offer = computed<ModelOffer | null>(() => (props.group ? null : { ...currentModelSource.value, model_allowlist: form.model_allowlist, model_filter_mode: form.model_filter_mode }))

// ------------------------------------------------------------------ save

const { saving, run } = useTabbedSave(tab)
/** The group itself was saved by the current attempt, whatever its later steps do. */
let groupSaved = false

async function saveGroup(body: Record<string, unknown>) {
  if (current.value) await api.patch(`/groups/${current.value.id}`, body)
  else created.value = (await api.post<Group>('/groups', body)) ?? null
}

/** Checks and sends the group's own fields. False when one is wrong: the error is next to it. */
async function saveForm(): Promise<boolean> {
  errors.value = {}
  if (!form.name.trim()) errors.value.name = t('common.required')
  const rm = String(form.rate_multiplier).trim()
  if (!validRate(rm)) errors.value.rate_multiplier = t('groups.multiplierInvalid')
  if (Object.keys(errors.value).length) return false
  // What is sent, not what the fields hold when the answer comes: a change made meanwhile stays unsaved.
  const sent: GroupForm = JSON.parse(JSON.stringify(form))
  const body = {
    name: form.name.trim(),
    description: form.description.trim(),
    status: form.status,
    // Decimal as a string, like money (docs/CONTRACTS.md §3.2).
    rate_multiplier: rm,
    visibility: form.visibility,
    model_allowlist: form.model_allowlist,
    model_filter_mode: form.model_filter_mode,
    environment: form.environment,
    conversion_loss_policy: form.conversion_loss_policy
  }
  try {
    await saveGroup(body)
  } catch (e) {
    applyFormError(e, errors)
    return false
  }
  opened.value = sent
  groupSaved = true
  return true
}

async function saveAccounts(gid: number): Promise<boolean> {
  const failed = (await accountsEditor.value?.save(gid)) ?? []
  if (failed.length) toast(t('groups.accounts.saveFailed', { names: failed.join(', ') }), 'error')
  return !failed.length
}
/** A sub-editor's step: it saves under the id the group has by then. */
const onGroup = (save: (gid: number) => Promise<boolean>) => async () => (current.value ? save(current.value.id) : true)

/** What Save does, in order: the group first, and its accounts before the prices of the models they provide. */
const steps: SaveStep<GroupEditTab>[] = [
  // Adds the drafted model patterns; nothing there can fail.
  {
    tab: 'models',
    save: () => {
      modelPolicyEditor.value?.commit()
      return true
    }
  },
  // Drafts that cannot be saved stop it before anything is.
  { tab: 'segments', save: () => segmentsEditor.value?.check() ?? true },
  { tab: 'pricing', save: () => pricingEditor.value?.check() ?? true },
  // A field error opens the tab of its field; any other failure was shown as a toast.
  { tab: () => tabOfErrors(errors.value) ?? tab.value, save: saveForm },
  { tab: 'accounts', save: onGroup(saveAccounts) },
  { tab: 'segments', save: onGroup(async (gid) => (await segmentsEditor.value?.save(gid)) ?? true) },
  { tab: 'pricing', save: onGroup(async (gid) => (await pricingEditor.value?.save(gid)) ?? true) }
]

async function submit() {
  groupSaved = false
  const done = await run(steps)
  // The list shows the group as saved even when a later step keeps the dialog open.
  if (groupSaved) emit('saved')
  if (!done) return
  toast(t(props.group ? 'common.updated' : 'common.created'), 'success')
  emit('update:open', false)
}

const leave = async () => (await confirmDiscard(dirtyTabs.value.length > 0)) && emit('update:open', false)
</script>

<template>
  <SFormModal
    :open="open"
    :title="current ? t('groups.editTitle', { name: current.name }) : t('groups.create')"
    width="2xl"
    :loading="saving"
    :submit-text="current ? t('common.save') : t('common.create')"
    :submit-attrs="{ 'data-testid': 'group-edit-submit' }"
    :enter-submit="false"
    data-testid="group-edit"
    @update:open="leave"
    @submit="submit"
  >
    <STabs v-if="open" v-model="tab" :tabs="tabs" :aria-label="t('groups.title')" data-testid="group-edit-tabs">
      <!-- Every tab stays mounted: the sub-editors keep their drafts while another tab shows. The least height keeps the dialog from collapsing on a short tab. -->
      <div class="min-h-[24rem]">
        <div v-show="tab === 'basic'" class="space-y-4">
          <SGrid>
            <SField :label="t('common.name')" required :error="errors.name">
              <SInput v-model="form.name" />
            </SField>
            <SField :label="t('common.status')" :error="errors.status">
              <SSelect v-model="form.status" :options="statusOptions" />
            </SField>
          </SGrid>
          <SField :label="t('common.description')" :error="errors.description">
            <STextarea v-model="form.description" :rows="2" />
          </SField>
          <SField :label="t('groups.visibility')" :error="errors.visibility">
            <SChoiceCards v-model="form.visibility" :options="visibilityOptions" data-testid="group-visibility" />
          </SField>
        </div>
        <GroupSegmentsEditor
          v-if="canSetSegments"
          v-show="tab === 'segments'"
          ref="segmentsEditor"
          :group-id="group?.id ?? null"
          :visibility="form.visibility"
          :default-rate="form.rate_multiplier"
          editable
          :disabled="saving"
          @rows="segmentRows = $event"
        />
        <div v-show="tab === 'pricing'" class="space-y-4">
          <SField :label="t('groups.rateMultiplier')" required :hint="t('groups.multiplierHint')" :error="errors.rate_multiplier" class="rounded-xl border border-line p-4">
            <SInput v-model="form.rate_multiplier" mono inputmode="decimal" class="sm:w-48" data-testid="group-default-rate">
              <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
            </SInput>
          </SField>
          <GroupPricingEditor ref="pricingEditor" :group-id="group?.id ?? null" :offer="offer" :segments="segmentRows" :default-rate="form.rate_multiplier" editable />
        </div>
        <SField v-show="tab === 'models'" :label="t('groups.modelRules')" :hint="t('groups.allowlistHint')" :error="errors.model_allowlist || errors.model_filter_mode">
          <GroupModelPolicyEditor :key="group?.id ?? 'new'" ref="modelPolicyEditor" v-model="form.model_allowlist" v-model:mode="form.model_filter_mode" :options="currentModelSource.models" :loading="modelsLoading" :unrestricted-accounts="currentModelSource.unrestricted_accounts" />
          <SHint v-if="modelLoadError" tone="danger" size="xs">{{ modelLoadError }}</SHint>
        </SField>
        <SField v-if="canEditAccounts" v-show="tab === 'accounts'" :label="t('groups.accounts.title')" :hint="t('groups.accounts.hint')">
          <GroupAccountsEditor ref="accountsEditor" :group-id="group?.id ?? null" @models="selectedModelSource = $event" />
        </SField>
        <div v-show="tab === 'advanced'" class="space-y-4">
          <SField :label="t('accounts.conversion.title')" :hint="t('groups.conversionHint')" :error="errors.conversion_loss_policy">
            <SSelect v-model="form.conversion_loss_policy" :options="conversionOptions" data-testid="group-conversion-policy" />
          </SField>
          <SSettingsGroup :title="t('settings.environment.title')" :description="t('settings.environment.groupHint')" data-testid="group-environment">
            <EnvironmentEditor v-model="form.environment" layer="group" field="environment" :errors="errors" :disabled="saving" />
          </SSettingsGroup>
        </div>
      </div>
    </STabs>
    <template #footer-start><SHint size="xs" data-testid="group-edit-save-hint">{{ t(current ? 'groups.saveHint' : 'groups.createHint') }}</SHint></template>
  </SFormModal>
</template>
