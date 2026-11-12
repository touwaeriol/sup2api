<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  SAlert,
  SBadge,
  SButton,
  SCard,
  SCheckbox,
  SDateTime,
  SDropdown,
  SField,
  SFilterBar,
  SFilterChips,
  SHint,
  SPagination,
  SRowAction,
  SSelect,
  STable,
  STableColumnSettings,
  useTableColumns,
  useViewport,
  type SelectOption,
  type TableColumn
} from '@sub2api/ui'
import WatchDetail from './WatchDetail.vue'
import WatchEditModal from './WatchEditModal.vue'
import WatchActionDialog from './WatchActionDialog.vue'
import CategoryChips from './CategoryChips.vue'
import FlowRef from './FlowRef.vue'
import UserListBadge from './UserListBadge.vue'
import UserRef from './UserRef.vue'
import { useCatalog } from './catalog'
import { useCategoryOptions } from './categories'
import { filterChips, type FilterField } from './eventFilters'
import { applyFilters } from './forms'
import { enumLabel, enumOptions, joinList, num } from './format'
import { canManage, errorMessage, useModHost, useWatchList } from './host'
import { watchNew } from './status'
import { levelTone } from './tones'
import type { Watch, WatchItem, WatchLevel, WatchListMeta } from './types'
import { useUserFilter } from './users'
import { levelRefused, userListHint, type WatchAction } from './watchLevels'

// Watch list (new-api watchlist): observe = moderated in the background and
// counted; enforce = moderated before the upstream call, a hit is refused.
// Filters, sort, header counts and per-user aggregates come from GET
// /watchlist, with each user's allowlist / blocklist mark of the settings:
// an allowlisted user is never moderated first whatever the level, a
// blocklisted user always. The filter bar holds the common filters (user,
// picked with the host's EntityPicker; level, sort, new records only), the
// others fold under "more filters" (applied ones are chips under the bar).
// Each table cell keeps to one line; the level changes and the removal are
// row actions, confirmed in a dialog.
const emit = defineEmits<{ (e: 'show-user', userId: number): void }>()
const host = useModHost()
const t = host.t
const { EntityPicker } = host.components
const manage = computed(() => canManage())
const catalog = useCatalog()
const { compact } = useViewport()

const meta = ref<WatchListMeta | null>(null)
// user_id / user_text: the user picker's picked id or name search, never both
const emptyFilter = () => ({ level: '', new_only: false, category: '', sort: 'new', user_id: '', user_text: '' })
const filter = reactive(emptyFilter())
const users = useUserFilter()
const query = () => ({ level: filter.level, new_only: filter.new_only || '', category: filter.category, sort: filter.sort, user_id: users.query.value.user_id ?? '', user_ids: users.query.value.user_ids ?? '' })

const list = useWatchList(query(), (m) => (meta.value = m))
const error = computed(() => (list.error.value ? errorMessage(list.error.value, t('watch.loadFailed')) : ''))
const apply = () => applyFilters(list.filters, query())
watch(() => [filter.level, filter.new_only, filter.category, filter.sort], apply)

async function search() {
  if ((await users.resolve(filter.user_id, filter.user_text)) && !apply()) list.reload()
}
function reset() {
  Object.assign(filter, emptyFilter())
  search()
}
defineExpose({ reload: list.reload })

/** A select without a label: its first option reads "<name>: all". */
const anyOf = (name: string, options: SelectOption[]) => [{ value: '', label: t('events.anyOf', { name }) }, ...options]
const levelOptions = computed(() => anyOf(t('watch.col.level'), enumOptions('level', ['observe', 'enforce'])))
const sortOptions = computed(() => enumOptions('watchList.sort', ['new', 'last_hit', 'severity', 'total']).map((o) => ({ ...o, label: host.i18n.t('ui.filterChip', { label: t('watchList.sortBy'), value: o.label }) })))
const categories = useCategoryOptions()
const categoryOptions = computed(() => anyOf(t('watchList.category'), categories.value))

