<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SAlert, SBadge, SButton, SCard, SGrid, SHint, SLink, SStatCard, STable, type TableColumn } from '@sub2api/ui'
import AnalysisModal from './AnalysisModal.vue'
import ProposalBox from './ProposalBox.vue'
import UserRef from './UserRef.vue'
import { dt, enumLabel, money, num } from './format'
import { canManageSettings, fetchAnalyses, useModHost } from './host'
import { useStatus } from './status'
import { riskTone, trendTone } from './tones'
import { useAsync } from './useAsync'
import { proposalOf, proposalsVersion, useFeatures } from './useProposal'
import type { AnalysisRow } from './types'
import type { SectionKey } from './nav'

// AI review (prototype, design §9): what the analyses concluded across users.
// On top the proposals waiting for a moderator (the inbox), below the latest
// analyses of every user with their outcome. A user opens in the watch list
// drawer; an analysis opens with its trace. A backend without the list
// (feature analyses_list) gets an explanation instead of failing requests.
// Every decision, here or in a drawer, reloads the lists (proposalsVersion).
const emit = defineEmits<{ (e: 'open-watch', userId: number): void; (e: 'go-tab', tab: SectionKey): void }>()
const host = useModHost()
const t = host.t
const status = useStatus()
const today = computed(() => status.data.value?.today)

const has = useFeatures()
const supported = computed(() => has('analyses_list'))
const PENDING_MAX = 50
const pending = useAsync(() => fetchAnalyses({ proposal: 'pending', page_size: PENDING_MAX }), () => t('ai.loadFailed'))
const recent = useAsync(() => fetchAnalyses({ page_size: 10 }), () => t('ai.loadFailed'))
const reload = async () => {
  if (supported.value) await Promise.all([pending.reload(), recent.reload()])
}
watch([supported, proposalsVersion], reload, { immediate: true })
defineExpose({ reload })

const pendingTotal = computed(() => pending.data.value?.page.total ?? 0)
const userOf = (r: AnalysisRow) => ({ level: r.level, staff: r.staff, allowlisted: r.allowlisted })

const pendingColumns = computed<TableColumn[]>(() => [
  { key: 'user_id', label: t('ai.col.user'), nowrap: true, mobile: 'title' },
  { key: 'risk', label: t('ai.col.risk'), nowrap: true, mobile: 'status' },
  { key: 'summary', label: t('ai.col.summary'), mobile: 'subtitle' },
  { key: 'proposal', label: t('ai.col.proposal'), mobile: 'meta', mobileSpan: 2 },
  { key: 'created_at', label: t('ai.col.time'), nowrap: true, mobile: 'meta' }
])
const recentColumns = computed<TableColumn[]>(() => [
  { key: 'user_id', label: t('ai.col.user'), nowrap: true, mobile: 'title' },
  { key: 'risk', label: t('ai.col.risk'), nowrap: true, mobile: 'status' },
  { key: 'summary', label: t('ai.col.summary'), ellipsis: '24rem', mobile: 'subtitle' },
  { key: 'outcome', label: t('ai.col.outcome'), nowrap: true, mobile: 'meta' },
  { key: 'cost', label: t('ai.col.cost'), align: 'right', nowrap: true, mobile: 'meta' },
  { key: 'created_at', label: t('ai.col.time'), nowrap: true, mobile: 'detail' }
])

const opened = ref<number | null>(null)
const modalOpen = ref(false)
function openAnalysis(id: number) {
  opened.value = id
  modalOpen.value = true
}
</script>

