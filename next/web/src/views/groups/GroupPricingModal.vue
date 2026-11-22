<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SButton, SModal } from '@sub2api/ui'
import type { Group } from '@/api/types'
import ModelPriceTable from '@/components/pricing/ModelPriceTable.vue'

// The pricing table of a group, for a caller who may read groups and not
// manage them: the price table of the group editor, shown and not edited.
defineProps<{ open: boolean; group: Group | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()
const { t } = useI18n()
</script>

<template>
  <SModal :open="open" :title="group ? t('groups.pricingTitle', { name: group.name }) : ''" width="2xl" @update:open="emit('update:open', $event)">
    <div v-if="open && group" data-testid="group-pricing-view">
      <ModelPriceTable :group-id="group.id" :base-rate="String(group.rate_multiplier)" />
    </div>
    <template #footer>
      <SButton variant="primary" data-testid="group-pricing-view-close" @click="emit('update:open', false)">{{ t('common.close') }}</SButton>
    </template>
  </SModal>
</template>
