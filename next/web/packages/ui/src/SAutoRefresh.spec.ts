import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SAutoRefresh from './SAutoRefresh.vue'
import { mountPinned } from './testViewport'

enableAutoUnmount(afterEach)

afterEach(() => {
  document.body.innerHTML = ''
})

describe('SAutoRefresh on a phone', () => {
  it('is an icon button with a running dot whose menu offers Refresh now', async () => {
    const onRefresh = vi.fn()
    const w = mountPinned(SAutoRefresh, { props: { modelValue: 30, onRefresh } })
    const button = w.get('[data-testid="auto-refresh"]')
    expect(button.text()).toBe('')
    expect(button.attributes('aria-label')).toBe('Auto refresh: Every 30s')
    expect(w.find('[data-testid="auto-refresh-dot"]').exists()).toBe(true)
    await button.trigger('click')
    await nextTick()
    const items = [...document.querySelectorAll<HTMLButtonElement>('[role^="menuitem"]')]
    expect(items.map(b => b.textContent?.trim())).toEqual(['Refresh now', 'Off', 'Every 10s', 'Every 30s', 'Every 60s'])
    items[0].click()
    expect(onRefresh).toHaveBeenCalledOnce()
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })

  it('has no dot while off and no Refresh now without @refresh', async () => {
    const w = mountPinned(SAutoRefresh, { props: { modelValue: 0 } })
    expect(w.find('[data-testid="auto-refresh-dot"]').exists()).toBe(false)
    await w.get('[data-testid="auto-refresh"]').trigger('click')
    await nextTick()
    expect(document.body.textContent).not.toContain('Refresh now')
  })
})

describe('SAutoRefresh button', () => {
  it('adds a Refresh button on a wide screen that refreshes now', async () => {
    const onRefresh = vi.fn()
    const w = mountPinned(SAutoRefresh, { props: { modelValue: 10, button: true, loading: false, onRefresh } }, { compact: false })
    const now = w.get('[data-testid="auto-refresh-now"]')
    expect(now.text()).toBe('Refresh')
    await now.trigger('click')
    expect(onRefresh).toHaveBeenCalledOnce()
  })
  it('keeps the one icon button on a phone', () => {
    const w = mountPinned(SAutoRefresh, { props: { modelValue: 10, button: true, onRefresh: () => {} } })
    expect(w.find('[data-testid="auto-refresh-now"]').exists()).toBe(false)
  })
})
