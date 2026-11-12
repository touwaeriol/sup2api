<script setup lang="ts">
import { computed } from 'vue'
import { SBadge, SButton, SHint, SLink } from '@sub2api/ui'
import { dt, enumLabel, idText } from './format'
import { useModHost } from './host'
import { proposalTone } from './tones'
import { blockedReason, useProposal, type ProposalUser } from './useProposal'
import type { Analysis, Proposal } from './types'

// A proposal for a user's watch entry (prototype, design §9.4): the change
// (from → to), what became of it and, for those who may manage, the action
// that fits: apply or reject a pending one, undo an applied one. A change
// the backend would refuse for this user says why instead. Used by the AI
// review page and the user drawer alike (proposalOf gives the proposal).
const props = defineProps<{ analysis: Analysis; proposal: Proposal; user: ProposalUser; compact?: boolean }>()
const emit = defineEmits<{ (e: 'decided'): void; (e: 'open-record', id: number): void }>()
const t = useModHost().t
const p = computed(() => props.proposal)
const { manage, canUndo, busy, decide } = useProposal()

const levelText = (l: Proposal['to_level']) => (l ? enumLabel('level', l) : t('ai.offList'))
const blocked = computed(() => blockedReason(p.value, props.user))
const pending = computed(() => p.value.status === 'pending' && !blocked.value)
const undoable = computed(() => !p.value.legacy && (p.value.status === 'applied' || p.value.status === 'auto_applied'))
const run = async (d: 'apply' | 'reject' | 'undo') => (await decide(props.analysis, p.value, d)) && emit('decided')
</script>

<template>
  <div class="mod-proposal" :class="{ 'mod-proposal-pending': pending }" data-testid="mod-proposal">
    <div class="mod-proposal-head">
      <span class="mod-proposal-move">
        {{ enumLabel('ai.action', p.action) }}
        <SHint inline size="xs">{{ levelText(p.from_level) }} → {{ levelText(p.to_level) }}</SHint>
      </span>
      <SBadge :tone="proposalTone(p.status)" dot>{{ enumLabel('ai.status', p.status) }}</SBadge>
    </div>
    <div v-if="!compact && (p.evidence_ids.length || p.decided_at)" class="mod-proposal-meta">
      <span v-if="p.evidence_ids.length">
        {{ t('ai.evidence', { n: p.evidence_ids.length }) }}:
        <SLink v-for="id in p.evidence_ids" :key="id" as="button" class="mod-num mod-ml" @click="emit('open-record', id)">#{{ id }}</SLink>
      </span>
      <span v-if="p.decided_at">{{ t('ai.decided', { who: p.decided_by ? idText(p.decided_by) : t('ai.system'), time: dt(p.decided_at) }) }}</span>
    </div>
    <SHint v-if="blocked && p.status === 'pending'" size="xs">{{ t(`ai.blocked.${blocked}`) }}</SHint>
    <div v-else-if="manage && (pending || undoable)" class="mod-proposal-actions">
      <template v-if="pending">
        <SButton size="sm" :variant="p.action === 'upgrade' ? 'danger' : 'primary'" :loading="busy === 'apply'" @click="run('apply')">{{ t('ai.apply') }}</SButton>
        <SButton v-if="!p.legacy" size="sm" variant="ghost" :loading="busy === 'reject'" @click="run('reject')">{{ t('ai.reject') }}</SButton>
      </template>
      <SButton v-else-if="canUndo" size="sm" variant="ghost" icon="refresh" :loading="busy === 'undo'" @click="run('undo')">{{ t('ai.undo') }}</SButton>
      <SHint v-else size="xs">{{ t('ai.undoLater') }}</SHint>
    </div>
  </div>
</template>
