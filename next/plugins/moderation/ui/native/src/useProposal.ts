import { computed, ref } from 'vue'
import { canManage, createWatch, decideProposal, deleteWatch, toastError, updateWatch, useModHost } from './host'
import { enumLabel } from './format'
import { useStatus } from './status'
import type { Analysis, Proposal, ProposalAction, ProposalDecision, Suggestion, WatchLevel } from './types'

// The proposal of an analysis (prototype, design §9.4) and deciding it.

/** Who the proposal is about, as far as acting on it goes. */
export interface ProposalUser {
  level: WatchLevel | null
  staff?: boolean
  allowlisted?: boolean
}

const SUGGESTED_LEVEL: Record<Suggestion, WatchLevel | null> = { block: 'enforce', observe: 'observe', release: null }
const RANK = { none: 0, observe: 1, enforce: 2 } as const
/** The change from one level to another (null = off the list), or null for none. */
const actionOf = (from: WatchLevel | null, to: WatchLevel | null): ProposalAction | null => {
  if (from === to || (from === null && to !== 'enforce')) return null
  if (to === null) return 'remove'
  return RANK[to] > RANK[from ?? 'none'] ? 'upgrade' : 'downgrade'
}

/**
 * The proposal shown for an analysis, or null: none, a failed analysis, or a
 * stale one (the entry changed while it ran). A backend without proposals
 * (proposal absent, not null) sends only a suggestion: it becomes a pending
 * proposal the watch list API applies (legacy), as "按 AI 建议执行" did.
 */
export function proposalOf(a: Analysis | null | undefined, user: ProposalUser): Proposal | null {
  if (!a || a.status !== 'ok' || a.error === 'stale') return null
  if (a.proposal !== undefined) return a.proposal
  if (!a.suggestion) return null
  const to = SUGGESTED_LEVEL[a.suggestion]
  const action = actionOf(user.level, to)
  if (!action) return null
  return { action, from_level: user.level, to_level: to, status: 'pending', evidence_ids: [], legacy: true }
}

/** Why a proposal cannot be applied to this user, or '' (the backend refuses these; the old bar hid them). */
export function blockedReason(p: Proposal, user: ProposalUser): string {
  if (user.staff) return 'staff'
  if (user.allowlisted && p.to_level === 'enforce') return 'allowlisted'
  return ''
}

/** Bumped by every decision: the views that show watch levels or proposals reload on it. */
export const proposalsVersion = ref(0)

/** The backend feature flags of the status (Runtime.features). */
export function useFeatures() {
  const status = useStatus()
  return (f: string) => !!status.data.value?.runtime.features?.includes(f)
}

/**
 * Deciding a proposal: applying or undoing asks first and names the change;
 * rejecting does not (it changes nothing). Undo is offered only once every
 * node implements proposals (feature "proposals").
 */
export function useProposal() {
  const host = useModHost()
  const t = host.t
  const has = useFeatures()
  const manage = computed(() => canManage())
  const canUndo = computed(() => has('proposals'))
  const busy = ref<ProposalDecision | null>(null)

  async function send(a: Analysis, p: Proposal, decision: ProposalDecision) {
    if (!p.legacy) return decideProposal(a.id, decision)
    const link = { analysis_id: a.id, reason: t('ai.applyReason', { id: a.id }) }
    if (!p.to_level) return deleteWatch(a.user_id, link)
    if (p.from_level) return updateWatch(a.user_id, { level: p.to_level, ...link })
    return createWatch({ user_id: a.user_id, level: p.to_level, ...link })
  }

  /** Decides p, the proposal of a; true when it was decided. */
  async function decide(a: Analysis, p: Proposal, decision: ProposalDecision): Promise<boolean> {
    if (busy.value) return false
    if (decision !== 'reject') {
      const action = enumLabel('ai.action', p.action)
      const message = decision === 'apply' ? t('ai.applyConfirm', { id: a.user_id, action }) : t('ai.undoConfirm', { action })
      if (!(await host.confirm({ message, confirmText: t(`ai.${decision}`), danger: decision === 'apply' && p.action === 'upgrade' }))) return false
    }
    busy.value = decision
    try {
      await send(a, p, decision)
      host.toast(t(`ai.done.${decision}`), 'success')
      proposalsVersion.value++
      return true
    } catch (e) {
      toastError(e)
      return false
    } finally {
      busy.value = null
    }
  }
  return { manage, canUndo, busy, decide }
}
