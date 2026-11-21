import { describe, expect, it } from 'vitest'
import { changedTabs, tabOfErrors } from './groupEditTabs'

describe('tabs of the group editor', () => {
  it('finds the tab of a field error, also of a part of a field', () => {
    expect(tabOfErrors({})).toBeUndefined()
    expect(tabOfErrors({ rate_multiplier: 'bad' })).toBe('pricing')
    expect(tabOfErrors({ model_filter_mode: 'bad' })).toBe('models')
    expect(tabOfErrors({ 'environment.HTTP_PROXY': 'bad' })).toBe('advanced')
    // The first tab in order wins when several have errors.
    expect(tabOfErrors({ conversion_loss_policy: 'bad', name: 'required' })).toBe('basic')
    expect(tabOfErrors({ unknown: 'bad' })).toBeUndefined()
  })

  it('marks the tabs whose fields changed', () => {
    const saved = { name: 'a', visibility: 'public', model_allowlist: ['x'], environment: { A: '1' } }
    expect(changedTabs({ ...saved }, saved)).toEqual([])
    expect(changedTabs({ ...saved, model_allowlist: ['x'] }, saved)).toEqual([])
    expect(changedTabs({ ...saved, visibility: 'restricted', environment: { A: '2' } }, saved)).toEqual(['basic', 'advanced'])
    expect(changedTabs({ ...saved, model_allowlist: ['x', 'y'] }, saved)).toEqual(['models'])
    expect(changedTabs({ ...saved, rate_multiplier: '0.8' }, { ...saved, rate_multiplier: '1' })).toEqual(['pricing'])
  })
})
