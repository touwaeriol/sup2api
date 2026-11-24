// Where a consent page's review comes from and what approving, rejecting and
// cancelling do: a registered version, a market version and an uploaded file
// (consent/useConsentTarget.ts). The matrix itself is ConsentView.spec.ts.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { toastState } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { PluginReview } from '@/api/types'
import { channel, serveStoreList } from '@/testing/marketFixtures'
import ConsentView from './ConsentView.vue'
import { clearPendingUpload, setPendingUpload } from './consent/pendingUpload'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  upload: vi.fn(),
  list: vi.fn(),
  raw: vi.fn(),
  push: vi.fn(),
  confirm: vi.fn(),
  route: { path: '', params: {} as Record<string, string>, query: {} as Record<string, string> },
  manage: true
}))
vi.mock('@sub2api/host', async (original) => ({
  ...(await original<object>()),
  api: { get: mocks.get, post: mocks.post, upload: mocks.upload, list: mocks.list },
  requestRaw: mocks.raw,
  isApiError: (e: unknown) => typeof e === 'object' && e !== null && 'code' in e
}))
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), confirm: mocks.confirm }))
vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
  useRoute: () => mocks.route,
  useRouter: () => ({ push: mocks.push }),
  onBeforeRouteLeave: () => undefined
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => {
    const has = (k: string | string[]) => mocks.manage || ![k].flat().includes('plugin:manage')
    return { has, hasDynamic: has, scopes: () => [{ all: true, own: false, group: 0, id: 0 }], me: { id: 1, roles: ['admin'], superuser: true }, permissionsEpoch: 0 }
  }
}))
vi.mock('./parts/PluginGatewayDecl.vue', () => ({ default: { name: 'PluginGatewayDecl', props: ['platforms', 'accountTypes', 'section'], template: '<div />' } }))
enableAutoUnmount(afterEach)

const SHA = 'c0ffee'
const review = (over: Partial<PluginReview> = {}): PluginReview => ({
  plugin_key: 'bar',
  version: '1.0.0',
  name: { en: 'Bar', zh: 'Bar' },
  publisher: 'bar-labs',
  trust: 'community',
  signature_status: 'valid',
  host_compat_ok: true,
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
  host_permissions: [{ id: 'storage.read', risk: 'low' }],
  diff: null,
  ...over
})
const upgrade = review({ plugin_key: 'guard', version: '0.2.0', upgrade_from: '0.1.0', diff: { added: [], widened: [], removed: [] } })
const CONSENT = { grants: [{ permission: 'storage.read' }], denied: [], new_permission_roles: {} }
const tt = (key: string, named: Record<string, unknown> = {}) => i18n.global.t(key, named)

/** The plugin as GET /plugins/:key answers after the consent: what decides enable or upgrade. */
function plugin(status: string, active: string | null = null) {
  mocks.get.mockImplementation(async (path: string) => (path === '/me/assignable-roles' ? [] : { key: 'x', status, active_version: active }))
}
const at = (path: string, params: Record<string, string> = {}, query: Record<string, string> = {}) => Object.assign(mocks.route, { path, params, query })
const atMarket = () => at('/plugins/install/market', {}, { source_id: '2', publisher: 'barlabs', key: 'bar', version: '1.0.0' })

function mountView() {
  return mount(ConsentView, { global: { plugins: [i18n, createPinia()], stubs: { teleport: true } } })
}
async function shown() {
  const w = mountView()
  await flushPromises()
  return w
}
async function click(w: Awaited<ReturnType<typeof shown>>, id: string) {
  await w.get(`[data-testid="${id}"]`).trigger('click')
  await flushPromises()
}
const formOf = (call: unknown[]) => Object.fromEntries((call[1] as FormData).entries())

