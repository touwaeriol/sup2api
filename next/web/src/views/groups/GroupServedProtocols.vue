<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint } from '@sub2api/ui'
import type { GroupServedProtocol } from '@/api/types'
import { usePlatforms } from '@/composables/platforms'
import PlatformEndpointList from '@/views/platforms/PlatformEndpointList.vue'
import { servedEndpoints } from './servedProtocols'

// What a group can be called on (DESIGN-2026-11-17-PROTOCOL-BINDING §5.2):
// the protocols its member accounts open (the server's served_protocols), as
// the endpoints of each platform with the number of accounts serving each.
// An endpoint that is not listed is served by no account and answers 404.
const props = defineProps<{ served: GroupServedProtocol[] }>()
const { t } = useI18n()
const platforms = usePlatforms()
platforms.load()

const groups = computed(() => servedEndpoints(platforms.platforms.value, props.served))
const accounts = computed(() => Object.fromEntries(props.served.map(s => [s.protocol, s.accounts])))
</script>

<template>
  <div class="space-y-2" data-testid="group-served-protocols">
    <SHint v-if="!groups.length" size="xs" tone="warning" data-testid="group-served-none">{{ t('platforms.servedNone') }}</SHint>
    <template v-else>
      <PlatformEndpointList v-for="g in groups" :key="g.id" v-bind="g" known :accounts="accounts" />
      <SHint size="xs">{{ t('platforms.servedHint') }}</SHint>
    </template>
  </div>
</template>
