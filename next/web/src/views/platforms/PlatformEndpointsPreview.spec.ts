import { beforeAll, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import PlatformEndpointsPreview from './PlatformEndpointsPreview.vue'

// The real platform list on a mocked API: the built-in platforms and two of plugins.
const get = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get } }))

const render = (props: InstanceType<typeof PlatformEndpointsPreview>['$props']) =>
  mount(PlatformEndpointsPreview, { props, global: { plugins: [i18n] } })
const sections = (w: ReturnType<typeof render>) => w.findAll('[data-testid="platform-endpoints"] > [data-platform]')
const ids = (w: ReturnType<typeof render>) => sections(w).map(s => s.attributes('data-platform'))

describe('platform endpoints preview', () => {
  beforeAll(async () => {
    i18n.global.locale.value = 'zh'
    const { BUILTIN_PLATFORMS } = await import('@/composables/platforms')
    get.mockResolvedValue([
      ...BUILTIN_PLATFORMS,
      {
        id: 'myvideo',
        label: { en: 'MyVideo', zh: 'MyVideo 视频' },
        builtin: false,
        endpoints: [{ method: 'POST', path: '/v1/video/generations', protocol: 'myvideo.generate', billing: 'usage' }]
      },
      { id: 'bare', label: { en: 'Bare', zh: 'Bare' }, builtin: false, endpoints: null }
    ])
    render({ served: [] })
    await flushPromises()
  })

  const served = (...protocols: string[]) => protocols.map(protocol => ({ protocol }))

  it('lists the endpoints of the served protocols under the brand badge of their platform, free ones marked', () => {
    const [anthropic, video] = sections(render({ served: served('myvideo.generate', 'anthropic.messages', 'anthropic.count_tokens') }))
    expect(anthropic.get('[data-testid="platform-badge"]').text()).toBe('Anthropic')
    expect(anthropic.findAll('li').map(li => li.text())).toEqual(['POST /v1/messages', 'POST /v1/messages/count_tokens(不计费)'])
    expect(video.get('[data-testid="platform-badge"]').text()).toBe('MMyVideo 视频')
    expect(video.findAll('li').map(li => li.text())).toEqual(['POST /v1/video/generations'])
  })

  it('cuts each platform at `max` endpoints and counts the rest', () => {
    const items = sections(render({ served: served('openai.chat', 'openai.responses', 'openai.embeddings'), max: 2 }))[0]
      .findAll('li')
      .map(li => li.text())
    expect(items).toEqual(['POST /v1/chat/completions', 'POST /v1/responses', '另有 1 个'])
  })

  it('lists only what is served: no other endpoint of the platform, no platform without a served protocol', () => {
    const w = render({ served: served('anthropic.messages') })
    expect(ids(w)).toEqual(['anthropic'])
    expect(sections(w)[0].findAll('li').map(li => li.text())).toEqual(['POST /v1/messages'])
  })

  it('keeps a protocol the platform list does not have, by its id', () => {
    const [gone] = sections(render({ served: served('gone.chat') }))
    expect(gone.attributes('data-platform')).toBe('gone')
    expect(gone.text()).toContain('gone.chat')
  })
})
