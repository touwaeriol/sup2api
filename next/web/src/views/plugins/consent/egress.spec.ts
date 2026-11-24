import { describe, expect, it } from 'vitest'
import type { PluginReview } from '@/api/types'
import { egressDomains } from './egress'

const review = (over: Partial<PluginReview>): PluginReview => ({ host_permissions: [], diff: null, ...over }) as PluginReview
const net = (domains: string[], extra = {}) => ({ id: 'net', risk: 'high' as const, scope: { domains }, ...extra })

describe('egress domains', () => {
  it('marks nothing new on an install', () => {
    expect(egressDomains(review({ host_permissions: [net(['a.com'])] }))).toEqual([{ host: 'a.com', added: false }])
  })
  it('marks the domains an upgrade adds to the current grant or the previous scope', () => {
    const widened = { added: [], widened: [{ id: 'net', risk: 'high' as const, previous_scope: { domains: ['a.com'] } }], removed: [] }
    expect(egressDomains(review({ host_permissions: [net(['a.com', 'b.com'])], diff: widened })).map(d => d.added)).toEqual([false, true])
    const granted = net(['a.com', 'b.com'], { current_status: 'granted', current_scope: { domains: ['b.com'] } })
    expect(egressDomains(review({ host_permissions: [granted], diff: { added: [], widened: [], removed: [] } })).map(d => d.added)).toEqual([true, false])
  })
  it('marks every domain when net itself is new, and none without net', () => {
    const added = { added: [{ id: 'net', risk: 'high' as const }], widened: [], removed: [] }
    expect(egressDomains(review({ host_permissions: [net(['a.com'])], diff: added }))).toEqual([{ host: 'a.com', added: true }])
    expect(egressDomains(review({}))).toEqual([])
  })
})
