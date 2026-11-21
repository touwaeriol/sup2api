<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SEmpty, SField, SHint, SInput, SRowAction, SSwitch, STable, type TableColumn, type Tone } from '@sub2api/ui'
import type { GroupModelPrices, ModelPriceRow, PriceRule, PriceSource, SegmentRef } from '@/api/pricing'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import PriceRuleEditor from './PriceRuleEditor.vue'
import { priceCells } from './pricingFormat'
import { draftedRow, ruleOf, rulePatches, setRuleDraft, validRule, type DraftedRow, type RuleScope } from './ruleDrafts'
import { priceLabel, ruleText } from './ruleText'
import { useDrafts } from './useDrafts'

// The models a group provides and what each costs there. Without a segment
// the table edits the group's own rules; with one it edits that segment's
// rules and shows the group's read-only. "Set" opens the rule's editor under
// the row, one row at a time. Edits stay in drafts (useDrafts; the list price
// follows them at once, through the same priceLabel as a saved one) until the
// parent calls save(), as it does with its other sub-editors after saving the
// main form. Loading again, as happens when the group or the segment
// changes, drops the drafts: a parent that lets the user switch should look
// at `dirty` first.
const props = defineProps<{ groupId: number | null; segment?: SegmentRef | null; editable?: boolean }>()
const { t } = useI18n()

/** Search and the filter appear from this many models on. */
const TOOLS_FROM = 8

/** The table keeps which editor is open: a new key closes it. */
const tableKey = ref(0)
const q = ref('')
const onlyRuled = ref(false)

const scope = computed<RuleScope>(() => (props.segment ? 'segment' : 'group'))
const path = computed(() => `/groups/${props.groupId}/model-prices`)
const query = computed(() => (props.segment ? { segment_id: props.segment.id } : undefined))

// A group not saved yet has no prices to ask for.
const fetchPrices = async () => (props.groupId === null ? null : api.get<GroupModelPrices>(path.value, query.value))
const state = useDrafts<GroupModelPrices | null, string, PriceRule | null>({ empty: () => null, fetch: fetchPrices, problem: incomplete })
const { saved: data, drafts, loading, error, dirty } = state

/** Loads the prices anew: the drafts and the open editor go. */
function load() {
  tableKey.value++
  return state.load()
}
// By id: the same segment handed over as a new object must not drop the drafts.
watch([() => props.groupId, () => props.segment?.id], load, { immediate: true })

function viewRow(saved: ModelPriceRow) {
  const row = draftedRow(saved, scope.value, drafts.value)
  return { ...row, tiers: priceCells(row.base).tiers, invalid: row.dirty && !validRule(ruleOf(row, scope.value)) }
}
const rows = computed(() => (data.value?.models ?? []).map(viewRow))
const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  // A rule being removed keeps its row in sight until it is saved.
  return rows.value.filter((r) => (!s || r.model.toLowerCase().includes(s)) && (!onlyRuled.value || r.source !== 'global' || r.dirty))
})

/** The rule columns, and what a model without a rule of that level follows. */
const levels = computed(() => {
  const all: Array<{ scope: RuleScope; label: string; follows: string }> = [
    { scope: 'group', label: t('pricing.table.groupRule'), follows: t('pricing.table.followGlobal') },
    { scope: 'segment', label: t('pricing.table.segmentRule'), follows: t('pricing.table.followGroup') }
  ]
  return props.segment ? all : all.slice(0, 1)
})
/** The level this table edits: the last one shown. */
const edited = computed(() => levels.value[levels.value.length - 1])
// The model column keeps a share of the width whatever its names are long (a
// long one is cut, its tooltip has it whole), so the table fits its place
// without scrolling sideways and the other columns wrap only when they must.
const columns = computed<TableColumn[]>(() => [
  { key: 'model', label: t('common.model'), class: 'w-[28%] max-w-0 overflow-hidden', mobile: 'title' },
  { key: 'base', label: t('pricing.table.base'), mobile: 'meta', mobileSpan: 2 },
  ...levels.value.map((l): TableColumn => ({ key: l.scope, label: l.label, mobile: 'meta', mobileSpan: 2 })),
  { key: 'list_price', label: t('pricing.table.listPrice'), mobile: 'meta', mobileSpan: 2 },
  ...(props.editable ? [{ key: 'actions', label: t('common.actions'), align: 'right' } satisfies TableColumn] : [])
])

function sourceTone(source: PriceSource): Tone {
  if (source === 'global') return 'gray'
  if (source === 'group') return 'info'
  return props.segment?.kind === 'personal' ? 'accent' : 'primary'
}
const rowAttrs = (row: DraftedRow) => ({ 'data-testid': 'model-price-row', 'data-model': row.model })

function draft(row: DraftedRow, rule: PriceRule | null) {
  drafts.value = setRuleDraft(drafts.value, row, rule)
}

