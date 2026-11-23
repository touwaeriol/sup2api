<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SBadge, SHint, SLink, useTabbedFormPart, type SelectOption } from '@sub2api/ui'
import type { Group } from '@/api/types'
import type { Segment, SegmentGroupSetting, SegmentRef } from '@/api/pricing'
import AccessRateTable from '@/components/pricing/AccessRateTable.vue'
import AddSelect from '@/components/pricing/AddSelect.vue'
import GroupVisibilityBadge from '@/components/pricing/GroupVisibilityBadge.vue'
import PersonalSegmentPicker from '@/components/pricing/PersonalSegmentPicker.vue'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import SegmentModelRates from '@/components/pricing/SegmentModelRates.vue'
import { blankSegmentSetting, setsAnything, type DraftedSetting } from '@/components/pricing/accessRateDrafts'
import { rateText, typedRate } from '@/components/pricing/pricingFormat'
import { useAccessRateDrafts } from '@/components/pricing/useAccessRateDrafts'
import { useModelRateDrafts } from '@/components/pricing/useModelRateDrafts'
import { fetchAllPages } from '@/composables/lookups'

// The user groups of an account group with something set apart: enabled (a
// private group), a rate of their own, model rates of their own, added here,
// or changed and not saved yet; every other user group pays the group's
// rates. Under each row, its model rates here (a model paid at a rate of its
// own; the others follow the row). "Add user group" lists a named one, "Add
// user" a personal one; Remove takes away everything a row has here, its
// model rates included (Undo keeps the row, not its unsaved model rates).
// These are the same settings the user group's "Account group rates" edit,
// from the other side. The rows follow the visibility and the base rate of
// the form being edited. Edits stay in drafts, which the group's request
// sends (`segment_rates`, `segment_model_rates`: the part of tab `tab` of the
// enclosing tabbed form while `editable`). A group not created yet (groupId
// null) starts from every named user group with nothing set.
const props = defineProps<{ groupId: number | null; visibility: Group['visibility']; defaultRate: string; editable: boolean; tab: string; disabled?: boolean }>()
const { t } = useI18n()

/** Personal segments picked here: fetched although the group has nothing for them yet. */
const included = ref<number[]>([])
/** Rows added here: listed although nothing is set for them yet. */
const added = ref<number[]>([])
const query = computed(() => (included.value.length ? { include: included.value.join(',') } : undefined))

async function fetchSettings(): Promise<SegmentGroupSetting[]> {
  if (props.groupId !== null) return api.get<SegmentGroupSetting[]>(`/groups/${props.groupId}/segments`, query.value)
  const [named, personal] = await Promise.all([fetchAllPages<Segment>('/segments'), Promise.all(included.value.map((id) => api.get<Segment>(`/segments/${id}`)))])
  return [...named, ...personal].map(blankSegmentSetting)
}

const drafts = useAccessRateDrafts({ side: 'segment', fetch: fetchSettings, terms: () => ({ visibility: props.visibility, groupDefault: props.defaultRate }) })
const { rows, loading, error, refresh, change } = drafts
const modelRates = useModelRateDrafts({
  far: 'segment_id',
  saved: () => drafts.saved.value.flatMap((s) => s.model_rates.map((r) => ({ far: s.segment.id, ...r }))),
  farName: (id) => drafts.saved.value.find((s) => s.segment.id === id)?.segment.name ?? `#${id}`
})
useTabbedFormPart(
  {
    tab: props.tab,
    // Both run, so each table marks what is wrong.
    check: () => [drafts.check(), modelRates.check()].every(Boolean),
    body: () => ({ ...drafts.body(), ...modelRates.body() }),
    dirty: () => drafts.dirty.value || modelRates.dirty.value,
    fields: [...drafts.fields, modelRates.field],
    applyErrors: (errors) => [drafts.applyErrors(errors), modelRates.applyErrors(errors)]
  },
  () => props.editable
)

function load() {
  included.value = []
  added.value = []
  modelRates.reset()
  return drafts.load()
}
watch(() => props.groupId, load, { immediate: true })

/** The rows the table lists. */
const listed = computed(() => rows.value.filter((r) => setsAnything(r) || r.dirty || added.value.includes(r.id) || drafts.rowErrors.value.has(r.id)))
/** The named user groups not listed yet. */
const addable = computed<SelectOption[]>(() => rows.value.filter((r) => r.segment.kind === 'named' && !listed.value.includes(r)).map((r) => ({ value: r.id, label: r.segment.name })))