describe('consent page sources', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    toastState.items.splice(0)
    clearPendingUpload()
    i18n.global.locale.value = 'zh'
    mocks.manage = true
    plugin('installed')
    mocks.post.mockImplementation(async (path: string) => (path.endsWith('review-from-market') ? review() : {}))
    mocks.list.mockImplementation(serveStoreList({ channels: [channel(2, { name: '测试', branch: 'develop', is_default: false })] }))
    mocks.raw.mockResolvedValue({ data: [], host_version: '0.1.0' })
  })

  describe('market', () => {
    beforeEach(atMarket)

    it('reviews before anything is installed and says so above the steps', async () => {
      const w = await shown()
      expect(mocks.post).toHaveBeenCalledWith('/plugins/review-from-market', { source_id: 2, publisher: 'barlabs', key: 'bar', version: '1.0.0' })
      expect(w.get('[data-testid="install-steps"]').text()).toContain(tt('pluginInstall.steps.install.note'))
      expect(w.get('[data-testid="consent-approve"]').text()).toBe(tt('plugins.consent.approve'))
      expect(w.text()).not.toContain(tt('plugins.consent.reject'))
    })

    it('a review the server refuses offers the way back', async () => {
      mocks.post.mockRejectedValue({ status: 400, code: 'invalid_argument', message: 'bad signature', details: {} })
      const w = await shown()
      expect(w.find('[data-testid="consent-approve"]').exists()).toBe(false)
      await w.findAll('[data-testid="consent-unavailable"] button').find((b) => b.text() === tt('common.back'))!.trigger('click')
      expect(mocks.push).toHaveBeenCalledWith('/market')
    })

    it('cancelling calls nothing and goes back to the market', async () => {
      const w = await shown()
      await click(w, 'consent-cancel')
      expect(mocks.post).toHaveBeenCalledTimes(1)
      expect(mocks.push).toHaveBeenCalledWith('/market')
    })

    it('approving installs with the consent for the reviewed package, then enables it', async () => {
      const w = await shown()
      await click(w, 'consent-approve')
      const consent = { ...CONSENT, package_sha256: SHA }
      expect(mocks.post).toHaveBeenCalledWith('/plugins/install-from-market', { source_id: 2, publisher: 'barlabs', key: 'bar', version: '1.0.0', consent })
      expect(mocks.post).toHaveBeenCalledWith('/plugins/bar/enable', undefined)
      expect(mocks.push).toHaveBeenCalledWith('/plugins/bar/rollout')
    })

    it('a package approved before is not consented again: its rollout starts', async () => {
      mocks.post.mockImplementation(async (path: string) => (path.endsWith('review-from-market') ? review({ consent_status: 'approved' }) : {}))
      const w = await shown()
      expect(w.get('[data-testid="install-steps"]').text()).toContain(tt('pluginInstall.approvedNote'))
      await click(w, 'consent-approve')
      expect(mocks.post).not.toHaveBeenCalledWith('/plugins/install-from-market', expect.anything())
      expect(mocks.post).toHaveBeenCalledWith('/plugins/bar/enable', undefined)
      expect(mocks.push).toHaveBeenCalledWith('/plugins/bar/rollout')
    })

    it('a changed package is reviewed again instead of installed (409 package_changed)', async () => {
      const w = await shown()
      mocks.post.mockImplementation(async (path: string) => {
        if (path.endsWith('install-from-market')) throw { status: 409, code: 'package_changed', message: 'changed', details: {} }
        return review()
      })
      await click(w, 'consent-approve')
      expect(toastState.items.map((x) => x.message)).toContain(tt('pluginInstall.packageChanged'))
      expect(mocks.post.mock.calls.filter(([p]) => p === '/plugins/review-from-market')).toHaveLength(2)
      expect(mocks.push).not.toHaveBeenCalled()
    })

    it('when the rollout cannot start, the plugin page offers it by hand', async () => {
      const w = await shown()
      mocks.post.mockImplementation(async (path: string) => {
        if (path.endsWith('/enable')) throw { status: 409, code: 'conflict', message: 'busy', details: {} }
        return {}
      })
      await click(w, 'consent-approve')
      expect(mocks.push).toHaveBeenCalledWith({ path: '/plugins/bar', query: { consented: '1.0.0' } })
    })

    it('without plugin:manage, approving goes to the plugin page without starting anything', async () => {
      mocks.manage = false
      const w = await shown()
      await click(w, 'consent-approve')
      expect(mocks.post.mock.calls.map(([p]) => p)).toEqual(['/plugins/review-from-market', '/plugins/install-from-market'])
      expect(mocks.push).toHaveBeenCalledWith({ path: '/plugins/bar', query: { consented: '1.0.0' } })
    })
  })

  describe('upload', () => {
    beforeEach(() => at('/plugins/install/upload'))

    it('without the chosen file (a reload) asks for it again and calls nothing', async () => {
      const w = await shown()
      expect(w.get('[data-testid="consent-unavailable"]').text()).toContain(tt('pluginInstall.uploadMissing'))
      expect(mocks.upload).not.toHaveBeenCalled()
      await w.get('[data-testid="consent-unavailable"] button').trigger('click')
      expect(mocks.push).toHaveBeenCalledWith('/plugins')
    })

    it('reviews the file, then uploads it with the consent and upgrades the running plugin', async () => {
      const file = new File(['pkg'], 'guard.s2plugin')
      setPendingUpload(file)
      mocks.upload.mockImplementation(async (path: string) => (path === '/plugins/review-upload' ? upgrade : { review: upgrade, consent: {} }))
      plugin('enabled', '0.1.0')
      const w = await shown()
      expect(formOf(mocks.upload.mock.calls[0])).toEqual({ file })
      expect(w.get('[data-testid="install-steps"]').text()).toContain(tt('pluginInstall.steps.upgrade.note', { from: '0.1.0', version: '0.2.0' }))
      expect(w.get('[data-testid="consent-cancel"]').text()).toBe(tt('pluginInstall.cancelUpgrade'))
      await click(w, 'consent-approve')
      expect(mocks.upload.mock.calls[1][0]).toBe('/plugins/upload')
      expect(JSON.parse(String(formOf(mocks.upload.mock.calls[1]).consent))).toEqual({ ...CONSENT, package_sha256: SHA })
      expect(mocks.post).toHaveBeenCalledWith('/plugins/guard/upgrade', { version: '0.2.0' })
      expect(mocks.push).toHaveBeenCalledWith('/plugins/guard/rollout')
    })
  })

  describe('registered version', () => {
    beforeEach(() => at('/plugins/demo/versions/1.0.0/consent', { key: 'demo', version: '1.0.0' }))

    it('approving records the consent, then enables the plugin; no steps, no cancel', async () => {
      mocks.get.mockImplementation(async (path: string) => (path.endsWith('/review') ? review({ plugin_key: 'demo' }) : { status: 'installed', active_version: null }))
      const w = await shown()
      expect(w.find('[data-testid="install-steps"]').exists()).toBe(false)
      expect(w.find('[data-testid="consent-cancel"]').exists()).toBe(false)
      await click(w, 'consent-approve')
      expect(mocks.post).toHaveBeenCalledWith('/plugins/demo/versions/1.0.0/consent', CONSENT)
      expect(mocks.post).toHaveBeenCalledWith('/plugins/demo/enable', undefined)
      expect(mocks.push).toHaveBeenCalledWith('/plugins/demo/rollout')
    })

    it('rejecting discards the version and goes back to the plugins', async () => {
      mocks.get.mockResolvedValue(review({ plugin_key: 'demo' }))
      mocks.confirm.mockResolvedValue(true)
      const w = await shown()
      await w.findAll('button').find((b) => b.text() === tt('plugins.consent.reject'))!.trigger('click')
      await flushPromises()
      expect(mocks.post).toHaveBeenCalledWith('/plugins/demo/versions/1.0.0/reject')
      expect(mocks.push).toHaveBeenCalledWith('/plugins')
    })
  })
})
