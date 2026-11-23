<script setup lang="ts" generic="T extends Record<string, any>">
// Data table. Cells render `cell-<key>` slots (or the value at a dotted key,
// '—' when empty); headers render `head-<key>` slots (or the label). A
// column with key 'actions' sticks to the right edge, set off by a shadow
// while columns scroll underneath it. Options:
// - expandable: chevron column + the `expand` slot under an open row; every
//   cell slot gets `expanded` and `toggleExpand()` of its row, so a cell (a
//   row action) can open it too (without expandable: false and a no-op). expandToggle false leaves the chevron column
//   out for a table opened from a cell only (phone cards keep their Details
//   toggle); expandSingle closes the other rows when one opens, each with
//   an `expand` (row, false) before the (row, true) of the one that opens;
//   defaultExpanded(row) opens the rows it is true for when first listed;
// - selectable + v-model:selected (array of row keys, see rowKey): checkbox
//   column with a select-all header (indeterminate when partly selected);
//   rowSelectable(row) disables individual rows; Shift+click on a checkbox
//   sets the range from the previous click; swipeSelect (default on) lets a
//   drag from the selection cell or a cell's padding select a row range;
// - clickable: rows look clickable, are focusable and emit row-click on
//   Enter / Space too (row-click is emitted on mouse clicks either way);
// - rowClass(row, index) / rowAttrs(row, index): extra class / attributes
//   (data-testid, ...) of a row's <tr>;
// - card: renders the table inside a card (no double border) with the
//   #toolbar slot above it and the #footer slot (pagination) below it.
// - reorderable + v-model:rows: leading drag handles; unique stable rowKey
//   values are required. reorderDisabled/loading prevent reordering.
// - error (truthy = the last load failed, e.g. useList's `error`): an empty
//   table says the load failed instead of "No data" (#error slot), a filled
//   one keeps its rows under a failure notice; @retry adds a Retry button.
// - mobile 'card' (default) / 'scroll': on a phone (useViewport
//   compact) rows become cards — columns placed by their `mobile` field (see
//   tableCard.ts), cells through the same `cell-<key>` slots with slot prop
//   `layout` 'card' (else 'table'), a Details toggle for detail fields and
//   the expand slot, a "select this page" row instead of the header checkbox
//   (no swipe / Shift range); #card="{ row, index, selected, toggle(on?),
//   expanded, toggleExpand }" replaces a card's content (the drag handle
//   stays). 'scroll' keeps the table. A page that filters columns itself
//   (useTableColumns + :columns="visibleColumns") should declare `mobile` on
//   every column, or on none, so hiding one never changes the placement.
import { computed, provide, ref, useId } from 'vue'
import { useUiT } from './i18n'
import SSpinner from './SSpinner.vue'
import SCheckbox from './SCheckbox.vue'
import SIcon from './SIcon.vue'
import type { TableColumn, TableReorderEvent } from './types'
import { displayValue, getPath } from './value'
import { useTableReorder } from './tableReorder'
import { useTableColumns, type TableColumnStorage } from './tableColumns'
import STableColumnSettings from './STableColumnSettings.vue'
import { useScrollClip } from './scrollClip'
import { useTableExpand } from './tableExpand'
import { useTableSelect } from './tableSelect'
import { useViewport } from './viewport'
import { cardFields, TABLE_CARD } from './tableCard'
import ReorderHandle from './internal/ReorderHandle.vue'
import TableCards from './internal/TableCards.vue'
import TableCell from './internal/TableCell.vue'
import TableLoadError from './internal/TableLoadError.vue'

