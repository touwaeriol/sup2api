<script setup lang="ts">
import { watch } from 'vue'
import { SAlert, SBadge, SEmpty, SHint } from '@sub2api/ui'
import UserRef from './UserRef.vue'
import { dt, enumLabel, watchReason } from './format'
import { fetchWatchEvents, useModHost } from './host'
import { useAsync } from './useAsync'

// The watch list history of a user, with who made each change. Reloads when
// `version` changes.
const props = defineProps<{ userId: number; version: number }>()
const emit = defineEmits<{ (e: 'total', n: number): void }>()
const t = useModHost().t

const history = useAsync(() => fetchWatchEvents(props.userId), () => t('watch.loadFailed'))
watch(() => [props.userId, props.version], history.reload, { immediate: true })
watch(history.data, (h) => emit('total', h?.length ?? 0))
</script>

<template>
  <div>
    <SAlert v-if="history.error.value" tone="danger" dense>{{ history.error.value }}</SAlert>
    <SEmpty v-else-if="!history.data.value?.length" :text="t('watch.noHistory')" />
    <ol v-else class="mod-history">
      <li v-for="e in history.data.value" :key="e.id">
        <div class="mod-history-head">
          <SBadge>{{ enumLabel('watchAction', e.action) }}</SBadge>
          <span v-if="e.from_level || e.to_level" class="mod-sub">{{ enumLabel('level', e.from_level) }} → {{ enumLabel('level', e.to_level) }}</span>
          <SHint inline size="xs">{{ dt(e.created_at) }}</SHint>
          <UserRef v-if="e.operator_id" :user-id="e.operator_id" class="mod-sub" />
        </div>
        <div v-if="e.reason" class="mod-sub">{{ watchReason(e.action, e.reason) }}</div>
      </li>
    </ol>
  </div>
</template>
