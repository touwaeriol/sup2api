import { describe, expect, it } from 'vitest'
import { validBranch } from './storeRules'

describe('store rules', () => {
  it('takes a branch as one ref name segment', () => {
    expect(validBranch('develop')).toBe(true)
    expect(validBranch('release-1.2')).toBe(true)
    for (const bad of ['', 'a/b', '../main', 'x'.repeat(65), 'has space', '.hidden', 'main.lock']) expect(validBranch(bad)).toBe(false)
  })
})
