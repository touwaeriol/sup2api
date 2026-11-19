import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import type { AccountBalance } from '@/api/types'
import { i18n } from '@/i18n'
import AccountBalanceCell from './AccountBalanceCell.vue'

const NOW = Date.parse('2026-11-18T12:00:00Z')
const ago = (s: number) => new Date(NOW - s * 1000).toISOString()

function render(balance: AccountBalance | null | undefined, refreshing = false) {
  return mount(AccountBalanceCell, { props: { balance, refreshing }, global: { plugins: [i18n] } })
}

enableAutoUnmount(afterEach)

describe('AccountBalanceCell', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    i18n.global.locale.value = 'zh'
  })
  afterEach(() => vi.useRealTimers())

  it('shows nothing for a type without balance', () => {
    const w = render(undefined)
    expect(w.find('[data-testid="balance-cell"]').exists()).toBe(false)
    expect(w.text()).toBe('—')
  })

  it('shows a balance never read with its refresh link', async () => {
    const w = render({ supported: true })
    expect(w.get('[data-testid="balance-value"]').text()).toBe('未查询')
    expect(w.find('[data-testid="balance-error"]').exists()).toBe(false)
    await w.get('button').trigger('click')
    expect(w.emitted('refresh')).toHaveLength(1)
  })

  it('shows the amount with the upstream unit as it is, and its age', () => {
    const w = render({ supported: true, amount: '12.500000', currency: 'USD', updated_at: ago(180) })
    const v = w.get('[data-testid="balance-value"]')
    expect(v.text()).toBe('12.5 USD')
    expect(v.attributes('title')).toBe('上游返回的数字与单位，原样显示，未做换算')
    expect(w.text()).toContain('3 分钟前')
    expect(w.find('[data-testid="balance-error"]').exists()).toBe(false)
  })

  it('shows the number alone when the upstream gave no unit', () => {
    const w = render({ supported: true, amount: '1250000.000000', updated_at: ago(60) })
    expect(w.get('[data-testid="balance-value"]').text()).toBe('1,250,000')
  })

  it('shows an unlimited key without a made-up amount', () => {
    const w = render({ supported: true, unlimited: true, updated_at: ago(30) })
    const v = w.get('[data-testid="balance-value"]')
    expect(v.text()).toBe('不限')
    expect(v.attributes('title')).toBe('上游报告此 Key 不限额度')
    expect(w.text()).not.toContain('$')
  })

  it('marks a failed read, keeping the last amount as stale', () => {
    const w = render({ supported: true, amount: '3.500000', currency: 'CNY', updated_at: ago(7200), error: 'transient: upstream answered 503', error_at: ago(300) })
    const badge = w.get('[data-testid="balance-error"]')
    expect(badge.text()).toBe('查询失败')
    expect(badge.attributes('title')).toBe('transient: upstream answered 503（5 分钟前）')
    expect(w.get('[data-testid="balance-value"]').text()).toBe('3.5 CNY')
    expect(w.text()).toContain('旧值')
  })

  it('shows a first failed read without an amount', () => {
    const w = render({ supported: true, error: 'auth_rejected: key revoked', error_at: ago(60) })
    expect(w.get('[data-testid="balance-error"]').text()).toBe('查询失败')
    expect(w.find('[data-testid="balance-value"]').exists()).toBe(false)
    expect(w.text()).not.toContain('旧值')
  })

  it('disables the link while refreshing', () => {
    const w = render({ supported: true }, true)
    expect(w.get('button').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('正在查询')
  })
})
