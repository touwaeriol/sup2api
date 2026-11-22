import { describe, expect, it } from 'vitest'
import { allowsModel, globMatch, offeredModels, type ModelOffer } from './offeredModels'

const offer = (over: Partial<ModelOffer> = {}): ModelOffer => ({ models: [], unrestricted_accounts: 0, model_allowlist: [], model_filter_mode: 'whitelist', ...over })
const priced = ['claude-haiku-4-5', 'gpt-4o', 'gpt-4o-mini']

describe('the models a group provides', () => {
  it('matches patterns as the core does', () => {
    expect(globMatch('gpt-*', 'gpt-4o')).toBe(true)
    expect(globMatch('gpt-?o', 'gpt-4o')).toBe(true)
    expect(globMatch('*-mini', 'gpt-4o')).toBe(false)
    expect(globMatch('a*b*c', 'axxbyyc')).toBe(true)
    expect(globMatch('gpt-4o', 'gpt-4o-mini')).toBe(false)
  })

  it('lets every model through an empty list, and those listed or not by its mode', () => {
    expect(allowsModel(offer(), 'x')).toBe(true)
    expect(allowsModel(offer({ model_allowlist: ['gpt-*'] }), 'gpt-4o')).toBe(true)
    expect(allowsModel(offer({ model_allowlist: ['gpt-*'] }), 'claude-haiku-4-5')).toBe(false)
    expect(allowsModel(offer({ model_allowlist: ['gpt-*'], model_filter_mode: 'blacklist' }), 'gpt-4o')).toBe(false)
  })

  it('are those the accounts declare, and every priced one once an account declares none, through the policy', () => {
    expect(offeredModels(offer({ models: ['o3', 'gpt-4o'] }), priced)).toEqual(['gpt-4o', 'o3'])
    expect(offeredModels(offer({ models: ['o3'], unrestricted_accounts: 1 }), priced)).toEqual(['claude-haiku-4-5', 'gpt-4o', 'gpt-4o-mini', 'o3'])
    expect(offeredModels(offer({ unrestricted_accounts: 2, model_allowlist: ['gpt-4o*'], model_filter_mode: 'blacklist' }), priced)).toEqual(['claude-haiku-4-5'])
    expect(offeredModels(offer(), priced)).toEqual([])
  })
})
