import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { MarketPlugin } from '@/api/types'
import MarketRow from './MarketRow.vue'

const tool: MarketPlugin = {
  source_id: 1,
  source_name: 'official',
  key: 'tool',
  name: { en: 'Tool' },
  publisher: 'acme',
  trust: 'verified',
  publisher_status: 'active',
  builtin: false,
  latest_version: '1.0.0',
  update_available: false,
  versions: [{ version: '1.0.0', compatible: true }]
}

const render = (props: Partial<InstanceType<typeof MarketRow>['$props']> = {}) =>
  mount(MarketRow, {
    props: { plugin: tool, installedVersion: '', hasDetail: false, state: 'install', blocked: '', canInstall: true, ...props },
    global: { plugins: [i18n], stubs: { RouterLink: true } }
  })

describe('MarketRow', () => {
  it('shows the latest version the core reports and opens the picker', async () => {
    const w = render()
    expect(w.text()).toContain('v1.0.0')
    await w.find('button').trigger('click')
    expect(w.emitted('pick')).toHaveLength(1)
  })
  it('explains and disables a blocked install', () => {
    const w = render({ blocked: 'revoked' })
    expect(w.find('[data-testid="market-blocked"]').text()).toContain('revoked')
    expect(w.find('button').attributes('disabled')).toBeDefined()
  })
  it('offers no action without plugin:install', () => {
    expect(render({ canInstall: false }).find('button').exists()).toBe(false)
  })
})
