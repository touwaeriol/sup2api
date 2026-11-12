<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SCard, SEmpty, SLink, SStatCard } from '@sub2api/ui'
import { num, pct } from '../format'
import { useModHost } from '../host'
import type { AIReviewCounts, TodayCounts } from '../types'
import type { SectionKey } from '../nav'

// Today's AI review (new-api ai-review-card): verdicts of the records the
// analyses reviewed, with a hint when false positives dominate.
const props = defineProps<{ review?: AIReviewCounts; today?: TodayCounts }>()
const emit = defineEmits<{ (e: 'go-tab', tab: SectionKey): void }>()
const t = useModHost().t

/** A false-positive share at or above this (with enough reviews) hints at strict thresholds. */
const HIGH_FALSE_POSITIVE = 0.4
const MIN_REVIEWED = 5

const shares = computed(() => {
  const r = props.review
  if (!r) return []
  return [
    { key: 'violation', count: r.violation, cls: 'mod-bar-danger', text: 'mod-text-danger' },
    { key: 'false_positive', count: r.false_positive, cls: 'mod-bar-success', text: 'mod-text-success' },
    { key: 'unsure', count: r.unsure, cls: 'mod-bar-warning', text: 'mod-text-warning' }
  ]
})
/** reviewed is the sum of the verdicts (the counters may lag behind it) */
const reviewed = computed(() => Math.max(props.review?.reviewed ?? 0, shares.value.reduce((s, x) => s + x.count, 0)))
const share = (n: number) => (reviewed.value ? n / reviewed.value : 0)
const tooStrict = computed(() => reviewed.value >= MIN_REVIEWED && share(props.review?.false_positive ?? 0) >= HIGH_FALSE_POSITIVE)
const tokens = computed(() => (props.today?.analysis_tokens_in ?? 0) + (props.today?.analysis_tokens_out ?? 0))
</script>

<template>
  <SCard :title="t('aiReview.title')" :subtitle="t('aiReview.hint')">
    <template #actions><SLink as="button" @click="emit('go-tab', 'watch')">{{ t('aiReview.watch') }}</SLink></template>
    <SEmpty v-if="!reviewed" :text="t('aiReview.empty')" icon="eyeglass" />
    <div v-else class="mod-stack">
      <SStatCard
        compact
        :label="t('aiReview.reviewed')"
        :value="num(reviewed)"
        :sub="t('aiReview.runs', { runs: num(today?.analyses), failed: num(today?.analyses_failed), tokens: num(tokens) })"
      />
      <div class="mod-bar" role="img" :aria-label="t('aiReview.title')">
        <span v-for="s in shares" :key="s.key" :class="s.cls" :style="{ width: `${share(s.count) * 100}%` }" />
      </div>
      <div class="mod-legend">
        <span v-for="s in shares" :key="s.key">
          <b :class="s.text">{{ t(`aiVerdict.${s.key}`) }} {{ num(s.count) }}</b>
          <span class="mod-sub"> ({{ pct(share(s.count)) }})</span>
        </span>
      </div>
      <SAlert v-if="tooStrict" tone="warning" dense>
        {{ t('aiReview.tooStrict') }}
        <SLink as="button" @click="emit('go-tab', 'flows')">{{ t('aiReview.checkFlows') }}</SLink>
      </SAlert>
      <p class="mod-sub">{{ t('aiReview.note') }}</p>
    </div>
  </SCard>
</template>
