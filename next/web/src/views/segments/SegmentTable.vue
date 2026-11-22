<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SChipList, SHint, SLink, STable, type ChipListItem, type TableColumn } from '@sub2api/ui'
import type { Segment } from '@/api/pricing'
import SegmentBadge from '@/components/pricing/SegmentBadge.vue'
import { hasDiscount, rateText } from '@/components/pricing/pricingFormat'
import { formatNumber } from '@/utils/format'

// The user groups and the users priced on their own in one table: who each
// is (a personal one by its user, with the group he came from), how many
// members, its discount, the private groups it may use and the groups it
// sets a rate of its own in, with those rates. `members` asks for the
// members of a named row (`linkMembers`: the count is a link); the caller
// brings `cell-actions`. Other attributes and slots (card, loading, #footer,
// …) are STable's.
defineOptions({ inheritAttrs: false })
defineProps<{ rows: Segment[]; linkMembers?: boolean }>()
const emit = defineEmits<{ (e: 'members', s: Segment): void }>()
const { t } = useI18n()

/** Private groups shown before the rest folds into "+N". */
const GROUPS_SHOWN = 3

const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('segments.columns.name'), mobile: 'title' },
  { key: 'member_count', label: t('segments.columns.members'), align: 'right', mobile: 'meta' },
  { key: 'discount', label: t('segments.columns.discount'), mobile: 'meta' },
  { key: 'granted_groups', label: t('segments.columns.granted'), mobile: 'meta', mobileSpan: 2 },
  { key: 'rate_overrides', label: t('segments.columns.overrides'), mobile: 'meta', mobileSpan: 2 },
  { key: 'actions', label: t('common.actions'), align: 'right' }
])

const groupChips = (s: Segment): ChipListItem[] => s.granted_groups.map((g) => ({ key: String(g.id), label: g.name }))
const rateChips = (s: Segment): ChipListItem[] => s.rate_overrides.map((g) => ({ key: String(g.id), label: `${g.name} ${rateText(g.rate)}` }))
const rowAttrs = (s: Segment) => ({ 'data-testid': 'segment-row', 'data-segment': s.id, 'data-kind': s.kind })
</script>

<template>
  <STable v-bind="$attrs" :columns="columns" :rows="rows" :row-attrs="rowAttrs">
    <template v-for="(_, name) in $slots" #[name]="scope">
      <slot :name="name" v-bind="scope ?? {}" />
    </template>
    <template #cell-name="{ row }">
      <span class="inline-flex max-w-full items-center gap-1.5">
        <span class="truncate font-medium text-fg" data-testid="segment-name-cell">{{ row.user?.email ?? row.name }}</span>
        <SBadge v-if="row.is_default" tone="info" size="xs" :title="t('segments.defaultHint')" data-testid="segment-default">{{ t('segments.default') }}</SBadge>
        <SegmentBadge v-if="row.kind === 'personal'" :segment="row" class="shrink-0" />
      </span>
      <SHint v-if="row.kind === 'personal' && row.origin" size="xs" data-testid="segment-origin">{{ t('segments.personal.origin', { name: row.origin.name }) }}</SHint>
      <SHint v-else-if="row.description" size="xs" class="max-w-xs truncate">{{ row.description }}</SHint>
    </template>
    <template #cell-member_count="{ row }">
      <SHint v-if="row.kind === 'personal'" inline>—</SHint>
      <SLink v-else-if="linkMembers" as="button" class="font-mono" data-testid="segment-members-link" @click="emit('members', row)">{{ formatNumber(row.member_count) }}</SLink>
      <span v-else class="font-mono" data-testid="segment-member-count">{{ formatNumber(row.member_count) }}</span>
    </template>
    <template #cell-discount="{ row }">
      <span v-if="hasDiscount(row)" class="font-mono font-medium" data-testid="segment-discount-cell">{{ rateText(row.discount) }}</span>
      <SHint v-else inline data-testid="segment-discount-cell">{{ t('common.none') }}</SHint>
    </template>
    <template #cell-granted_groups="{ row }">
      <SChipList v-if="row.granted_groups.length" :items="groupChips(row)" :max="GROUPS_SHOWN" />
      <SHint v-else inline>{{ t('segments.publicOnly') }}</SHint>
    </template>
    <template #cell-rate_overrides="{ row }">
      <SChipList v-if="row.rate_overrides.length" :items="rateChips(row)" :max="GROUPS_SHOWN" data-testid="segment-overrides-cell" />
      <SHint v-else inline data-testid="segment-overrides-cell">{{ t('common.none') }}</SHint>
    </template>
  </STable>
</template>
