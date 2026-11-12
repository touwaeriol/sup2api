import { describe, expect, it } from 'vitest'
import { blockedReason, proposalOf } from './useProposal'
import type { Analysis, Proposal } from './types'

const base: Analysis = { id: 9, user_id: 4, source: 'auto', status: 'ok', steps: 1, tokens_in: 0, tokens_out: 0, duration_ms: 0, created_at: '' }
const pending: Proposal = { action: 'upgrade', from_level: 'observe', to_level: 'enforce', status: 'pending', evidence_ids: [1] }

describe('proposalOf', () => {
  it('shows the backend proposal, or none when the backend says none', () => {
    expect(proposalOf({ ...base, proposal: pending }, { level: 'observe' })).toBe(pending)
    expect(proposalOf({ ...base, proposal: null, suggestion: 'block' }, { level: 'observe' })).toBeNull()
  })
  it('turns the suggestion of a backend without proposals into a pending legacy proposal', () => {
    expect(proposalOf({ ...base, suggestion: 'block' }, { level: 'observe' })).toEqual({ action: 'upgrade', from_level: 'observe', to_level: 'enforce', status: 'pending', evidence_ids: [], legacy: true })
    expect(proposalOf({ ...base, suggestion: 'observe' }, { level: 'enforce' })?.action).toBe('downgrade')
    expect(proposalOf({ ...base, suggestion: 'release' }, { level: 'observe' })?.action).toBe('remove')
    expect(proposalOf({ ...base, suggestion: 'observe' }, { level: 'observe' })).toBeNull()
    expect(proposalOf({ ...base, suggestion: 'observe' }, { level: null })).toBeNull()
  })
  it('shows nothing for a failed or stale analysis', () => {
    expect(proposalOf({ ...base, status: 'failed', proposal: pending }, { level: 'observe' })).toBeNull()
    expect(proposalOf({ ...base, error: 'stale', proposal: pending }, { level: 'observe' })).toBeNull()
  })
})

describe('blockedReason', () => {
  it('names what the backend would refuse', () => {
    expect(blockedReason(pending, { level: 'observe', staff: true })).toBe('staff')
    expect(blockedReason(pending, { level: 'observe', allowlisted: true })).toBe('allowlisted')
    expect(blockedReason({ ...pending, action: 'downgrade', to_level: 'observe' }, { level: 'enforce', allowlisted: true })).toBe('')
  })
})
