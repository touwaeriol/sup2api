<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SHint, SIcon, SStatusBadge } from '@sub2api/ui'
import type { Group } from '@/api/types'
import type { SegmentGroupSetting } from '@/api/pricing'
import AccessRateTable from './AccessRateTable.vue'
import { blankGroupSetting } from './accessRateDrafts'
import { rateText } from './pricingFormat'
import { useAccessRateDrafts } from './useAccessRateDrafts'
import { fetchAllPages } from '@/composables/lookups'

// What a segment (a user group, or the personal one of a user) gets in every
// group: whether it may use a private group and the rate it pays there. Edits
// stay in drafts (the rate that applies follows them at once) until the
// parent calls save(), as it does with its other sub-editors after saving the
// main form. A segment not created yet (segmentId null) starts from every
// group with nothing set, and save(id) takes the id it got. `discount` is the
// segment's discount as the parent's form has it now, so the rates follow it
// before it is saved; `disabled` holds the fields still while the parent saves.
const props = defineProps<{ segmentId: number | null; editable?: boolean; disabled?: boolean; discount?: string }>()
const { t } = useI18n()

async function fetchSettings(): Promise<SegmentGroupSetting[]> {
  if (props.segmentId !== null) return api.get<SegmentGroupSetting[]>(`/segments/${props.segmentId}/groups`)
  return (await fetchAllPages<Group>('/groups')).map(blankGroupSetting)
}

const drafts = useAccessRateDrafts({ side: 'group', fetch: fetchSettings, terms: () => ({ discount: props.discount }) })
const { rows, loading, error, dirty, refresh, load, change } = drafts
watch(() => props.segmentId, load, { immediate: true })

/** Saves the drafts for segment `id`: false when a rate cannot be saved yet, a failed request rejects. */
async function save(id = props.segmentId): Promise<boolean> {
  if (id === null || !props.editable) return true
  return drafts.save((items) => api.put<SegmentGroupSetting[]>(`/segments/${id}/groups`, { items }))
}

defineExpose({ check: drafts.check, save, dirty })
</script>

<template>
  <AccessRateTable side="group" :rows="rows" :editable="editable" :disabled="disabled" :loading="loading" :error="error" data-testid="segment-groups" @retry="refresh" @change="change">
    <template #lead="{ row }">
      <b class="truncate font-medium">{{ row.group.name }}</b>
      <SBadge v-if="row.group.visibility === 'restricted'" size="xs" data-testid="segment-group-private">
        <SIcon name="lock" class="h-3 w-3" />
        {{ t('pricingAccess.private') }}
      </SBadge>
      <SStatusBadge v-if="row.group.status !== 'active'" :status="row.group.status" size="xs" />
    </template>
    <template #note="{ row }">
      <SHint size="xs">{{ t('pricingAccess.groupRate') }} {{ rateText(row.group.rate_multiplier) }}</SHint>
    </template>
  </AccessRateTable>
</template>
