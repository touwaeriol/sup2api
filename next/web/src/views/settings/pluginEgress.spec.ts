import { describe, expect, it } from 'vitest'
import { listField, normalizeDomain, parseDomainLines } from './pluginEgress'

describe('normalizeDomain (the server rules, CONTRACTS §113)', () => {
  it('stores a domain trimmed, lower-case, without the trailing dot; IDN in punycode; IPv6 in brackets', () => {
    expect(normalizeDomain('  API.Example.COM. ')).toBe('api.example.com')
    expect(normalizeDomain('例子.测试')).toBe('xn--fsqu00a.xn--0zwm56d')
    expect(normalizeDomain('93.184.216.34')).toBe('93.184.216.34')
    expect(normalizeDomain('2001:DB8::1')).toBe('[2001:db8::1]')
    expect(normalizeDomain('[2001:db8::1]')).toBe('[2001:db8::1]')
    expect(normalizeDomain('localhost')).toBe('localhost')
    expect(normalizeDomain('123')).toBe('123')
  })

  it('refuses schemes, paths, ports, wildcards and names that are not host names', () => {
    for (const bad of ['', ' ', 'https://example.com', 'example.com/x', 'example.com:443', '*.example.com', '*', 'exa mple.com',
      '-bad.com', 'bad-.com', 'a..b', 'under_score.com', 'user@example.com', `${'a'.repeat(64)}.com`]) {
      expect(normalizeDomain(bad), bad).toBeNull()
    }
  })
})

describe('parseDomainLines', () => {
  it('skips blank lines, drops duplicates and listed domains, and numbers the lines it cannot take', () => {
    expect(parseDomainLines(['a.com', '', 'A.com.', 'https://b.com', 'c.com', 'listed.com', 'x:1'], ['listed.com'])).toEqual({
      domains: ['a.com', 'c.com'],
      errors: [
        { line: 4, value: 'https://b.com' },
        { line: 7, value: 'x:1' }
      ]
    })
  })
})

describe('listField', () => {
  it('is the blocked list while everything is allowed, the allowed list while everything is blocked', () => {
    expect(listField('allow_all')).toBe('blocked_domains')
    expect(listField('block_all')).toBe('allowed_domains')
  })
})
