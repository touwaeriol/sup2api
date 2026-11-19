<script setup lang="ts">
// Matrix view of 模型的协议限定 (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1):
// a row per model of the account (and per model that only has a rule), then
// the other models; a column per own protocol the account opens and per
// protocol the type reaches through a conversion. A row without a rule shows,
// greyed, what the model is served on: the protocols of the other models'
// row once that has a rule, else the default (the open own protocols, no
// conversion). Ticking a cell makes the row explicit, from what it showed;
// "restore default" drops its rule. Selected rows take one protocol set at
// once ("choose models, set their protocols").
// The table scrolls sideways under its first columns, like PermissionMatrix.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCheckbox, SHint, useScrollClip } from '@sub2api/ui'
import ConversionGradeBadge from '@/components/ConversionGradeBadge.vue'
import { columnState, matrixModels, openColumns, protocolsOf, REST, toggleCell, toggleColumn, withModels, withoutModels, type ProtocolColumn } from './protocolBinding'
import { useProtocolLabels } from './protocolLabels'
import ProtocolChips from './ProtocolChips.vue'
import type { ProtocolEditor } from './useProtocolEdit'

const props = defineProps<{ editor: ProtocolEditor; models: string[]; mapping?: Record<string, string>; disabled?: boolean }>()
const { t } = useI18n()
const labels = useProtocolLabels()
const { columns, protocols, rules, defaults } = props.editor

const own = computed(() => openColumns(columns.value, protocols.value))
const shown = computed(() => [...own.value, ...columns.value.cross])
const rows = computed(() => matrixModels(props.models, rules.value))
const ownPlatforms = computed(() => labels.platformsName([...new Set(own.value.map(c => c.platform))].map(id => ({ id }))))
/** The protocols of a row: its rule, else what a model without one is served on. */
const idsOf = (model: string) => protocolsOf(rules.value, model, defaults.value)
/** A row without a rule goes by the other models' row once that has one. */
const followsRest = (model: string) => !(model in rules.value) && REST in rules.value

function toggle(model: string, c: ProtocolColumn) {
  rules.value = toggleCell(rules.value, model, c, defaults.value)
}
const reset = (models: string[]) => (rules.value = withoutModels(rules.value, models))

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

const scroller = ref<HTMLElement>()
const table = ref<HTMLElement>()
const clipped = useScrollClip(scroller, table)
</script>

