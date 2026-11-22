<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SHint, SInput, SRowAction, type SelectOption } from '@sub2api/ui'
import type { GroupModelPrices } from '@/api/pricing'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import { useLatestRequest } from '@/composables/useLatestRequest'
import AddSelect from './AddSelect.vue'
import type { DraftedModelRate } from './modelRateDrafts'
import { applyRule, rateText } from './pricingFormat'
import { priceLabel } from './ruleText'

// The model rates of a user group in one account group, under that group's
// row: each model added here is paid at its list price × its own rate; the
// others follow `rate`, the user group's rate there. Editable, "Add model"
// offers the account group's models with a global price that have no rate
// yet; a row reports what changes (`add` at `rate`, `change`, `remove`,
// `undo`) and the parent keeps the drafts. `errors` are the server's
// refusals by row key.
const props = defineProps<{ rows: DraftedModelRate[]; groupId: number; rate: string; editable?: boolean; disabled?: boolean; errors?: ReadonlyMap<string, string> }>()
const emit = defineEmits<{
  (e: 'add', model: string, rate: string): void
  (e: 'change', row: DraftedModelRate, rate: string): void
  (e: 'remove' | 'undo', row: DraftedModelRate): void
}>()
const { t } = useI18n()

const prices = useLatestRequest(() => (props.editable ? props.groupId : null), (gid) => api.get<GroupModelPrices>(`/groups/${gid}/model-prices`), 0)

const addable = computed<SelectOption[]>(() => {
  const taken = new Set(props.rows.map((r) => r.model))
  const models = prices.result.value?.models ?? []
  return models.filter((m) => m.base && !taken.has(m.model)).map((m) => ({ value: m.model, label: m.model, hint: priceLabel(t, applyRule(m.base, m.price)) }))
})
const add = (model: SelectOption['value']) => emit('add', String(model), props.rate)
</script>

<template>
  <div class="space-y-2" data-testid="segment-model-rates" :data-group="groupId">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <SHint inline size="xs">{{ t('segments.modelRates.intro', { rate: rateText(rate || null) }) }}</SHint>
      <AddSelect
        v-if="editable"
        :options="addable"
        :label="t('segments.modelRates.add')"
        :no-results="t('segments.modelRates.addNone')"
        :footer="t('segments.modelRates.addFooter')"
        :disabled="disabled || prices.loading.value"
        data-testid="segment-model-rate-add"
        @add="add"
      />
    </div>
    <SHint v-if="prices.error.value" size="xs" tone="danger">{{ prices.error.value }}</SHint>
    <SHint v-if="!rows.length" size="xs" data-testid="segment-model-rates-empty">{{ t('segments.modelRates.empty') }}</SHint>
    <ul v-else class="divide-y divide-line rounded-lg border border-line">
      <li v-for="row in rows" :key="row.key" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1.5" data-testid="segment-model-rate-row" :data-model="row.model">
        <span class="inline-flex min-w-0 flex-1 flex-wrap items-center gap-1.5" :class="row.removed ? 'line-through opacity-60' : ''">
          <ModelBadge :model="row.model" />
          <SBadge v-if="row.removed" tone="danger" size="xs">{{ t('segments.modelRates.removed') }}</SBadge>
          <SBadge v-else-if="errors?.has(row.key)" tone="danger" size="xs" :title="errors.get(row.key)" data-testid="segment-model-rate-refused">{{ errors.get(row.key) }}</SBadge>
          <SBadge v-else-if="row.dirty" :tone="row.invalid ? 'danger' : 'warning'" size="xs" data-testid="segment-model-rate-unsaved">{{ t('pricingAccess.unsaved') }}</SBadge>
        </span>
        <SInput
          v-if="editable"
          :model-value="row.rate"
          size="sm"
          mono
          inputmode="decimal"
          class="w-28"
          :error="row.invalid"
          :disabled="disabled || row.removed"
          :aria-label="t('segments.modelRates.rateOf', { model: row.model })"
          data-testid="segment-model-rate-input"
          @update:model-value="emit('change', row, String($event ?? '').trim())"
        >
          <template #prefix><span class="text-xs text-fg-subtle">×</span></template>
        </SInput>
        <span v-else class="font-mono text-sm" data-testid="segment-model-rate-value">{{ rateText(row.rate) }}</span>
        <template v-if="editable">
          <SRowAction v-if="row.removed" :label="t('segments.modelRates.undo')" :disabled="disabled" data-testid="segment-model-rate-undo" @click="emit('undo', row)" />
          <SRowAction v-else tone="danger" :label="t('common.remove')" :disabled="disabled" data-testid="segment-model-rate-remove" @click="emit('remove', row)" />
        </template>
      </li>
    </ul>
  </div>
</template>
