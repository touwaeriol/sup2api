<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SHint, SLink, SStatusBadge, useTabbedFormPart } from '@sub2api/ui'
import type { Group } from '@/api/types'
import type { SegmentGroupSetting } from '@/api/pricing'
import { fetchAllPages } from '@/composables/lookups'
import AccessRateTable from './AccessRateTable.vue'
import GroupVisibilityBadge from './GroupVisibilityBadge.vue'
import SegmentModelRates from './SegmentModelRates.vue'
import { blankGroupSetting } from './accessRateDrafts'
import { rateText } from './pricingFormat'
import { useAccessRateDrafts } from './useAccessRateDrafts'
import { useModelRateDrafts } from './useModelRateDrafts'

// What a user group ("segment": a named one, or the personal one of a user)
// gets in every account group: whether it may use a private one, its rate
// there (empty follows the account group), and under each row its model
// rates there (a model paid at a rate of its own; the others follow the
// row). Edits stay in drafts (what applies follows them at once), which the
// segment's request sends: the part of tab `tab` of the enclosing tabbed
// form while `editable` (it may turn so in place: a user made personal in
// the pricing drawer). A segment not created yet (segmentId null) starts
// from every account group with nothing set. `disabled` holds the fields
// still while it saves.
const props = defineProps<{ segmentId: number | null; tab: string; editable?: boolean; disabled?: boolean }>()
const { t } = useI18n()

async function fetchSettings(): Promise<SegmentGroupSetting[]> {
  if (props.segmentId !== null) return api.get<SegmentGroupSetting[]>(`/segments/${props.segmentId}/groups`)
  return (await fetchAllPages<Group>('/groups')).map(blankGroupSetting)
}

const groups = useAccessRateDrafts({ side: 'group', fetch: fetchSettings })
const modelRates = useModelRateDrafts({
  far: 'group_id',
  saved: () => groups.saved.value.flatMap((s) => s.model_rates.map((r) => ({ far: s.group.id, ...r }))),
  farName: (id) => groups.saved.value.find((s) => s.group.id === id)?.group.name ?? `#${id}`
})
const { rows, loading, error, refresh, change } = groups
watch(
  () => props.segmentId,
  () => {
    modelRates.reset()
    return groups.load()
  },
  { immediate: true }
)

useTabbedFormPart(
  {
    tab: props.tab,
    // Both run, so each table marks what is wrong.
    check: () => [groups.check(), modelRates.check()].every(Boolean),
    body: () => ({ ...groups.body(), ...modelRates.body() }),
    dirty: () => groups.dirty.value || modelRates.dirty.value,
    fields: [...groups.fields, modelRates.field],
    applyErrors: (errors) => [groups.applyErrors(errors), modelRates.applyErrors(errors)]
  },
  () => !!props.editable
)
</script>

<template>
  <AccessRateTable
    side="group"
    :rows="rows"
    :editable="editable"
    :disabled="disabled"
    :loading="loading"
    :error="error"
    :row-errors="groups.rowErrors.value"
    expandable
    :expand-toggle="false"
    data-testid="segment-groups"
    @retry="refresh"
    @change="change"
  >
    <template #lead="{ row }">
      <b class="truncate font-medium">{{ row.group.name }}</b>
      <GroupVisibilityBadge v-if="row.group.visibility === 'restricted'" visibility="restricted" size="xs" data-testid="segment-group-private" />
      <SStatusBadge v-if="row.group.status !== 'active'" :status="row.group.status" size="xs" />
    </template>
    <template #note="{ row, expanded, toggleExpand }">
      <SHint size="xs">
        {{ t('pricingAccess.groupRate') }} {{ rateText(row.group.rate_multiplier) }} ·
        <SLink as="button" :aria-expanded="expanded" data-testid="segment-model-rates-toggle" @click="toggleExpand()">
          {{ t('segments.modelRates.toggle', { n: modelRates.rowsOf(row.id).length }) }}
        </SLink>
      </SHint>
    </template>
    <template #expand="{ row }">
      <SegmentModelRates
        :rows="modelRates.rowsOf(row.id)"
        :group-id="row.id"
        :override="row.rate_override"
        :group-rate="row.rate.group_default"
        :editable="editable"
        :disabled="disabled"
        :errors="modelRates.rowErrors.value"
        @add="(model, rate) => modelRates.add(row.id, model, rate)"
        @change="(r, rate) => modelRates.change(r, rate)"
        @remove="(r) => modelRates.change(r, null)"
        @undo="modelRates.undo"
      />
    </template>
  </AccessRateTable>
</template>
