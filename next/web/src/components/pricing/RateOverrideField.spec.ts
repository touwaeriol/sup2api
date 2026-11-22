import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import RateOverrideField from './RateOverrideField.vue'

i18n.global.locale.value = 'en'

function render(modelValue: string | null, follow = '1.2') {
  const w = mount(RateOverrideField, { props: { modelValue, follow }, global: { plugins: [i18n] } })
  return { w, input: w.get('[data-testid="rate-override-input"]') }
}

describe('rate override field', () => {
  it('says what an empty field follows', () => {
    const { w, input } = render(null)
    expect(input.attributes('placeholder')).toBe('Follows 1.2')
    expect(input.attributes('aria-invalid')).toBeUndefined()
    expect(w.find('[data-testid="rate-override-invalid"]').exists()).toBe(false)
    expect(render(null, '0.80').input.attributes('placeholder')).toBe('Follows 0.8')
  })
  it('emits the typed rate, and null again once cleared', async () => {
    const { w, input } = render(null)
    await input.setValue(' 0.85 ')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['0.85'])
    await w.setProps({ modelValue: '0.85' })
    await w.get('button').trigger('click')
    expect(w.emitted('update:modelValue')?.[1]).toEqual([null])
  })
  it('marks a value that is not a rate', () => {
    const { w, input } = render('abc')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(w.get('[data-testid="rate-override-invalid"]').text()).toBe('Enter a number from 0 to 999999.9999, up to 4 decimals')
  })
})