const props = withDefaults(
  defineProps<{
    columns: TableColumn[]
    rows: T[]
    loading?: boolean
    rowKey?: string
    /** Show an expand toggle and render the "expand" slot under a row. */
    expandable?: boolean
    /** False: no chevron column; a cell opens its row with the slot prop toggleExpand. */
    expandToggle?: boolean
    /** Opening a row closes the others. */
    expandSingle?: boolean
    /** Rows it is true for start open the first time they are listed (a row seen before keeps what the user made of it). */
    defaultExpanded?: (row: T) => boolean
    /** Keep expanded content within the visible width of a scrollable table. */
    fitExpandedToContainer?: boolean
    emptyText?: string
    dense?: boolean
    selectable?: boolean
    selected?: unknown[]
    /** Drag across rows to select a range (selectable tables only). */
    swipeSelect?: boolean
    rowSelectable?: (row: T) => boolean
    clickable?: boolean
    rowClass?: (row: T, index: number) => unknown
    rowAttrs?: (row: T, index: number) => Record<string, unknown> | undefined
    card?: boolean
    reorderable?: boolean
    reorderDisabled?: boolean
    /** Enables the SDK column picker, persisted under this stable table key. */
    columnSettingsKey?: string
    /** Optional server/custom persistence; defaults to browser localStorage. */
    columnStorage?: TableColumnStorage
    /** The last load failed (any truthy value, e.g. useList's error). */
    error?: unknown
    errorText?: string
    /** Declared as a prop so the Retry button shows only when someone listens. */
    onRetry?: () => void
    /** Phone form: rows as cards (default) or the scrolling table. */
    mobile?: 'card' | 'scroll'
  }>(),
  {
    rowKey: 'id',
    expandToggle: true,
    selected: () => [],
    swipeSelect: true,
    mobile: 'card',
    emptyText: undefined,
    rowSelectable: undefined,
    rowClass: undefined,
    rowAttrs: undefined,
    defaultExpanded: undefined,
    error: undefined,
    errorText: undefined,
    onRetry: undefined
  }
)
const emit = defineEmits<{
  (e: 'row-click', row: T): void
  (e: 'expand', row: T, open: boolean): void
  (e: 'update:selected', keys: unknown[]): void
  (e: 'update:rows', rows: T[]): void
  (e: 'reorder', event: TableReorderEvent<T>): void
  (e: 'filter', column: TableColumn, value: string, row: T): void
}>()
const t = useUiT()
const columnState = useTableColumns(
  () => props.columns,
  () => props.columnSettingsKey,
  () => props.columnStorage
)
const visibleColumns = computed(() => (props.columnSettingsKey ? columnState.visibleColumns.value : props.columns))
const { compact } = useViewport()
const cardLayout = computed(() => compact.value && props.mobile === 'card')
const rootClass = computed(() => {
  if (props.card) return 'card overflow-hidden'
  if (cardLayout.value) return undefined
  return ['table-container', { 's-table-fit-expanded': props.fitExpandedToContainer }]
})
provide(TABLE_CARD, false)
const fields = computed(() => cardFields(visibleColumns.value, props.columns))
const table = ref<HTMLTableElement>()
const cards = ref<{ list?: HTMLElement }>()
const root = ref<HTMLElement>()
const inner = ref<HTMLElement>()
const clipped = useScrollClip(
  computed(() => (props.card ? inner.value : root.value)),
  table
)
const reorderHelp = useId()
const reorderMessage = ref('')
const reorder = useTableReorder(
  computed(() => cards.value?.list ?? table.value),
  props,
  event => {
    reorderMessage.value = t('ui.rowReordered', { from: event.from + 1, to: event.to + 1 })
    emit('update:rows', event.rows)
    emit('reorder', event)
  }
)

function keyOf(row: T, i: number): unknown {
  const k = row[props.rowKey]
  return k === undefined || k === null ? i : k
}
/** Rows with their key (rowKey, else the index), computed once per render. */
const keyed = computed(() => props.rows.map((row, i) => ({ row, i, key: keyOf(row, i) })))
/** Reorderable rows are not natively draggable, whatever rowAttrs says. */
const attrsOf = (row: T, i: number) => (props.reorderable ? { ...props.rowAttrs?.(row, i), draggable: false } : props.rowAttrs?.(row, i))

const { expanded, toggle, expansion } = useTableExpand(keyed, {
  expandable: () => !!props.expandable,
  single: () => !!props.expandSingle,
  initially: () => props.defaultExpanded,
  onExpand: (row, open) => emit('expand', row, open)
})
/** The props of a page slot rendered by the cards: a cell slot gets its row's expansion there too. */
function cardSlotProps(name: string | number, scope: Record<string, any> | undefined) {
  if (!scope || !String(name).startsWith('cell-')) return scope ?? {}
  return { ...scope, ...expansion(scope.row, keyOf(scope.row, scope.index)) }
}

const ALIGN = { left: 'text-left', right: 'text-right', center: 'text-center' }
const valueOf = (row: T, key: string): unknown => (key.includes('.') ? getPath(row, key) : row[key])

function cellStyle(c: TableColumn) {
  if (!c.ellipsis) return undefined
  return {
    maxWidth: typeof c.ellipsis === 'string' ? c.ellipsis : '16rem',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap'
  }
}

const { selectedSet, canSelect, selectableKeys, allSelected, someSelected, toggleAll, markShift, toggleRow, swipe } = useTableSelect(
  keyed,
  table,
  {
    selected: () => props.selected,
    rowSelectable: () => props.rowSelectable,
    swipe: () => !!props.selectable && props.swipeSelect && !props.loading,
    update: keys => emit('update:selected', keys)
  }
)

const chevrons = computed(() => !!props.expandable && props.expandToggle)
const span = computed(
  () => visibleColumns.value.length + Number(chevrons.value) + Number(props.selectable || false) + Number(props.reorderable || false)
)

function onRowKey(e: KeyboardEvent, row: T) {
  if (!props.clickable || e.target !== e.currentTarget) return
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    emit('row-click', row)
  }
}
</script>

