<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SChoiceCards, SField, SFormModal, SGrid, SHint, SInput, SSelect, SSettingsGroup, STabs, STextarea, toast, type ChoiceOption, type TabItem } from '@sub2api/ui'
import type { ConversionLossPolicy, EnvironmentValues, Group } from '@/api/types'
import { useStatusOptions } from '@/composables/crud'
import { notifyError } from '@/utils/errors'
import { applyFormError } from '@/utils/formErrors'
import { policyOptions } from '@/utils/conversion'
import { markUnsaved, useTabbedSave, type SaveStep } from '@/composables/useTabbedSave'
import ModelPriceTable from '@/components/pricing/ModelPriceTable.vue'
import { typedRate, validRate } from '@/components/pricing/pricingFormat'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { latestOnly } from '@/composables/useLatestRequest'
import EnvironmentEditor from '@/components/environment/EnvironmentEditor.vue'
import GroupModelPolicyEditor from './GroupModelPolicyEditor.vue'
import { GROUP_EDIT_TABS, changedTabs, tabOfErrors, type GroupEditTab } from './groupEditTabs'

// Creates a group (`group` null) or edits one, in tabs. The form holds the
// group's own fields; its model prices are a sub-editor with drafts of its
// own, saved once the group itself is. The accounts of a group are set on the
// accounts, and the user groups that may use it at which rate on the user
// groups ("Group rates"), so neither is edited here. Prices can be set before
// the group exists: a new group has no accounts yet, so it provides no models
// and its price table lists only those added there. Drafts that cannot be
// saved stop Save before anything is sent; a step that fails keeps the dialog
// open on its tab; a tab with unsaved changes is marked, and closing with one
// asks first. Only the Save button submits: Enter in a rate or a search field
// of a tab must not save the whole group.
const props = defineProps<{ open: boolean; group: Group | null; initialTab?: GroupEditTab }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved'): void }>()
const { t } = useI18n()
const modelPolicyEditor = ref<InstanceType<typeof GroupModelPolicyEditor>>()
const pricingEditor = ref<InstanceType<typeof ModelPriceTable>>()
/** The models the accounts of a saved group declare, for its model policy; a new group has no accounts. */
interface ModelSource { models: string[]; unrestricted_accounts: number }
const modelSource = ref<ModelSource>({ models: [], unrestricted_accounts: 0 })
const modelsLoading = ref(false)
const modelLoadError = ref('')
const beginModels = latestOnly()
async function loadModels(gid: number | null) {
  const latest = beginModels()
  modelSource.value = { models: [], unrestricted_accounts: 0 }
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
/** The group rate the form has, once it is one: what the price table's paid prices are at. */
const paidRate = computed(() => typedRate(form.rate_multiplier) ?? '')
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
    tab.value = props.initialTab ?? 'basic'
  },
  { immediate: true }
)

// ------------------------------------------------------------------ tabs

const dirtyTabs = computed<string[]>(() => [...changedTabs(form, opened.value), ...(pricingEditor.value?.dirty ? ['pricing'] : [])])
const tabs = computed<TabItem[]>(() => markUnsaved(GROUP_EDIT_TABS.map((key) => ({ key, label: t(`groups.tabs.${key}`) })), (key) => dirtyTabs.value.includes(key)))

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
  if (!validRate(rm)) errors.value.rate_multiplier = t('pricing.rate.invalid')
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

/** Saves the drafted prices under the id the group has by then. */
async function savePrices(): Promise<boolean> {
  const gid = current.value?.id
  return gid === undefined || ((await pricingEditor.value?.save(gid)) ?? true)
}

/** What Save does, in order: the group first, then its model prices. */
const steps: SaveStep<GroupEditTab>[] = [
  // Adds the drafted model patterns; nothing there can fail.
  {
    tab: 'models',
    save: () => {
      modelPolicyEditor.value?.commit()
      return true
    }
  },
  // Drafted prices that cannot be saved stop it before anything is.
  { tab: 'pricing', save: () => pricingEditor.value?.check() ?? true },
  // A field error opens the tab of its field; any other failure was shown as a toast.
  { tab: () => tabOfErrors(errors.value) ?? tab.value, save: saveForm },
  { tab: 'pricing', save: savePrices }
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
      <!-- Every tab stays mounted: the price table keeps its drafts while another tab shows. The least height keeps the dialog from collapsing on a short tab. -->
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
        <div v-show="tab === 'pricing'" class="space-y-4">
          <SField :label="t('groups.rateMultiplier')" required :hint="t('groups.multiplierHint')" :error="errors.rate_multiplier" class="rounded-xl border border-line p-4">
            <SInput v-model="form.rate_multiplier" mono inputmode="decimal" class="sm:w-48" data-testid="group-default-rate">
              <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
            </SInput>
          </SField>
          <ModelPriceTable ref="pricingEditor" :group-id="group?.id ?? null" :rate="paidRate" editable />
        </div>
        <SField v-show="tab === 'models'" :label="t('groups.modelRules')" :hint="t('groups.allowlistHint')" :error="errors.model_allowlist || errors.model_filter_mode">
          <GroupModelPolicyEditor :key="group?.id ?? 'new'" ref="modelPolicyEditor" v-model="form.model_allowlist" v-model:mode="form.model_filter_mode" :options="modelSource.models" :loading="modelsLoading" :unrestricted-accounts="modelSource.unrestricted_accounts" />
          <SHint v-if="modelLoadError" tone="danger" size="xs">{{ modelLoadError }}</SHint>
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
