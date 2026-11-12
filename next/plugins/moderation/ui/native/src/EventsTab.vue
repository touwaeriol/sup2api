<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  SAlert,
  SBulkBar,
  SButton,
  SCard,
  SDateRange,
  SDateTime,
  SField,
  SFilterBar,
  SFilterChips,
  SIcon,
  SInput,
  SPagination,
  SSelect,
  STable,
  STableColumnSettings,
  useTableColumns,
  useViewport,
  type RangeKey,
  type TableColumn
} from '@sub2api/ui'
import ResultBadge from './ResultBadge.vue'
import ReviewActions from './ReviewActions.vue'
import WatchDetail from './WatchDetail.vue'
import ScoreChips from './ScoreChips.vue'
import StagePath from './StagePath.vue'
import EventDetail from './EventDetail.vue'
import FlowRef from './FlowRef.vue'
import UserRef from './UserRef.vue'
import VerdictBadge from './VerdictBadge.vue'
import { useCatalog } from './catalog'
import { useCategoryOptions } from './categories'
import { filterChips, flowQuery, flowValue, isSet, pairOptions, type FilterField } from './eventFilters'
import { applyFilters, defaultGroupCandidate } from './forms'
import { enumLabel, enumOptions, idText, isSuspicious, ms, num } from './format'
import { canManage, errorMessage, useEventList, useModHost } from './host'
import type { EventFilter, ReviewStatus } from './types'
import { useBulkReview } from './useBulkReview'
import { useUserFilter } from './users'

// Records (new-api records): a filter bar + paginated table (useList). The
// bar shows the common filters (search, time, result, user: picked, or a
// name search resolved to ids through the core); the others fold under "more
// filters". Users, groups and API keys are picked with the host's
// EntityPicker. The applied ones of those, and the conditions only a link from
// the detail sets (request, endpoint, session), are removable chips under
// the bar. Each table cell keeps to one line; the secondary facts are in
// tooltips and optional columns. A row opens the detail modal (?record=<id>
// opens one directly); a user opens the user detail drawer; selected
// suspicious rows (hit, block) can be reviewed in bulk.
const props = defineProps<{ preset?: { user_id: number; n: number } | null }>()
const host = useModHost()
const t = host.t
const { EntityPicker } = host.components
const manage = computed(() => canManage())
const catalog = useCatalog()
const { compact } = useViewport()

const emptyForm = () => ({
  result: 'suspicious',
  // the user picker: a picked id or a name search, never both
  user_id: '',
  user_text: '',
  q: '',
  // more filters; flow stands for two query parameters (eventFilters)
  review_status: '',
  severity: '',
  category: '',
  flow: '',
  group_id: '',
  api_key_id: '',
  timing: '',
  watch_level: '',
  replayed: '',
  // set only from the record detail
  request_id: '',
  endpoint: '',
  session_fp: ''
})
type FormKey = keyof ReturnType<typeof emptyForm>
const form = reactive(emptyForm())
if (props.preset) form.user_id = String(props.preset.user_id)
const range = ref<RangeKey>('all')
const from = ref('')
const to = ref('')
const rangeKeys: RangeKey[] = ['all', 'today', '7d', '30d', 'custom']

/** The user filter resolved to user_id / user_ids. */
const users = useUserFilter(props.preset ? [props.preset.user_id] : null)

function query() {
  return {
    result: form.result,
    timing: form.timing,
    review_status: form.review_status,
    severity: form.severity,
    watch_level: form.watch_level,
    replayed: form.replayed,
    category: form.category,
    ...flowQuery(form.flow),
    group_id: form.group_id,
    api_key_id: form.api_key_id,
    user_id: users.query.value.user_id ?? '',
    user_ids: users.query.value.user_ids ?? '',
    request_id: form.request_id.trim(),
    endpoint: form.endpoint.trim(),
    session_fp: form.session_fp.trim(),
    q: form.q.trim(),
    from: from.value,
    to: to.value
  }
}

const list = useEventList(query())
const error = computed(() => (list.error.value ? errorMessage(list.error.value, t('events.loadFailed')) : ''))

const apply = () => applyFilters(list.filters, query())

async function search() {
  if ((await users.resolve(form.user_id, form.user_text)) && !apply()) list.reload()
}

/**
 * The user picker changed. The key picker lists only the picked user's keys,
 * and this page cannot tell whose key an earlier pick is: a new user filter
 * drops it (for another user it would match nothing), clearing the user
 * filter keeps it.
 */
function pickUser() {
  if (form.user_id || form.user_text) form.api_key_id = ''
  search()
}