function add(id: number) {
  if (!added.value.includes(id)) added.value = [...added.value, id]
}
/** Lists a personal segment, fetching it when the group has nothing for it yet. */
function include(segment: SegmentRef) {
  add(segment.id)
  if (rows.value.some((r) => r.id === segment.id)) return
  included.value = [...included.value, segment.id]
  return refresh()
}
/** Takes away everything a row has here; once that is saved it is no longer listed. */
function remove(row: DraftedSetting) {
  added.value = added.value.filter((id) => id !== row.id)
  modelRates.discard(row.id)
  change(row, { removed: true })
}

const restricted = computed(() => props.visibility === 'restricted')
/** The base rate the form has, once it is one. */
const groupRate = computed(() => rateText(typedRate(props.defaultRate) ?? rows.value[0]?.rate.group_default))
/** A private group nobody may use, though rows are listed (an empty table says so itself). */
const nobody = computed(() => restricted.value && !loading.value && !error.value && listed.value.length > 0 && !listed.value.some((r) => r.granted))
</script>

<template>
  <div class="space-y-3" data-testid="group-segments">
    <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
      <span class="inline-flex min-w-0 flex-1 basis-80 flex-wrap items-center gap-2 text-sm text-fg-muted" data-testid="group-segments-intro">
        <GroupVisibilityBadge :visibility="visibility" />
        <span>
          {{ t(restricted ? 'pricingAccess.inGroup.restricted' : 'pricingAccess.inGroup.public') }}
          {{ t('pricingAccess.baseRate') }} <b class="font-mono font-medium text-fg">{{ groupRate }}</b>
        </span>
      </span>
      <span v-if="editable" class="flex flex-wrap items-center gap-2">
        <AddSelect
          :options="addable"
          :label="t('pricingAccess.inGroup.addSegment')"
          :no-results="t('pricingAccess.inGroup.addNone')"
          :disabled="disabled || loading"
          data-testid="group-segments-add"
          @add="typeof $event === 'number' && add($event)"
        />
        <PersonalSegmentPicker size="sm" :label="t('pricingAccess.inGroup.addUser')" :disabled="disabled" @select="include" />
      </span>
    </div>
    <SAlert v-if="nobody" tone="warning" dense data-testid="group-segments-nobody">
      {{ t('pricingAccess.inGroup.nobody') }}
      <template v-if="editable">{{ t('pricingAccess.inGroup.nobodyHint') }}</template>
    </SAlert>
    <SHint v-if="editable && !restricted" size="xs" data-testid="group-segments-kept">{{ t('pricingAccess.inGroup.keptGrants') }}</SHint>
    <AccessRateTable
      side="segment"
      :rows="listed"
      :editable="editable"
      :removable="editable"
      :disabled="disabled"
      :loading="loading"
      :error="error"
      :row-errors="drafts.rowErrors.value"
      :empty-text="t(`pricingAccess.inGroup.empty.${visibility}`)"
      expandable
      :expand-toggle="false"
      @retry="refresh"
      @change="change"
      @remove="remove"
      @undo="drafts.undo"
    >
      <template #lead="{ row }">
        <b class="truncate font-medium">{{ row.segment.name }}</b>
        <SBadge v-if="row.segment.kind === 'named'" size="xs" class="whitespace-nowrap">{{ t('pricingAccess.members', { n: row.member_count ?? 0 }, row.member_count ?? 0) }}</SBadge>
        <SegmentBadge v-else :segment="row.segment" />
      </template>
      <template #note="{ row, expanded, toggleExpand }">
        <SHint size="xs">
          <SLink as="button" :aria-expanded="expanded" data-testid="group-segment-model-rates-toggle" @click="toggleExpand()">
            {{ t('segments.modelRates.toggle', { n: modelRates.rowsOf(row.id).length }) }}
          </SLink>
          <template v-if="editable && row.model_rates.length"> · {{ t('pricingAccess.inGroup.modelRates') }}</template>
        </SHint>
      </template>
      <template #expand="{ row }">
        <SegmentModelRates
          :rows="modelRates.rowsOf(row.id)"
          :group-id="groupId"
          :override="row.rate_override"
          :group-rate="row.rate.group_default"
          :editable="editable"
          :disabled="disabled || row.removed"
          :errors="modelRates.rowErrors.value"
          @add="(model, rate) => modelRates.add(row.id, model, rate)"
          @change="(r, rate) => modelRates.change(r, rate)"
          @remove="(r) => modelRates.change(r, null)"
          @undo="modelRates.undo"
        />
      </template>
    </AccessRateTable>
    <SHint v-if="editable" size="xs">{{ t('pricingAccess.overrideNote') }}</SHint>
  </div>
</template>
