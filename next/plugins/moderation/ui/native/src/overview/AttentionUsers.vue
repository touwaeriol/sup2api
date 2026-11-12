<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { SBadge, SChipList, SLink } from '@sub2api/ui'
import AsyncCard from './AsyncCard.vue'
import UserRef from '../UserRef.vue'
import { onOverviewTick } from './tick'
import { useAsync } from '../useAsync'
import { categoryLabel, enumLabel } from '../format'
import { fetchWatchList, useModHost } from '../host'
import type { WatchItem } from '../types'
import type { SectionKey } from '../nav'
import { levelTone } from '../tones'

// "最需关注的人": the first six of the watch list in its default order (new
// records since last seen first), one line a user: level, top categories
// (the rest behind "+N"), new records and trend. A row opens the user's
// watch detail.
const emit = defineEmits<{ (e: 'open-watch', userId: number): void; (e: 'go-tab', tab: SectionKey): void }>()
const t = useModHost().t

const list = useAsync(() => fetchWatchList({ page: 1, page_size: 6, sort: 'new' }), () => t('watch.loadFailed'))
onMounted(list.reload)
onOverviewTick(list.reload)
const items = computed(() => list.data.value?.items || [])
const categoryChips = (w: WatchItem) => [
  ...w.top_categories.map((c) => ({ key: c.category, label: `${categoryLabel(c.category, c.label)} ${c.count}`, tone: 'warning' as const, title: c.category })),
  ...(w.top_other ? [{ key: '_other', label: t('chips.other', { n: w.top_other }) }] : [])
]
</script>

<template>
  <AsyncCard
    :title="t('attention.title')"
    :subtitle="t('attention.hint')"
    :error="list.error.value"
    :loading="list.loading.value"
    :empty="!items.length"
    :empty-text="t('attention.empty')"
    empty-icon="user"
  >
    <template #actions><SLink as="button" @click="emit('go-tab', 'watch')">{{ t('attention.all') }}</SLink></template>
    <ul class="mod-attention">
      <li v-for="w in items" :key="w.user_id">
        <button type="button" class="mod-attention-item" @click="emit('open-watch', w.user_id)">
          <UserRef :user-id="w.user_id" class="mod-ellipsis" />
          <SBadge :tone="levelTone(w.level)" size="xs">{{ enumLabel('level', w.level) }}</SBadge>
          <span class="mod-attention-cats"><SChipList :items="categoryChips(w)" :max="1" /></span>
          <span v-if="w.hits_since_seen" class="mod-text-warning mod-sub">{{ t('attention.new', { n: w.hits_since_seen }) }}</span>
          <span v-else class="mod-sub">{{ t('attention.noNew') }}</span>
        </button>
      </li>
    </ul>
  </AsyncCard>
</template>