function reset(userId = '') {
  Object.assign(form, emptyForm(), { user_id: userId })
  range.value = 'all'
  from.value = ''
  to.value = ''
  search()
}

const resultOptions = computed(() => [
  { value: '', label: t('events.anyOf', { name: t('events.result') }) },
  { value: 'suspicious', label: t('events.suspicious') },
  ...enumOptions('result', ['block', 'hit', 'overturned', 'error', 'pass'])
])
const categories = useCategoryOptions()

interface Field extends FilterField<FormKey> {
  searchable?: boolean
  /** Picked with the EntityPicker instead: its kind, lookup scope and the page's own candidates. */
  picker?: { kind: 'group' | 'api_key'; scope?: Record<string, string>; extra?: Array<{ id: number; name: string }> }
}
const advancedFields = computed<Field[]>(() => {
  const fields: Field[] = [
    { key: 'review_status', label: t('events.conclusion'), options: enumOptions('review', ['pending', 'confirmed', 'false_positive']) },
    { key: 'severity', label: t('events.severity'), options: enumOptions('severity', ['high', 'medium', 'low']) },
    { key: 'category', label: t('events.category'), options: categories.value, searchable: true },
    {
      key: 'flow',
      label: t('events.col.flow'),
      searchable: true,
      // versions 1…current of each flow, newest first
      options: catalog.state.flows.map((f) => ({
        value: null,
        label: f.name,
        options: pairOptions(f.id, [{ value: '', label: t('events.allVersions') }, ...Array.from({ length: f.version }, (_, i) => ({ value: f.version - i, label: `v${f.version - i}` }))])
      }))
    },
    { key: 'group_id', label: t('stats.group'), text: (id) => catalog.groupName(Number(id)), picker: { kind: 'group', extra: defaultGroupCandidate(t('defaultGroup')) } },
    // only the keys of the picked user, when one is picked
    { key: 'api_key_id', label: t('events.apiKey'), text: (id) => idText(Number(id)), picker: { kind: 'api_key', scope: { user_id: form.user_id } } },
    { key: 'timing', label: t('events.timing'), options: enumOptions('timing', ['pre', 'post']) },
    { key: 'watch_level', label: t('events.watch'), options: [{ value: 'any', label: t('events.watched') }, ...enumOptions('level', ['observe', 'enforce'])] },
    {
      key: 'replayed',
      label: t('replay.filter'),
      options: [
        { value: 'true', label: t('replay.only') },
        { value: 'false', label: t('replay.without') }
      ]
    }
  ]
  return fields.map((f) => (f.options ? { ...f, options: [{ value: '', label: t('events.anyOf', { name: f.label }) }, ...f.options] } : f))
})
const hiddenFields = computed<Field[]>(() => [
  { key: 'request_id', label: t('events.requestId') },
  { key: 'endpoint', label: t('events.endpoint') },
  { key: 'session_fp', label: t('events.session') }
])

// The chips and the counts show the applied filters (what the list shows),
// not what is typed and not yet searched.
const applied = computed<Record<string, unknown>>(() => ({ ...list.filters, flow: flowValue(list.filters) }))
const chips = computed(() => filterChips([...advancedFields.value, ...hiddenFields.value], applied.value))
const advancedCount = computed(() => advancedFields.value.filter((f) => isSet(applied.value[f.key])).length)
/** The default-slot filters off their default (SFilterBar adds advancedCount for the phone Filters badge). */
const activeCount = computed(() => {
  const f = applied.value
  return [isSet(f.from) || isSet(f.to), f.result !== 'suspicious', isSet(f.user_id) || isSet(f.user_ids)].filter(Boolean).length
})
/** Reset is enabled while any filter is applied (the chips hold the more filters and the hidden ones) or the form is off its default. */
const dirty = computed(() => activeCount.value + chips.value.length > 0 || isSet(applied.value.q) || Object.entries(emptyForm()).some(([k, v]) => form[k as FormKey] !== v))

function removeChip(key: string) {
  form[key as FormKey] = ''
  apply()
}
function clearChips() {
  for (const f of [...advancedFields.value, ...hiddenFields.value]) form[f.key] = ''
  apply()
}

// Selects apply at once (the pickers on their `change`); typed text on search.
// Compared as a string: a catalog refresh that rebuilds the fields with the
// same values does not apply what is typed.
watch(() => JSON.stringify([form.result, from.value, to.value, ...advancedFields.value.filter((f) => f.options).map((f) => form[f.key])]), apply)
watch(
  () => props.preset,
  (p) => p && reset(String(p.user_id))
)