// The "more filters"; the ones in effect are removable chips under the bar.
const advanced = computed<Array<FilterField<'category'> & { searchable?: boolean }>>(() => [
  { key: 'category', label: t('watchList.category'), options: categoryOptions.value, searchable: true }
])
const chips = computed(() => filterChips(advanced.value, filter))
/** Bar filters off their default (the phone sheet adds the chips to its badge). */
const activeCount = computed(() => [filter.level, filter.new_only, filter.sort !== 'new'].filter(Boolean).length)
const dirty = computed(() => activeCount.value + chips.value.length > 0 || filter.user_id !== '' || filter.user_text !== '')
function removeChip(key: string) {
  filter[key as 'category'] = ''
}
function clearChips() {
  for (const f of advanced.value) filter[f.key] = ''
}

const sevText = (r: WatchItem) => `${t('watchList.sev.high', { n: r.by_severity.high })} · ${t('watchList.sev.medium', { n: r.by_severity.medium })} · ${t('watchList.sev.low', { n: r.by_severity.low })}`

const columns = computed<TableColumn[]>(() => {
  const c: TableColumn[] = [
    { key: 'user_id', label: t('watch.col.user'), nowrap: true, hideable: false, mobile: 'title' },
    { key: 'level', label: t('watch.col.level'), nowrap: true, mobile: 'status' },
    { key: 'suspicious', label: t('watchList.col.suspicious'), align: 'right', nowrap: true, mobile: 'meta' },
    { key: 'severity', label: t('events.severity'), nowrap: true, defaultHidden: true, mobile: 'detail' },
    { key: 'categories', label: t('watchList.col.categories'), nowrap: true, mobile: 'meta' },
    { key: 'group', label: t('watchList.col.group'), ellipsis: '10rem', mobile: 'detail' },
    { key: 'flow', label: t('events.col.flow'), ellipsis: '12rem', mobile: 'detail' },
    { key: 'last_hit_at', label: t('watch.col.lastHit'), nowrap: true, mobile: 'meta' },
    { key: 'expires_at', label: t('watch.col.expires'), nowrap: true, mobile: 'detail' },
    { key: 'source', label: t('watch.col.source'), nowrap: true, defaultHidden: true, mobile: 'detail' },
    { key: 'violation_count', label: t('watch.col.violations'), align: 'right', nowrap: true, defaultHidden: true, mobile: 'detail' },
    { key: 'note', label: t('watch.col.note'), grow: true, defaultHidden: true, mobile: 'detail' }
  ]
  if (manage.value) c.push({ key: 'actions', label: t('actions'), align: 'right', hideable: false })
  return c
})
const columnState = useTableColumns(columns, 'moderation-watch')

// ---- add / edit
const editOpen = ref(false)
const editing = ref<WatchItem | null>(null)
function openEdit(w: WatchItem | null) {
  editing.value = w
  editOpen.value = true
}

// ---- row actions: switch the level, edit; remove under "more"
const otherLevel = (r: WatchItem): WatchLevel => (r.level === 'enforce' ? 'observe' : 'enforce')
const acting = ref<WatchItem | null>(null)
const action = ref<WatchAction | null>(null)
function ask(r: WatchItem, a: WatchAction) {
  acting.value = r
  action.value = a
}
const moreActions = computed(() => [{ key: 'remove', label: t('watch.remove'), danger: true }])

// ---- detail drawer
// The drawer is the address: ?tab=watch&user=<id> opens it, closing it drops
// user, and leaving the section closes it (useSection drops user). Other
// sections open a user with that address too (ModerationDashboard
// openWatch), so nothing here races a section change.
const route = host.router.currentRoute
const linkedUser = computed(() => {
  const n = Number(route.value.query.tab === 'watch' ? route.value.query.user : NaN)
  return Number.isSafeInteger(n) && n > 0 ? n : null
})
/** The user shown, kept while the drawer closes. */
const detailUser = ref<number | null>(null)
watch(linkedUser, (id) => id && (detailUser.value = id), { immediate: true })
const detailOpen = computed({
  get: () => linkedUser.value !== null,
  set: (open) => !open && host.router.replace({ query: { ...route.value.query, user: undefined } })
})
const detailItem = computed(() => list.items.value.find((x) => x.user_id === detailUser.value) ?? null)
const openDetail = (userId: number) => host.router.push({ query: { ...route.value.query, tab: 'watch', user: String(userId) } })

