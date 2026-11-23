<script setup lang="ts">
import { computed, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SChoiceCards, SField, SGrid, SHint, SInput, SSelect, SSettingsGroup, STextarea, toast, useTabbedForm, type ChoiceOption, type FormTab, type TabbedFormPart } from '@sub2api/ui'
import type { Group } from '@/api/types'
import { useStatusOptions } from '@/composables/crud'
import { policyOptions } from '@/utils/conversion'
import TabbedFormModal from '@/components/TabbedFormModal.vue'
import ModelPriceTable from '@/components/pricing/ModelPriceTable.vue'
import RateInput from '@/components/pricing/RateInput.vue'
import { typedRate } from '@/components/pricing/pricingFormat'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { latestOnly } from '@/composables/useLatestRequest'
import EnvironmentEditor from '@/components/environment/EnvironmentEditor.vue'
import GroupModelPolicyEditor from './GroupModelPolicyEditor.vue'
import GroupSegmentsEditor from './GroupSegmentsEditor.vue'
import { GROUP_EDIT_TABS, TAB_FIELDS, blankForm, fieldChanged, fieldProblems, fieldsBody, formOf, withoutFields, type GroupEditTab } from './groupForm'

// Creates an account group (`group` null) or edits one, in tabs: what it is,
// its model list, its prices (base rate, and per model a price and a rate of
// its own), the rates of user groups in it, and the advanced settings. Every
// tab can be filled in at once, a new group's too: the tabs keep drafts, and
// Save sends them all in one request (useTabbedForm), so the group, its
// prices and its user group rates are saved together or not at all. A field
// that is wrong, here or as the server answers, marks its tab and opens it;
// a tab with unsaved changes is marked, and closing with one asks first
// (TabbedFormModal). The user group rates take segment:read: without it
// that tab is not shown. Accounts are put in groups on the accounts.
const props = defineProps<{ open: boolean; group: Group | null; initialTab?: GroupEditTab }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved'): void }>()
const { t } = useI18n()
const modelPolicyEditor = ref<InstanceType<typeof GroupModelPolicyEditor>>()
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

const errors = ref<Record<string, string>>({})
const form = reactive(blankForm())
/** The form as it was opened: what a changed field is compared with, and what PATCH leaves out. */
const opened = ref(blankForm())
/** The base rate the form has, once it is one: what the tables' paid prices and rates are at. */
const baseRate = computed(() => typedRate(form.rate_multiplier) ?? '')
/** Whether each model of the pricing tab is priced as drafted there, for the user group rates. */
const priced = shallowRef<ReadonlyMap<string, boolean>>()
const tab = ref<GroupEditTab>('basic')
const auth = useAuthStore()
const tabKeys = computed(() => GROUP_EDIT_TABS.filter((key) => key !== 'segments' || auth.has('segment:read')))
const tabbed = useTabbedForm(tab, tabKeys)

/** The part of the request a tab's own fields make. */
function fieldsPart(key: GroupEditTab): TabbedFormPart {
  const keys = TAB_FIELDS[key]
  return {
    tab: key,
    fields: keys,
    dirty: () => keys.some((k) => fieldChanged(form, opened.value, k)),
    check() {
      // Adds the model patterns still being typed; nothing there can be wrong.
      if (key === 'models') modelPolicyEditor.value?.commit()
      const problems = fieldProblems(form, keys, t)
      errors.value = { ...withoutFields(errors.value, keys), ...problems }
      return !Object.keys(problems).length
    },
    body: () => fieldsBody(form, opened.value, keys, !props.group),
    applyErrors: (e) => (errors.value = { ...errors.value, ...e })
  }
}
GROUP_EDIT_TABS.filter((key) => TAB_FIELDS[key].length).forEach((key) => tabbed.register(fieldsPart(key)))

const tabs = computed<FormTab[]>(() => tabKeys.value.map((key) => ({ key, label: t(`groups.tabs.${key}`) })))
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
    errors.value = {}
    tabbed.clearErrors()
    tab.value = props.initialTab && tabKeys.value.includes(props.initialTab) ? props.initialTab : 'basic'
  },
  { immediate: true }
)

// ------------------------------------------------------------------ save

/** One request: the group with its prices and rates (POST /groups, PATCH /groups/:id). */
const send = (body: Record<string, unknown>) => (props.group ? api.patch<Group>(`/groups/${props.group.id}`, body) : api.post<Group>('/groups', body))
function saved() {
  emit('saved')
  toast(t(props.group ? 'common.updated' : 'common.created'), 'success')
}
</script>

<template>
  <TabbedFormModal
    v-model:tab="tab"
    :open="open"
    :title="group ? t('groups.editTitle', { name: group.name }) : t('groups.create')"
    :tabs="tabs"
    :form="tabbed"
    :send="send"
    :submit-text="group ? t('common.save') : t('common.create')"
    testid="group-edit"
    min-height="min-h-[24rem]"
    @update:open="emit('update:open', $event)"
    @saved="saved"
  >
    <template #default="{ tab: key }">
      <div v-if="key === 'basic'" class="space-y-4">
        <SGrid>
          <SField :label="t('common.name')" required :error="errors.name">
            <SInput v-model="form.name" data-testid="group-name" />
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
      <SField v-else-if="key === 'models'" :label="t('groups.modelRules')" :hint="t('groups.allowlistHint')" :error="errors.model_allowlist || errors.model_filter_mode">
        <GroupModelPolicyEditor :key="group?.id ?? 'new'" ref="modelPolicyEditor" v-model="form.model_allowlist" v-model:mode="form.model_filter_mode" :options="modelSource.models" :loading="modelsLoading" :unrestricted-accounts="modelSource.unrestricted_accounts" />
        <SHint v-if="modelLoadError" tone="danger" size="xs">{{ modelLoadError }}</SHint>
      </SField>
      <div v-else-if="key === 'pricing'" class="space-y-4">
        <SField :label="t('groups.rateMultiplier')" required :hint="t('groups.multiplierHint')" :error="errors.rate_multiplier" class="rounded-xl border border-line p-4">
          <RateInput v-model="form.rate_multiplier" size="md" class="sm:w-48" data-testid="group-default-rate" />
        </SField>
        <ModelPriceTable :group-id="group?.id ?? null" :base-rate="baseRate" editable tab="pricing" @priced="priced = $event" />
      </div>
      <GroupSegmentsEditor
        v-else-if="key === 'segments'"
        :group-id="group?.id ?? null"
        :visibility="form.visibility"
        :default-rate="form.rate_multiplier"
        :priced="priced"
        editable
        :disabled="tabbed.saving.value"
        tab="segments"
      />
      <div v-else class="space-y-4">
        <SField :label="t('accounts.conversion.title')" :hint="t('groups.conversionHint')" :error="errors.conversion_loss_policy">
          <SSelect v-model="form.conversion_loss_policy" :options="conversionOptions" data-testid="group-conversion-policy" />
        </SField>
        <SSettingsGroup :title="t('settings.environment.title')" :description="t('settings.environment.groupHint')" data-testid="group-environment">
          <EnvironmentEditor v-model="form.environment" layer="group" field="environment" :errors="errors" :disabled="tabbed.saving.value" />
        </SSettingsGroup>
      </div>
    </template>
    <template #footer-start><SHint size="xs" data-testid="group-edit-save-hint">{{ t(group ? 'groups.saveHint' : 'groups.createHint') }}</SHint></template>
  </TabbedFormModal>
</template>
