<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SModal, STabs, type TabItem } from '@sub2api/ui'
import type { Group } from '@/api/types'
import type { DraftedSetting } from '@/components/pricing/accessRateDrafts'
import GroupPricingEditor from './GroupPricingEditor.vue'
import GroupSegmentsEditor from './GroupSegmentsEditor.vue'
import type { GroupPricingTab } from './groupEditTabs'

// What a group costs and who may use it at which rate, for a caller who may
// read groups and not manage them: the price and user group tabs of the group
// editor, shown and not edited.
const props = defineProps<{ open: boolean; group: Group | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()
const { t } = useI18n()

const tab = ref<GroupPricingTab>('pricing')
/** The rows of the user groups tab, for the paid prices of the price tab. */
const segmentRows = ref<DraftedSetting[]>([])
const tabs = computed<TabItem[]>(() => (['pricing', 'segments'] as const).map((key) => ({ key, label: t(`groups.tabs.${key}`) })))

watch(
  () => props.open,
  (v) => {
    if (v) tab.value = 'pricing'
  },
  { immediate: true }
)
</script>

<template>
  <SModal :open="open" :title="group ? t('groups.pricingTitle', { name: group.name }) : ''" width="2xl" @update:open="emit('update:open', $event)">
    <STabs v-if="open && group" v-model="tab" :tabs="tabs" :aria-label="t('groups.pricingTitle', { name: group.name })" data-testid="group-pricing-view">
      <!-- Both tabs stay mounted: the prices tab takes the rates of the user groups from the other. -->
      <div class="min-h-[24rem]">
        <GroupPricingEditor v-show="tab === 'pricing'" :group-id="group.id" :segments="segmentRows" :default-rate="String(group.rate_multiplier)" />
        <GroupSegmentsEditor v-show="tab === 'segments'" :group-id="group.id" :visibility="group.visibility" :default-rate="String(group.rate_multiplier)" @rows="segmentRows = $event" />
      </div>
    </STabs>
    <template #footer>
      <SButton variant="primary" data-testid="group-pricing-view-close" @click="emit('update:open', false)">{{ t('common.close') }}</SButton>
    </template>
  </SModal>
</template>