<template>
  <component :is="card ? 'section' : 'div'" ref="root" :class="rootClass">
    <template v-if="reorderable">
      <span :id="reorderHelp" class="sr-only">{{ t('ui.reorderHelp') }}</span>
      <span class="sr-only" role="status" aria-live="polite">{{ reorderMessage }}</span>
    </template>
    <div v-if="columnSettingsKey || (card && $slots.toolbar)" class="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
      <slot name="toolbar" />
      <STableColumnSettings v-if="columnSettingsKey" :state="columnState" class="ms-auto" />
    </div>
    <TableCards
      v-if="cardLayout"
      ref="cards"
      :fields="fields"
      :rows="keyed"
      :loading="loading"
      :error="error"
      :error-text="errorText"
      :retry="onRetry"
      :empty-text="emptyText"
      :surface="card"
      :selectable="selectable"
      :selected="selectedSet"
      :can-select="canSelect"
      :all-selected="allSelected"
      :some-selected="someSelected"
      :none-selectable="!selectableKeys.length"
      :expandable="expandable"
      :expanded="expanded"
      :clickable="clickable"
      :reorder="reorderable ? reorder : undefined"
      :reorder-help="reorderHelp"
      :row-class="rowClass"
      :row-attrs="attrsOf"
      @row-click="emit('row-click', $event)"
      @select="toggleRow"
      @select-all="toggleAll"
      @expand="toggle"
      @filter="(column, value, row) => emit('filter', column, value, row)"
    >
      <template v-for="(_, name) in $slots" #[name]="scope">
        <slot :name="name" v-bind="cardSlotProps(name, scope)" />
      </template>
    </TableCards>
    <div
      v-else
      ref="inner"
      :class="card ? ['table-container s-table-in-card', { 's-table-fit-expanded': fitExpandedToContainer }] : 'contents'"
    >
      <table
        ref="table"
        class="table"
        data-reorder-list
        :class="{ 'table-dense': dense, 's-table-clipped': clipped }"
        :aria-busy="loading || undefined"
        @mousedown="selectable && swipe.mouseDown($event)"
        @click.capture="swipe.clickCapture"
      >
        <thead>
          <tr>
            <th v-if="reorderable" class="w-10" :aria-label="t('ui.reorderRows')" />
            <th v-if="selectable" class="w-10 !pr-0" :title="swipeSelect ? t('ui.bulkHint') : undefined">
              <SCheckbox
                bare
                :model-value="allSelected"
                :indeterminate="someSelected"
                :disabled="!selectableKeys.length"
                :aria-label="t('ui.selectAll')"
                @update:model-value="toggleAll"
              />
            </th>
            <th v-if="chevrons" class="w-8" />
            <th
              v-for="c in visibleColumns"
              :key="c.key"
              :style="c.width ? { width: c.width } : undefined"
              :class="[ALIGN[c.align ?? 'left'], c.headerClass, { 's-table-actions': c.key === 'actions', 's-table-grow': c.grow }]"
            >
              <slot :name="`head-${c.key}`" :column="c">
                {{ c.label }}
              </slot>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading && rows.length === 0">
            <td :colspan="span" class="py-10 text-center text-gray-400">
              <SSpinner />
            </td>
          </tr>
          <tr v-else-if="error && rows.length === 0">
            <td :colspan="span" class="py-10 text-center" data-testid="table-error">
              <slot name="error" :error="error">
                <TableLoadError :text="errorText" :retry="onRetry" />
              </slot>
            </td>
          </tr>
          <tr v-else-if="rows.length === 0">
            <td :colspan="span" class="py-10 text-center text-gray-400">
              <slot name="empty">
                {{ emptyText || t('ui.noData') }}
              </slot>
            </td>
          </tr>
          <tr v-if="error && rows.length > 0 && !loading">
            <td :colspan="span" class="!p-0" data-testid="table-error">
              <TableLoadError inline :class="dense ? 'px-3 py-1.5' : 'px-3 py-2.5'" :text="errorText" :retry="onRetry" />
            </td>
          </tr>
          <template v-for="{ row, i, key } in keyed" :key="String(key)">
            <tr
              v-bind="attrsOf(row, i)"
              :class="[
                loading ? 'opacity-60' : '',
                clickable ? 'cursor-pointer focus:outline-none focus-visible:bg-primary-50/60 dark:focus-visible:bg-primary-900/20' : '',
                selectable && selectedSet.has(key) ? 's-table-selected' : '',
                reorderable ? reorder.rowClass(i) : '',
                rowClass?.(row, i)
              ]"
              :data-reorder-row="reorderable ? i : undefined"
              :data-select-row="selectable ? i : undefined"
              :tabindex="clickable ? 0 : undefined"
              @click="emit('row-click', row)"
              @keydown="onRowKey($event, row)"
            >
              <td v-if="reorderable" class="w-10 !pr-0" @click.stop>
                <ReorderHandle :index="i" :reorder="reorder" :describedby="reorderHelp" />
              </td>
              <td v-if="selectable" class="s-table-select w-10 !pr-0" data-select-handle @click.capture="markShift" @click.stop>
                <SCheckbox
                  bare
                  :model-value="selectedSet.has(key)"
                  :disabled="!canSelect(row)"
                  :aria-label="t('ui.selectRow')"
                  @update:model-value="toggleRow(key, $event, i)"
                />
              </td>
              <td v-if="chevrons" class="w-8 !pr-0">
                <button
                  type="button"
                  class="rounded p-0.5 text-gray-400 hover:text-gray-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:hover:text-gray-200"
                  :aria-expanded="expanded.has(key)"
                  :aria-label="expanded.has(key) ? t('ui.collapseRow') : t('ui.expandRow')"
                  @click.stop="toggle(row, key)"
                >
                  <SIcon name="chevron-right" class="h-4 w-4 transition-transform" :class="expanded.has(key) ? 'rotate-90' : ''" />
                </button>
              </td>
              <td
                v-for="c in visibleColumns"
                :key="c.key"
                :class="[
                  ALIGN[c.align ?? 'left'],
                  c.class,
                  { 's-table-actions': c.key === 'actions', 'whitespace-nowrap': c.nowrap, 's-table-grow': c.grow }
                ]"
                :style="cellStyle(c)"
                :title="(c.ellipsis || c.grow) && !$slots[`cell-${c.key}`] ? displayValue(valueOf(row, c.key)) : undefined"
              >
                <slot
                  :name="`cell-${c.key}`"
                  :row="row"
                  :value="valueOf(row, c.key)"
                  :index="i"
                  layout="table"
                  v-bind="expansion(row, key)"
                >
                  <TableCell :column="c" :value="valueOf(row, c.key)" @filter="emit('filter', c, $event, row)" />
                </slot>
              </td>
            </tr>
            <tr v-if="expandable && expanded.has(key)" class="s-table-expand">
              <td :colspan="span" class="bg-gray-50/60 dark:bg-dark-900/40">
                <div v-if="fitExpandedToContainer" class="s-table-expand-content">
                  <slot name="expand" :row="row" />
                </div>
                <slot v-else name="expand" :row="row" />
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
    <div v-if="card && $slots.footer" class="border-t border-line px-4">
      <slot name="footer" />
    </div>
  </component>
