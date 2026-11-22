<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SHint, SRowAction, SSwitch, STable, type TableColumn } from '@sub2api/ui'
import DraftBadge from './DraftBadge.vue'
import GroupVisibilityBadge from './GroupVisibilityBadge.vue'
import RateOverrideField from './RateOverrideField.vue'
import { rateText } from './pricingFormat'
import type { DraftedSetting, SettingDraft, SettingSide } from './accessRateDrafts'

// Who may use an account group and at which rate, as one table: the user
// groups of one account group or the account groups of one user group
// (`side`: the end the rows differ in), with the same columns either way: the
// name, whether it may be used (public, or a grant), its rate (empty follows
// the account group) and the rate it pays with where that comes from. The
// caller names a row in #lead (on one line) and #note (a line under it,
// which gets the row's `expanded` and `toggleExpand`: with STable's
// `expandable`, the #expand slot shows under an open row). Editable, a row
// has a switch and a rate field and reports what they change (`change`);
// read-only it says the same in words. Where the rows are the user groups of
// an account group, a public one keeps its grants on disabled switches: they
// apply again once it is private. `removable` adds a Remove action per row
// (`remove`); `rowErrors` are the server's refusals by row id. Other
// attributes (loading, error, empty-text, @retry, expandable, expand-toggle)
// are STable's.
defineOptions({ inheritAttrs: false })
const props = defineProps<{ rows: DraftedSetting[]; side: SettingSide; editable?: boolean; disabled?: boolean; removable?: boolean; rowErrors?: ReadonlyMap<number, string> }>()
const emit = defineEmits<{ (e: 'change', row: DraftedSetting, draft: SettingDraft): void; (e: 'remove', row: DraftedSetting): void }>()
const { t } = useI18n()

const columns = computed<TableColumn[]>(() => [
  // The names take the width the fixed columns leave, down to a readable minimum.
  { key: 'lead', label: props.side === 'segment' ? t('pricingAccess.segment') : t('common.group'), grow: true, class: 'min-w-[11rem]', mobile: 'title' },
  { key: 'access', label: t('pricingAccess.access'), nowrap: true, mobile: 'meta' },
  { key: 'rate_override', label: t('pricingAccess.rate'), nowrap: true, mobile: 'meta' },
  { key: 'rate', label: t('pricingAccess.paid'), nowrap: true, mobile: 'meta' },
  ...(props.removable ? [{ key: 'actions', label: t('common.actions'), align: 'right' } satisfies TableColumn] : [])
])
const rowAttrs = (row: DraftedSetting) => ({ 'data-testid': 'access-rate-row', [`data-${props.side}`]: row.id })
const nameOf = (row: DraftedSetting) => row[props.side].name
/** A grant kept on a public group shows on a switch nobody can turn: only the group's form edits its grants this way. */
const keepsGrants = computed(() => props.editable && props.side === 'segment')

/** Where the rate a row pays comes from, in words. */
const sourceText = (row: DraftedSetting) => t(`pricingAccess.source.${row.rate.source}`)
</script>

<template>
  <STable v-bind="$attrs" :columns="columns" :rows="rows" dense :row-attrs="rowAttrs">
    <template #cell-lead="{ row, expanded, toggleExpand }">
      <div :class="row.accessible ? '' : 'text-fg-subtle'">
        <span class="inline-flex max-w-full flex-wrap items-center gap-1.5">
          <slot name="lead" :row="row" />
          <DraftBadge :dirty="row.dirty" :error="rowErrors?.get(row.id)" testid="access-rate" />
        </span>
        <SHint v-if="rowErrors?.has(row.id)" size="xs" tone="danger" data-testid="access-rate-error">{{ rowErrors.get(row.id) }}</SHint>
        <slot name="note" :row="row" v-bind="{ expanded, toggleExpand }" />
      </div>
    </template>
    <template #cell-access="{ row }">
      <span class="inline-flex items-center gap-2">
        <GroupVisibilityBadge v-if="row.group.visibility === 'public'" visibility="public" data-testid="access-rate-public" />
        <SSwitch
          v-if="editable && (row.group.visibility === 'restricted' || keepsGrants)"
          size="sm"
          :model-value="row.granted"
          :disabled="disabled || row.group.visibility === 'public'"
          :label="t(row.granted ? 'pricingAccess.granted' : 'pricingAccess.notGranted')"
          :aria-label="t(`pricingAccess.grant.${side}`, { name: nameOf(row) })"
          data-testid="access-rate-grant"
          @update:model-value="emit('change', row, { granted: $event })"
        />
        <template v-else-if="row.group.visibility === 'restricted'">
          <SBadge v-if="row.granted" tone="primary" data-testid="access-rate-access">{{ t('pricingAccess.granted') }}</SBadge>
          <SHint v-else inline size="xs" data-testid="access-rate-access">{{ t('pricingAccess.notGranted') }}</SHint>
        </template>
      </span>
    </template>
    <template #cell-rate_override="{ row }">
      <RateOverrideField
        :model-value="row.rate_override"
        :follow="row.rate.group_default"
        :readonly="!editable"
        :follow-text="t('pricingAccess.follow')"
        :disabled="disabled"
        :aria-label="t('pricingAccess.rateOf', { name: nameOf(row) })"
        @update:model-value="emit('change', row, { rate_override: $event })"
      />
    </template>
    <template #cell-rate="{ row }">
      <template v-if="row.accessible">
        <span class="font-mono font-semibold" data-testid="access-rate-value">{{ rateText(row.rate.value) }}</span>
        <SHint size="xs" data-testid="access-rate-source">{{ sourceText(row) }}</SHint>
      </template>
      <SHint v-else inline size="xs" data-testid="access-rate-source">{{ t('pricingAccess.unavailable') }}</SHint>
    </template>
    <template v-if="$slots.expand" #expand="{ row }">
      <slot name="expand" :row="row" />
    </template>
    <template #cell-actions="{ row }">
      <SRowAction tone="danger" :label="t('pricingAccess.remove')" :disabled="disabled" data-testid="access-rate-remove" @click="emit('remove', row)" />
    </template>
  </STable>
</template>
