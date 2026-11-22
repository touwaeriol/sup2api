<script setup lang="ts">
// The account editor's "scheduling and limits" section: priority, weight,
// whether it is scheduled and auto-disabled (following the global switch),
// concurrency and rate limits, the usage limit (CONTRACTS §73) and the limits
// per model list (§68). Each row follows the type's common controls.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SInput, SSettingRow, SSettingsGroup, SSwitch } from '@sub2api/ui'
import type { Account } from '@/api/types'
import AccountUsageLimitCard from './AccountUsageLimitCard.vue'
import ModelLimitsEditor from './ModelLimitsEditor.vue'
import RateLimitEditor from './controls/RateLimitEditor.vue'
import type { ControlViews } from './commonControls'
import { LIMIT_ROWS, SCHEDULING_ROWS } from './editorErrors'
import type { ModelLimitCard } from './modelLimits'
import type { AccountBasics } from './useAccountSave'
import type { AutoDisablePolicy } from './useAutoDisablePolicy'
import type { UsageLimitEditor } from './useUsageLimitEdit'

const props = defineProps<{
  controls: ControlViews
  errors: Record<string, string>
  saving: boolean
  autoDisable: AutoDisablePolicy
  usageLimit: UsageLimitEditor
  account?: Account | null
  /** The models a model list can hold. */
  modelOptions: string[]
}>()
// The editor's basics, one reactive object edited in place (given with :basic).
const basic = defineModel<AccountBasics>('basic', { required: true })
const modelLimits = defineModel<ModelLimitCard[]>('modelLimits', { required: true })
const { t } = useI18n()
const { limit, status, loadError, errors: limitErrors } = props.usageLimit
const { enabled: autoDisableEnabled } = props.autoDisable

const anyVisible = (names: readonly (keyof ControlViews)[]) => names.some((n) => props.controls[n].visible)
/** Priority and weight rows of the scheduling group. */
const fields = computed(() =>
  [
    { key: 'priority' as const, label: t('accounts.priority'), hint: t('accounts.priorityHint'), min: 0, max: 1_000_000 },
    { key: 'weight' as const, label: t('accounts.weight'), hint: t('accounts.weightHint'), min: 1, max: 1000 }
  ].filter((f) => props.controls[f.key].visible)
)
const autoDisableHint = computed(() => {
  if (autoDisableEnabled.value === false) return t('accounts.editorUi.autoDisableGlobalOff')
  if (props.controls.auto_disable.reason) return props.controls.auto_disable.reason
  if (autoDisableEnabled.value === true) return t('accounts.editorUi.autoDisableHint')
  return props.autoDisable.failed.value ? t('accounts.editorUi.autoDisableUnavailable') : t('common.loading')
})
</script>

<template>
  <SSettingsGroup v-if="anyVisible(SCHEDULING_ROWS)" :title="t('accounts.scheduling')" :description="t('accounts.editorUi.schedulingCardHint')" data-testid="account-scheduling">
    <SSettingRow v-for="f in fields" :key="f.key" :label="f.label" :description="controls[f.key].reason || f.hint" :error="errors[f.key]">
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
    v-model="limit"
    :status="status"
    :load-error="loadError"
    :usage="account?.usage"
    :disabled="controls.usage_limit.disabled || saving"
    :reason="controls.usage_limit.reason"
    :errors="limitErrors"
    @retry="usageLimit.load"
  />

  <SSettingsGroup v-if="controls.model_limits.visible" :title="t('accounts.modelLimits.title')" :description="controls.model_limits.reason || t('accounts.modelLimits.hint')" data-testid="account-model-limits">
    <div class="p-4">
      <ModelLimitsEditor v-model="modelLimits" :options="modelOptions" :errors="errors" :disabled="controls.model_limits.disabled || saving" />
    </div>
  </SSettingsGroup>
</template>
