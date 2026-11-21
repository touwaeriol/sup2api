<script setup lang="ts">
// Visual view of 模型的协议限定: a row per model of the account (and per model
// that only has a rule), then the other models. Nothing scrolls sideways at
// any width: the own protocols of a row fold into one summary that opens
// their toggle chips under it, and the conversions are toggle chips that wrap
// inside their cell. A row without a rule shows, muted, what the model is
// served on; switching anything makes the row explicit, from what it showed,
// and "restore default" drops its rule. Selected rows take one protocol set
// at once. Models outside the account's list get a row through the input at
// the end. On a phone a row stacks: model, own protocols, conversions.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCheckbox, SChip, SHint, SIcon, toast } from '@sub2api/ui'
import ModelTagSelect from '@/components/models/ModelTagSelect.vue'
import { useOptionalT } from '@/composables/optionalText'
import { hopsText } from '@/utils/conversion'
import { columnState, matrixModels, openColumns, protocolsOf, REST, toggleCell, toggleColumn, withModels, withoutModels, type ProtocolColumn } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'
import ProtocolChips from './ProtocolChips.vue'
import type { ProtocolEditor } from './useProtocolEdit'

const props = defineProps<{
  editor: ProtocolEditor
  /** The account's model list: the rows. */
  models: string[]
  mapping?: Record<string, string>
  /** Models offered by the input adding a row. */
  options: string[]
  disabled?: boolean
}>()
const { t } = useI18n()
const tr = useOptionalT()
const labels = useProtocolLabels()
const { columns, protocols, rules, defaults } = props.editor

const own = computed(() => openColumns(columns.value, protocols.value))
const rows = computed(() => matrixModels(props.models, rules.value))
const ownPlatforms = computed(() => labels.platformsName([...new Set(own.value.map(c => c.platform))].map(id => ({ id }))))
/** The protocols of a row: its rule, else what a model without one is served on. */
const idsOf = (model: string) => protocolsOf(rules.value, model, defaults.value)
const isOn = (model: string, c: ProtocolColumn) => columnState(idsOf(model), c) !== 'off'
/** A row without a rule goes by the other models' row once that has one. */
const followsRest = (model: string) => !(model in rules.value) && REST in rules.value

/** The own protocols of a row in a few words. */
function ownSummary(model: string): string {
  if (!(model in rules.value)) return t(followsRest(model) ? 'accounts.protocols.rules.followsRest' : 'accounts.protocols.rules.ownDefault')
  const on = own.value.filter(c => isOn(model, c))
  if (!on.length) return t('accounts.protocols.rules.ownNone')
  if (on.length === own.value.length) return t('accounts.protocols.rules.ownAll', { n: on.length })
  return on.length === 1 ? labels.columnName(on[0]) : t('accounts.protocols.rules.ownSome', { n: on.length, total: own.value.length })
}
const crossTitle = (c: ProtocolColumn) => [`${c.method} ${c.path}`, tr(`accounts.conversion.grades.${c.grade}`, undefined, c.grade), hopsText(c.hops)].filter(Boolean).join(' · ')

function toggle(model: string, c: ProtocolColumn) {
  rules.value = toggleCell(rules.value, model, c, defaults.value)
}
const reset = (models: string[]) => (rules.value = withoutModels(rules.value, models))
/** Models outside the account's list start from the default protocols. */
const add = (models: string[]) => (rules.value = withModels(rules.value, models.filter(m => !(m in rules.value)), defaults.value))
const invalidModel = (model: string) => toast(t('accounts.modelInvalid', { model }), 'warning')

// ---------------------------------------------------------------- rows opened on their own protocols

const opened = ref<string[]>([])
const toggleOpen = (model: string) => (opened.value = opened.value.includes(model) ? opened.value.filter(m => m !== model) : [...opened.value, model])

// ---------------------------------------------------------------- selected rows take one protocol set

const selected = ref<string[]>([])
/** The protocols the selected rows will take; starts from the default. */
const draft = ref<string[]>([])
watch(rows, r => (selected.value = selected.value.filter(m => r.includes(m))))
watch(() => selected.value.length > 0, on => on && (draft.value = [...defaults.value]))
const allSelected = computed(() => rows.value.length > 0 && rows.value.every(m => selected.value.includes(m)))

function select(model: string, on: boolean) {
  selected.value = on ? [...selected.value, model] : selected.value.filter(m => m !== model)
}
function applyDraft() {
  rules.value = withModels(rules.value, selected.value, draft.value)
  selected.value = []
}
function resetSelected() {
  reset(selected.value)
  selected.value = []
}

const GRID = 'grid grid-cols-[1rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-3 md:grid-cols-[1rem_minmax(0,12rem)_minmax(0,9.5rem)_minmax(0,1fr)_auto]'
</script>

