<script setup lang="ts">
import { computed, ref } from 'vue'
import { SCard, SEmpty, SGrid, SLink, SSegmented, SStatCard } from '@sub2api/ui'
import { num } from '../format'
import { useModHost } from '../host'
import type { Funnel } from '../types'
import type { SectionKey } from '../nav'

// Today's funnel per flow (new-api funnel-card), left to right: what entered,
// what each stage received, hit and passed on (a later stage's pass
// overturns), and the flow result.
const props = defineProps<{ funnels: Funnel[] }>()
const emit = defineEmits<{ (e: 'go-tab', tab: SectionKey): void }>()
const t = useModHost().t

const picked = ref<number | null>(null)
const funnel = computed(() => props.funnels.find((f) => f.flow_id === picked.value) ?? props.funnels[0])
const options = computed(() => props.funnels.map((f) => ({ value: f.flow_id, label: f.name || `#${f.flow_id}` })))
</script>

<template>
  <SCard :title="t('funnel.title')" :subtitle="t('funnel.hint')">
    <template #actions><SLink as="button" @click="emit('go-tab', 'flows')">{{ t('funnel.edit') }}</SLink></template>
    <SEmpty v-if="!funnel" :text="t('funnel.empty')" icon="filter" />
    <div v-else class="mod-stack">
      <SSegmented v-if="funnels.length > 1" :model-value="funnel.flow_id" :options="options" size="sm" @update:model-value="picked = Number($event)" />
      <SGrid variant="stats" md-template="repeat(auto-fit, minmax(7.5rem, 1fr))" :gap="3">
        <SStatCard compact :label="t('funnel.entered')" :value="num(funnel.stages[0]?.received)" />
        <SStatCard
          v-for="(s, i) in funnel.stages"
          :key="s.stage"
          compact
          :label="t('trace.stageN', { n: s.stage })"
          :value="num(s.received)"
          :sub="t('funnel.hit', { n: num(s.hit) })"
          :sub2="i === 0 ? t('funnel.passed', { n: num(s.passed) }) : t('funnel.overturned', { n: num(s.passed) })"
        />
        <SStatCard compact :label="t('funnel.result')" :value="t('funnel.suspicious', { n: num(funnel.suspicious) })" :sub="t('funnel.blocked', { n: num(funnel.blocked) })" />
      </SGrid>
    </div>
  </SCard>
</template>
