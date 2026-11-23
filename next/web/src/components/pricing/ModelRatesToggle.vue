<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SButton } from '@sub2api/ui'

// The button under a row of grants and rates that opens the model rates of
// the pair: how many there are ("Model rates 2"), or, editable and without
// any, an offer to set some. Other attributes (data-testid) are SButton's.
defineProps<{ count: number; expanded: boolean; editable?: boolean }>()
const emit = defineEmits<{ (e: 'toggle'): void }>()
const { t } = useI18n()
</script>

<template>
  <SButton
    size="xs"
    :variant="count ? 'secondary' : 'ghost'"
    icon="price"
    :icon-right="expanded ? 'chevron-down' : 'chevron-right'"
    :aria-expanded="expanded"
    @click="emit('toggle')"
  >
    {{ count || !editable ? t('segments.modelRates.toggle', { n: count }) : t('segments.modelRates.setUp') }}
  </SButton>
</template>
