<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SEmpty, SHint, SInput, SRowAction, SSwitch, STable, type SelectOption, type TableColumn } from '@sub2api/ui'
import type { CatalogPrice, GroupModelPrices, ModelPriceRow, PriceRule } from '@/api/pricing'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import AddSelect from './AddSelect.vue'
import PriceRuleEditor from './PriceRuleEditor.vue'
import { withDraft } from './drafts'
import { priceCells, rateText, timesRate } from './pricingFormat'
import { catalogPrices, catalogRow, draftedRow, rulePatches, setRuleDraft, validRule, type DraftedRow } from './ruleDrafts'
import { priceLabel, ruleText } from './ruleText'
import { useDrafts } from './useDrafts'

// The model prices of a group: each model's original price, the group's own
// price (the original × N, or a price of its own) and what is paid at `rate`.
// The rows are the models the group provides and those it prices, as the
// server lists them. A group not saved yet (groupId null) has no accounts, so
// it provides nothing yet: its table starts empty. Any model with an original
// price can be added and priced before an account provides it. "Change" opens
// a row's editor under it, one row at a time. Edits stay in drafts (useDrafts)
// until the parent calls save(gid), as it does with its other sub-editors once
// the group is saved.
const props = defineProps<{ groupId: number | null; rate: string; editable?: boolean }>()
const { t } = useI18n()

/** Search and the filter appear from this many models on. */
const TOOLS_FROM = 8

interface Saved {
  rows: ModelPriceRow[]
  /** The original prices, to add a model with. */
  catalog: CatalogPrice[]
}

async function fetchSaved(): Promise<Saved> {
  const { groupId } = props
  const [prices, catalog] = await Promise.all([
    groupId === null ? null : api.get<GroupModelPrices>(`/groups/${groupId}/model-prices`),
    props.editable ? api.get<CatalogPrice[]>('/pricing/catalog') : []
  ])
  return { rows: prices?.models ?? [], catalog }
}
const state = useDrafts<Saved, string, PriceRule | null>({ empty: () => ({ rows: [], catalog: [] }), fetch: fetchSaved, problem: incomplete })
const { saved, drafts, loading, error, dirty } = state

/** The table keeps which editor is open: a new key closes it. */
const tableKey = ref(0)
const q = ref('')
const onlyRuled = ref(false)
/** Models added here, in the order added. */
const added = ref<string[]>([])

function load() {
  tableKey.value++
  added.value = []
  return state.load()
}
watch(() => props.groupId, load, { immediate: true })

const prices = computed(() => catalogPrices(saved.value.catalog))
const listed = computed<ModelPriceRow[]>(() => {
  const rows = new Map(saved.value.rows.map((r) => [r.model, r]))
  added.value.filter((m) => !rows.has(m)).forEach((m) => rows.set(m, catalogRow(m, prices.value.get(m) ?? null)))
  return [...rows.values()]
})
const savedModels = computed(() => new Set(saved.value.rows.map((r) => r.model)))

function viewRow(row: ModelPriceRow) {
  const r = draftedRow(row, drafts.value)
  return {
    ...r,
    tiers: priceCells(r.base).tiers,
    paid: r.list_price && timesRate(r.list_price, props.rate),
    invalid: r.dirty && !validRule(r.group_rule),
    /** Added here and not saved: it can go again. */
    removable: added.value.includes(r.model) && !savedModels.value.has(r.model)
  }
}
const rows = computed(() => listed.value.map(viewRow))
const ruled = computed(() => rows.value.filter((r) => r.group_rule).length)
const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  // A rule being removed keeps its row in sight until it is saved.
  return rows.value.filter((r) => (!s || r.model.toLowerCase().includes(s)) && (!onlyRuled.value || r.group_rule || r.dirty))
})

/** The models that can be added: those with an original price the table does not list. */
const addable = computed<SelectOption[]>(() => {
  const listedModels = new Set(listed.value.map((r) => r.model))
  return saved.value.catalog.filter((c) => !listedModels.has(c.model)).map((c) => ({ value: c.model, label: c.model, hint: priceLabel(t, c.price) }))
})

// The model column keeps a share of the width whatever its names are long (a
// long one is cut, its tooltip has it whole), so the table fits the dialog
// without scrolling sideways.
const columns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('common.model'), class: 'w-[28%] max-w-0 overflow-hidden', mobile: 'title' },
  { key: 'base', label: t('pricing.table.base'), mobile: 'meta' },
  { key: 'group', label: t('pricing.table.group'), mobile: 'meta' },
  { key: 'paid', label: t('pricing.table.paid', { rate: rateText(props.rate || null) }), mobile: 'meta' },
  ...(props.editable ? [{ key: 'actions', label: t('common.actions'), align: 'right' } satisfies TableColumn] : [])
])
const rowAttrs = (row: DraftedRow) => ({ 'data-testid': 'model-price-row', 'data-model': row.model })

