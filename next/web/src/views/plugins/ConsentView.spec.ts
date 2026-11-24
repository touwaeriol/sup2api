import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { SEmpty, SHint, SIcon, toastState } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { PluginReview } from '@/api/types'
import ConsentView from './ConsentView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), push: vi.fn(), can: true, lacks: new Set<string>(), superuser: true, epoch: 0, managed: [] as object[] }))
// An API error is anything carrying a code (the tests reject with plain objects).
vi.mock('@sub2api/host', async (original) => ({
  ...(await original<object>()),
  api: { get: mocks.get, post: mocks.post },
  isApiError: (e: unknown) => typeof e === 'object' && e !== null && 'code' in e
}))
vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
  useRoute: () => ({ path: '/plugins/demo/versions/1.2.0/consent', params: { key: 'demo', version: '1.2.0' }, query: {} }),
  useRouter: () => ({ push: mocks.push }),
  onBeforeRouteLeave: () => undefined
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => {
    const has = (k: string | string[]) => mocks.can && ![k].flat().some((x) => mocks.lacks.has(x))
    return {
      has,
      hasDynamic: has,
      // The permissions a consent creates are new: only the super admin holds them already.
      scopes: () => (mocks.superuser ? [{ all: true, own: false, group: 0, id: 0 }] : []),
      me: { id: 1, roles: ['admin', 'lead'], superuser: mocks.superuser },
      permissionsEpoch: mocks.epoch
    }
  }
}))
vi.mock('./parts/PluginGatewayDecl.vue', () => ({ default: { name: 'PluginGatewayDecl', props: ['platforms', 'accountTypes', 'section'], template: '<div />' } }))
enableAutoUnmount(afterEach)

const review = (over: Partial<PluginReview> = {}): PluginReview => ({
  plugin_key: 'demo',
  display_id: 'demo',
  version: '1.2.0',
  name: { en: 'Demo', zh: '示例' },
  publisher: 'acme',
  trust: 'community',
  signature_status: 'valid',
  host_compat_ok: true,
  host_compat: '>=1.0.0',
  capabilities: [],
  gateway_endpoints: [],
  hooks: [],
  jobs: [],
  events: [],
  routes: [],
  menus: [],
  user_permissions: [{ key: 'demo.read', label: { en: 'Read', zh: '读取' }, risk: 'low' }],
  external_services: [],
  host_permissions: [
    { id: 'http.client', risk: 'high' },
    { id: 'storage.read', risk: 'low' }
  ],
  diff: null,
  ...over
})

function render(r: PluginReview) {
  mocks.get.mockImplementation(async (path: string) => (path === '/me/assignable-roles' ? mocks.managed : r))
  return mountView()
}
const managed = (key: string, name: string, builtin: boolean) => ({ key, name: { en: name, zh: name }, builtin, grantable: true })

// A missing import of a @sub2api/ui component is only a runtime warning: Vue
// renders an unknown element and everything that component drew disappears.
// Turning that warning into a failure is what guards the page structure.
function mountView() {
  return mount(ConsentView, {
    global: {
      plugins: [i18n, createPinia()],
      stubs: { teleport: true },
      config: {
        warnHandler: (msg) => {
          if (msg.includes('Failed to resolve component')) throw new Error(msg)
        }
      }
    }
  })
}

