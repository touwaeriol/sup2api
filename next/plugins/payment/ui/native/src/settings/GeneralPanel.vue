<script setup lang="ts">
// General tab: online top-up switch, currency and price, limits, amount
// presets and discount tiers.
import { computed } from 'vue'
import { SInput, SSelect, SSettingRow, SSettingsGroup, SSwitch, STagInput } from '@sub2api/ui'
import { usePaymentHost } from '../host'
import DiscountEditor from './DiscountEditor.vue'
import { bindSwitch, CURRENCIES, currencySymbol, type SettingsForm } from './settingsForm'

const props = defineProps<{ form: SettingsForm }>()
const host = usePaymentHost()
const t = host.t

/** The numeric limits, one row each. */
const LIMITS = [
  { key: 'min_topup', unit: 'usd', step: 0.01 },
  { key: 'max_topup', unit: 'usd', step: 0.01 },
  { key: 'order_timeout_minutes', unit: 'minutes', step: 1 },
  { key: 'max_pending_orders', unit: 'orders', step: 1 }
] as const

const currency = computed(() => props.form.get<string>('currency') || 'CNY')
const currencyOptions = computed(() => CURRENCIES.map((c) => ({ value: c.code, label: `${t(`settings.currency.${c.code}`)} (${c.code})` })))

/** "Topping up $100 costs ¥730.00" under the price. */
const priceExample = computed(() => {
  const price = props.form.get<number>('price')
  if (!(price > 0)) return t('settings.general.price.help')
  const pay = currencySymbol(currency.value) + host.i18n.formatNumber(price * 100, 2)
  return t('settings.general.price.example', { pay })
})

/** Presets as chips: numbers only, ascending. */
const presets = computed(() => (props.form.get<number[]>('amount_options') ?? []).map(String))
function setPresets(list: string[]) {
  const nums = list.map(Number).filter((n) => Number.isFinite(n) && n > 0)
  props.form.set('amount_options', [...new Set(nums)].sort((a, b) => a - b))
}
</script>

<template>
  <SSettingsGroup :title="t('settings.general.recharge')">
    <SSettingRow prop="enabled" :label="t('settings.general.enabled.label')" :description="t('settings.general.enabled.help')">
      <SSwitch v-bind="bindSwitch(form, 'enabled')" data-testid="payment-enabled" />
    </SSettingRow>
    <SSettingRow prop="currency" :label="t('settings.general.currency.label')" :description="t('settings.general.currency.help')">
      <SSelect v-bind="form.bind('currency')" :options="currencyOptions" class="payment-control" data-testid="payment-currency" />
    </SSettingRow>
    <SSettingRow prop="price" :label="t('settings.general.price.label')" :description="priceExample">
      <span class="payment-price">
        <span class="payment-muted">{{ t('settings.general.price.prefix') }}</span>
        <SInput type="number" v-bind="form.bindNumber('price')" :min="0" :step="0.01" :suffix="currency" class="payment-number" data-testid="payment-price" />
      </span>
    </SSettingRow>
  </SSettingsGroup>

  <SSettingsGroup :title="t('settings.general.limits')">
    <SSettingRow v-for="f in LIMITS" :key="f.key" :prop="f.key" :label="t(`settings.general.${f.key}.label`)" :description="t(`settings.general.${f.key}.help`)">
      <SInput type="number" v-bind="form.bindNumber(f.key)" :min="0" :step="f.step" :suffix="t(`settings.unit.${f.unit}`)" class="payment-number" />
    </SSettingRow>
  </SSettingsGroup>

  <SSettingsGroup :title="t('settings.general.presets.title')">
    <SSettingRow prop="amount_options" stacked :label="t('settings.general.presets.label')" :description="t('settings.general.presets.help')">
      <STagInput :model-value="presets" :placeholder="t('settings.general.presets.placeholder')" data-testid="payment-presets" @update:model-value="setPresets" />
    </SSettingRow>
  </SSettingsGroup>

  <DiscountEditor :form="form" :currency="currency" />
</template>
