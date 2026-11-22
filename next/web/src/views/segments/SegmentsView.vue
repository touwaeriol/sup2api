<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@sub2api/host'
import { SButton, SDropdown, SInput, SPageHeader, SPagination, SSegmented, confirm, type MenuAction, type SelectOption } from '@sub2api/ui'
import type { Segment, SegmentKind, SegmentListKind, SegmentRef } from '@/api/pricing'
import PersonalRemoveDialog from '@/components/pricing/PersonalRemoveDialog.vue'
import PersonalSegmentPicker from '@/components/pricing/PersonalSegmentPicker.vue'
import PriceSimulatorModal from '@/components/pricing/PriceSimulatorModal.vue'
import { canSimulatePrices } from '@/components/pricing/quoteSubjects'
import { useSegmentsLookup } from '@/components/pricing/segmentLookup'
import { canListMembers, personalUserOf } from '@/components/pricing/segmentMembers'
import { deleteResource } from '@/composables/crud'
import { useList } from '@/composables/useList'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { formatNumber } from '@/utils/format'
import SegmentEditor, { type SegmentEditorTab } from './SegmentEditor.vue'
import SegmentTable from './SegmentTable.vue'

// User groups ("segments"): every user is in exactly one, and it decides
// which private groups he may use and at what rate. The users priced on
// their own, each a segment of his own, are in the same list after the named
// ones (GET /segments?kind=all), and the kind filter narrows it to either. A
// caller who may not manage segments opens them read-only; the members of
// one are listed only to a caller who reads users.
const { t } = useI18n()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const canManage = computed(() => auth.has('segment:manage'))
const canMembers = computed(() => canListMembers(auth))
const canSimulate = computed(() => canSimulatePrices(auth))
const list = useList<Segment>('/segments', { q: '', kind: 'all' satisfies SegmentListKind })

// ------------------------------------------------------------------ kinds

const KINDS: SegmentKind[] = ['named', 'personal']
/** How many of each kind match the search, for the filter's labels. */
const counts = reactive<Record<SegmentKind, number>>({ named: 0, personal: 0 })
/** The search the counts are of; null once a change makes them stale. */
let countedQ: string | null = null

async function countKinds() {
  const q = (countedQ = list.filters.q)
  try {
    const totals = await Promise.all(KINDS.map((kind) => api.list<Segment>('/segments', { kind, q, page_size: 1 })))
    KINDS.forEach((kind, i) => (counts[kind] = totals[i].page.total ?? 0))
  } catch {
    // The labels keep the last counts; the next load tries again.
    countedQ = null
  }
}
// A load of the list for another search, or after a change, counts again; a page or a kind does not.
watch(list.items, () => list.filters.q !== countedQ && countKinds())

const kindOptions = computed<SelectOption[]>(() => [
  { value: 'all', label: t('segments.kinds.all', { n: formatNumber(counts.named + counts.personal) }) },
  { value: 'named', label: t('segments.kinds.named', { n: formatNumber(counts.named) }) },
  { value: 'personal', label: t('segments.kinds.personal', { n: formatNumber(counts.personal) }) }
])

/** Segments, their members or their settings changed: the list, its counts and the selects follow. */
function changed() {
  countedQ = null
  list.reload()
  useSegmentsLookup(true)
}

// ------------------------------------------------------------------ create / edit

const editing = ref<Segment | null>(null)
const editorOpen = ref(false)
const editorTab = ref<SegmentEditorTab>('basic')

function openEditor(s: Segment | null, tab: SegmentEditorTab = 'basic') {
  editing.value = s
  editorTab.value = tab
  editorOpen.value = true
}

async function openById(id: number | string) {
  try {
    openEditor(await api.get<Segment>(`/segments/${id}`))
  } catch (e) {
    notifyError(e)
  }
}

