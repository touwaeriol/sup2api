<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { SInput, SSegmented, SSettingRow, SSettingsGroup, SSpinner, type SelectOption } from '@sub2api/ui'
import type { BillingSettings } from '@/api/types'
import SettingsSaveBar from '@/components/SettingsSaveBar.vue'
import { useSettingsForm } from '@/composables/useSettingsForm'
import { useAuthStore } from '@/stores/auth'

// Billing settings (GET/PUT /settings/billing).
const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('settings:manage'))
const DECIMAL = /^-?\d+(\.\d{1,8})?$/
// What a request pays when its client hangs up mid-answer (CONTRACTS §25.8).
const DISCONNECT_POLICIES = ['continue', 'precharge', 'free'] as const
const disconnectOptions = computed<SelectOption[]>(() => DISCONNECT_POLICIES.map((p) => ({ value: p, label: t(`settings.billing.disconnect.${p}`) })))

type Form = Omit<BillingSettings, 'pre_consume_tokens'> & {
  pre_consume_tokens: number | null
}

const { form, errors, dirty, saving, loading, isLoaded, load, save, reset } = useSettingsForm<Form, BillingSettings>({
  path: '/settings/billing',
  defaults: {
    pre_consume_tokens: 500,
    min_balance: '0',
    big_cost_warning_usd: '10',
    client_disconnect_policy: 'continue'
  },
  fromServer: (r) => ({
    pre_consume_tokens: r.pre_consume_tokens ?? 500,
    min_balance: String(r.min_balance ?? '0'),
    big_cost_warning_usd: String(r.big_cost_warning_usd ?? '10'),
    client_disconnect_policy: DISCONNECT_POLICIES.find(p => p === r.client_disconnect_policy) ?? 'continue'
  }),
  toBody: (f, errs) => {
    const tokens = f.pre_consume_tokens ?? NaN
    const min = String(f.min_balance).trim()
    const warn = String(f.big_cost_warning_usd).trim()
    if (!Number.isInteger(tokens) || tokens < 0 || tokens > 100000000) errs.pre_consume_tokens = t('settings.billing.preConsumeInvalid')
    if (!DECIMAL.test(min)) errs.min_balance = t('settings.billing.decimalInvalid')
    if (!DECIMAL.test(warn) || Number(warn) < 0) errs.big_cost_warning_usd = t('settings.billing.decimalInvalid')
    return {
      pre_consume_tokens: tokens,
      min_balance: min,
      big_cost_warning_usd: warn,
      client_disconnect_policy: f.client_disconnect_policy
    }
  }
})

onMounted(load)
</script>

<template>
  <SSettingsGroup :title="t('settings.billing.title')" data-testid="billing-settings">
    <template v-if="canManage" #actions><SettingsSaveBar :dirty="dirty" :saving="saving" :can-manage="canManage" @save="save" @reset="reset" /></template>
    <SSpinner v-if="loading && !isLoaded" block />
    <template v-else>
      <SSettingRow :label="t('settings.billing.preConsumeTokens')" :description="t('settings.billing.preConsumeHint')" :error="errors.pre_consume_tokens">
        <SInput v-model="form.pre_consume_tokens" type="number" min="0" max="100000000" step="1" class="w-full sm:w-48" :disabled="!canManage" />
      </SSettingRow>
      <SSettingRow :label="t('settings.billing.disconnectPolicy')" :description="t(`settings.billing.disconnectHint.${form.client_disconnect_policy}`)">
        <SSegmented
          :model-value="form.client_disconnect_policy"
          :options="disconnectOptions"
          size="sm"
          :disabled="!canManage"
          :aria-label="t('settings.billing.disconnectPolicy')"
          data-testid="client-disconnect-policy"
          @update:model-value="form.client_disconnect_policy = $event as BillingSettings['client_disconnect_policy']"
        />
      </SSettingRow>
      <SSettingRow :label="t('settings.billing.minBalance')" :description="t('settings.billing.minBalanceHint')" :error="errors.min_balance">
        <SInput v-model="form.min_balance" mono inputmode="decimal" :suffix="t('common.usd')" class="w-full sm:w-48" :disabled="!canManage" />
      </SSettingRow>
      <SSettingRow :label="t('settings.billing.bigCost')" :description="t('settings.billing.bigCostHint')" :error="errors.big_cost_warning_usd">
        <SInput v-model="form.big_cost_warning_usd" mono inputmode="decimal" :suffix="t('common.usd')" class="w-full sm:w-48" :disabled="!canManage" />
      </SSettingRow>
    </template>
  </SSettingsGroup>
</template>
