<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SBadge, SHint } from '@sub2api/ui'
import type { Group } from '@/api/types'
import type { Segment, SegmentGroupSetting, SegmentRef } from '@/api/pricing'
import AccessRateTable from '@/components/pricing/AccessRateTable.vue'
import PersonalSegmentPicker from '@/components/pricing/PersonalSegmentPicker.vue'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import { blankSetting, type DraftedSetting } from '@/components/pricing/accessRateDrafts'
import { hasDiscount, rateText, typedRate } from '@/components/pricing/pricingFormat'
import { fetchNamedSegments } from '@/components/pricing/segmentLookup'
import { useAccessRateDrafts } from '@/components/pricing/useAccessRateDrafts'

// Who may use a group and at which rate: one row per user group, the named
// ones first, then the personal ones that have something in this group or
// were picked here. Edits stay in drafts until the parent calls save(gid)
// after saving the group itself, as it does with the accounts editor. The
// rows follow the visibility and the group rate of the form being edited,
// and go to the parent (`rows`) for the tabs that show what each pays.
// A group not created yet (groupId null) starts from every named user group
// with nothing set. Without `editable` the rows are only shown.
const props = defineProps<{ groupId: number | null; visibility: Group['visibility']; defaultRate: string; editable?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'rows', rows: DraftedSetting[]): void }>()
const { t } = useI18n()

/** Personal segments picked here: listed although the group has nothing for them yet. */
const included = ref<number[]>([])
const query = computed(() => (included.value.length ? { include: included.value.join(',') } : undefined))

async function fetchSettings(): Promise<SegmentGroupSetting[]> {
  if (props.groupId !== null) return api.get<SegmentGroupSetting[]>(`/groups/${props.groupId}/segments`, query.value)
  const [named, personal] = await Promise.all([fetchNamedSegments(), Promise.all(included.value.map((id) => api.get<Segment>(`/segments/${id}`)))])
  return [...named, ...personal].map(blankSetting)
}

const drafts = useAccessRateDrafts({ side: 'segment', fetch: fetchSettings, terms: () => ({ visibility: props.visibility, groupDefault: props.defaultRate }) })
const { rows, loading, error, dirty, refresh, change } = drafts

function load() {
  included.value = []
  return drafts.load()
}
watch(() => props.groupId, load, { immediate: true })
watch(rows, (v) => emit('rows', v), { immediate: true })

/** Lists a personal segment that has nothing in this group yet. */
function include(segment: SegmentRef) {
  if (rows.value.some((r) => r.id === segment.id)) return
  included.value = [...included.value, segment.id]
  return refresh()
}

const restricted = computed(() => props.visibility === 'restricted')
/** The group rate the form has, once it is one. */
const groupRate = computed(() => rateText(typedRate(props.defaultRate) ?? rows.value[0]?.rate.group_default))
/** A private group nobody may use. */
const nobody = computed(() => restricted.value && !loading.value && !error.value && !rows.value.some((r) => r.granted))

/** Saves the changed rows for group `gid`: false when a rate cannot be saved yet, a failed request rejects. */
const save = (gid: number) => drafts.save((items) => api.put<SegmentGroupSetting[]>(`/groups/${gid}/segments`, { items }, { query: query.value }))

defineExpose({ check: drafts.check, save, dirty })
</script>

<template>
  <div class="space-y-3" data-testid="group-segments">
    <div class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
      <span class="inline-flex min-w-0 flex-1 basis-80 flex-wrap items-center gap-2 text-sm text-fg-muted" data-testid="group-segments-intro">
        <SBadge :tone="restricted ? 'gray' : 'success'">{{ t(restricted ? 'pricingAccess.private' : 'pricingAccess.public') }}</SBadge>
        <span>
          {{ t(restricted ? 'pricingAccess.inGroup.restricted' : 'pricingAccess.inGroup.public') }}
          {{ t('pricingAccess.groupRate') }} <b class="font-mono font-medium text-fg">{{ groupRate }}</b>
        </span>
      </span>
      <PersonalSegmentPicker v-if="editable" size="sm" :disabled="disabled" @select="include" />
    </div>
    <SAlert v-if="nobody" tone="warning" dense data-testid="group-segments-nobody">
      {{ t('pricingAccess.inGroup.nobody') }}
      <template v-if="editable">{{ t('pricingAccess.inGroup.nobodyHint') }}</template>
    </SAlert>
    <SHint v-if="editable && !restricted" size="xs" data-testid="group-segments-kept">{{ t('pricingAccess.inGroup.keptGrants') }}</SHint>
    <AccessRateTable side="segment" :rows="rows" :editable="editable" :disabled="disabled" :loading="loading" :error="error" @retry="refresh" @change="change">
      <template #lead="{ row }">
        <b class="truncate font-medium">{{ row.segment.name }}</b>
        <SBadge v-if="row.segment.kind === 'named'" size="xs" class="whitespace-nowrap">{{ t('pricingAccess.members', { n: row.member_count ?? 0 }, row.member_count ?? 0) }}</SBadge>
        <SegmentBadge v-else :segment="row.segment" />
      </template>
      <template #note="{ row }">
        <SHint v-if="hasDiscount(row.rate)" size="xs">{{ t('pricingAccess.discountOf', { rate: Number(row.rate.discount) }) }}</SHint>
      </template>
    </AccessRateTable>
    <SHint v-if="editable" size="xs">{{ t('pricingAccess.overrideNote') }}</SHint>
  </div>
</template>
