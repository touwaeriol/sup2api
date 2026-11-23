<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint, SRowAction, type SelectOption } from '@sub2api/ui'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import AddSelect from './AddSelect.vue'
import DraftBadge from './DraftBadge.vue'
import { addModelOptions } from './addModelOptions'
import { fetchCatalog, listPriceIn, useGroupModels, type CatalogFetch, type GroupModels } from './groupModels'
import RateInput from './RateInput.vue'
import type { DraftedModelRate } from './modelRateDrafts'
import { effectiveRate, listPriceOf, rateText } from './pricingFormat'

// The model rates of a user group in one account group, under the row of
// the pair (in the user group's editor, or the account group's): each model
// added here is paid at its list price × its own rate; the others pay the
// rate that applies to each: `override`, the user group's rate there, else
// the account group's model rate, else `groupRate`, its base rate.
// Editable, "Add model" offers the models without a rate yet
// (addModelOptions: those the account group's accounts offer, then those
// with a global price), from its saved price table and the global prices:
// the parent's `models` when it has them for every row (null while they
// load), else asked here (the global prices by `catalog`: an editor of
// several rows passes one that asks them once); any other model name may be
// typed. A model with no price there says so: its requests are refused until
// it has one, and its rate applies from then on. `priced` (by model)
// overrides what the saved table says with what the account group's pricing
// tab has drafted. A row reports
// what changes (`add` at the rate the model pays now, `change`, `remove`,
// `undo`) and the parent keeps the drafts. `errors` are the server's
// refusals by row key.
const props = defineProps<{
  rows: DraftedModelRate[]
  groupId: number | null
  override: string | null
  groupRate: string
  models?: GroupModels | null
  catalog?: CatalogFetch
  editable?: boolean
  disabled?: boolean
  errors?: ReadonlyMap<string, string>
  priced?: ReadonlyMap<string, boolean>
}>()
const emit = defineEmits<{
  (e: 'add', model: string, rate: string): void
  (e: 'change', row: DraftedModelRate, rate: string): void
  (e: 'remove' | 'undo', row: DraftedModelRate): void
}>()
const { t } = useI18n()

const prices = useGroupModels(
  () => (props.editable && props.models === undefined ? (props.groupId ?? 0) : null),
  () => (props.catalog ?? fetchCatalog)()
)

/** The account group's models; null while they load, and when nobody asked (read-only). */
const known = computed(() => (props.models === undefined ? prices.result.value : props.models) ?? null)
const groupModels = computed<GroupModels>(() => known.value ?? { rows: [], catalog: [] })
const addable = computed<SelectOption[]>(() => {
  const offered = groupModels.value.rows.filter((m) => m.offered).map((m) => ({ model: m.model, price: listPriceOf(m.base, m.price) }))
  return addModelOptions(t, offered, groupModels.value.catalog, new Set(props.rows.map((r) => r.model)))
})
/** A model the account group has no price for (editable): as drafted on its pricing tab, else once its models are known. */
function unpriced(model: string): boolean {
  if (!props.editable) return false
  const drafted = props.priced?.get(model)
  if (drafted !== undefined) return !drafted
  return !!known.value && !listPriceIn(groupModels.value, model)
}
/** The rate a model pays now, without a rate of the user group's own for it. */
function paidRate(model: string): string {
  const groupModel = groupModels.value.rows.find((m) => m.model === model)?.rate
  return effectiveRate({ segment: props.override, group_model: groupModel, group: props.groupRate }).value
}
const add = (model: SelectOption['value']) => emit('add', String(model), paidRate(String(model)))
</script>

<template>
  <div class="space-y-2" data-testid="segment-model-rates" :data-group="groupId ?? undefined">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <SHint inline size="xs">{{ t('segments.modelRates.intro') }}</SHint>
      <AddSelect
        v-if="editable"
        :options="addable"
        :label="t('segments.modelRates.add')"
        :no-results="t('segments.modelRates.addNone')"
        :footer="t('pricing.add.footer')"
        :disabled="disabled || prices.loading.value"
        creatable
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
          <DraftBadge :dirty="row.dirty" :invalid="row.invalid" :removed="row.removed" :error="errors?.get(row.key)" show-error testid="segment-model-rate" />
          <SHint v-if="unpriced(row.model)" inline size="xs" tone="warning" class="basis-full" data-testid="segment-model-rate-unpriced">{{ t('pricing.price.unpriced') }}</SHint>
        </span>
        <RateInput
          v-if="editable"
          :model-value="row.rate"
          class="w-28"
          :error="row.invalid"
          :disabled="disabled || row.removed"
          :aria-label="t('segments.modelRates.rateOf', { model: row.model })"
          data-testid="segment-model-rate-input"
          @update:model-value="emit('change', row, $event.trim())"
        />
        <span v-else class="font-mono text-sm" data-testid="segment-model-rate-value">{{ rateText(row.rate) }}</span>
        <template v-if="editable">
          <SRowAction v-if="row.removed" :label="t('segments.modelRates.undo')" :disabled="disabled" data-testid="segment-model-rate-undo" @click="emit('undo', row)" />
          <SRowAction v-else tone="danger" :label="t('common.remove')" :disabled="disabled" data-testid="segment-model-rate-remove" @click="emit('remove', row)" />
        </template>
      </li>
    </ul>
  </div>
</template>
