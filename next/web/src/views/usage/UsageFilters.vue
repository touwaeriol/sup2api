<script setup lang="ts">
// The search toolbar of the usage lists, after new-api's log filters: the time
// range (#range), the log type and the common fields on the first row, the
// rest behind the bar's More filters (usageSearchLayout), then the totals
// (#stats) with extra toggles (#actions, then the secondary #tools) beside
// Reset / Search. A pick (an entity picker's, a select's) applies at once,
// with any filter typed so far, and so does the time range slot; typed text
// applies on Search (or Enter) only. Parameters set off the first row (More
// filters, or ones the search keeps no field for, brought by a link or a
// table action) show as removable chips under the bar, a picked entity by
// its name.
// With `logTypes` the first filter picks what the page lists, as new-api's log
// type does; it applies at once like any pick. A type these fields do not
// search (`searchable` = false) brings its own in #filters. The `view` says
// whose usage is searched: an operator's search, the caller's own entities,
// or (`account`, CONTRACTS §103.5) the accounts in the caller's reach by their
// facts only, entities picked, never named.
// The filters sit in an SFilterBar: on a phone the range sits above it, the
// log type and the first-row fields stay in sight beside its Filters button
// and the More filters fold into its sheet, where a pick waits with the rest
// for Done, which applies the search; #tools go to the end of that sheet.
import { computed, reactive, useSlots, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SFilterBar, SFilterChips, SSelect, useViewport } from '@sub2api/ui'
import UsageFilterFields from './UsageFilterFields.vue'
import UsageSearchButtons from './UsageSearchButtons.vue'
import { chipParams, entityPatch, fieldParams, keyHoldsForUser, usageSearchLayout, useUsageSearchLabels } from './usageFilters'
import type { UsageView } from './view'

const props = withDefaults(defineProps<{
  modelValue: Record<string, unknown>
  view: UsageView
  /** The account filter, for an operator who may read accounts. */
  accounts?: boolean
  /** The request types (protocol ids) under the current search, for the type select. */
  protocolOptions?: string[]
  /** The models under the current search, for the model picker (besides the models it looks up). */
  modelOptions?: string[]
  /** What the page can list, for the log type select; none hides it. */
  logTypes?: Array<{ value: string; label: string }>
  logType?: string
  searchable?: boolean
}>(), { accounts: false, protocolOptions: () => [], modelOptions: () => [], logTypes: () => [], logType: '', searchable: true })
const emit = defineEmits<{ 'update:modelValue': [value: Record<string, string>]; 'update:logType': [value: string]; reset: [] }>()
const { t } = useI18n()
const { compact } = useViewport()
const slots = useSlots()
/** The stats and actions row; without either the reset and search buttons end the filter row. */
const bottomRow = computed(() => !!slots.stats || !!slots.actions)
const layout = computed(() => usageSearchLayout({ view: props.view, accounts: props.accounts, errors: props.logType === 'errors' }))
/** The caller's own records: the pickers and the names are of their own entities. */
const mine = computed(() => props.view === 'self')
const { label, valueLabel } = useUsageSearchLabels(() => props.protocolOptions, () => String(props.modelValue.protocol ?? ''), mine)
const isSet = (key: string) => String(props.modelValue[key] ?? '') !== ''
const draft = reactive<Record<string, string>>({})
watch(() => props.modelValue, value => {
  for (const key of layout.value.params) draft[key] = String(value[key] ?? '')
}, { deep: true, immediate: true })
/** The More filters in effect: the bar's badge, and on a phone its Filters button's (the sheet holds no other search field). */
const advancedCount = computed(() => (props.searchable ? layout.value.advanced.filter(field => fieldParams(field).some(isSet)).length : 0))
const chips = computed(() =>
  props.searchable
    ? layout.value.tagged.filter(isSet).map(key => ({ key, label: label(key), value: valueLabel(key, String(props.modelValue[key])) }))
    : []
)
/** Applies the parameters `keys` from the draft, or cleared. */
function emitParams(keys: string[], clear = false) {
  emit('update:modelValue', Object.fromEntries(keys.map(key => [key, clear ? '' : String(draft[key] ?? '').trim()])))
}
const apply = () => emitParams(layout.value.params)
function reset() {
  emitParams(layout.value.params, true)
  emit('reset')
}
function pickLogType(value: unknown) {
  emit('update:logType', String(value))
  apply()
}
/**
 * Settles the draft's API key for the user just picked: cleared unless it
 * holds for them. False when the user or the key changed while that was
 * looked up: they are left alone, for the pick that changed them to apply.
 */
