<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SBadge, SIcon } from '@sub2api/ui'
import type { Group } from '@/api/types'

// Whether an account group is public (everyone may use it) or private (only
// the user groups it is granted to), as a badge. Other attributes
// (data-testid, …) are the badge's.
defineProps<{ visibility: Group['visibility']; size?: 'xs' | 'sm' }>()
const { t } = useI18n()
</script>

<template>
  <SBadge v-if="visibility === 'public'" tone="success" :size="size">{{ t('pricingAccess.public') }}</SBadge>
  <SBadge v-else tone="gray" :size="size">
    <SIcon name="lock" class="h-3 w-3" />
    {{ t('pricingAccess.private') }}
  </SBadge>
</template>
