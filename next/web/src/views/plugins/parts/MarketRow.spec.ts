import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import { channel, storePlugin } from '@/testing/marketFixtures'
import MarketRow from './MarketRow.vue'

const tool = storePlugin('acme', 'tool', {
  versions: [
    { version: '1.0.0', compatible: true, host_compat: '>=0.1.0' },
    { version: '0.9.0', compatible: true, deprecated: { reason: { en: 'broken' } } }
  ]
})

const render = (props: Partial<InstanceType<typeof MarketRow>['$props']> = {}) =>
  mount(MarketRow, {
    props: { plugin: tool, channel: channel(1), installedVersion: '', state: 'install', blocked: '', canInstall: true, ...props },
    global: { plugins: [i18n], stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } }
  })

describe('MarketRow', () => {
  it('shows the id as publisher/key, the core it needs and installs the latest version', async () => {
    i18n.global.locale.value = 'en'
    const w = render()
    expect(w.get('[data-plugin-id]').text()).toBe('acme/tool')
    expect(w.text()).toContain(i18n.global.t('market.requiresCore', { range: '>=0.1.0' }))
    await w.get('[data-testid="market-install"]').trigger('click')
    expect(w.emitted('install')).toEqual([['1.0.0']])
  })
  it('notes the deprecated version', () => {
    expect(render().get('[data-testid="market-version-deprecated"]').text()).toContain('v0.9.0')
  })
  it('explains and disables a blocked install', () => {
    const w = render({ blocked: 'revoked' })
    expect(w.get('[data-testid="market-blocked"]').text()).toContain('revoked')
    expect(w.get('[data-testid="market-not-installable"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="market-versions"]').exists()).toBe(false)
  })
  it('offers no install without plugin:install, only the versions', () => {
    const w = render({ canInstall: false })
    expect(w.find('[data-testid="market-install"]').exists()).toBe(false)
    expect(w.find('[data-testid="market-versions"]').exists()).toBe(true)
  })
})