function draft(row: DraftedRow, rule: PriceRule | null) {
  drafts.value = setRuleDraft(drafts.value, row, rule)
}
function add(model: SelectOption['value']) {
  if (typeof model === 'string') added.value = [...added.value, model]
}
function remove(model: string) {
  added.value = added.value.filter((m) => m !== model)
  drafts.value = withDraft(drafts.value, model, undefined)
}

/** Why the drafts cannot be saved yet: one is not filled in. The toast names its model; its row is marked, comes into sight, and its editor says why. */
function incomplete(): string | null {
  const invalid = rows.value.find((r) => r.invalid)
  if (!invalid) return null
  q.value = ''
  onlyRuled.value = false
  return t('pricing.table.invalid', { model: invalid.model })
}

/**
 * Saves the drafts for group `gid` and shows what the server answers. False
 * when a draft cannot be saved yet (see `incomplete`). A failed request
 * rejects and keeps the drafts.
 */
async function save(gid: number): Promise<boolean> {
  const done = await state.save(async (sent) => {
    const answer = await api.put<GroupModelPrices>(`/groups/${gid}/model-prices`, { items: rulePatches(sent) })
    return { catalog: saved.value.catalog, rows: answer.models }
  })
  // Once everything is saved the open editor has nothing left to show; one on a rule edited meanwhile stays.
  if (done && !dirty.value) tableKey.value++
  return done
}

defineExpose({ check: state.check, save, dirty })
</script>

<template>
  <div class="space-y-2" data-testid="model-price-table">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-semibold text-fg">{{ t('pricing.table.title') }}</h3>
        <SBadge tone="gray" data-testid="model-price-count">{{ t('pricing.table.count', { n: rows.length }, rows.length) }}<template v-if="ruled"> · {{ t('pricing.table.ruled', { n: ruled }, ruled) }}</template></SBadge>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <template v-if="rows.length > TOOLS_FROM">
          <!-- Enter filters the list; it must not submit a surrounding form. -->
          <SInput v-model="q" class="w-48" size="sm" icon="search" clearable :placeholder="t('pricing.table.search')" :aria-label="t('pricing.table.search')" data-testid="model-price-search" @keydown.enter.prevent />
          <SSwitch v-model="onlyRuled" size="sm" :label="t('pricing.table.onlyRuled')" data-testid="model-price-only-ruled" />
        </template>
        <AddSelect
          v-if="editable"
          :options="addable"
          :label="t('pricing.table.add')"
          :no-results="t('pricing.table.addNone')"
          :footer="t('pricing.table.addFooter')"
          :disabled="loading"
          data-testid="model-price-add"
          @add="add"
        />
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
            <SBadge v-if="row.dirty" :tone="row.invalid ? 'danger' : 'warning'" size="xs" :title="row.invalid ? t('pricing.table.invalid', { model: row.model }) : undefined" data-testid="model-price-unsaved">
              {{ t('pricing.table.unsaved') }}
            </SBadge>
          </span>
        </template>
        <template #cell-base="{ row }">
          <span class="whitespace-nowrap font-mono text-xs" :class="row.group_rule ? 'text-fg-subtle line-through' : row.base ? '' : 'text-fg-muted'" data-testid="model-price-base">{{ priceLabel(t, row.base) }}</span>
          <SHint v-if="row.tiers > 1" size="xs">{{ t('pricing.price.tiered', { n: row.tiers }) }}</SHint>
        </template>
        <template #cell-group="{ row }">
          <span v-if="row.group_rule" class="inline-flex flex-wrap items-baseline gap-x-1.5" data-testid="model-price-group">
            <span class="whitespace-nowrap font-mono text-xs">{{ priceLabel(t, row.list_price) }}</span>
            <SHint inline size="xs">{{ ruleText(t, row.group_rule) }}</SHint>
          </span>
          <SHint v-else inline size="xs" data-testid="model-price-group">{{ t('pricing.table.useBase') }}</SHint>
        </template>
        <template #cell-paid="{ row }">
          <span class="whitespace-nowrap font-mono text-xs font-semibold" data-testid="model-price-paid">{{ row.list_price ? (row.paid ? priceLabel(t, row.paid) : '—') : priceLabel(t, null) }}</span>
        </template>
        <template #cell-actions="{ row, expanded, toggleExpand }">
          <div class="flex items-center justify-end gap-1">
            <SRowAction :label="expanded ? t('pricing.table.done') : t('pricing.table.set')" :aria-expanded="expanded" data-testid="model-price-set" @click="toggleExpand()" />
            <SRowAction v-if="row.group_rule" tone="danger" :label="t('pricing.table.clear')" data-testid="model-price-clear" @click="draft(row, null)" />
            <SRowAction v-if="row.removable" :label="t('common.remove')" data-testid="model-price-remove" @click="remove(row.model)" />
          </div>
        </template>
        <template #expand="{ row }">
          <div class="space-y-2">
            <p class="text-xs font-semibold text-fg">{{ t('pricing.table.editing', { model: row.model }) }}</p>
            <PriceRuleEditor :model-value="row.group_rule" :base="row.base" :rate="rate" @update:model-value="draft(row, $event)" />
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
