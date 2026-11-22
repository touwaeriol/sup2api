import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import DraftBadge from './DraftBadge.vue'

i18n.global.locale.value = 'en'

const render = (props: InstanceType<typeof DraftBadge>['$props']) => mount(DraftBadge, { props: { testid: 'row', ...props }, global: { plugins: [i18n] } })

describe('draft badge', () => {
  it('shows nothing for a row as saved', () => {
    expect(render({}).html()).toBe('<!--v-if-->')
  })
  it('names the state, removal first, then a refusal, then an unsaved edit', () => {
    expect(render({ dirty: true, error: 'no', removed: true }).find('[data-testid="row-removed"]').exists()).toBe(true)
    expect(render({ dirty: true, error: 'no' }).get('[data-testid="row-refused"]').attributes('title')).toBe('no')
    expect(render({ dirty: true }).get('[data-testid="row-unsaved"]').text()).toBe(i18n.global.t('pricing.draft.unsaved'))
  })
  it('shows the refusal as the label when asked', () => {
    expect(render({ error: 'rate too low', showError: true }).text()).toBe('rate too low')
  })
  it('says why an unsaved edit cannot be saved', () => {
    expect(render({ dirty: true, invalid: true, invalidTitle: 'not a rate' }).get('[data-testid="row-unsaved"]').attributes('title')).toBe('not a rate')
    expect(render({ dirty: true }).get('[data-testid="row-unsaved"]').attributes('title')).toBeUndefined()
  })
})
