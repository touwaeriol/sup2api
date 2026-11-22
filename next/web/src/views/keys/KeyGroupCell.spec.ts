import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import ProblemIcon from '@/components/ProblemIcon.vue'
import KeyGroupCell from './KeyGroupCell.vue'

const render = (props: Record<string, unknown>) => {
  i18n.global.locale.value = 'zh'
  return mount(KeyGroupCell, { props: { name: 'vip', ...props }, global: { plugins: [i18n] } })
}
type Cell = ReturnType<typeof render>
const badge = (w: Cell) => w.find('[data-testid="group-badge"]')
/** The problem icon's sentence (its tooltip and accessible name) and tone, undefined without a problem. */
const problem = (w: Cell) => {
  const icon = w.findComponent(ProblemIcon)
  return icon.exists() ? { text: icon.props('text'), tone: icon.props('tone') } : undefined
}

describe('API key group cell', () => {
  it('shows the group as a badge of its name and rate, and nothing else while it serves the key', () => {
    const w = render({ multiplier: '0.8', platforms: ['anthropic'], status: 'active' })
    expect(badge(w).text()).toBe('vip×0.8')
    expect(problem(w)).toBeUndefined()
    // Platforms the server did not report are not a problem either.
    expect(problem(render({}))).toBeUndefined()
  })

  it('marks a private group granted to the owner', () => {
    expect(render({ status: 'active', exclusive: true }).get('[data-testid="key-group-exclusive"]').text()).toBe('专属')
    expect(render({ status: 'active' }).find('[data-testid="key-group-exclusive"]').exists()).toBe(false)
  })

  it('marks a group without usable accounts with a warning icon, the sentence in its tooltip only', () => {
    const w = render({ platforms: [], status: 'active' })
    expect(problem(w)).toEqual({ text: '该账号分组暂时无法提供服务（没有可用账号）。', tone: 'warning' })
    expect(w.text()).toBe('vip')
    expect(w.get('[data-testid="key-group-problem"]').attributes('aria-label')).toBe('该账号分组暂时无法提供服务（没有可用账号）。')
  })

  it('says why the group cannot serve the key: deleted (no badge), disabled or no longer open', () => {
    const deleted = render({ name: '', platforms: [], status: 'deleted' })
    expect(badge(deleted).exists()).toBe(false)
    expect(problem(deleted)).toEqual({ text: '账号分组已删除，请重新选择账号分组', tone: 'danger' })
    expect(deleted.text()).toBe('')

    const disabled = render({ platforms: ['anthropic'], status: 'disabled' })
    expect(badge(disabled).exists()).toBe(true)
    expect(problem(disabled)).toEqual({ text: '账号分组已停用', tone: 'danger' })
    expect(problem(render({ platforms: ['anthropic'], status: 'unavailable' }))?.text).toBe('账号分组已不对你开放')
  })

  it('shows no problem for a group state added by a newer server', () => {
    expect(problem(render({ platforms: ['anthropic'], status: 'paused' }))).toBeUndefined()
  })
})
