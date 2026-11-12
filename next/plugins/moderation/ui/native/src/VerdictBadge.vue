<script setup lang="ts">
import { computed } from 'vue'
import { SBadge, SHint } from '@sub2api/ui'
import { finalVerdict } from './format'
import { useModHost } from './host'
import type { ModEvent } from './types'

// Final verdict of a record (§72.9): the moderator's review.
const props = defineProps<{ event: Pick<ModEvent, 'review_status' | 'result'> }>()
const t = useModHost().t
const v = computed(() => finalVerdict(props.event))
</script>

<template>
  <span v-if="v" class="mod-verdict-badge">
    <SBadge :tone="v.tone" dot>{{ t(`verdict.${v.key}`) }}</SBadge>
  </span>
  <SHint v-else inline>—</SHint>
</template>
