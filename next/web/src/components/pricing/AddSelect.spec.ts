import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import AddSelect from './AddSelect.vue'

enableAutoUnmount(afterEach)
i18n.global.locale.value = 'zh'

const options = [{ value: 'gpt-5', label: 'gpt-5' }]
const render = (creatable: boolean) => mount(AddSelect, { props: { options, label: '添加模型', noResults: '没有可添加的模型', creatable }, global: { plugins: [i18n] } })

describe('AddSelect', () => {
  it('adds what is picked and holds no value', async () => {
    const w = render(false)
    const input = w.get('input')
    await input.trigger('focus')
    await w.get('[role="option"]').trigger('click')
    expect(w.emitted('add')).toEqual([['gpt-5']])
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('adds a typed name that is no option when creatable, and only then', async () => {
    const w = render(true)
    const input = w.get('input')
    await input.trigger('focus')
    await input.setValue('  claude-opus-x-test ')
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['添加“claude-opus-x-test”'])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('add')).toEqual([['claude-opus-x-test']])
    const plain = render(false)
    await plain.get('input').trigger('focus')
    await plain.get('input').setValue('claude-opus-x-test')
    expect(plain.find('[role="option"]').exists()).toBe(false)
    expect(plain.text()).toContain('没有可添加的模型')
  })

  it('does not add again a typed name that is listed already (a greyed option)', async () => {
    const w = mount(AddSelect, { props: { options: [...options, { value: 'o3', label: 'o3', tag: '已添加', disabled: true }], label: '添加模型', noResults: '没有可添加的模型', creatable: true }, global: { plugins: [i18n] } })
    const input = w.get('input')
    await input.trigger('focus')
    await input.setValue('o3')
    expect(w.findAll('[role="option"]').map((o) => o.text())).toEqual(['o3已添加'])
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('add')).toBeUndefined()
  })
})
