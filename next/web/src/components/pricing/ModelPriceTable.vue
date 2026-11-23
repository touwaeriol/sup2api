<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SEmpty, SHint, SInput, SRowAction, SSwitch, STable, useTabbedFormPart, type SelectOption, type TableColumn } from '@sub2api/ui'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import AddSelect from './AddSelect.vue'
import { addModelOptions } from './addModelOptions'
import DraftBadge from './DraftBadge.vue'
import ModelPriceEditor from './ModelPriceEditor.vue'
import RateOverrideField from './RateOverrideField.vue'
import { priceCells, rateText } from './pricingFormat'
import { paidLabel, priceLabel } from './priceLabels'
import { usePricingTable, type PricingRow } from './usePricingTable'

// The pricing table of a group: per model its original price, its model
// price (a price of its own; empty follows the original price), its model
// rate (empty follows `baseRate`, the group's base rate as the form has it)
// and what is paid: the price that applies × the one rate that applies. The
// rows are the models the group provides and those it sets something on; a
// group not saved yet (groupId null) provides nothing yet, so its table
// starts empty. Any model name can be added (addModelOptions offers those
// the accounts offer, then those with a global price); one without any
// price says it is not priced, and its
// requests are refused until it has one. "Change
// price" opens a row's price editor under it, one row at a time. Edits stay
// in drafts, which the group's request sends (the part of tab `tab` of the
// enclosing tabbed form); without `editable` the table only shows. `priced`
// reports, by model, whether each row is priced as drafted (the user group
// rates of the same editor say which models are not).
const props = defineProps<{ groupId: number | null; baseRate: string; editable?: boolean; tab?: string }>()
const emit = defineEmits<{ (e: 'priced', priced: ReadonlyMap<string, boolean>): void }>()
const { t } = useI18n()

/** Search and the filter appear from this many models on. */
const TOOLS_FROM = 8

const table = usePricingTable({ groupId: () => props.groupId, baseRate: () => props.baseRate, editable: () => !!props.editable })
const { rows, drafts, loading, error, dirty, added } = table
useTabbedFormPart({ tab: props.tab ?? '', check, body: table.body, dirty, fields: ['model_prices', 'model_rates'], applyErrors }, () => !!props.editable && props.tab !== undefined)

/** The table keeps which editor is open: a new key closes it. */
const tableKey = ref(0)
const q = ref('')
const onlySet = ref(false)

function load() {
  tableKey.value++
  added.value = []
  return table.load()
}
watch(() => props.groupId, load, { immediate: true })

watch(() => new Map(rows.value.map((r) => [r.model, r.priced])), (priced) => emit('priced', priced), { immediate: true })

const isSet = (r: PricingRow) => r.price !== null || r.rate !== null
const setCount = computed(() => rows.value.filter(isSet).length)
const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  // A row being cleared keeps its place in sight until it is saved.
  return rows.value.filter((r) => (!s || r.model.toLowerCase().includes(s)) && (!onlySet.value || isSet(r) || r.dirty))
})

/** The models offered to add (any other name may be typed); the table lists every model the accounts offer already. */
const addable = computed<SelectOption[]>(() => addModelOptions(t, [], table.saved.value.catalog, new Set(rows.value.map((r) => r.model))))

// The model column keeps a share of the width whatever its names are long (a
// long one is cut, its tooltip has it whole), so the table fits the dialog.
const columns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('common.model'), class: 'w-[24%] max-w-0 overflow-hidden', mobile: 'title' },
  { key: 'base', label: t('pricing.table.base'), mobile: 'meta' },
  { key: 'price', label: t('pricing.table.price'), mobile: 'meta' },
  { key: 'rate', label: t('pricing.table.rate'), mobile: 'meta' },
  { key: 'paid', label: t('pricing.table.paid'), mobile: 'meta' },
  ...(props.editable ? [{ key: 'actions', label: t('common.actions'), align: 'right' } satisfies TableColumn] : [])
])
const rowAttrs = (row: PricingRow) => ({ 'data-testid': 'model-price-row', 'data-model': row.model })

/** Brings every row back in sight: one that is wrong has to be seen. */
function showAll() {
  q.value = ''
  onlySet.value = false
}
function check(): boolean {
  const ok = table.check()
  if (!ok) showAll()
  return ok
}
function applyErrors(errors: Record<string, string>) {
  table.applyErrors(errors)
  showAll()
}
/** Adds a model; one the table lists is offered greyed, so it never comes twice. */
function add(model: SelectOption['value']) {
  if (typeof model === 'string') table.add(model)
}
</script>

