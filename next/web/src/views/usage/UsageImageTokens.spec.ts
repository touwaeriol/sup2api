import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import PriceEstimator from '@/views/prices/PriceEstimator.vue'
import UsageTokenFacts from './UsageTokenFacts.vue'

const api = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@sub2api/host', async (orig) => ({ ...(await orig<object>()), api }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: () => false }) }))
enableAutoUnmount(afterEach)

// Image generations and edits (CONTRACTS §118): the image share of the tokens.
describe('image tokens', () => {
  it('shows the image rows of a usage only when an image endpoint reported them', () => {
    i18n.global.locale.value = 'zh'
    const image = mount(UsageTokenFacts, { props: { tokens: { input_tokens: 50, output_tokens: 4200, image_input_tokens: 40, image_output_tokens: 4000 } }, global: { plugins: [i18n] } })
    expect(image.text()).toContain('图片输入')
    expect(image.text()).toContain('图片输出')
    expect(image.text()).toContain('4,000')
    const chat = mount(UsageTokenFacts, { props: { tokens: { input_tokens: 50, output_tokens: 7, image_input_tokens: 0, image_output_tokens: 0 } }, global: { plugins: [i18n] } })
    expect(chat.text()).not.toContain('图片')
  })

  it('lets the trial calculation enter img and img_o', async () => {
    vi.useFakeTimers()
    i18n.global.locale.value = 'zh'
    api.post.mockResolvedValue({ cost: '0.1363', tier: '', rules: [], breakdown: {} })
    const wrapper = mount(PriceEstimator, { props: { source: { mode: 'expression', expression: 'p*5 + img*8 + img_o*32 + c*10' } }, global: { plugins: [i18n] } })
    await wrapper.findAll('button').find(b => b.text().includes('更多用量'))!.trigger('click')
    expect(wrapper.text()).toContain('图片输入')
    expect(wrapper.text()).toContain('图片输出')
    await vi.runAllTimersAsync()
    await flushPromises()
    const usage = api.post.mock.calls.at(-1)![1].usage
    expect(usage).toMatchObject({ p: 100000, c: 2000, img: 0, img_o: 0 })
    vi.useRealTimers()
  })

  it('names the two image protocols', () => {
    for (const [locale, names] of Object.entries({ en: ['Images', 'Image Edits'], zh: ['Images', 'Image Edits'] })) {
      i18n.global.locale.value = locale as 'en' | 'zh'
      expect([i18n.global.t('accounts.protocols.names.openai.images'), i18n.global.t('accounts.protocols.names.openai.images_edit')]).toEqual(names)
    }
  })
})
