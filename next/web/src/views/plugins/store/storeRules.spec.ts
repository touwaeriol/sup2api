import { describe, expect, it } from 'vitest'
import { validBranch, validRepo } from './storeRules'

describe('store rules', () => {
  it('takes a GitHub repository as owner/name', () => {
    expect(validRepo('touwaeriol/sup2api-plugin')).toBe(true)
    expect(validRepo(' ourteam/s2.plugins_x ')).toBe(true)
    for (const bad of ['ourteam', 'https://github.com/a/b', '-team/x', 'a/b/c', 'a/..', 'a/.', `${'o'.repeat(40)}/x`]) expect(validRepo(bad)).toBe(false)
  })

  it('takes a branch as one ref name segment', () => {
    expect(validBranch('develop')).toBe(true)
    expect(validBranch('release-1.2')).toBe(true)
    for (const bad of ['', 'a/b', '../main', 'x'.repeat(65), 'has space']) expect(validBranch(bad)).toBe(false)
  })
})
