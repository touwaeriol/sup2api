import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import AccountLastTest from './AccountLastTest.vue'

const last = (o: object) => ({ at: new Date().toISOString(), ok: true, latency_ms: 12, model: 'm', message: '', ...o })
const render = (o: object) => mount(AccountLastTest, { props: { last: last(o) }, global: { plugins: [i18n] } })

// A passed test with a message passed with warnings (CONTRACTS §122.5).
describe('AccountLastTest', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('marks a passed test with warnings and shows them in the title', () => {
    const w = render({ message: '本地没有配置模型 m 的价格' })
    expect(w.get('[data-testid="account-last-test-warning"]').text()).toBe('有警告')
    expect(w.get('[title]').attributes('title')).toContain('本地没有配置模型 m 的价格')
  })

  it('shows no warning for a plain pass or a failure', () => {
    expect(render({}).find('[data-testid="account-last-test-warning"]').exists()).toBe(false)
    expect(render({ ok: false, message: 'boom' }).find('[data-testid="account-last-test-warning"]').exists()).toBe(false)
  })
})
