<script setup lang="ts">
import { computed } from 'vue'
import type { GroupServedProtocol } from '@/api/types'
import { usePlatforms } from '@/composables/platforms'
import { servedEndpoints } from '@/views/groups/servedProtocols'
import PlatformEndpointList from './PlatformEndpointList.vue'

// The endpoints of the protocols a group serves (the server's
// served_protocols, as the group's detail lists them), grouped by platform:
// what an API key of the group can call. `max` limits endpoints per platform.
const props = defineProps<{ served: GroupServedProtocol[]; max?: number }>()
const platforms = usePlatforms()
platforms.load()

const own = computed(() => servedEndpoints(platforms.platforms.value, props.served))
</script>

<template>
  <div class="space-y-2" data-testid="platform-endpoints">
    <PlatformEndpointList v-for="g in own" :key="g.id" v-bind="g" :max="max" />
  </div>
</template>