/**
 * Keeps a row in step with the drawer: an entry patches its row; null (the
 * user left the list, or records were reviewed) reloads the list.
 */
function patchRow(userId: number | null, w: Partial<Watch> | null) {
  const i = list.items.value.findIndex((x) => x.user_id === userId)
  if (w && i >= 0) list.items.value[i] = { ...list.items.value[i], ...w }
  else if (!w) list.reload()
}
/** A row's level change or removal: patch the row at once, then reload for the header counts. */
function onAction(w: Watch | null) {
  if (w) patchRow(acting.value?.user_id ?? null, w)
  void list.reload()
}
/** The user's records: the section change closes the drawer. */
const showUser = (id: number) => emit('show-user', id)
</script>

<template>
  <div>
    <SHint class="mod-hint">{{ t('watch.hint') }}</SHint>

    <form @submit.prevent="search">
      <SFilterBar :aria-label="t('filter')" :active-count="activeCount" :advanced-count="chips.length" :dirty="dirty" :on-reset="reset" @done="search">
        <template #primary>
          <SField :error="users.error.value" class="mod-filter-lg">
            <EntityPicker v-model:id="filter.user_id" v-model:text="filter.user_text" kind="user" size="sm" :placeholder="t('events.user')" @change="search" />
          </SField>
        </template>
        <SSelect v-model="filter.level" size="sm" class="mod-filter-sm" :options="levelOptions" :aria-label="t('watch.col.level')" :title="t('watch.col.level')" />
        <SSelect v-model="filter.sort" size="sm" class="mod-filter-sm" :options="sortOptions" :aria-label="t('watchList.sortBy')" :title="t('watchList.sortBy')" />
        <SCheckbox v-model="filter.new_only" :label="t('watchList.newOnly')" />
        <template #advanced>
          <SSelect v-for="f in advanced" :key="f.key" v-model="filter[f.key]" size="sm" :options="f.options!" :searchable="f.searchable" :aria-label="f.label" :title="f.label" />
        </template>
        <template #end>
          <STableColumnSettings :state="columnState" />
          <SButton v-if="!compact" type="submit" variant="primary" size="sm" icon="search" :loading="list.loading.value">{{ t('events.search') }}</SButton>
        </template>
      </SFilterBar>
    </form>
    <SFilterChips :items="chips" class="mod-filter-chips" @remove="removeChip" @clear="clearChips" />

    <!-- counts on the left, the page actions on the right: not mixed into the filters -->
    <div class="mod-counts mod-section">
      <template v-if="meta">
        <SBadge tone="info">{{ t('watchList.counts.watching', { n: num(meta.counts.watching) }) }}</SBadge>
        <SBadge tone="warning">{{ t('watchList.counts.withNew', { n: num(watchNew(meta.counts).total) }) }}</SBadge>
        <SBadge tone="danger">{{ t('watchList.counts.blocking', { n: num(meta.counts.blocking) }) }}</SBadge>
      </template>
      <span class="mod-ml-auto mod-inline mod-gap">
        <SButton v-if="manage" size="sm" variant="primary" icon="plus" @click="openEdit(null)">{{ t('watch.add') }}</SButton>
      </span>
    </div>

    <SAlert v-if="error" tone="danger" class="mod-section">{{ error }}</SAlert>

    <SCard :padded="false" class="mod-section">
      <STable
        :columns="columnState.visibleColumns.value"
        :rows="list.items.value"
        :loading="list.loading.value"
        row-key="user_id"
        :empty-text="t('watch.empty')"
        dense
        clickable
        @row-click="openDetail($event.user_id)"
      >
        <template #cell-user_id="{ row }">
          <span class="mod-cell-line">
            <UserRef :user-id="row.user_id" link :title="t('watch.showRecords')" @click="emit('show-user', $event)" />
            <UserListBadge :allowlisted="row.allowlisted" :blocklisted="row.blocklisted" size="xs" />
            <SBadge v-if="row.hits_since_seen" tone="warning" size="xs">{{ t('watchList.newHits', { n: row.hits_since_seen }) }}</SBadge>
          </span>
        </template>
        <template #cell-level="{ row }">
          <SBadge :tone="levelTone(row.level)" :title="userListHint(row) || undefined">{{ enumLabel('level', row.level) }}</SBadge>
        </template>
        <template #cell-suspicious="{ row }">
          <span class="mod-num" :title="sevText(row)">{{ num(row.suspicious_total) }}</span>
        </template>
        <template #cell-severity="{ row }"><span class="mod-num">{{ sevText(row) }}</span></template>
        <template #cell-categories="{ row }"><CategoryChips :categories="row.top_categories" :other="row.top_other" :max="1" /></template>
        <template #cell-group="{ row }">
          <span v-if="row.group_id || row.flow" :title="joinList([row.group_id, ...row.other_groups].map(catalog.groupName))">
            {{ catalog.groupName(row.group_id) }}<span v-if="row.other_groups.length" class="mod-num mod-sub"> +{{ row.other_groups.length }}</span>
          </span>
          <SHint v-else inline>—</SHint>
        </template>
        <template #cell-flow="{ row }">
          <FlowRef v-if="row.flow" :flow-id="row.flow.flow_id" :version="row.flow.version" :name="row.flow.name" compact />
          <SHint v-else inline>{{ t('watchList.noFlow') }}</SHint>
        </template>
        <template #cell-last_hit_at="{ row }"><SDateTime :value="row.last_hit_at" /></template>
        <template #cell-expires_at="{ row }"><SDateTime :value="row.expires_at" mode="time" :empty="t('watch.noExpiry')" /></template>
        <template #cell-source="{ row }">{{ enumLabel('watchSource', row.source) }}</template>
        <template #cell-violation_count="{ row }"><span class="mod-num">{{ num(row.violation_count) }}</span></template>
        <template #cell-note="{ row }"><span :title="row.note || undefined">{{ row.note || '—' }}</span></template>
        <template #cell-actions="{ row }">
          <span class="mod-row-actions" @click.stop>
            <SRowAction
              :label="t(`watchActions.set.${otherLevel(row)}`)"
              :tone="otherLevel(row) === 'enforce' ? 'danger' : 'default'"
              :disabled="levelRefused(otherLevel(row), row.allowlisted)"
              :title="levelRefused(otherLevel(row), row.allowlisted) ? userListHint(row) : undefined"
              @click="ask(row, { kind: 'level', level: otherLevel(row) })"
            />
            <SRowAction :label="t('edit')" @click="openEdit(row)" />
            <SDropdown :actions="moreActions" @select="$event === 'remove' && ask(row, { kind: 'release', level: null })" />
          </span>
        </template>
      </STable>
      <div class="mod-pager">
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </div>
    </SCard>

    <WatchActionDialog v-model:action="action" :user-id="acting?.user_id ?? null" :allowlisted="acting?.allowlisted" :blocklisted="acting?.blocklisted" @changed="onAction" />
    <WatchEditModal v-model:open="editOpen" :entry="editing" :allowlisted="editing?.allowlisted" :blocklisted="editing?.blocklisted" @saved="list.reload" />
    <WatchDetail v-model:open="detailOpen" :user-id="detailUser" :item="detailItem" @changed="patchRow(detailUser, $event)" @show-user="showUser" />
  </div>
</template>