<template>
  <div class="space-y-3" data-testid="protocol-rows">
    <div v-if="selected.length" class="flex flex-wrap items-center gap-2 rounded-xl border border-primary-200 bg-primary-50 px-3 py-2 text-sm dark:border-primary-800 dark:bg-primary-900/20" data-testid="protocol-rows-bulk">
      <span class="font-semibold text-fg">{{ t('accounts.protocols.rules.selected', { n: selected.length }) }}</span>
      <span class="text-fg-muted">{{ t('accounts.protocols.rules.assign') }}</span>
      <ProtocolChips :columns="{ own, cross: columns.cross }" :ids="draft" @toggle="draft = toggleColumn(draft, $event)" @remove="draft = draft.filter(id => id !== $event)" />
      <span class="ms-auto flex flex-wrap items-center gap-1.5">
        <SButton size="xs" variant="primary" data-testid="protocol-rows-apply" @click="applyDraft">{{ t('accounts.protocols.rules.apply') }}</SButton>
        <SButton size="xs" data-testid="protocol-rows-reset-selected" @click="resetSelected">{{ t('accounts.protocols.rules.reset') }}</SButton>
        <SButton size="xs" variant="ghost" @click="selected = []">{{ t('accounts.protocols.rules.clearSelection') }}</SButton>
      </span>
    </div>

    <div class="divide-y divide-line overflow-hidden rounded-xl border border-line text-sm" role="table">
      <div :class="GRID" class="bg-surface-2 py-2 text-xs font-medium text-fg-subtle" role="row">
        <SCheckbox bare size="xs" :model-value="allSelected" :indeterminate="selected.length > 0 && !allSelected" :disabled="disabled || !rows.length" :aria-label="t('accounts.protocols.rules.selectAll')" data-testid="protocol-rows-select-all" @update:model-value="selected = $event ? [...rows] : []" />
        <span class="col-span-2 md:col-span-1" role="columnheader">{{ t('accounts.protocols.rules.model') }}</span>
        <span class="hidden text-primary-600 dark:text-primary-300 md:block" role="columnheader">{{ t('accounts.protocols.rules.groupOwn', { platforms: ownPlatforms }) }}</span>
        <span v-if="columns.cross.length" class="hidden text-warning-600 dark:text-warning-400 md:block" role="columnheader">{{ t('accounts.protocols.rules.groupCross') }}</span>
      </div>

      <div v-for="model in [...rows, REST]" :key="model" role="row" :class="model === REST ? 'bg-surface-2' : ''" :data-model="model" :data-default="!(model in rules) || undefined" :data-follows-rest="followsRest(model) || undefined">
        <div :class="GRID" class="py-2">
          <SCheckbox v-if="model !== REST" bare size="xs" :model-value="selected.includes(model)" :disabled="disabled" :aria-label="t('accounts.protocols.rules.selectModel', { model })" data-testid="protocol-rows-select" @update:model-value="select(model, $event)" />
          <span v-else />
          <span class="min-w-0" role="rowheader">
            <span class="block truncate" :class="model === REST ? 'font-medium' : 'font-mono text-xs'" :title="labels.modelName(model)">{{ labels.modelName(model) }}</span>
            <span v-if="mapping?.[model]" class="block truncate text-xxs text-fg-subtle">{{ t('accounts.editorUi.mappedTo', { model: mapping[model] }) }}</span>
          </span>
          <span class="col-start-3 row-start-1 justify-self-end md:col-start-5">
            <SButton v-if="model in rules" size="xs" variant="ghost" :disabled="disabled" data-testid="protocol-rows-reset" @click="reset([model])">{{ t('accounts.protocols.rules.reset') }}</SButton>
          </span>
          <span class="col-span-2 col-start-2 flex items-center gap-2 md:col-span-1 md:col-start-3 md:row-start-1" role="cell">
            <span class="text-xs text-fg-subtle md:hidden">{{ t('accounts.protocols.rules.ownShort') }}</span>
            <button
              type="button"
              class="inline-flex h-7 max-w-full items-center gap-1 rounded-control border px-2 text-xs transition-colors hover:border-primary-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
              :class="model in rules ? 'border-line-strong text-fg' : 'border-line text-fg-subtle'"
              :aria-expanded="opened.includes(model)"
              :aria-label="t('accounts.protocols.rules.ownOf', { model: labels.modelName(model) })"
              data-testid="protocol-rows-own"
              @click="toggleOpen(model)"
            >
              <span class="truncate">{{ ownSummary(model) }}</span>
              <SIcon name="chevron-down" class="h-3.5 w-3.5 shrink-0 transition-transform duration-150" :class="opened.includes(model) ? 'rotate-180' : ''" />
            </button>
          </span>
          <span v-if="columns.cross.length" class="col-span-2 col-start-2 flex flex-wrap items-center gap-1.5 md:col-span-1 md:col-start-4 md:row-start-1" role="cell">
            <span class="text-xs text-fg-subtle md:hidden">{{ t('accounts.protocols.rules.crossShort') }}</span>
            <SChip v-for="c in columns.cross" :key="c.key" :pressed="isOn(model, c)" :disabled="disabled" :title="crossTitle(c)" :data-protocol="c.key" @update:pressed="toggle(model, c)">{{ labels.columnName(c) }}</SChip>
          </span>
        </div>
        <div v-if="opened.includes(model)" class="flex flex-wrap items-center gap-1.5 border-t border-dashed border-line bg-surface-2/60 px-3 py-2 pl-10" data-testid="protocol-rows-own-panel">
          <SChip v-for="c in own" :key="c.key" :pressed="isOn(model, c)" :disabled="disabled" :title="`${c.method} ${c.path}`" :data-protocol="c.key" :class="model in rules ? '' : 'opacity-60'" @update:pressed="toggle(model, c)">{{ labels.columnName(c) }}</SChip>
        </div>
      </div>

      <div class="px-3 py-2" data-testid="protocol-rows-add">
        <ModelTagSelect :model-value="[]" :options="options" :placeholder="t('accounts.protocols.rules.addModel')" :readonly="disabled" @update:model-value="add" @invalid="invalidModel" />
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <SHint size="xs" class="min-w-0 flex-1">{{ t(REST in rules ? 'accounts.protocols.rules.legendFollowsRest' : 'accounts.protocols.rules.legendDefault') }}</SHint>
      <SButton size="xs" variant="ghost" :disabled="disabled || !Object.keys(rules).length" data-testid="protocol-rows-reset-all" @click="reset(Object.keys(rules))">{{ t('accounts.protocols.rules.resetAll') }}</SButton>
    </div>
  </div>
</template>
