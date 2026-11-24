// A market upgrade's consent: the source check (channel, publisher, release,
// size, sha256, host range), the egress domains and the acknowledgement new
// high-risk host permissions wait for.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { i18n } from '@/i18n'
import type { PluginReview } from '@/api/types'
import { channel, serveStoreList, storePlugin } from '@/testing/marketFixtures'
import ConsentView from './ConsentView.vue'
import { formatBytes } from '@/utils/format'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), list: vi.fn(), raw: vi.fn(), push: vi.fn() }))
vi.mock('@sub2api/host', async (original) => ({
  ...(await original<object>()),
  api: { get: mocks.get, post: mocks.post, list: mocks.list },
  requestRaw: mocks.raw,
  isApiError: (e: unknown) => typeof e === 'object' && e !== null && 'code' in e
}))
vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
  useRoute: () => ({ path: '/plugins/install/market', params: {}, query: { source_id: '2', publisher: 'sub2api', key: 'codex', version: '0.2.3' } }),
  useRouter: () => ({ push: mocks.push }),
  onBeforeRouteLeave: () => undefined
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ has: () => true, hasDynamic: () => true, scopes: () => [{ all: true, own: false, group: 0, id: 0 }], me: { id: 1, roles: ['admin'], superuser: true }, permissionsEpoch: 0 })
}))
vi.mock('./parts/PluginGatewayDecl.vue', () => ({ default: { name: 'PluginGatewayDecl', props: ['platforms', 'accountTypes', 'section'], template: '<div />' } }))
enableAutoUnmount(afterEach)

const SHA = '9f3a6d0be41c7a28f5e0d93b1c66a4e2b8d17f0c5a93e4d2b16c78a0e5f4c21e'
const RELEASE = 'https://github.com/touwaeriol/sup2api/releases/tag/plugin-codex-v0.2.3'
const tt = (key: string, named: Record<string, unknown> = {}, n?: number) => (n === undefined ? i18n.global.t(key, named) : i18n.global.t(key, named, n))

const review = (over: Partial<PluginReview> = {}): PluginReview => ({
  plugin_key: 'codex',
  version: '0.2.3',
  upgrade_from: '0.2.1',
  name: { en: 'Codex', zh: 'Codex' },
  publisher: 'sub2api',
  trust: 'official',
  signature_status: 'indexed',
  host_compat_ok: true,
  host_compat: '>=0.1.0',
  package_sha256: SHA,
  capabilities: [],
  gateway_endpoints: [],
  hooks: [],
  jobs: [],
  events: [],
  routes: [],
  menus: [],
  user_permissions: [],
  external_services: [],
  host_permissions: [
    { id: 'users.read', risk: 'high' },
    { id: 'jobs', risk: 'medium' },
    { id: 'net', risk: 'high', scope: { domains: ['chatgpt.com', 'ab.chatgpt.com'] }, current_status: 'granted', current_scope: { domains: ['chatgpt.com'] } }
  ],
  diff: { added: [{ id: 'users.read', risk: 'high' }, { id: 'jobs', risk: 'medium' }], widened: [{ id: 'net', risk: 'high' }], removed: [] },
  ...over
})

async function render(r: PluginReview) {
  mocks.post.mockImplementation(async (path: string) => (path.endsWith('review-from-market') ? r : {}))
  const w = mount(ConsentView, { global: { plugins: [i18n, createPinia()], stubs: { teleport: true } } })
  await flushPromises()
  return w
}
const approve = (w: Awaited<ReturnType<typeof render>>) => w.get('[data-testid="consent-approve"]').element as HTMLButtonElement

describe('market upgrade consent', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    i18n.global.locale.value = 'zh'
    mocks.get.mockResolvedValue([])
    mocks.list.mockImplementation(serveStoreList({ channels: [channel(1), channel(2, { name: '测试', branch: 'develop', is_default: false })] }))
    const codex = storePlugin('sub2api', 'codex', { versions: [{ version: '0.2.3', compatible: true, size: 19_293_798, release_url: RELEASE }] })
    mocks.raw.mockResolvedValue({ data: [codex], host_version: '0.1.5' })
  })

  it('checks the source: channel, publisher, release, size, the full sha256 and the host range', async () => {
    const w = await render(review())
    expect(mocks.raw).toHaveBeenCalledWith('GET', '/market/plugins', { query: { source_id: 2 } })
    const check = w.get('[data-testid="consent-source-check"]').text()
    expect(check).toContain(tt('market.origin.sourceValue', { store: '官方商店', channel: '测试', branch: 'develop' }))
    expect(check).toContain('plugin-codex-v0.2.3')
    expect(check).toContain(formatBytes(19_293_798))
    expect(check).toContain(SHA)
    expect(w.get('[data-testid="consent-host-compat"]').text()).toBe(tt('market.origin.satisfied'))
    expect(w.get('[data-testid="consent-version-change"]').text()).toContain('v0.2.1 → v0.2.3')
  })

  it('waits for the acknowledgement of a new high-risk permission before approving', async () => {
    const w = await render(review())
    const card = w.get('[data-testid="consent-new-risk"]')
    expect(card.findAll('[data-new-risk]').map((r) => r.attributes('data-new-risk'))).toEqual(['users.read'])
    expect(card.text()).toContain(tt('pluginInstall.newRisk', { n: 1 }, 1))
    expect(approve(w).disabled).toBe(true)
    expect(w.get('[data-testid="consent-ack-required"]').text()).toBe(tt('pluginInstall.ackRequired'))
    await w.get('[data-testid="consent-ack"] input, input[data-testid="consent-ack"]').setValue(true)
    expect(approve(w).disabled).toBe(false)
    expect(w.find('[data-testid="consent-ack-required"]').exists()).toBe(false)
  })

  it('asks for nothing more when no high-risk permission is new', async () => {
    const w = await render(review({ diff: { added: [{ id: 'jobs', risk: 'medium' }], widened: [], removed: [] } }))
    expect(w.find('[data-testid="consent-new-risk"]').exists()).toBe(false)
    expect(approve(w).disabled).toBe(false)
  })

  it('lists the egress domains, the new one marked', async () => {
    const w = await render(review())
    const domains = w.findAll('[data-testid="consent-egress"] [data-domain]')
    expect(domains.map((d) => [d.attributes('data-domain'), d.text().includes(tt('pluginInstall.egressNew'))])).toEqual([
      ['chatgpt.com', false],
      ['ab.chatgpt.com', true]
    ])
  })
})