describe('plugin consent page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.can = true
    mocks.lacks = new Set()
    mocks.superuser = true
    mocks.epoch++
    mocks.managed = [managed('admin', '管理员', true), managed('lead', 'Lead', false), managed('auditor', 'Auditor', false), managed('operator', '运维', true)]
    i18n.global.locale.value = 'zh'
    mocks.post.mockResolvedValue({})
  })

  // The viewer's matrix: super admin column fixed, admin / operator built in, lead custom (under "more roles").
  const column = (key: string, over: object = {}) => ({ key, name: { en: key, zh: key }, builtin: true, superuser: false, nameable: true, ...over })
  const COLUMNS = [column('super_admin', { superuser: true, nameable: false, reason: 'superuser_role' }), column('admin'), column('operator'), column('lead', { builtin: false })]
  const cell = (grant: string[] = [], over: object = {}) => ({ grant, nameable: true, withheld: false, ...over })
  /** A new permission suggested to admin, which is where it starts. */
  const perm = (key: string, over: object = {}) => ({
    key,
    label: { en: key, zh: key },
    risk: 'low' as const,
    new: true,
    defaults: { admin: [''] },
    cells: { super_admin: cell([], { nameable: false, reason: 'superuser_role' }), admin: cell(['']), operator: cell(), lead: cell() },
    ...over
  })
  const HELD = { super_admin: cell([], { nameable: false, reason: 'superuser_role' }), admin: cell([''], { nameable: false, reason: 'held', withheld: true }), operator: cell(), lead: cell() }
  const withMatrix = (over: Partial<PluginReview>) => review({ role_columns: COLUMNS, host_permissions: [{ id: 'storage.read', risk: 'low' }], ...over })
  const td = (w: ReturnType<typeof render>, local: string, role: string) => w.get(`[data-testid="permission-matrix"] tr[data-permission="plugin.demo:${local}"] td[data-role="${role}"]`)
  const roleBox = (w: ReturnType<typeof render>, local: string, role: string) => td(w, local, role).get<HTMLInputElement>('input[type="checkbox"]')
  async function approve(w: ReturnType<typeof render>) {
    await w.get('[data-testid="consent-approve"]').trigger('click')
    await flushPromises()
    return mocks.post.mock.calls[0]?.[1]
  }

  it('sends the ticked permissions as grants, and only the rows changed from where they started, each with every role the caller may name', async () => {
    const w = render(withMatrix({ user_permissions: [perm('demo.read'), perm('demo.write'), perm('demo.old', { new: false })], host_permissions: [{ id: 'http.client', risk: 'high' }, { id: 'storage.read', risk: 'low' }] }))
    await flushPromises()
    // Each row starts at what the consent gives without overriding it (cells.grant).
    expect(roleBox(w, 'demo.read', 'admin').element.checked).toBe(true)
    expect(roleBox(w, 'demo.read', 'operator').element.checked).toBe(false)
    expect(w.find('tr[data-permission="plugin.demo:demo.old"]').exists()).toBe(false)
    await roleBox(w, 'demo.write', 'operator').setValue(true)
    await w.get('[data-testid="host-permissions"] input[type="checkbox"]').setValue(true)
    await approve(w)
    expect(mocks.post).toHaveBeenCalledWith('/plugins/demo/versions/1.2.0/consent', {
      grants: [{ permission: 'http.client' }, { permission: 'storage.read' }],
      denied: [],
      new_permission_roles: { 'demo.write': { admin: true, operator: true, lead: [] } }
    })
    expect(mocks.push).toHaveBeenCalledWith({ path: '/plugins/demo', query: { consented: '1.2.0' } })
  })

  it('clearing a role sends it empty; going back to where a row started sends nothing', async () => {
    const w = render(withMatrix({ user_permissions: [perm('demo.read'), perm('demo.write')] }))
    await flushPromises()
    await roleBox(w, 'demo.read', 'admin').setValue(false)
    await roleBox(w, 'demo.write', 'operator').setValue(true)
    await roleBox(w, 'demo.write', 'operator').setValue(false)
    expect((await approve(w)).new_permission_roles).toEqual({ 'demo.read': { admin: [], operator: [], lead: [] } })
  })

  it('sends a scoped row as its scopes: "*" for all, suffixes for the rest', async () => {
    const flow = perm('flow:manage', {
      scope: { resource: 'flow', own: true },
      defaults: { admin: [''], operator: ['own'] },
      cells: { ...perm('x').cells, operator: cell(['own']) }
    })
    const w = render(withMatrix({ user_permissions: [flow] }))
    await flushPromises()
    const select = td(w, 'flow:manage', 'operator').get<HTMLSelectElement>('select')
    const texts = select.findAll('option').map((o) => o.text())
    expect(select.element.value).toBe(String(texts.indexOf(i18n.global.t('plugins.matrix.scopeOwn'))))
    await select.setValue(String(texts.indexOf(i18n.global.t('plugins.matrix.notGranted'))))
    expect((await approve(w)).new_permission_roles).toEqual({ 'flow:manage': { admin: ['*'], operator: [], lead: [] } })
  })

  it('locks what the server says the caller may not name: held for review, counted in the footer, never sent', async () => {
    const w = render(withMatrix({ user_permissions: [perm('demo.read', { cells: HELD })] }))
    await flushPromises()
    expect(td(w, 'demo.read', 'admin').attributes('data-lock')).toBe('pending_review')
    expect(td(w, 'demo.read', 'admin').text()).toContain(i18n.global.t('plugins.matrix.pendingReview'))
    expect(roleBox(w, 'demo.read', 'admin').element.disabled).toBe(true)
    const pending = i18n.global.t('plugins.matrix.summaryPending', { n: 1 })
    expect(w.get('[data-testid="consent-summary-pending"]').text()).toBe(pending)
    // Overriding the row leaves the held cell held: still counted, still not sent.
    await roleBox(w, 'demo.read', 'operator').setValue(true)
    expect(w.get('[data-testid="consent-summary-pending"]').text()).toBe(pending)
    expect((await approve(w)).new_permission_roles).toEqual({ 'demo.read': { operator: true, lead: [] } })
  })

  it('a critical permission cannot be granted by a non-super installer', async () => {
    const critical = { super_admin: cell([], { nameable: false, reason: 'superuser_role' }), admin: cell([], { nameable: false, reason: 'critical' }), operator: cell([], { nameable: false, reason: 'critical' }), lead: cell([], { nameable: false, reason: 'critical' }) }
    const w = render(withMatrix({ user_permissions: [perm('exempt', { risk: 'critical', defaults: {}, cells: critical })] }))
    await flushPromises()
    expect(td(w, 'exempt', 'operator').attributes('data-lock')).toBe('critical')
    expect(roleBox(w, 'exempt', 'operator').element.disabled).toBe(true)
    expect(w.get('tr[data-permission="plugin.demo:exempt"]').text()).toContain(i18n.global.t('plugins.matrix.criticalOnly'))
    expect(w.find('[data-testid="consent-summary-pending"]').exists()).toBe(false)
  })

  it('a cell the caller may not name with nothing to hold is locked without the review mark', async () => {
    const w = render(withMatrix({ user_permissions: [perm('demo.read', { cells: { ...HELD, operator: cell([], { nameable: false, reason: 'held' }) } })] }))
    await flushPromises()
    expect(td(w, 'demo.read', 'operator').attributes('data-lock')).toBe('role_readonly')
    expect(td(w, 'demo.read', 'operator').text()).not.toContain(i18n.global.t('plugins.matrix.pendingReview'))
    expect(td(w, 'demo.read', 'operator').attributes('title')).toBe(i18n.global.t('plugins.matrix.roleReadonly'))
    expect(w.get('[data-testid="consent-summary-pending"]').text()).toBe(i18n.global.t('plugins.matrix.summaryPending', { n: 1 }))
  })

  it('a renamed row starts from what it inherits, marked so, and is edited cell by cell (C2)', async () => {
    const renamed = perm('flow:edit', {
      defaults: undefined,
      renamed: true,
      inherit_from: ['flow:write'],
      cells: { ...perm('x').cells, admin: cell(), operator: cell(['']) }
    })
    const w = render(withMatrix({ user_permissions: [renamed, perm('demo.read')] }))
    await flushPromises()
    const row = w.get('tr[data-permission="plugin.demo:flow:edit"]')
    expect(row.get('[data-testid="matrix-inherited"]').text()).toBe(i18n.global.t('plugins.matrix.inheritedFrom', { keys: 'flow:write' }))
    expect(roleBox(w, 'flow:edit', 'operator').element.checked).toBe(true)
    expect(roleBox(w, 'flow:edit', 'operator').element.disabled).toBe(false)
    expect(roleBox(w, 'flow:edit', 'admin').element.disabled).toBe(false)
    // No suggestion to go back to.
    expect(row.find('[data-testid="matrix-restore-row"]').exists()).toBe(false)

    // Left as inherited, it is not sent; changed, it is sent whole for the roles the caller may name.
    await roleBox(w, 'demo.read', 'operator').setValue(true)
    await roleBox(w, 'flow:edit', 'operator').setValue(false)
    await roleBox(w, 'flow:edit', 'admin').setValue(true)
    expect((await approve(w)).new_permission_roles).toEqual({
      'demo.read': { admin: true, operator: true, lead: [] },
      'flow:edit': { admin: true, operator: [], lead: [] }
    })
  })

  it('a renamed row whose holders the viewer may not see is locked, saying why (C4)', async () => {
    const renamed = perm('flow:edit', {
      defaults: undefined,
      renamed: true,
      inherit_from: ['flow:write'],
      cells: { ...perm('x').cells, admin: { nameable: true, withheld: false }, operator: { nameable: true, withheld: false }, lead: { nameable: true, withheld: false } }
    })
    const w = render(withMatrix({ user_permissions: [renamed, perm('demo.read')] }))
    await flushPromises()
    // Not an empty box reading "not granted": a badge, explained in the legend.
    expect(td(w, 'flow:edit', 'operator').find('input').exists()).toBe(false)
    expect(td(w, 'flow:edit', 'operator').get('[data-testid="cell-by-inheritance"]').text()).toBe(i18n.global.t('plugins.matrix.byInheritance'))
    expect(td(w, 'flow:edit', 'operator').attributes('title')).toBe(i18n.global.t('plugins.matrix.holdersHidden'))
    expect(w.get('[data-testid="matrix-inherit-legend"]').text()).toContain(i18n.global.t('plugins.matrix.holdersHidden'))
    await roleBox(w, 'demo.read', 'operator').setValue(true)
    expect(Object.keys((await approve(w)).new_permission_roles)).toEqual(['demo.read'])
  })

  it('renders a review whose cells or removed rows lack the holder fields (C4)', async () => {
    const bare = perm('demo.read', { cells: { super_admin: { nameable: false, reason: 'superuser_role', withheld: false }, admin: { nameable: true, withheld: false }, operator: cell(), lead: cell() } })
    const w = render(withMatrix({ user_permissions: [bare], removed_user_permissions: [{ key: 'old', label: { en: 'Old', zh: '旧' } }] as never, diff: { added: [], widened: [], removed: [] } as never }))
    await flushPromises()
    expect(roleBox(w, 'demo.read', 'admin').element.checked).toBe(false)
    expect(roleBox(w, 'demo.read', 'admin').element.disabled).toBe(false)
    expect(w.get('[data-testid="consent-diff-user"]').text()).toContain(i18n.global.t('plugins.consent.diffUser', { added: 1, revived: 0, removed: 1 }))
  })

  it('a non-super installer may give a new scoped permission any scope: it need not hold it', async () => {
    mocks.superuser = false
    const flow = perm('flow:manage', { scope: { resource: 'flow', own: true }, defaults: { operator: ['own'] }, cells: { ...perm('x').cells, admin: cell(), operator: cell(['own']) } })
    const w = render(withMatrix({ user_permissions: [flow] }))
    await flushPromises()
    const select = td(w, 'flow:manage', 'operator').get<HTMLSelectElement>('select')
    const texts = () => select.findAll('option').map((o) => o.text())
    const { t } = i18n.global
    expect(texts()).toEqual([t('plugins.matrix.notGranted'), t('plugins.matrix.scopeAll'), t('plugins.matrix.scopeOwn'), t('plugins.matrix.scopeCustom')])
    await select.setValue(String(texts().indexOf(t('plugins.matrix.notGranted'))))
    // Cleared, it can still go back (and further).
    await select.setValue(String(texts().indexOf(t('plugins.matrix.scopeAll'))))
    expect((await approve(w)).new_permission_roles).toEqual({ 'flow:manage': { admin: [], operator: ['*'], lead: [] } })
  })

  it('footer sums the critical grants a super admin makes', async () => {
    const w = render(withMatrix({ user_permissions: [perm('exempt', { risk: 'critical', defaults: {}, cells: { ...perm('x').cells, admin: cell() } })] }))
    await flushPromises()
    expect(w.find('[data-testid="consent-summary-critical"]').exists()).toBe(false)
    await roleBox(w, 'exempt', 'operator').setValue(true)
    expect(w.get('[data-testid="consent-summary-critical"]').text()).toBe(i18n.global.t('plugins.matrix.summaryCritical', { n: 1, roles: 'operator' }, 1))
  })

  it('footer sums the sensitive grants it makes', async () => {
    const w = render(withMatrix({ user_permissions: [perm('a', { risk: 'sensitive' }), perm('b', { risk: 'sensitive', cells: HELD }), perm('c')] }))
    await flushPromises()
    // a to admin; b to admin is held, so it is not granted here.
    expect(w.get('[data-testid="consent-summary-sensitive"]').text()).toBe(i18n.global.t('plugins.matrix.summarySensitive', { n: 1, roles: 'admin' }, 1))
    await roleBox(w, 'b', 'operator').setValue(true)
    expect(w.get('[data-testid="consent-summary-sensitive"]').text()).toBe(i18n.global.t('plugins.matrix.summarySensitive', { n: 2, roles: ['admin', 'operator'].join(i18n.global.t('roles.listSep')) }, 2))
  })

  it('toasts the grants the consent held back for review', async () => {
    toastState.items.splice(0)
    mocks.post.mockResolvedValue({ withheld: [{ permission: 'plugin.demo:demo.read', role: 'admin', scope: '', risk: 'low', via: 'default', needs_consent: false, withheld: true }] })
    const w = render(withMatrix({ user_permissions: [perm('demo.read', { cells: HELD })] }))
    await flushPromises()
    await approve(w)
    expect(toastState.items.map((x) => x.message)).toContain(i18n.global.t('plugins.matrix.withheld', { n: 1 }))
  })

  it('words a critical refusal itself', async () => {
    toastState.items.splice(0)
    mocks.post.mockRejectedValue({ status: 403, code: 'permission_denied', message: 'denied', details: { reason: 'critical', superuser: true, permissions: ['exempt'] } })
    const w = render(withMatrix({}))
    await flushPromises()
    await approve(w)
    expect(toastState.items.map((x) => x.message)).toContain(i18n.global.t('plugins.matrix.criticalOnly'))
  })

  it('shows the fixed statement that the plugin receives every request in full, without a checkbox', async () => {
    const statement = { en: 'receives every request', zh: '收到每个请求的完整内容' }
    const w = render(review({ request_access: statement }))
    await flushPromises()
    const alert = w.get('[data-testid="consent-request-access"]')
    expect(alert.text()).toContain(statement.zh)
    expect(alert.text()).toContain(i18n.global.t('plugins.consent.requestAccessTitle'))
    expect(alert.find('input').exists()).toBe(false)
    const none = render(review())
    await flushPromises()
    expect(none.find('[data-testid="consent-request-access"]').exists()).toBe(false)
  })

  it('tells beforehand when db.schema needs the unconfined override the caller lacks', async () => {
    mocks.lacks = new Set(['plugin:grant:db_schema_unconfined'])
    const hp = [{ id: 'db.schema', risk: 'critical' }] as never
    const w = render(review({ host_permissions: hp, needs_db_schema_unconfined: true }))
    await flushPromises()
    expect((w.get('[data-testid="consent-approve"]').element as HTMLButtonElement).disabled).toBe(true)
    expect(w.text()).toContain('plugin:grant:db_schema_unconfined')
    const ok = render(review({ host_permissions: hp, needs_db_schema_unconfined: false }))
    await flushPromises()
    expect((ok.get('[data-testid="consent-approve"]').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('on an upgrade, roles the caller may not name go to review instead of blocking', async () => {
    const grants = [
      { permission: 'plugin.demo:demo.read', role: 'admin', scope: '', risk: 'sensitive', via: 'default', needs_consent: true, nameable: false, withheld: true },
      { permission: 'plugin.demo:demo.write', role: 'admin', scope: '', risk: 'low', via: 'default', needs_consent: false, nameable: true, withheld: false }
    ]
    mocks.superuser = false
    const w = render(withMatrix({
      user_permissions: [perm('demo.read', { risk: 'sensitive', cells: HELD }), perm('demo.write')],
      diff: { added: [], widened: [], removed: [] } as never,
      new_role_grants: grants as never,
      role_grants_need_consent: true
    }))
    await flushPromises()
    expect((w.get('[data-testid="consent-approve"]').element as HTMLButtonElement).disabled).toBe(false)
    // The "needs confirmation" column, per row.
    expect(w.get('[data-testid="permission-matrix"] thead').text()).toContain(i18n.global.t('plugins.matrix.needsConfirm'))
    expect(w.get('tr[data-permission="plugin.demo:demo.read"]').text()).toContain(i18n.global.t('plugins.matrix.needConfirmSensitive'))
    expect(w.get('tr[data-permission="plugin.demo:demo.write"]').text()).toContain(i18n.global.t('plugins.matrix.autoGrant'))
    expect((await approve(w)).new_permission_roles).toEqual({})
  })

  it('on an upgrade, lists only the new permissions, with kept, revived and removed ones as information', async () => {
    const w = render(withMatrix({
      user_permissions: [perm('demo.new'), perm('demo.kept', { new: false }), perm('demo.back', { new: true, revived: true, label: { en: 'Back', zh: '回来' } })],
      removed_user_permissions: [{ key: 'demo.gone', label: { en: 'Gone', zh: '已去' }, holders: ['admin', 'operator'] }],
      diff: { added: [], widened: [], removed: [] } as never
    }))
    await flushPromises()
    expect(w.findAll('[data-testid="permission-matrix"] tbody tr[data-permission]').map((r) => r.attributes('data-permission'))).toEqual(['plugin.demo:demo.new'])
    const changes = w.get('[data-testid="upgrade-permission-changes"]')
    expect(changes.text()).toContain(i18n.global.t('plugins.matrix.upgradeKept', { n: 1 }))
    expect(changes.get('[data-testid="upgrade-revived"]').text()).toContain('回来')
    expect(changes.get('[data-testid="upgrade-removed"]').text()).toContain(i18n.global.t('plugins.matrix.removedHint', { m: 2 }))
    // A first install has none of it.
    const first = render(withMatrix({ user_permissions: [perm('demo.new')] }))
    await flushPromises()
    expect(first.find('[data-testid="upgrade-permission-changes"]').exists()).toBe(false)
    expect(first.find('[data-testid="permission-matrix"] thead').text()).not.toContain(i18n.global.t('plugins.matrix.needsConfirm'))
  })

  it('keeps a scope on the grant it belongs to', async () => {
    const w = render(review({ host_permissions: [{ id: 'storage.read', risk: 'low', scope: { domains: ['a.example'] } } as never] }))
    await flushPromises()
    await w.get('[data-testid="consent-approve"]').trigger('click')
    await flushPromises()
    expect(mocks.post.mock.calls[0][1].grants).toEqual([{ permission: 'storage.read', scope: { domains: ['a.example'] } }])
  })

  it('blocks approval when a permission needs a right the caller lacks', async () => {
    mocks.can = false
    const w = render(review({ host_permissions: [{ id: 'db.core_read', risk: 'critical', requires: 'plugin:grant:critical' }] }))
    await flushPromises()
    expect((w.get('[data-testid="consent-approve"]').element as HTMLButtonElement).disabled).toBe(true)
    expect(w.text()).toContain('db.core_read')
  })

  it('on an upgrade, keeps what was granted before without the grant right and sends only new grants', async () => {
    mocks.lacks = new Set(['plugin:grant:critical'])
    const w = render(review({
      host_permissions: [
        { id: 'ui.native', risk: 'critical', current_status: 'granted', current_scope: null },
        { id: 'http.client', risk: 'high', current_status: 'granted' },
        { id: 'db.core_read', risk: 'critical', optional: true }
      ] as never,
      diff: { added: [{ id: 'db.core_read', risk: 'critical' }], widened: [{ id: 'http.client', risk: 'high' }], removed: [] } as never
    }))
    await flushPromises()
    // ui.native is kept; http.client is widened, so it is a new grant; db.core_read is new and needs the right.
    // The diff lists items as the server sends them (review.go DiffItem), by id.
    expect(w.findAll('[data-testid="hp-carried"]')).toHaveLength(1)
    expect(w.text()).not.toContain('[object Object]')
    await w.get('input[data-testid="consent-ack"]').setValue(true) // db.core_read is new and critical: acknowledged first
    expect((w.get('[data-testid="consent-approve"]').element as HTMLButtonElement).disabled).toBe(false)
    await w.findAll('[data-testid="host-permissions"] input[type="checkbox"]').find((b) => !(b.element as HTMLInputElement).disabled && !(b.element as HTMLInputElement).checked)!.setValue(true)
    await w.get('[data-testid="consent-approve"]').trigger('click')
    await flushPromises()
    expect(mocks.post.mock.calls[0][1]).toMatchObject({ grants: [{ permission: 'http.client' }], denied: ['db.core_read'] })
  })

  it('on an upgrade, grants a revoked permission only when ticked, low risk included', async () => {
    const w = render(review({
      host_permissions: [
        { id: 'kv', risk: 'low', current_status: 'revoked', revoked: true },
        { id: 'log', risk: 'low', optional: true, current_status: 'revoked', revoked: true },
        { id: 'config', risk: 'low' }
      ] as never,
      diff: { added: [{ id: 'kv', risk: 'low' }, { id: 'log', risk: 'low' }], widened: [], removed: [] } as never
    }))
    await flushPromises()
    expect(w.findAll('[data-testid="hp-revoked"]')).toHaveLength(2)
    expect(w.text()).toContain('已撤销，勾选才重新授予')
    // kv is required: left unticked it blocks the approval.
    await w.get('[data-testid="consent-approve"]').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    const boxes = w.findAll('[data-testid="host-permissions"] input[type="checkbox"]')
    expect(boxes).toHaveLength(2)
    expect(boxes.every((b) => !(b.element as HTMLInputElement).checked)).toBe(true)
    await boxes[0].setValue(true) // kv
    await w.get('[data-testid="consent-approve"]').trigger('click')
    await flushPromises()
    expect(mocks.post.mock.calls[0][1]).toMatchObject({ grants: [{ permission: 'config' }, { permission: 'kv' }], denied: ['log'] })
  })

  describe('database', () => {
    const CORE_READ = { id: 'db.core_read', risk: 'critical' as const }
    const migration = (id: string, status: 'new' | 'changed' | 'applied') => ({ id, checksum: 'sha256:' + id, sql: `CREATE TABLE plg_demo__${id} ();`, status })

    it('lists the tables outside its own under db.core_read: table, columns (all when none), privileges', async () => {
      const access = [{ table: 'users', columns: ['id', 'email'], privileges: ['select' as const] }, { table: 'groups', privileges: ['select' as const] }]
      // The core sends the access as db.core_read's scope too: listed once, as the table list.
      const w = render(review({ host_permissions: [{ id: 'db.schema', risk: 'high' }, { ...CORE_READ, scope: { access: ['select:users.id', 'select:users.email', 'select:groups.*'] } }], database_access: access }))
      await flushPromises()
      const rows = w.findAll('[data-testid="host-permissions"] li')
      const coreRead = rows.find((r) => r.text().includes('db.core_read'))!
      expect(coreRead.get('[data-table="users"]').text()).toContain('列：id, email')
      expect(coreRead.get('[data-table="users"]').text()).toContain('查询')
      expect(coreRead.get('[data-table="groups"]').text()).toContain('列：全部列')
      expect(coreRead.get('[data-hp-desc="db.core_read"]').text()).toBe('按申请的表、列和权限访问；其他插件的表不能申请。')
      expect(coreRead.text()).not.toContain('select:users.id')
      // Asking only to select: no warning, and nothing marked new on an install.
      expect(w.find('[data-testid="database-access-warn"]').exists()).toBe(false)
      expect(w.text()).not.toContain('新增')
      expect(rows.find((r) => r.text().includes('db.schema'))!.find('[data-testid="database-access"]').exists()).toBe(false)
    })

    it('says what writing and referencing core tables mean, by the privileges asked for', async () => {
      const WRITE = '直接写核心的表会绕过核心的缓存、权限版本和审计。'
      const REFERENCE = '外键引用会限制核心删除被引用的行。'
      const cases = [
        [['insert'], '新增', [WRITE]],
        [['update'], '修改', [WRITE]],
        [['delete'], '删除', [WRITE]],
        [['references'], '外键引用', [REFERENCE]],
        [['update', 'references'], '修改', [WRITE, REFERENCE]]
      ] as const
      for (const [privileges, label, warnings] of cases) {
        const w = render(review({ host_permissions: [CORE_READ], database_access: [{ table: 'usage_logs', columns: ['status'], privileges: ['select', ...privileges] }] }))
        await flushPromises()
        expect(w.get('[data-table="usage_logs"]').text()).toContain(label)
        expect(w.findAll('[data-testid="database-access-warn"] p').map((p) => p.text())).toEqual(warnings)
        w.unmount()
      }
    })

    it('on an upgrade, marks the tables and privileges the grant in force does not hold yet', async () => {
      const w = render(review({
        host_permissions: [{ ...CORE_READ, current_status: 'granted', current_scope: { access: ['select:users.id', 'select:groups.*'] } }],
        database_access: [
          { table: 'users', columns: ['id'], privileges: ['select'] },
          { table: 'groups', privileges: ['select', 'references'] },
          { table: 'usage_logs', columns: ['status'], privileges: ['select', 'update'] }
        ],
        diff: { added: [], widened: [{ id: 'db.core_read', risk: 'critical' }], removed: [] }
      }))
      await flushPromises()
      const users = w.get('[data-table="users"]')
      expect(users.find('[data-testid="access-added"]').exists()).toBe(false)
      expect(users.get('[data-privilege="select"]').text()).toBe('查询')
      const groups = w.get('[data-table="groups"]')
      expect(groups.find('[data-testid="access-added"]').exists()).toBe(false)
      expect(groups.get('[data-privilege="select"]').text()).toBe('查询')
      expect(groups.get('[data-privilege="references"]').text()).toBe('外键引用 · 新增')
      const logs = w.get('[data-table="usage_logs"]')
      expect(logs.get('[data-testid="access-added"]').text()).toBe('新增')
      expect(logs.get('[data-privilege="update"]').text()).toBe('修改')
      // Widened: consented to again, not carried.
      expect(w.find('[data-testid="hp-carried"]').exists()).toBe(false)
    })

    it('shows no table list without database_access', async () => {
      for (const over of [{}, { database_access: [] }]) {
        const w = render(review({ host_permissions: [CORE_READ], ...over }))
        await flushPromises()
        expect(w.find('[data-testid="database-access"]').exists()).toBe(false)
        w.unmount()
      }
    })

    it('lists the scripts approving runs with their SQL, the ones already run behind a toggle', async () => {
      const w = render(review({
        database: { migrations: ['0001', '0002', '0003'] },
        database_migrations: [migration('0001', 'applied'), migration('0002', 'changed'), migration('0003', 'new')]
      }))
      await flushPromises()
      expect(w.get('[data-testid="provides-database"]').text()).toBe('表前缀 plg_demo__、迁移脚本 3 个')
      const card = w.get('[data-testid="database-migrations"]')
      expect(card.text()).toContain('只能新建以 plg_demo__ 开头的表、索引、序列、视图和类型，不能建函数和触发器')
      expect(card.get('[data-migration="0002"]').text()).toContain('内容有变，将重新执行')
      expect(card.get('[data-migration="0003"]').text()).toContain('将执行')
      expect(card.find('[data-migration="0001"]').exists()).toBe(false)
      expect(card.find('[data-testid="migration-sql"]').exists()).toBe(false)
      const toggle = card.get('[data-migration="0003"] button')
      expect(toggle.text()).toBe('查看 0003 的 SQL')
      await toggle.trigger('click')
      const sql = card.get('[data-testid="migration-sql"]')
      expect(sql.text()).toBe('CREATE TABLE plg_demo__0003 ();')
      expect(sql.attributes()).toMatchObject({ tabindex: '0', role: 'region', 'aria-label': '0003 的 SQL' })
      await card.findAll('button').find((b) => b.text().includes('已执行且未变的脚本（1 个）'))!.trigger('click')
      expect(card.find('[data-testid="database-migrations-applied"] [data-migration="0001"]').exists()).toBe(true)
    })

    it('says nothing runs when every script ran as it is, and shows no card without scripts', async () => {
      const w = render(review({ database: { migrations: ['0001'] }, database_migrations: [migration('0001', 'applied')] }))
      await flushPromises()
      expect(w.get('[data-testid="database-migrations"]').text()).toContain('本次没有要执行的脚本。')
      const none = render(review())
      await flushPromises()
      expect(none.find('[data-testid="database-migrations"]').exists()).toBe(false)
    })
  })

  it('asks for the missing required permissions instead of submitting', async () => {
    const w = render(review({ host_permissions: [{ id: 'http.client', risk: 'high' }] }))
    await flushPromises()
    await w.get('[data-testid="consent-approve"]').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('keeps the page structure: icons and hints render after a split into child components', async () => {
    const w = render(review())
    await flushPromises()
    // Resolved components (not unknown elements): the back arrow and the publisher hint.
    expect(w.findComponent(SIcon).exists()).toBe(true)
    expect(w.findComponent(SHint).exists()).toBe(true)
    expect(w.find('svg').exists()).toBe(true)
    expect(w.text()).toContain(i18n.global.t('plugins.publisher'))
  })

  it('shows the empty state when the review cannot be loaded', async () => {
    mocks.get.mockRejectedValue(new Error('not found'))
    const w = mountView()
    await flushPromises()
    expect(w.findComponent(SEmpty).exists()).toBe(true)
    expect(w.text()).toContain(i18n.global.t('plugins.consent.notFound'))
  })

  it('says the package is gone rather than offering an approval (410 package_gone)', async () => {
    mocks.get.mockRejectedValue({ status: 410, code: 'package_gone', message: 'no reachable node holds the package of this version; bring a node that holds it back online, or upload it again', details: {} })
    for (const locale of ['zh', 'en'] as const) {
      i18n.global.locale.value = locale
      const w = mountView()
      await flushPromises()
      expect(w.get('[data-testid="consent-unavailable"]').text()).toContain(i18n.global.t('plugins.consent.packageGone', { version: '1.2.0' }))
      expect(w.find('[data-testid="consent-approve"]').exists()).toBe(false)
      w.unmount()
    }
  })
})