/** ?edit=<id> opens that user group, a personal one too: other pages link to it this way. The parameter is used once and leaves the address bar. */
function openFromQuery(id: unknown) {
  if (typeof id !== 'string') return
  router.replace({ query: { ...route.query, edit: undefined } })
  return openById(id)
}
watch(() => route.query.edit, openFromQuery, { immediate: true })

/** Opens the personal segment of the user picked: the one he had, or the one just made for him. */
function picked(segment: SegmentRef, created: boolean) {
  if (created) changed()
  return openById(segment.id)
}

// ------------------------------------------------------------------ row menu

function rowActions(s: Segment): MenuAction[] {
  if (s.kind === 'personal') return [{ key: 'moveBack', label: t('segments.personal.remove'), hidden: !canManage.value }]
  return [
    { key: 'members', label: t('segments.tabs.members'), hidden: !canMembers.value },
    { key: 'simulate', label: t('segments.simulate'), hidden: !canSimulate.value },
    { key: 'delete', label: t('common.delete'), danger: true, hidden: !canManage.value, disabled: s.is_default }
  ]
}
function onAction(s: Segment, key: string) {
  if (key === 'members') openEditor(s, 'members')
  else if (key === 'simulate') simulate(s)
  else if (key === 'delete') remove(s)
  else if (key === 'moveBack') askMoveBack(s)
}

const simulatorPreset = ref<{ segmentId: number }>()
const simulatorOpen = ref(false)

function simulate(s: Segment) {
  simulatorPreset.value = { segmentId: s.id }
  simulatorOpen.value = true
}

/** Deleting names the user group in the title and says what goes with it. */
async function remove(s: Segment) {
  const ask = { title: t('segments.deleteTitle', { name: s.name }), message: t('segments.deleteConfirm', { n: s.member_count }, s.member_count), confirmText: t('common.delete'), danger: true }
  return (await confirm(ask)) && deleteResource(`/segments/${s.id}`, undefined, changed)
}

// ------------------------------------------------------------------ move a personal user back

const movingBack = ref<Segment | null>(null)
const moveBackOpen = ref(false)

function askMoveBack(s: Segment) {
  movingBack.value = s
  moveBackOpen.value = true
}

function movedBack() {
  editorOpen.value = false
  changed()
}
</script>

<template>
  <div>
    <SPageHeader :title="t('segments.title')" :description="t('segments.description')">
      <template #actions>
        <PersonalSegmentPicker :label="t('segments.convert.pickShort')" @select="picked" />
        <SButton v-if="canManage" variant="primary" icon="plus" fab data-testid="segment-create" @click="openEditor(null)">{{ t('segments.create') }}</SButton>
      </template>
      <template #filters-primary>
        <SSegmented v-model="list.filters.kind" size="sm" :options="kindOptions" :aria-label="t('segments.kinds.label')" data-testid="segment-kind" />
        <SInput v-model="list.filters.q" class="w-64" icon="search" clearable :aria-label="t('common.search')" :placeholder="t('segments.searchPlaceholder')" data-testid="segment-search" />
      </template>
    </SPageHeader>

    <SegmentTable card :rows="list.items.value" :link-members="canMembers" :loading="list.loading.value" :error="list.error.value" @retry="list.reload" @members="openEditor($event, 'members')">
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <SButton variant="ghost" size="sm" data-testid="segment-edit" @click="openEditor(row)">{{ t(canManage ? 'common.edit' : 'common.view') }}</SButton>
          <SDropdown :actions="rowActions(row)" @select="onAction(row, $event)" />
        </div>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </SegmentTable>

    <SegmentEditor v-model:open="editorOpen" :segment="editing" :tab="editorTab" :readonly="!canManage" @changed="changed" @remove="askMoveBack" />
    <PersonalRemoveDialog v-model:open="moveBackOpen" :user="personalUserOf(movingBack)" :origin="movingBack?.origin" @done="movedBack" />
    <PriceSimulatorModal v-if="canSimulate" v-model:open="simulatorOpen" :preset="simulatorPreset" />
  </div>
</template>
