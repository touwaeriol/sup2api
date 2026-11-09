import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import CCGatewayView from './CCGatewayView.vue'

const auth = vi.hoisted(() => ({ denied: [] as string[] }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ has: (perm: string) => !auth.denied.includes(perm) }) }))
// The page's own parts have their specs; the header is what is under test.
vi.mock('./RemoteSettings.vue', () => ({ default: { render: () => null } }))
vi.mock('./AccountRuntimes.vue', () => ({ default: { render: () => null } }))

const stubs = {
  SPageHeader: { template: '<header><slot name="actions" /></header>' },
  SButton: { props: ['to'], template: '<a :href="to"><slot /></a>' }
}
const headerLinks = (props: object = {}) =>
  mount(CCGatewayView, { props, global: { plugins: [i18n], stubs } })
    .findAll('header a')
    .map(a => [a.attributes('href'), a.text()])

describe('the CCGateway page header', () => {
  it('links to the plugin for those who may open it', () => {
    i18n.global.locale.value = 'zh'
    auth.denied = []
    expect(headerLinks()).toEqual([['/plugins/ccgateway', '插件详情']])
    auth.denied = ['plugin:read']
    expect(headerLinks()).toEqual([])
  })

  it("is left out inside the plugin's detail page", () => {
    auth.denied = []
    expect(headerLinks({ embedded: true })).toEqual([])
  })
})