async function settleKey(): Promise<boolean> {
  const asked = { ...draft }
  const holds = await keyHoldsForUser(asked)
  if (['user_id', 'api_key_id'].some(key => draft[key] !== asked[key])) return false
  if (!holds) Object.assign(draft, entityPatch('api_key', {}))
  return true
}
/**
 * Takes a pick into the draft and applies the search at once, as the log type
 * does; `held` leaves it to Done (the phone's Filters sheet). A new user keeps
 * the API key only when it is theirs, which the search waits to know.
 */
async function pick(patch: Record<string, string>, held = false) {
  Object.assign(draft, patch)
  if ('user_id' in patch && !(await settleKey())) return
  if (!held) apply()
}
</script>

<template>
  <form
    class="mb-4 w-full rounded-xl border border-line bg-surface p-3 shadow-sm"
    @submit.prevent="apply"
  >
    <div class="flex flex-wrap items-start gap-2">
      <!-- On a phone the range takes its own row above the bar; on a wide screen it opens the bar's row. -->
      <div
        v-if="compact && $slots.range"
        class="min-w-0 max-w-full"
      >
        <slot name="range" />
      </div>
      <SFilterBar
        :card="false"
        class="min-w-[16rem] flex-1"
        :aria-label="t('stats.filters')"
        :advanced-count="advancedCount"
        @done="apply"
      >
        <template
          v-if="$slots.range || logTypes.length || searchable"
          #primary
        >
          <slot
            v-if="!compact"
            name="range"
          />
          <!-- Two to a row beside the Filters button on a phone, an odd one out taking its row and so does a picker
               that is set (data-set), to show the name it picked; on a wide screen the search fields share the row
               (up to a width: one that wraps does not stretch across the bar), while a type with its own filters
               keeps its selects at their size. -->
          <div
            class="flex flex-wrap gap-2 md:contents max-md:[&>*]:min-w-0 max-md:[&>*]:flex-[1_1_6.5rem] max-md:[&>[data-set]]:basis-full"
            :class="searchable ? 'md:[&>*]:max-w-[22rem] md:[&>*]:flex-[1_1_9rem]' : 'md:[&>*]:flex-[0_1_12rem]'"
          >
            <SSelect
              v-if="logTypes.length"
              :model-value="logType"
              size="sm"
              :aria-label="t('usage.search.logType')"
              :options="logTypes"
              data-testid="filter-log-type"
              @update:model-value="pickLogType"
            />
            <UsageFilterFields
              v-if="searchable"
              :fields="layout.primary"
              :values="draft"
              :mine="mine"
              :text-search="view !== 'account'"
              :model-options="modelOptions"
              @update="Object.assign(draft, $event)"
              @pick="pick"
            />
          </div>
        </template>
        <div
          v-if="$slots.filters"
          class="md:contents md:[&>*]:flex-[0_1_12rem]"
        >
          <slot name="filters" />
        </div>
        <template
          v-if="searchable"
          #advanced
        >
          <UsageFilterFields
            :fields="layout.advanced"
            :values="draft"
            :mine="mine"
            :text-search="view !== 'account'"
            :protocol-options="protocolOptions"
            @update="Object.assign(draft, $event)"
            @pick="pick($event, compact)"
          />
        </template>
        <template
          v-if="compact && $slots.tools"
          #end
        >
          <slot name="tools" />
        </template>
      </SFilterBar>
      <div
        v-if="!searchable"
        class="ms-auto flex flex-wrap items-center justify-end gap-2"
      >
        <slot name="actions" />
        <slot
          v-if="!compact"
          name="tools"
        />
      </div>
      <!-- With no stats or actions the reset and search buttons end this row instead of taking one of their own. -->
      <div
        v-if="searchable && !bottomRow"
        class="ms-auto flex items-center gap-2"
      >
        <slot
          v-if="!compact"
          name="tools"
        />
        <UsageSearchButtons @reset="reset" />
      </div>
    </div>
    <SFilterChips
      class="mt-2"
      :items="chips"
      @remove="emitParams(chipParams($event), true)"
      @clear="emitParams(layout.tagged, true)"
    />
    <div
      v-if="searchable && bottomRow"
      class="mt-3 flex flex-wrap items-center gap-2"
    >
      <slot name="stats" />
      <div class="ms-auto flex flex-wrap items-center justify-end gap-2">
        <slot name="actions" />
        <slot
          v-if="!compact"
          name="tools"
        />
        <UsageSearchButtons @reset="reset" />
      </div>
    </div>
  </form>
</template>
