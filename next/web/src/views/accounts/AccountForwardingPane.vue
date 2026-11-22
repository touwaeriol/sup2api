<script setup lang="ts">
// The account editor's advanced section, request forwarding: header
// passthrough (CONTRACTS §81), header override (§108), parameter override
// (§84), the conversion loss policy (CONVERT-MATRIX §6.3: null inherits each
// group's, the hint shows what applies) and the account layer of the
// environment variables (§74.3, shown when the caller may read them).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint, SSelect, SSettingRow, SSettingsGroup } from '@sub2api/ui'
import EnvironmentEditor from '@/components/environment/EnvironmentEditor.vue'
import { useEnvironmentLayer, useGroupsWith } from '@/composables/lookups'
import { groupPolicies, policyOptions, policySourceHint } from '@/utils/conversion'
import HeaderPassthroughEditor from './controls/HeaderPassthroughEditor.vue'
import HeaderOverrideField from './headerOverride/HeaderOverrideField.vue'
import ParamOverrideField from './paramOverride/ParamOverrideField.vue'
import type { ControlViews } from './commonControls'
import type { AccountBasics } from './useAccountSave'

defineProps<{
  controls: ControlViews
  errors: Record<string, string>
  saving: boolean
  /** The account's models, for the parameter override's dry run. */
  models: string[]
  /** Whether a model is converted or a protocol adapted: else nothing can be lost and the policy is greyed. */
  conversionUsed: boolean
}>()
// The editor's basics, one reactive object edited in place (given with :basic).
const basic = defineModel<AccountBasics>('basic', { required: true })
const { t } = useI18n()
const showEnvironment = useEnvironmentLayer('account')
const groupList = useGroupsWith(() => basic.value.group_ids)
const conversionOptions = computed(() => policyOptions(t, t('accounts.conversion.inherit')))
const conversionHint = computed(() => policySourceHint(t, basic.value.conversion_loss_policy, groupPolicies(basic.value.group_ids, groupList.value)))
</script>

<template>
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
</template>