<template>
  <div class="space-y-3" data-testid="protocol-matrix">
    <div v-if="selected.length" class="flex flex-wrap items-center gap-2 rounded-xl border border-primary-200 bg-primary-50 px-3 py-2 text-sm dark:border-primary-800 dark:bg-primary-900/20" data-testid="protocol-matrix-bulk">
      <span class="font-semibold text-fg">{{ t('accounts.protocols.rules.selected', { n: selected.length }) }}</span>
      <span class="text-fg-muted">{{ t('accounts.protocols.rules.assign') }}</span>
      <ProtocolChips :columns="{ own, cross: columns.cross }" :ids="draft" @toggle="draft = toggleColumn(draft, $event)" @remove="draft = draft.filter(id => id !== $event)" />
      <span class="ms-auto flex flex-wrap items-center gap-1.5">
        <SButton size="xs" variant="primary" data-testid="protocol-matrix-apply" @click="applyDraft">{{ t('accounts.protocols.rules.apply') }}</SButton>
        <SButton size="xs" data-testid="protocol-matrix-reset-selected" @click="resetSelected">{{ t('accounts.protocols.rules.reset') }}</SButton>
        <SButton size="xs" variant="ghost" @click="selected = []">{{ t('accounts.protocols.rules.clearSelection') }}</SButton>
      </span>
    </div>

    <div class="relative">
      <div ref="scroller" class="overflow-x-auto rounded-xl border border-line">
        <table ref="table" class="w-full border-collapse text-sm">
          <thead>
            <tr class="bg-surface-2 text-xs">
              <th class="w-8" />
              <th class="sticky left-0 z-[1] bg-surface-2" />
              <th v-if="own.length" :colspan="own.length" scope="colgroup" class="px-2 pt-2 text-center font-medium text-primary-600 dark:text-primary-300" data-testid="protocol-matrix-own">
                {{ t('accounts.protocols.rules.groupOwn', { platforms: ownPlatforms }) }}
              </th>
              <th v-if="columns.cross.length" :colspan="columns.cross.length" scope="colgroup" class="border-l-2 border-line px-2 pt-2 text-center font-medium text-warning-600 dark:text-warning-400" data-testid="protocol-matrix-cross">
                {{ t('accounts.protocols.rules.groupCross') }}
              </th>
              <th />
            </tr>
            <tr class="border-b border-line bg-surface-2">
              <th class="w-8 py-2 pl-3 text-left">
                <SCheckbox bare size="xs" :model-value="allSelected" :indeterminate="selected.length > 0 && !allSelected" :disabled="disabled || !rows.length" :aria-label="t('accounts.protocols.rules.selectAll')" data-testid="protocol-matrix-select-all" @update:model-value="selected = $event ? [...rows] : []" />
              </th>
              <th scope="col" class="sticky left-0 z-[1] min-w-[9rem] bg-surface-2 px-3 py-2 text-left font-medium text-fg-subtle">{{ t('accounts.protocols.rules.model') }}</th>
              <th
                v-for="(c, i) in shown"
                :key="c.key"
                scope="col"
                class="min-w-[5.5rem] px-2 py-2 text-center align-bottom font-medium text-fg-subtle"
                :class="c.via === 'converted' && i === own.length ? 'border-l-2 border-line' : ''"
                :title="`${c.method} ${c.path}`"
                :data-protocol="c.key"
              >
                <span class="block whitespace-nowrap">{{ labels.protocolName(c.key) }}</span>
                <span v-if="c.via === 'converted'" class="mt-0.5 flex items-center justify-center gap-1 whitespace-nowrap text-xxs font-normal">
                  {{ labels.platformLabel(c.platform) }}<ConversionGradeBadge :grade="c.grade" :via="c.hops" size="xs" />
                </span>
                <span v-else class="mt-0.5 block text-xxs font-normal text-fg-subtle">{{ t(c.via === 'adapted' ? 'accounts.protocols.own.adaptedShort' : 'accounts.protocols.own.native') }}</span>
              </th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="model in [...rows, REST]" :key="model" class="group border-t border-line first:border-t-0" :class="model === REST ? 'bg-surface-2' : ''" :data-model="model" :data-default="!(model in rules) || undefined" :data-follows-rest="followsRest(model) || undefined">
              <td class="w-8 py-2 pl-3">
                <SCheckbox v-if="model !== REST" bare size="xs" :model-value="selected.includes(model)" :disabled="disabled" :aria-label="t('accounts.protocols.rules.selectModel', { model })" data-testid="protocol-matrix-select" @update:model-value="select(model, $event)" />
              </td>
              <th scope="row" class="sticky left-0 z-[1] min-w-[9rem] max-w-[16rem] px-3 py-2 text-left font-normal" :class="model === REST ? 'bg-surface-2 font-medium' : 'bg-surface'">
                <span class="block truncate" :class="model === REST ? '' : 'font-mono text-xs'" :title="labels.modelName(model)">{{ labels.modelName(model) }}</span>
                <span v-if="mapping?.[model]" class="block truncate text-xxs text-fg-subtle">{{ t('accounts.editorUi.mappedTo', { model: mapping[model] }) }}</span>
              </th>
              <td v-for="(c, i) in shown" :key="c.key" class="px-2 py-2 text-center" :class="c.via === 'converted' && i === own.length ? 'border-l-2 border-line' : ''" :data-protocol="c.key">
                <SCheckbox
                  bare
                  size="xs"
                  :class="model in rules ? '' : 'opacity-40 grayscale'"
                  :model-value="columnState(idsOf(model), c) === 'on'"
                  :indeterminate="columnState(idsOf(model), c) === 'partial'"
                  :disabled="disabled"
                  :aria-label="t('accounts.protocols.rules.cell', { model: labels.modelName(model), protocol: labels.columnName(c) })"
                  @update:model-value="toggle(model, c)"
                />
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-xs text-fg-subtle">
                <SButton v-if="model in rules" size="xs" variant="ghost" :disabled="disabled" data-testid="protocol-matrix-reset-row" @click="reset([model])">{{ t('accounts.protocols.rules.reset') }}</SButton>
                <template v-else>{{ t(followsRest(model) ? 'accounts.protocols.rules.followsRest' : 'accounts.protocols.rules.default') }}</template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="clipped" class="pointer-events-none absolute inset-y-px right-px w-10 rounded-r-xl bg-gradient-to-l from-surface to-transparent" />
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <SHint size="xs" class="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1">
        <span>{{ t(REST in rules ? 'accounts.protocols.rules.legendFollowsRest' : 'accounts.protocols.rules.legendDefault') }}</span>
        <span>{{ t('accounts.protocols.rules.legendExplicit') }}</span>
      </SHint>
      <SButton size="xs" variant="ghost" :disabled="disabled || !Object.keys(rules).length" data-testid="protocol-matrix-reset-all" @click="reset(Object.keys(rules))">{{ t('accounts.protocols.rules.resetAll') }}</SButton>
    </div>
  </div>
</template>
