import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginAvatar from './PluginAvatar.vue'

const render = (icon?: string, assetBase?: string) =>
  mount(PluginAvatar, { props: { name: 'OpenAI', pluginKey: 'openai', icon, assetBase } })

describe('plugin avatar', () => {
  it('draws a package icon from the asset base of its version', () => {
    const w = render('assets/openai.svg', '/plugin-ui/openai/0.5.0-0123abcd/')
    expect(w.get('img').attributes('src')).toBe('/plugin-ui/openai/0.5.0-0123abcd/assets/openai.svg')
  })

  it('falls back to a letter without an icon it can load', async () => {
    expect(render().text()).toBe('O')
    expect(render('text:AI').text()).toBe('AI')
    // A package path is nothing without the asset base.
    expect(render('assets/openai.svg').text()).toBe('O')
    const w = render('assets/openai.svg', '/plugin-ui/openai/0.5.0-0123abcd')
    await w.get('img').trigger('error')
    expect(w.find('img').exists()).toBe(false)
    expect(w.text()).toBe('O')
  })
})