defineExpose({ reload: list.reload })

const columns = computed<TableColumn[]>(() => [
  { key: 'created_at', label: t('events.col.time'), nowrap: true, mobile: 'subtitle' },
  { key: 'result', label: t('events.col.result'), nowrap: true, mobile: 'status' },
  { key: 'timing', label: t('events.timing'), nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'verdict', label: t('events.col.verdict'), nowrap: true, mobile: 'meta' },
  { key: 'scores', label: t('events.col.categories'), nowrap: true, mobile: 'meta' },
  { key: 'stages', label: t('events.col.stages'), nowrap: true, mobile: 'meta' },
  { key: 'text_excerpt', label: t('events.col.text'), grow: true, mobile: 'title' },
  { key: 'user_id', label: t('events.col.user'), nowrap: true, mobile: 'subtitle' },
  { key: 'group_id', label: t('stats.group'), ellipsis: '10rem', defaultHidden: true, mobile: 'detail' },
  { key: 'api_key_id', label: t('events.apiKey'), nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'model', label: t('events.col.model'), ellipsis: '12rem', mobile: 'meta' },
  { key: 'endpoint', label: t('events.endpoint'), ellipsis: '12rem', defaultHidden: true, mobile: 'detail' },
  { key: 'flow', label: t('events.col.flow'), ellipsis: '12rem', mobile: 'detail' },
  { key: 'latency_ms', label: t('events.col.latency'), align: 'right', nowrap: true, mobile: 'detail' }
])
const columnState = useTableColumns(columns, 'moderation-events')

// The open record follows ?record=<id>, so a record can be linked to.
const selected = ref<number | null>(null)
const detailOpen = ref(false)
function open(id: number) {
  selected.value = id
  detailOpen.value = true
}
const deepLink = Number(host.router.currentRoute.value.query.record)
if (Number.isSafeInteger(deepLink) && deepLink > 0) open(deepLink)
watch([detailOpen, selected], ([isOpen, id]) => {
  const route = host.router.currentRoute.value
  const record = isOpen && id ? String(id) : undefined
  if (route.query.record !== record) host.router.replace({ query: { ...route.query, record } })
})

function onDeleted(id: number) {
  list.items.value = list.items.value.filter((r) => r.id !== id)
  list.total.value = Math.max(0, list.total.value - 1)
  if (!list.items.value.length && list.page.value > 1) list.page.value--
}

function onReviewed(ids: number[], status: ReviewStatus) {
  for (const r of list.items.value) if (ids.includes(r.id)) r.review_status = status
}

function filterUser(id: number) {
  userOpen.value = false
  Object.assign(form, { user_id: String(id), user_text: '' })
  search()
}

/**
 * From the record detail: the records of its endpoint or sticky session, or
 * of the request a replayed hit took its verdict from (alone: that request
 * may be another user's, of any result and time).
 */
function filterBy(q: EventFilter) {
  if (q.request_id) {
    range.value = 'all'
    from.value = to.value = ''
    Object.assign(form, emptyForm(), { result: '' })
  }
  Object.assign(form, q)
  search()
}

// User detail drawer (GET /users/:user_id), for any user of a record.
const userOpen = ref(false)
const userId = ref<number | null>(null)
function openUser(id: number) {
  userId.value = id
  userOpen.value = true
}

const { checked, reviewing, review } = useBulkReview(onReviewed)
/** rows of the page that can be reviewed (suspicious) */
const reviewable = computed(() => list.items.value.filter(isSuspicious))
watch(() => list.items.value, () => (checked.value = []))
</script>