<template>
  <div class="space-y-2" data-testid="model-price-table">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold text-fg">{{ t('pricing.table.title') }}</h3>
        <SBadge tone="gray" data-testid="model-price-count">{{ t('pricing.table.count', { n: rows.length }, rows.length) }}<template v-if="setCount"> · {{ t('pricing.table.set', { n: setCount }, setCount) }}</template></SBadge>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <template v-if="rows.length > TOOLS_FROM">
          <!-- Enter filters the list; it must not submit a surrounding form. -->
          <SInput v-model="q" class="w-48" size="sm" icon="search" clearable :placeholder="t('pricing.table.search')" :aria-label="t('pricing.table.search')" data-testid="model-price-search" @keydown.enter.prevent />
          <SSwitch v-model="onlySet" size="sm" :label="t('pricing.table.onlySet')" data-testid="model-price-only-set" />
        </template>
        <AddSelect v-if="editable" :options="addable" :label="t('pricing.table.add')" :no-results="t('pricing.table.addNone')" :footer="t('pricing.add.footer')" :disabled="loading" creatable data-testid="model-price-add" @add="add" />
      </div>
    </div>

    <SEmpty v-if="!loading && !error && !rows.length" size="sm" icon="price" :text="t(groupId === null ? 'pricing.table.emptyNew' : 'pricing.table.emptyModels')" data-testid="model-price-empty" />
    <template v-else>
      <STable
        :key="tableKey"
        :columns="columns"
        :rows="shown"
        :loading="loading"
        :error="error"
        row-key="model"
        dense
        :expandable="editable"
        :expand-toggle="false"
        expand-single
        :empty-text="t('pricing.table.emptyFiltered')"
        :row-attrs="rowAttrs"
        @retry="load"
      >
        <template #cell-model="{ row }">
          <span class="inline-flex max-w-full flex-wrap items-center gap-1.5">
            <ModelBadge :model="row.model" />
            <SBadge v-if="!row.offered" tone="warning" size="xs" :title="t('pricing.table.notOfferedHint')" data-testid="model-price-not-offered">{{ t('pricing.table.notOffered') }}</SBadge>
            <DraftBadge :dirty="row.dirty" :invalid="row.invalid" :error="row.error" :invalid-title="t('pricing.table.invalid', { model: row.model })" testid="model-price" />
          </span>
          <SHint v-if="row.error" size="xs" tone="danger" data-testid="model-price-error">{{ row.error }}</SHint>
          <SHint v-if="!row.priced" size="xs" tone="warning" data-testid="model-price-unpriced">{{ t('pricing.price.unpriced') }}</SHint>
        </template>
        <template #cell-base="{ row }">
          <span class="whitespace-nowrap font-mono text-xs" :class="row.price ? 'text-fg-subtle line-through' : row.base ? '' : 'text-fg-muted'" data-testid="model-price-base">{{ priceLabel(t, row.base) }}</span>
          <SHint v-if="priceCells(row.base).tiers > 1" size="xs">{{ t('pricing.price.tiered', { n: priceCells(row.base).tiers }) }}</SHint>
        </template>
        <template #cell-price="{ row }">
          <span v-if="row.price" class="whitespace-nowrap font-mono text-xs" data-testid="model-price-price">{{ priceLabel(t, row.list_price) }}</span>
          <SHint v-else inline size="xs" data-testid="model-price-price">{{ t('pricing.table.followBase') }}</SHint>
        </template>
        <template #cell-rate="{ row }">
          <RateOverrideField
            :model-value="row.rate"
            :follow="baseRate"
            :readonly="!editable"
            :follow-text="t('pricing.table.followRate')"
            :aria-label="t('pricing.table.rateOf', { model: row.model })"
            @update:model-value="table.change(row, { rate: $event })"
          />
        </template>
        <template #cell-paid="{ row }">
          <span class="whitespace-nowrap font-mono text-xs font-semibold" data-testid="model-price-paid">{{ paidLabel(t, row.list_price, row.applied.value) }}</span>
          <SHint size="xs" data-testid="model-price-paid-source">{{ rateText(row.applied.value || null) }} · {{ t(`pricing.table.rateSource.${row.applied.source}`) }}</SHint>
        </template>
        <template #cell-actions="{ row, expanded, toggleExpand }">
          <div class="flex items-center justify-end gap-1">
            <SRowAction :label="expanded ? t('pricing.table.done') : t('pricing.table.change')" :aria-expanded="expanded" data-testid="model-price-set" @click="toggleExpand()" />
            <SRowAction v-if="isSet(row)" tone="danger" :label="t('pricing.table.clear')" data-testid="model-price-clear" @click="table.change(row, { price: null, rate: null })" />
            <SRowAction v-if="row.removable" :label="t('common.remove')" data-testid="model-price-remove" @click="table.remove(row.model)" />
          </div>
        </template>
        <template #expand="{ row }">
          <div class="space-y-2">
            <p class="text-xs font-semibold text-fg">{{ t('pricing.table.editing', { model: row.model }) }}</p>
            <ModelPriceEditor :model-value="row.price" :base="row.base" :rate="row.applied.value" @update:model-value="table.change(row, { price: $event })" />
          </div>
        </template>
      </STable>
      <p v-if="groupId === null" class="rounded-xl border border-dashed border-line p-4 text-center text-xs text-fg-muted" data-testid="model-price-no-accounts">{{ t('pricing.table.noAccounts') }}</p>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <SHint size="xs">{{ t('pricing.table.unit') }}</SHint>
        <SBadge v-if="dirty" tone="warning" data-testid="model-price-dirty">{{ t('pricing.table.unsavedCount', drafts.size) }}</SBadge>
        <SHint v-else-if="editable" size="xs">{{ t('pricing.table.clearHint') }}</SHint>
      </div>
    </template>
  </div>
</template>
