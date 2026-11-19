import { describe, expect, it } from 'vitest'
import { DOMWrapper, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import GroupModelPolicyEditor from './GroupModelPolicyEditor.vue'

describe('group model policy editor', () => {
  const render = () => mount(GroupModelPolicyEditor, { props: { modelValue: ['claude-haiku-*'], mode: 'whitelist', options: ['claude-opus-5-5'] }, global: { plugins: [i18n] } })
  it('fills group IDs without broad wildcard suggestions and preserves existing rules', async () => {
    const w = render()
    await w.get('[data-testid="policy-fill-group"]').trigger('click')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([['claude-haiku-*', 'claude-opus-5-5']])
    expect(w.find('input[aria-label="gpt-*"]').exists()).toBe(false)
  })
  it('switches policy mode without overwriting the patterns', async () => {
    const w = render()
    // The option is a choice card: pick the radio of the card holding the test hook.
    const radio = w.get('[data-testid="policy-blacklist"]').element.closest('label')!.querySelector('input')!
    await new DOMWrapper(radio).setValue(true)
    expect(w.emitted('update:mode')).toEqual([['blacklist']])
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })
  it('adds wildcard patterns and deduplicates pasted lists', async () => {
    const w = render()
    await w.get('[data-testid="policy-draft"]').trigger('paste', { clipboardData: { getData: () => 'claude-opus-*,claude-haiku-*\ngpt-*' } })
    await w.get('[data-testid="policy-draft"]').trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')?.[0]).toEqual([['claude-haiku-*', 'claude-opus-*', 'gpt-*']])
  })
  it('accepts a model pattern of any length', async () => {
    const w = render()
    const long = 'm'.repeat(300)
    await w.get('[data-testid="policy-draft"]').setValue(long)
    await w.get('[data-testid="policy-draft"]').trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')?.[0]).toEqual([['claude-haiku-*', long]])
    expect(w.text()).not.toContain('200')
  })
})
