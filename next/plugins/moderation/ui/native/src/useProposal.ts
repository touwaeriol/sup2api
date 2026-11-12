import { computed, ref } from 'vue'
import { canManage, decideProposal, toastError, useModHost } from './host'
import { enumLabel } from './format'
import type { Analysis, ProposalDecision } from './types'

/**
 * Deciding a proposal (prototype, design §9): applying or undoing one asks
 * first and names the change; rejecting does not (it changes nothing). The
 * answer is the analysis as it is now, for the caller to show.
 */
export function useProposal() {
  const host = useModHost()
  const t = host.t
  const manage = computed(() => canManage())
  const busy = ref<ProposalDecision | null>(null)

  async function decide(a: Analysis, decision: ProposalDecision): Promise<Analysis | null> {
    const p = a.proposal
    if (!p || busy.value) return null
    if (decision !== 'reject') {
      const action = enumLabel('ai.action', p.action)
      const message = decision === 'apply' ? t('ai.applyConfirm', { id: a.user_id, action }) : t('ai.undoConfirm', { action })
      if (!(await host.confirm({ message, confirmText: t(`ai.${decision}`), danger: decision === 'apply' && p.action === 'upgrade' }))) return null
    }
    busy.value = decision
    try {
      const next = await decideProposal(a.id, decision)
      host.toast(t(`ai.done.${decision}`), 'success')
      return next
    } catch (e) {
      toastError(e)
      return null
    } finally {
      busy.value = null
    }
  }
  return { manage, busy, decide }
}