</template>

<style scoped>
.table-container {
  max-width: 100%;
  min-width: 0;
  isolation: isolate;
}
.s-table-in-card {
  border: 0;
  border-radius: 0;
  background: transparent;
}
.table {
  border-collapse: separate;
  border-spacing: 0;
}
/* Rows and phone cards alike. */
:deep(.s-table-dragging) {
  opacity: 0.45;
}
.s-table-drop-before > td {
  border-top: 2px solid rgb(var(--primary-500, 20 184 166));
}
.s-table-drop-after > td {
  border-bottom: 2px solid rgb(var(--primary-500, 20 184 166));
}
.s-table-fit-expanded {
  container-type: inline-size;
}
.s-table-expand-content {
  position: sticky;
  left: 0;
  width: 100cqw;
  max-width: 100%;
}
.s-table-selected > td {
  background: rgb(20 184 166 / 0.06);
}
.s-table-select {
  cursor: pointer;
}
:global(.s-table-swipe-band) {
  position: fixed;
  z-index: 60;
  pointer-events: none;
  border: 1.5px solid rgb(var(--primary-500, 20 184 166) / 0.45);
  border-radius: 4px;
  background: rgb(var(--primary-500, 20 184 166) / 0.1);
}
/* A `grow` column takes the width the others leave and truncates to one
   line; a block / flex root of its cell slot is held to the cell width so
   its own truncation applies. */
.s-table-grow {
  width: 100%;
  max-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.s-table-grow > :deep(*) {
  max-width: 100%;
}
.s-table-actions {
  position: sticky;
  right: 0;
  z-index: 1;
  white-space: nowrap;
  background: rgb(var(--surface, 255 255 255));
}
.s-table-clipped > * > tr > .s-table-actions {
  box-shadow:
    -1px 0 0 rgb(var(--line, 229 231 235)),
    -6px 0 8px -8px #64748b;
}
.table th.s-table-actions {
  z-index: 2;
  background: rgb(var(--surface-2, 249 250 251));
}
.table tbody tr:hover > .s-table-actions {
  background: rgb(var(--surface-2, 249 250 251));
}
/* In a dialog or a drawer the sticky cell paints the overlay's surface,
   which in dark is the second one (OverlaySurface), not the page's. */
:global(.dark .modal-content td.s-table-actions) {
  background: rgb(var(--surface-2, 30 41 59));
}
</style>
