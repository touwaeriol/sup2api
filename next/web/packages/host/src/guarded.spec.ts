import { describe, expect, it } from 'vitest'
import { provideHost, type HostContext } from './index'
import { useGuardedSettings, type AccountTypeView } from './guarded'

function host(granted: string[]) {
  provideHost({ permissions: { superuser: () => false, has: (k: string) => granted.includes(k), any: () => false } } as unknown as HostContext)
}

const type: AccountTypeView = {
  plugin_key: 'relay',
  type: 'official',
  guarded_settings: [
    { field: 'base_url', allowed: ['https://api.example.com'] },
    { field: 'region', allowed: ['us', 'eu'] }
  ]
}

describe('useGuardedSettings', () => {
  it('restricts the guarded fields without account:settings:custom', () => {
    host([])
    const g = useGuardedSettings(() => type)
    expect(g.guarded.value).toBe(true)
    expect(g.allowed('base_url')).toEqual(['https://api.example.com'])
    expect(g.locked('base_url')).toBe(true)
    expect(g.allowed('region')).toEqual(['us', 'eu'])
    expect(g.locked('region')).toBe(false)
    expect(g.allowed('api_key')).toBeNull()
    expect(g.locked('api_key')).toBe(false)
  })
  it('lifts the guard for account:settings:custom, and without a type', () => {
    host(['account:settings:custom'])
    expect(useGuardedSettings(() => type).allowed('base_url')).toBeNull()
    host([])
    const none = useGuardedSettings(() => null)
    expect(none.guarded.value).toBe(false)
    expect(none.allowed('base_url')).toBeNull()
  })
})
