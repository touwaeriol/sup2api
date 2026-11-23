import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { MarketPlugin } from '@/api/types'
import MarketVersionPicker from './MarketVersionPicker.vue'

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
  versions: [
    { version: '1.0.0', compatible: true, release_url: 'https://github.com/acme/tool/releases/tag/v1.0.0' },
    { version: '1.1.0', compatible: true, deprecated: { reason: { en: 'broken' } } },
    { version: '2.0.0', compatible: false }
  ]
}

const render = (installedVersion = '') => {
  const w = mount(MarketVersionPicker, {
    props: { plugin: null, installedVersion, hostVersion: '0.1.0' },
    global: { plugins: [i18n], stubs: { teleport: true } },
    attachTo: document.body
  })
  return w.setProps({ plugin: tool }).then(() => w)
}

describe('MarketVersionPicker', () => {
  it('picks the latest version the core reports and installs it', async () => {
    const w = await render()
    expect(w.find('[data-version="2.0.0"] [data-testid="version-incompatible"]').exists()).toBe(true)
    expect(w.find('[data-version="1.1.0"] [data-testid="version-deprecated"]').exists()).toBe(true)
    expect(w.find('[data-version="1.0.0"] [data-testid="version-release-notes"]').attributes('href')).toBe(
      'https://github.com/acme/tool/releases/tag/v1.0.0'
    )
    await w.find('[data-testid="market-continue"]').trigger('click')
    expect(w.emitted('install')).toEqual([['1.0.0']])
    w.unmount()
  })
  it('does not reinstall the installed version', async () => {
    const w = await render('1.0.0')
    expect(w.find('[data-testid="market-continue"]').attributes('disabled')).toBeDefined()
    w.unmount()
  })
})