<template>
  <div>
    <form @submit.prevent="search">
      <SFilterBar :aria-label="t('filter')" :active-count="activeCount" :advanced-count="advancedCount" :dirty="dirty" :on-reset="reset" @done="search">
        <template #primary>
          <SInput v-model="form.q" type="search" size="sm" icon="search" class="mod-filter-q" :placeholder="t('events.q')" :aria-label="t('events.q')" />
        </template>
        <SDateRange v-model:range="range" v-model:from="from" v-model:to="to" :keys="rangeKeys" size="sm" :label="false" />
        <SSelect v-model="form.result" size="sm" class="mod-filter-md" :options="resultOptions" :aria-label="t('events.result')" :title="t('events.result')" />
        <SField :error="users.error.value" class="mod-filter-lg">
          <EntityPicker v-model:id="form.user_id" v-model:text="form.user_text" kind="user" size="sm" :placeholder="t('events.user')" @change="pickUser" />
        </SField>
        <template #advanced>
          <template v-for="f in advancedFields" :key="f.key">
            <SSelect v-if="f.options" v-model="form[f.key]" size="sm" :options="f.options" :searchable="f.searchable" :aria-label="f.label" :title="f.label" />
            <EntityPicker v-else-if="f.picker" v-model:id="form[f.key]" v-bind="f.picker" size="sm" :placeholder="f.label" :title="f.label" @change="apply" />
          </template>
        </template>
        <template #end>
          <!-- The column settings sit in the search bar, not on a row of their own above the table. -->
          <STableColumnSettings :state="columnState" />
          <SButton v-if="!compact" type="submit" variant="primary" size="sm" icon="search" :loading="list.loading.value">{{ t('events.search') }}</SButton>
        </template>
      </SFilterBar>
    </form>
    <SFilterChips :items="chips" class="mod-filter-chips" @remove="removeChip" @clear="clearChips" />

    <SAlert v-if="error" tone="danger" class="mod-section">{{ error }}</SAlert>

    <SBulkBar
      v-if="manage"
      :count="checked.length"
      :total="list.total.value"
      :page-selected="checked.length > 0 && checked.length === reviewable.length"
      class="mod-section"
      @select-page="checked = reviewable.map((r) => r.id)"
      @clear="checked = []"
    >
      <template #idle>{{ t('review.hint') }}</template>
      <ReviewActions :loading="reviewing" @review="review" />
    </SBulkBar>

    <SCard :padded="false" class="mod-section">
      <STable
        v-model:selected="checked"
        :columns="columnState.visibleColumns.value"
        :rows="list.items.value"
        :loading="list.loading.value"
        :empty-text="t('events.empty')"
        :selectable="manage"
        :row-selectable="isSuspicious"
        dense
        clickable
        @row-click="open($event.id)"
      >
        <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="time" /></template>
        <template #cell-result="{ row }">
          <span :title="row.timing ? enumLabel('timing', row.timing) : undefined"><ResultBadge :result="row.result" :replayed="row.replayed" /></span>
        </template>
        <template #cell-timing="{ row }">{{ enumLabel('timing', row.timing) }}</template>
        <template #cell-verdict="{ row }"><VerdictBadge :event="row" /></template>
        <template #cell-scores="{ row }"><ScoreChips :trace="row.trace" /></template>
        <template #cell-stages="{ row }"><StagePath :trace="row.trace" /></template>
        <template #cell-text_excerpt="{ row }">
          <span class="mod-cell-line mod-text-cell">
            <span class="mod-ellipsis" :title="row.text_excerpt">{{ row.text_excerpt || '—' }}</span>
            <span v-if="row.image_count" class="mod-image-count" :title="t('events.images', { n: row.image_count })">
              <SIcon name="image" />
              {{ row.image_count }}
            </span>
          </span>
        </template>
        <template #cell-user_id="{ row }">
          <span class="mod-cell-line">
            <UserRef :user-id="row.user_id" link :title="t('events.openUser')" @click="openUser" />
            <SButton
              v-if="row.user_id"
              size="xs"
              variant="ghost"
              icon="filter"
              icon-only
              class="mod-row-tool"
              :aria-label="t('events.filterUser')"
              :title="t('events.filterUser')"
              @click.stop="filterUser(row.user_id)"
            />
          </span>
        </template>
        <template #cell-group_id="{ row }">{{ catalog.groupName(row.group_id) }}</template>
        <template #cell-api_key_id="{ row }"><span class="mod-num">{{ idText(row.api_key_id) }}</span></template>
        <template #cell-model="{ row }">
          <span :title="row.endpoint ? `${row.model} · ${row.endpoint}` : row.model">{{ row.model || '—' }}</span>
        </template>
        <template #cell-flow="{ row }"><FlowRef :flow-id="row.flow_id" :version="row.flow_version" compact /></template>
        <template #cell-latency_ms="{ row }">
          <span class="mod-num" :title="row.queue_ms ? t('detail.queued', { n: num(row.queue_ms) }) : undefined">{{ ms(row.latency_ms) }}</span>
        </template>
      </STable>
      <div class="mod-pager">
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </div>
    </SCard>

    <EventDetail v-model:open="detailOpen" :event-id="selected" filterable @deleted="onDeleted" @reviewed="onReviewed" @show-user="filterUser" @open-user="openUser" @filter="filterBy" />
    <WatchDetail v-model:open="userOpen" :user-id="userId" @show-user="filterUser" />
  </div>
</template>
