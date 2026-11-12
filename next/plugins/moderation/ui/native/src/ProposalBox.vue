<script setup lang="ts">
import { computed } from 'vue'
import { SBadge, SButton, SHint, SLink } from '@sub2api/ui'
import { dt, enumLabel, idText } from './format'
import { useModHost } from './host'
import { proposalTone } from './tones'
import { useProposal } from './useProposal'
import type { Analysis, Proposal } from './types'

// The proposal of one analysis (prototype, design §9): the change it asks for
// (from → to), what became of it and, for those who may manage, the one
// action that fits its state: apply or reject a pending one, undo an applied
// one. Used by the AI review page and the user drawer alike.
const props = defineProps<{ analysis: Analysis & { proposal: Proposal }; compact?: boolean }>()
const emit = defineEmits<{ (e: 'decided', a: Analysis): void; (e: 'open-record', id: number): void }>()
const t = useModHost().t
const p = computed(() => props.analysis.proposal)
const { manage, busy, decide } = useProposal()

const levelText = (l: Proposal['to_level']) => (l ? enumLabel('level', l) : t('ai.offList'))
const undoable = computed(() => p.value.status === 'applied' || p.value.status === 'auto_applied')
const run = async (d: 'apply' | 'reject' | 'undo') => {
  const a = await decide(props.analysis, d)
  if (a) emit('decided', a)
}
</script>

<template>
  <div class="mod-proposal" :class="{ 'mod-proposal-pending': p.status === 'pending' }" data-testid="mod-proposal">
    <div class="mod-proposal-head">
      <span class="mod-proposal-move">
        {{ enumLabel('ai.action', p.action) }}
        <SHint inline size="xs">{{ levelText(p.from_level) }} → {{ levelText(p.to_level) }}</SHint>
      </span>
      <SBadge :tone="proposalTone(p.status)" dot>{{ enumLabel('ai.status', p.status) }}</SBadge>
    </div>
    <div v-if="!compact" class="mod-proposal-meta">
      <span v-if="p.evidence_ids.length">
        {{ t('ai.evidence', { n: p.evidence_ids.length }) }}:
        <SLink v-for="id in p.evidence_ids" :key="id" as="button" class="mod-num mod-ml" @click="emit('open-record', id)">#{{ id }}</SLink>
      </span>
      <span v-if="p.decided_at">{{ t('ai.decided', { who: p.decided_by ? idText(p.decided_by) : t('ai.system'), time: dt(p.decided_at) }) }}</span>
    </div>
    <div v-if="manage && (p.status === 'pending' || undoable)" class="mod-proposal-actions">
      <template v-if="p.status === 'pending'">
        <SButton size="sm" :variant="p.action === 'upgrade' ? 'danger' : 'primary'" :loading="busy === 'apply'" @click="run('apply')">{{ t('ai.apply') }}</SButton>
        <SButton size="sm" variant="ghost" :loading="busy === 'reject'" @click="run('reject')">{{ t('ai.reject') }}</SButton>
      </template>
      <SButton v-else size="sm" variant="ghost" icon="refresh" :loading="busy === 'undo'" @click="run('undo')">{{ t('ai.undo') }}</SButton>
    </div>
  </div>
</template>
