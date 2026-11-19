import { describe, expect, it } from 'vitest'
import type { Account } from '@/api/types'
import { buildBulkPatch, type BulkFields, type BulkFlags } from './bulkEdit'

const account = (over: Partial<Account> = {}) => ({ id: 1, name: 'a', priority: 10, weight: 1, max_concurrency: 10, tpm_limit: 0, rpm_limit: 0, spm_limit: 0, group_ids: [1, 2], proxy_id: null, auto_disable: true, ...over }) as unknown as Account
const fields = (over: Partial<BulkFields> = {}): BulkFields => ({ priority: 10, weight: 1, max_concurrency: 10, tpm_limit: 0, rpm_limit: 0, spm_limit: 0, spm_window_seconds: 60, group_ids: [], groupMode: 'replace', proxy_id: null, auto_disable: true, protocols: null, model_protocols: [], ...over })
const flags = (over: Partial<BulkFlags> = {}): BulkFlags => ({ priority: false, weight: false, max_concurrency: false, tpm_limit: false, rpm_limit: false, spm_limit: false, groups: false, proxy: false, auto_disable: false, protocols: false, model_protocols: false, ...over })

describe('bulk account patch', () => {
  it('sends only ticked fields that differ from the account', () => {
    expect(buildBulkPatch(account(), fields({ priority: 5, weight: 3, rpm_limit: 60 }), flags({ priority: true, rpm_limit: true }))).toEqual({ priority: 5, rpm_limit: 60 })
    expect(buildBulkPatch(account({ priority: 5 }), fields({ priority: 5 }), flags({ priority: true }))).toBeNull()
    expect(buildBulkPatch(account(), fields({ weight: 1, spm_limit: 4 }), flags({ weight: true, spm_limit: true }))).toEqual({ spm_limit: 4 })
    expect(buildBulkPatch(account({ spm_limit: 4 }), fields({ spm_limit: 4 }), flags({ spm_limit: true }))).toBeNull()
  })

  it('applies the session limit with its period, an absent period being 1 minute', () => {
    expect(buildBulkPatch(account({ spm_limit: 4 }), fields({ spm_limit: 4, spm_window_seconds: 300 }), flags({ spm_limit: true }))).toEqual({ spm_window_seconds: 300 })
    expect(buildBulkPatch(account({ spm_window_seconds: 300 }), fields({ spm_limit: 9, spm_window_seconds: 300 }), flags({ spm_limit: true }))).toEqual({ spm_limit: 9 })
    expect(buildBulkPatch(account({ spm_window_seconds: 300 }), fields({ spm_window_seconds: null }), flags({ spm_limit: true }))).toEqual({ spm_window_seconds: 60 })
    expect(buildBulkPatch(account(), fields({ spm_window_seconds: 3600 }), flags())).toBeNull()
  })

  it('replaces, adds to or removes from each account its own groups', () => {
    const a = account({ group_ids: [1, 2] })
    expect(buildBulkPatch(a, fields({ group_ids: [3], groupMode: 'replace' }), flags({ groups: true }))).toEqual({ group_ids: [3] })
    expect(buildBulkPatch(a, fields({ group_ids: [2, 3], groupMode: 'add' }), flags({ groups: true }))).toEqual({ group_ids: [1, 2, 3] })
    expect(buildBulkPatch(a, fields({ group_ids: [2, 9], groupMode: 'remove' }), flags({ groups: true }))).toEqual({ group_ids: [1] })
    expect(buildBulkPatch(a, fields({ group_ids: [9], groupMode: 'remove' }), flags({ groups: true }))).toBeNull()
    expect(buildBulkPatch(a, fields({ group_ids: [], groupMode: 'replace' }), flags({ groups: true }))).toEqual({ group_ids: [] })
  })

  it('sets and clears the proxy and the automatic disabling preference', () => {
    expect(buildBulkPatch(account(), fields({ proxy_id: 4 }), flags({ proxy: true }))).toEqual({ proxy_id: 4 })
    expect(buildBulkPatch(account({ proxy_id: 4 }), fields({ proxy_id: null }), flags({ proxy: true }))).toEqual({ proxy_id: null })
    expect(buildBulkPatch(account(), fields({ auto_disable: false }), flags({ auto_disable: true }))).toEqual({ auto_disable: false })
  })

  it('replaces the own protocols and the protocols per model of each account, leaving one that already has them', () => {
    const rules = [{ models: ['gpt-5.6', 'claude-sonnet-4-5'], protocols: ['openai.chat', 'anthropic.messages'] }]
    const both = flags({ protocols: true, model_protocols: true })
    expect(buildBulkPatch(account(), fields({ protocols: ['openai.chat'], model_protocols: rules }), both)).toEqual({ protocols: ['openai.chat'], model_protocols: rules })
    // Back to every own protocol (null) and to no rule.
    expect(buildBulkPatch(account({ protocols: ['openai.chat'], model_protocols: rules }), fields(), both)).toEqual({ protocols: null, model_protocols: [] })
    // The same protocols in another order, the same rules split differently: nothing to send.
    const same = account({ protocols: ['openai.responses', 'openai.chat'], model_protocols: [{ models: ['claude-sonnet-4-5'], protocols: ['anthropic.messages', 'openai.chat'] }, { models: ['gpt-5.6'], protocols: ['openai.chat', 'anthropic.messages'] }] })
    expect(buildBulkPatch(same, fields({ protocols: ['openai.chat', 'openai.responses'], model_protocols: rules }), both)).toBeNull()
    // An empty list closes every own protocol, which is not "all".
    expect(buildBulkPatch(account(), fields({ protocols: [] }), flags({ protocols: true }))).toEqual({ protocols: [] })
    expect(buildBulkPatch(account(), fields({ protocols: ['openai.chat'], model_protocols: rules }), flags())).toBeNull()
  })
})