/** Why the drafts cannot be saved yet: one is not filled in. The toast names its model; its row is marked, comes into sight, and its editor says why. */
function incomplete(): string | null {
  const invalid = rows.value.find((r) => r.invalid)
  if (!invalid) return null
  q.value = ''
  return t('pricing.table.invalid', { model: invalid.model })
}

/**
 * Saves the drafts and shows what the server answers. False when a draft
 * cannot be saved yet (see `incomplete`). A failed request rejects and keeps
 * the drafts.
 */
async function save(): Promise<boolean> {
  if (props.groupId === null) return true
  const saved = await state.save((sent) => api.put<GroupModelPrices>(path.value, { items: rulePatches(sent, props.segment?.id ?? null) }, { query: query.value }))
  // Once everything is saved the open editor has nothing left to show; one on a rule edited meanwhile stays.
  if (saved && !dirty.value) tableKey.value++
  return saved
}

defineExpose({ save, dirty })
</script>

<template>
  <div data-testid="model-price-table">
    <SEmpty v-if="groupId === null" size="sm" icon="price" :text="t('pricing.table.emptyNew')" data-testid="model-price-empty" />
    <SEmpty v-else-if="data && !data.models.length" size="sm" icon="price" :text="t('pricing.table.emptyModels')" data-testid="model-price-empty" />
    <template v-else>
      <div v-if="rows.length > TOOLS_FROM" class="mb-2 flex flex-wrap items-center gap-3">
        <!-- Enter filters the list; it must not submit a surrounding form. -->
        <SInput v-model="q" class="w-56" size="sm" icon="search" clearable :placeholder="t('pricing.table.search')" :aria-label="t('pricing.table.search')" data-testid="model-price-search" @keydown.enter.prevent />
        <SSwitch v-model="onlyRuled" size="sm" :label="t('pricing.table.onlyRuled')" data-testid="model-price-only-ruled" />
      </div>
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
          <span class="inline-flex max-w-full items-center gap-1.5">
            <ModelBadge :model="row.model" />
            <SBadge v-if="row.dirty" :tone="row.invalid ? 'danger' : 'warning'" size="xs" :title="row.invalid ? t('pricing.table.invalid', { model: row.model }) : undefined" data-testid="model-price-unsaved">
              {{ t('pricing.table.unsaved') }}
            </SBadge>
          </span>
        </template>
        <template #cell-base="{ row }">
          <span class="whitespace-nowrap font-mono text-xs" :class="row.base ? '' : 'text-fg-muted'" data-testid="model-price-base">{{ priceLabel(t, row.base) }}</span>
          <SHint v-if="row.tiers > 1" size="xs">{{ t('pricing.price.tiered', { n: row.tiers }) }}</SHint>
        </template>
        <template v-for="level in levels" :key="level.scope" #[`cell-${level.scope}`]="{ row }">
          <template v-if="ruleOf(row, level.scope)">
            <span class="text-xs" :data-testid="`model-price-${level.scope}-rule`">{{ ruleText(t, ruleOf(row, level.scope)) }}</span>
          </template>
          <span v-else class="text-xs text-fg-muted" :data-testid="`model-price-${level.scope}-rule`">{{ level.follows }}</span>
          <SHint v-if="!segment && row.segment_rule_count" size="xs" data-testid="model-price-segment-rules">{{ t('pricing.table.segmentRules', row.segment_rule_count) }}</SHint>
        </template>
        <template #cell-list_price="{ row }">
          <span class="inline-flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
            <span class="whitespace-nowrap font-mono text-xs" data-testid="model-price-list">{{ priceLabel(t, row.list_price) }}</span>
            <SBadge :tone="sourceTone(row.source)" size="xs" data-testid="model-price-source">{{ t(`pricing.source.${row.source}`) }}</SBadge>
          </span>
        </template>
        <template #cell-actions="{ row, expanded, toggleExpand }">
          <div class="flex items-center justify-end gap-1">
            <SRowAction :label="expanded ? t('pricing.table.done') : t('pricing.table.set')" :aria-expanded="expanded" data-testid="model-price-set" @click="toggleExpand()" />
            <SRowAction v-if="ruleOf(row, scope)" tone="danger" :label="t('pricing.table.clear')" data-testid="model-price-clear" @click="draft(row, null)" />
          </div>
        </template>
        <template #expand="{ row }">
          <SField size="sm" :label="edited.label">
            <PriceRuleEditor :model-value="ruleOf(row, scope)" :base="row.base" :inherit-label="edited.follows" @update:model-value="draft(row, $event)" />
          </SField>
        </template>
      </STable>
      <div class="mt-1.5 flex flex-wrap items-center justify-between gap-2">
        <SHint size="xs">{{ t('pricing.table.unit') }}</SHint>
        <SBadge v-if="dirty" tone="warning" data-testid="model-price-dirty">{{ t('pricing.table.unsavedCount', drafts.size) }}</SBadge>
      </div>
    </template>
  </div>
</template>