<template>
  <div>
    <SAlert v-if="!supported" tone="info" data-testid="mod-ai-unsupported">{{ t('ai.unsupported') }}</SAlert>
    <div v-else class="mod-stack">
      <SGrid variant="stats" md-template="repeat(auto-fit, minmax(170px, 1fr))">
        <SStatCard :label="t('ai.stats.pending')" :value="pending.error.value ? '—' : num(pendingTotal)" icon="inbox" :tone="pendingTotal ? 'warning' : undefined" />
        <SStatCard :label="t('ai.stats.today')" :value="num(today?.analyses)" :sub="t('ai.stats.failed', { n: num(today?.analyses_failed) })" icon="bolt" />
        <SStatCard :label="t('ai.stats.tokens')" :value="num((today?.analysis_tokens_in ?? 0) + (today?.analysis_tokens_out ?? 0))" icon="usage" />
      </SGrid>

      <SCard :title="t('ai.pendingTitle')" :subtitle="t('ai.pendingHint')" data-testid="mod-ai-pending">
        <template v-if="canManageSettings()" #actions>
          <SLink as="button" @click="emit('go-tab', 'analysis')">{{ t('ai.settingsLink') }}</SLink>
        </template>
        <SAlert v-if="pending.error.value" tone="danger" dense>{{ pending.error.value }}</SAlert>
        <STable v-else :columns="pendingColumns" :rows="pending.data.value?.items ?? []" :loading="pending.loading.value" row-key="id" :empty-text="t('ai.pendingEmpty')">
          <template #cell-user_id="{ row }">
            <UserRef :user-id="row.user_id" link @click="emit('open-watch', $event)" />
            <span class="mod-sub mod-ml">{{ row.level ? enumLabel('level', row.level) : t('ai.offList') }}</span>
          </template>
          <template #cell-risk="{ row }">
            <SBadge v-if="row.risk" :tone="riskTone(row.risk)">{{ enumLabel('ai.risk', row.risk) }}</SBadge>
            <SBadge v-if="row.trend" :tone="trendTone(row.trend)" size="xs" class="mod-ml">{{ enumLabel('trend', row.trend) }}</SBadge>
          </template>
          <template #cell-summary="{ row }">
            <SLink as="button" class="mod-ai-summary" @click="openAnalysis(row.id)">{{ row.summary || t('ai.noSummary') }}</SLink>
          </template>
          <template #cell-proposal="{ row }">
            <ProposalBox v-if="proposalOf(row, userOf(row))" :analysis="row" :proposal="proposalOf(row, userOf(row))!" :user="userOf(row)" compact />
          </template>
          <template #cell-created_at="{ row }">{{ dt(row.created_at) }}</template>
        </STable>
        <SHint v-if="pendingTotal > PENDING_MAX" size="xs">{{ t('ai.pendingMore', { n: num(PENDING_MAX), total: num(pendingTotal) }) }}</SHint>
      </SCard>

      <SCard :title="t('ai.recentTitle')" :subtitle="t('ai.recentHint')" data-testid="mod-ai-recent">
        <template #actions>
          <SButton size="sm" variant="ghost" icon="refresh" :loading="recent.loading.value" @click="recent.reload()">{{ t('refresh') }}</SButton>
        </template>
        <SAlert v-if="recent.error.value" tone="danger" dense>{{ recent.error.value }}</SAlert>
        <STable v-else :columns="recentColumns" :rows="recent.data.value?.items ?? []" :loading="recent.loading.value" row-key="id">
          <template #cell-user_id="{ row }"><UserRef :user-id="row.user_id" link @click="emit('open-watch', $event)" /></template>
          <template #cell-risk="{ row }">
            <SBadge v-if="row.status !== 'ok'" tone="danger" dot>{{ enumLabel('analysisStatus', row.status) }}</SBadge>
            <SBadge v-else-if="row.risk" :tone="riskTone(row.risk)">{{ enumLabel('ai.risk', row.risk) }}</SBadge>
          </template>
          <template #cell-summary="{ row }">
            <SLink as="button" class="mod-ai-summary" :title="row.summary" @click="openAnalysis(row.id)">{{ row.summary || (row.status === 'ok' ? t('ai.noSummary') : enumLabel('analysisErr', row.error)) }}</SLink>
          </template>
          <template #cell-outcome="{ row }">
            <span v-if="row.proposal">{{ enumLabel('ai.action', row.proposal.action) }} · {{ enumLabel('ai.status', row.proposal.status) }}</span>
            <span v-else class="mod-sub">{{ t('ai.noChange') }}</span>
          </template>
          <template #cell-cost="{ row }"><span class="mod-num">{{ row.cost ? money(row.cost) : '—' }}</span></template>
          <template #cell-created_at="{ row }">{{ dt(row.created_at) }}</template>
        </STable>
      </SCard>

    </div>
    <AnalysisModal v-model:open="modalOpen" :analysis-id="opened" />
  </div>
</template>
