<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SSelect, type SelectOption } from '@sub2api/ui'
import type { SegmentKind } from '@/api/pricing'
import ModelPriceTable from '@/components/pricing/ModelPriceTable.vue'
import type { DraftedSetting } from '@/components/pricing/accessRateDrafts'
import type { ModelOffer } from '@/components/pricing/offeredModels'
import { rateText, validRate } from '@/components/pricing/pricingFormat'

// The model prices of a group, paid at whose rate the caller picks: everyone
// at the group rate (`defaultRate`, as the form has it now), or a user group
// or a personal user at the rate it gets in the group. `segments` are the
// rows of the user groups tab beside it, under their drafts, so the rates
// follow what is being edited there. The table keeps its drafts until the
// parent calls save(gid).
const props = defineProps<{ groupId: number | null; offer?: ModelOffer | null; segments: DraftedSetting[]; defaultRate: string; editable?: boolean }>()
const { t } = useI18n()

const table = ref<InstanceType<typeof ModelPriceTable>>()
/** Whose rate the paid prices are at: a user group's id, or everyone's. */
const payer = ref<number | 'all'>('all')

const groupRate = computed(() => (validRate(props.defaultRate) ? props.defaultRate.trim() : ''))
const optionOf = (s: DraftedSetting): SelectOption => ({
  value: s.id,
  label: `${s.segment.name} ${rateText(s.rate.value || null)}`,
  tag: s.accessible ? undefined : t('groups.pricing.notAccessible'),
  muted: !s.accessible
})
const kindOptions = (kind: SegmentKind) => props.segments.filter((s) => s.segment.kind === kind).map(optionOf)
const payers = computed<SelectOption[]>(() => {
  const groups: SelectOption[] = [
    { value: 'named', label: t('groups.pricing.payerSegments'), options: kindOptions('named') },
    { value: 'personal', label: t('groups.pricing.payerPersonal'), options: kindOptions('personal') }
  ]
  return [{ value: 'all', label: t('groups.pricing.payerAll', { rate: rateText(groupRate.value || null) }) }, ...groups.filter((g) => g.options?.length)]
})
/** The rate paid at: a user group gone from the rows falls back to everyone's. */
const rate = computed(() => {
  const row = props.segments.find((s) => s.id === payer.value)
  return row ? row.rate.value : groupRate.value
})

defineExpose({
  save: (gid: number) => table.value?.save(gid) ?? Promise.resolve(true),
  dirty: computed(() => !!table.value?.dirty)
})
</script>

<template>
  <ModelPriceTable ref="table" :group-id="groupId" :offer="offer" :rate="rate" :editable="editable">
    <template #rate>
      <label class="flex items-center gap-2 text-xs text-fg-muted">
        {{ t('groups.pricing.payer') }}
        <SSelect :model-value="payer" :options="payers" size="sm" class="w-60 max-w-full" searchable :aria-label="t('groups.pricing.payer')" data-testid="group-pricing-payer" @update:model-value="payer = typeof $event === 'number' ? $event : 'all'" />
      </label>
    </template>
  </ModelPriceTable>
</template>
