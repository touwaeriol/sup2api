<script setup lang="ts">
// Discount tiers: from `amount` (balance) up, the user pays `rate` of the
// price; when several tiers apply the lowest rate wins. Edited in place.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SChip, SEmpty, SField, SInput, SRowAction, SSettingsGroup, STable, type TableColumn } from '@sub2api/ui'
import { formatCurrency, formatNumber } from '@/utils/format'
import { discountLabel } from '../format'
import { priceOf } from '../wallet/useTopupQuote'
import type { SettingsModel } from './settingsForm'
import type { TopupSettingsForm } from './useTopupSettings'

type Tier = SettingsModel['discounts'][number]

const props = defineProps<{ form: TopupSettingsForm; currency: string; exponent: number }>()
const { t } = useI18n()

const tiers = computed(() => props.form.get<Tier[]>('discounts') ?? [])
const columns = computed<TableColumn[]>(() => [
  { key: 'amount', label: t('topup.settings.discounts.amount'), width: '11rem', mobile: 'meta' },
  { key: 'rate', label: t('topup.settings.discounts.rate'), width: '9rem', mobile: 'meta' },
  { key: 'phrase', label: t('topup.settings.discounts.phrase'), nowrap: true, mobile: 'meta' },
  { key: 'example', label: t('topup.settings.discounts.example'), mobile: 'meta' },
  { key: 'actions', label: '', width: '4.5rem' }
])

const money = (n: number) => formatCurrency(n, props.currency)

/** What a top-up of exactly this tier's amount costs, before and after the discount. */
function example(tier: Tier) {
  const price = props.form.get<number>('price')
  if (!(tier.amount > 0 && tier.rate > 0 && price > 0)) return '—'
  const { original, pay } = priceOf(tier.amount, price, tier.rate, props.exponent)
  return t('topup.settings.discounts.exampleText', { amount: formatNumber(tier.amount, 2), pay: money(pay), original: money(original) })
}

/** A new tier above the highest one. */
function add() {
  const top = Math.max(0, ...tiers.value.map(d => d.amount || 0))
  props.form.set('discounts', [...tiers.value, { amount: top ? top * 2 : 100, rate: 0.95 }])
}

function remove(i: number) {
  props.form.set(
    'discounts',
    tiers.value.filter((_, j) => j !== i)
  )
}
</script>

<template>
  <SSettingsGroup :title="t('topup.settings.discounts.title')" :description="t('topup.settings.discounts.help')">
    <template #actions>
      <SButton size="sm" icon="plus" data-testid="payment-discount-add" @click="add">{{ t('topup.settings.discounts.add') }}</SButton>
    </template>
    <div class="py-3">
      <SEmpty v-if="!tiers.length" :text="t('topup.settings.discounts.empty')" icon="price" />
      <!-- row-key names no field: rows key by index (amounts change while typing) -->
      <STable v-else :columns="columns" :rows="tiers" row-key="__index" dense>
        <template #cell-amount="{ index }">
          <SField :prop="`discounts.${index}.amount`">
            <SInput
              type="number"
              size="sm"
              v-bind="form.bindNumber(`discounts.${index}.amount`)"
              :min="0"
              :step="1"
              :suffix="t('topup.settings.unit.usd')"
              :aria-label="t('topup.settings.discounts.amount')"
            />
          </SField>
        </template>
        <template #cell-rate="{ index }">
          <SField :prop="`discounts.${index}.rate`">
            <SInput
              type="number"
              size="sm"
              v-bind="form.bindNumber(`discounts.${index}.rate`)"
              :min="0"
              :max="1"
              :step="0.01"
              :aria-label="t('topup.settings.discounts.rate')"
            />
          </SField>
        </template>
        <template #cell-phrase="{ row }">
          <SChip v-if="discountLabel(row.rate)" size="xs" tone="success">{{ discountLabel(row.rate) }}</SChip>
          <span v-else class="text-[0.8125rem] text-fg-muted">—</span>
        </template>
        <template #cell-example="{ row }">
          <span class="text-[0.8125rem] text-fg-muted">{{ example(row) }}</span>
        </template>
        <template #cell-actions="{ index }">
          <SRowAction :label="t('common.delete')" tone="danger" @click="remove(index)" />
        </template>
      </STable>
    </div>
  </SSettingsGroup>
</template>
