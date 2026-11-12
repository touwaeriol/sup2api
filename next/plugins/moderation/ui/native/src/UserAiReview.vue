<script setup lang="ts">
import { computed, ref } from 'vue'
import { isApiError } from '@sub2api/host'
import { SAlert, SBadge, SButton, SCard, SHint } from '@sub2api/ui'
import ProposalBox from './ProposalBox.vue'
import { dt, enumLabel, num } from './format'
import { analyzeWatch, canManage, errorMessage, useModHost } from './host'
import { riskTone, trendTone } from './tones'
import type { Analysis, Proposal, UserDetail } from './types'

// The AI review of one user (prototype, design §9), first thing in the user
// drawer: the latest assessment (risk, trend, summary), its proposal for the
// watch entry with the action that fits it, the analysis in progress, what
// triggers the next one, and "重新复核". The older analyses are on the
// drawer's tab below. Emits changed after anything that changes the user.
const props = defineProps<{ detail: UserDetail; state: 'idle' | 'pending' | 'running' }>()
const emit = defineEmits<{ (e: 'changed'): void; (e: 'open-record', id: number): void }>()
const host = useModHost()
const t = host.t
const manage = computed(() => canManage())
const entry = computed(() => props.detail.watch)
const latest = computed(() => props.detail.latest_analysis)
/** The entry changed while the analysis ran: shown, but its proposal no longer applies (§72.13.4). */
const stale = computed(() => latest.value?.error === 'stale')
const failed = computed(() => !!latest.value && latest.value.status !== 'ok')
const withProposal = (a: Analysis | null): a is Analysis & { proposal: Proposal } => !!a?.proposal && !stale.value

/** What starts the next analysis: violations to go, or the review before the entry expires. */
const next = computed(() => {
  const a = props.detail.analysis
  if (!entry.value || props.state !== 'idle') return ''
  const parts = a.every ? [t('ai.next', { n: num(Math.max(a.every - a.violations_since_analysis, 0)) })] : []
  if (entry.value.expires_at) parts.push(t('ai.nextExpiry', { time: dt(entry.value.expires_at) }))
  return parts.join(t('clauseSep'))
})

const analyzing = ref(false)
async function analyze() {
  analyzing.value = true
  try {
    await analyzeWatch(props.detail.user_id)
    host.toast(t('watch.analyzeQueued'), 'success')
  } catch (e) {
    const code = isApiError(e) ? (e.status === 404 ? 'not_found' : e.code) : ''
    const known: Record<string, [string, 'warning' | 'info']> = {
      no_analysis_channel: ['watch.noAnalysisChannel', 'warning'],
      analysis_pending: ['watch.analysisPending', 'info'],
      not_found: ['watch.notWatched', 'warning']
    }
    const [key, tone] = known[code] ?? []
    host.toast(key ? t(key) : errorMessage(e, t('actionFailed')), tone ?? 'error')
  } finally {
    analyzing.value = false
  }
  emit('changed')
}
</script>

<template>
  <SCard :title="t('ai.cardTitle')" dense data-testid="mod-user-ai">
    <template #actions>
      <SBadge v-if="state !== 'idle'" :tone="state === 'running' ? 'info' : 'warning'" dot>{{ enumLabel('analysisState', state) }}</SBadge>
      <SButton v-if="manage && entry" size="sm" icon="refresh" :loading="analyzing" :disabled="state !== 'idle'" @click="analyze">{{ t('ai.rerun') }}</SButton>
    </template>
    <div class="mod-stack">
      <template v-if="latest">
        <div class="mod-inline mod-gap mod-wrap">
          <SBadge v-if="latest.risk && !failed" :tone="riskTone(latest.risk)">{{ enumLabel('ai.risk', latest.risk) }}</SBadge>
          <SBadge v-if="latest.trend && !failed" :tone="trendTone(latest.trend)">{{ t('analysis.trend') }}: {{ enumLabel('trend', latest.trend) }}</SBadge>
          <SBadge v-if="failed" tone="danger" dot>{{ enumLabel('analysisStatus', latest.status) }}</SBadge>
          <SHint inline size="xs">#{{ latest.id }} · {{ enumLabel('source', latest.source) }} · {{ dt(latest.created_at) }}</SHint>
        </div>
        <SAlert v-if="failed" tone="danger" dense>{{ enumLabel('analysisErr', latest.error) }}</SAlert>
        <SAlert v-else-if="stale" tone="warning" dense>{{ enumLabel('analysisErr', 'stale') }}</SAlert>
        <p v-if="latest.summary" class="mod-analysis-summary mod-prewrap">{{ latest.summary }}</p>
        <ProposalBox v-if="withProposal(latest)" :analysis="latest" @decided="emit('changed')" @open-record="emit('open-record', $event)" />
        <SHint v-else-if="!failed && !stale" size="xs">{{ t('ai.noChange') }}</SHint>
      </template>
      <SHint v-else size="xs">{{ t('ai.never') }}</SHint>
      <SHint v-if="next" size="xs">{{ next }}</SHint>
    </div>
  </SCard>
</template>
