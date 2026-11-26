<script setup lang="ts">
// General tab: online top-up switch, currency and price, limits, amount
// presets and discount tiers. The currencies offered are the server's.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SInput, SSelect, SSettingRow, SSettingsGroup, SSwitch, STagInput } from '@sub2api/ui'
import { formatCurrency } from '@/utils/format'
import type { CurrencyInfo } from '../topup'
import { priceOf } from '../wallet/useTopupQuote'
import DiscountEditor from './DiscountEditor.vue'
import type { TopupSettingsForm } from './useTopupSettings'

const props = defineProps<{ form: TopupSettingsForm; currencies: CurrencyInfo[] }>()
const { t, locale } = useI18n()

/** The numeric limits, one row each. */
const LIMITS = [
  { key: 'min_topup', unit: 'usd', step: 0.01 },
  { key: 'max_topup', unit: 'usd', step: 0.01 },
  { key: 'order_timeout_minutes', unit: 'minutes', step: 1 },
  { key: 'max_pending_orders', unit: 'orders', step: 1 }
] as const

const currency = computed(() => props.form.get<string>('currency'))
/** Decimals of the chosen currency, as the server rounds prices to. */
const exponent = computed(() => props.currencies.find(c => c.code === currency.value)?.exponent ?? 2)
/** "Chinese yuan (CNY)": the currency's name in the console language. */
const currencyOptions = computed(() => {
  const names = new Intl.DisplayNames(locale.value === 'zh' ? 'zh-CN' : 'en', { type: 'currency' })
  return props.currencies.map(c => ({ value: c.code, label: `${names.of(c.code) ?? c.code} (${c.code})` }))
})

/** "Topping up $100 costs ¥730.00" under the price. */
const priceExample = computed(() => {
  const price = props.form.get<number>('price')
  if (!(price > 0)) return t('topup.settings.general.price.help')
  return t('topup.settings.general.price.example', { pay: formatCurrency(priceOf(100, price, 1, exponent.value).pay, currency.value) })
})

/** Presets as chips: numbers only, ascending. */
const presets = computed(() => (props.form.get<number[]>('amount_options') ?? []).map(String))
function setPresets(list: string[]) {
  const nums = list.map(Number).filter(n => Number.isFinite(n) && n > 0)
  props.form.set(
    'amount_options',
    [...new Set(nums)].sort((a, b) => a - b)
  )
}
</script>

<template>
  <SSettingsGroup :title="t('topup.settings.general.recharge')">
    <SSettingRow prop="enabled" :label="t('topup.settings.general.enabled.label')" :description="t('topup.settings.general.enabled.help')">
      <SSwitch v-bind="form.bind('enabled')" data-testid="payment-enabled" />
    </SSettingRow>
    <SSettingRow
      prop="currency"
      :label="t('topup.settings.general.currency.label')"
      :description="t('topup.settings.general.currency.help')"
    >
      <SSelect v-bind="form.bind('currency')" :options="currencyOptions" class="w-full sm:w-60" data-testid="payment-currency" />
    </SSettingRow>
    <SSettingRow prop="price" :label="t('topup.settings.general.price.label')" :description="priceExample">
      <span class="flex items-center gap-2 whitespace-nowrap">
        <span class="text-[0.8125rem] text-fg-muted">{{ t('topup.settings.general.price.prefix') }}</span>
        <SInput
          type="number"
          v-bind="form.bindNumber('price')"
          :min="0"
          :step="0.01"
          :suffix="currency"
          class="min-w-0 flex-1 sm:w-44 sm:flex-none"
          data-testid="payment-price"
        />
      </span>
    </SSettingRow>
  </SSettingsGroup>

  <SSettingsGroup :title="t('topup.settings.general.limits')">
    <SSettingRow
      v-for="f in LIMITS"
      :key="f.key"
      :prop="f.key"
      :label="t(`topup.settings.general.${f.key}.label`)"
      :description="t(`topup.settings.general.${f.key}.help`)"
    >
      <SInput
        type="number"
        v-bind="form.bindNumber(f.key)"
        :min="0"
        :step="f.step"
        :suffix="t(`topup.settings.unit.${f.unit}`)"
        class="w-full sm:w-44"
      />
    </SSettingRow>
  </SSettingsGroup>

  <SSettingsGroup :title="t('topup.settings.general.presets.title')">
    <SSettingRow
      prop="amount_options"
      stacked
      :label="t('topup.settings.general.presets.label')"
      :description="t('topup.settings.general.presets.help')"
    >
      <STagInput
        :model-value="presets"
        :placeholder="t('topup.settings.general.presets.placeholder')"
        data-testid="payment-presets"
        @update:model-value="setPresets"
      />
    </SSettingRow>
  </SSettingsGroup>

  <DiscountEditor :form="form" :currency="currency" :exponent="exponent" />
</template>
