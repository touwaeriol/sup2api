import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ApiError, type PluginHost } from '@sub2api/host'
import { SAlert, SCHEMA_WIDGETS_KEY, SField, SFormModal, SSegmented } from '@sub2api/ui'
import schema from '../../../forms/settings.schema.json'
import ui from '../../../forms/settings.ui.json'
import BindingEditModal from './BindingEditModal.vue'
import BindingsTab from './BindingsTab.vue'
import BlocksTab from './BlocksTab.vue'
import SettingsTab from './SettingsTab.vue'
import WatchEditModal from './WatchEditModal.vue'
import { fetchAccountGroups, fetchAccountNames, setHost } from './host'
import { resetSessionCaches } from './session'
import type { Binding, Watch } from './types'

// The forms pick their user, group and account with the core's entity picker,
// taken from host.components: stood in for here by a component with its
// contract (kind, v-model:id as a string, '' = none, extra candidates, lookup
// scope). What a form sends is the picked id as a number.
const EntityPicker = defineComponent({
  props: { kind: String, id: String, extra: Array, scope: Object, disabled: Boolean },
  emits: ['update:id'],
  render: () => h('div')
})
/** The console's entity-select schema widget: the value of the field in, the new value out. */
const EntitySelect = defineComponent({
  props: { modelValue: null, schema: Object, ui: Object, multiple: Boolean, disabled: Boolean },
  emits: ['update:modelValue'],
  render: () => h('div')
})

type Call = (path: string, body?: unknown) => Promise<unknown>
const pluginGet = vi.fn<Call>()
const coreGet = vi.fn<Call>()
const post = vi.fn<Call>()
const put = vi.fn<Call>()
const corePut = vi.fn<Call>()
/** The stored plugin settings (GET /plugins/moderation/settings). */
let stored: Record<string, unknown> = {}
/** The bindings of GET /bindings. */
let bindings: Binding[] = []

beforeEach(() => {
  stored = {}
  bindings = []
  for (const f of [pluginGet, coreGet, post, put, corePut]) f.mockReset()
  pluginGet.mockImplementation(async (path) => (path === '/bindings' ? { bindings, account_bindings_active: true } : path === '/jev/default-questions' ? { builtin: { preamble: '', items: [] } } : []))
  coreGet.mockImplementation(async (path) => (path.startsWith('/lookup/') ? { items: [], more: false } : { values: stored, schema, ui_schema: ui }))
  post.mockResolvedValue({})
  put.mockResolvedValue({ bindings: [], account_bindings_active: true })
  corePut.mockImplementation(async () => ({ values: stored, schema, ui_schema: ui }))
  const t = (key: string) => key
  setHost({
    t,
    i18n: { t, text: (v: { zh?: string } | string) => (typeof v === 'string' ? v : (v?.zh ?? '')), locale: { value: 'zh' } },
    components: { EntityPicker },
    plugin: { key: 'moderation' },
    pluginApi: { get: pluginGet, post, put },
    api: { get: coreGet, put: corePut, list: async () => ({ items: [], paged: false, page: {} }) },
    permissions: { superuser: () => true, has: () => true },
    can: () => true,
    toast: vi.fn(),
    confirm: vi.fn(async () => true)
  } as unknown as PluginHost)
})
afterEach(() => {
  resetSessionCaches()
  setHost(null)
})
enableAutoUnmount(afterEach)

const fieldError = (w: ReturnType<typeof mount>, label: string) => w.findAllComponents(SField).find((f) => f.props('label') === label)?.props('error')
const click = async (w: ReturnType<typeof mount>, text: string) => {
  const button = [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === text)
  if (!button) throw new Error(`no button "${text}"`)
  button.click()
  await flushPromises()
  return w
}

describe('the block form', () => {
  async function opened() {
    const w = mount(BlocksTab, { attachTo: document.body })
    await flushPromises()
    await click(w, 'blocks.manual')
    const picker = w.findComponent(EntityPicker)
    const submit = async () => {
      w.findComponent(SFormModal).vm.$emit('submit')
      await flushPromises()
    }
    return { w, picker, submit }
  }

  it('bans the picked user by its id as a number', async () => {
    const { picker, submit } = await opened()
    expect([picker.props('kind'), picker.props('id')]).toEqual(['user', ''])
    picker.vm.$emit('update:id', '7')
    await submit()
    expect(post).toHaveBeenCalledWith('/blocks', { user_id: 7, duration_hours: 24 })
  })

  it('sends nothing without a user, or for an id that is none as it stands', async () => {
    const { w, picker, submit } = await opened()
    for (const id of ['', '0', '007', '9007199254740993']) {
      picker.vm.$emit('update:id', id)
      await submit()
      expect(fieldError(w, 'blocks.col.user')).toBe('blocks.invalidUser')
    }
    expect(post).not.toHaveBeenCalled()
  })

  it("shows the backend's refusal of a user at the user", async () => {
    post.mockRejectedValue(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'user_id', code: 'staff', message: 'a moderator' }] } }))
    const { w, picker, submit } = await opened()
    picker.vm.$emit('update:id', '1')
    await submit()
    expect(fieldError(w, 'blocks.col.user')).toBe('a moderator')
    expect(w.findComponent(SFormModal).props('open')).toBe(true)
  })

  it('opens empty again after a ban', async () => {
    const { w, picker, submit } = await opened()
    picker.vm.$emit('update:id', '7')
    await submit()
    expect(w.findComponent(SFormModal).props('open')).toBe(false)
    await click(w, 'blocks.manual')
    expect(w.findComponent(EntityPicker).props('id')).toBe('')
    await submit()
    expect(post).toHaveBeenCalledTimes(1)
  })
})

describe('WatchEditModal', () => {
  const entry = { user_id: 7, level: 'observe', expires_at: null, note: '' } as unknown as Watch
  async function opened(of: Watch | null = null) {
    const w = mount(WatchEditModal, { attachTo: document.body, props: { open: false, entry: of } })
    await w.setProps({ open: true })
    await flushPromises()
    return w
  }

  it('adds the picked user to the watch list by its id as a number', async () => {
    const w = await opened()
    const picker = w.findComponent(EntityPicker)
    expect([picker.props('kind'), picker.props('id')]).toEqual(['user', ''])
    picker.vm.$emit('update:id', '4')
    await click(w, 'save')
    expect(post).toHaveBeenCalledWith('/watchlist', { user_id: 4, level: 'observe' })
    expect(w.emitted('saved')).toHaveLength(1)
  })

  it('asks for a user before it sends anything', async () => {
    const w = await opened()
    await click(w, 'save')
    expect(fieldError(w, 'events.user')).toBe('blocks.invalidUser')
    expect(post).not.toHaveBeenCalled()
    expect(w.emitted('saved')).toBeUndefined()
  })

  it("shows the backend's refusal of a user at the user", async () => {
    post.mockRejectedValue(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'user_id', code: 'staff', message: 'a moderator' }] } }))
    const w = await opened()
    w.findComponent(EntityPicker).vm.$emit('update:id', '1')
    await click(w, 'save')
    expect(fieldError(w, 'events.user')).toBe('a moderator')
  })

  it('does not offer another user for an entry', async () => {
    const w = await opened(entry)
    expect(w.findComponent(EntityPicker).exists()).toBe(false)
  })

  it('opens empty again: not with the user picked before, nor with the user of an entry edited before', async () => {
    const w = await opened()
    w.findComponent(EntityPicker).vm.$emit('update:id', '4')
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    expect(w.findComponent(EntityPicker).props('id')).toBe('')
    await w.setProps({ open: false, entry })
    await w.setProps({ open: true })
    await w.setProps({ open: false, entry: null })
    await w.setProps({ open: true })
    await flushPromises()
    expect(w.findComponent(EntityPicker).props('id')).toBe('')
    await click(w, 'save')
    expect(post).not.toHaveBeenCalled()
  })
})

describe('BindingEditModal', () => {
  async function opened(binding: Binding | null = null) {
    const w = mount(BindingEditModal, { attachTo: document.body, props: { open: false, binding } })
    await w.setProps({ open: true })
    await flushPromises()
    const pickers = () => Object.fromEntries(w.findAllComponents(EntityPicker).map((p) => [p.props('kind'), p]))
    const kind = async (k: string) => {
      w.findComponent(SSegmented).vm.$emit('update:modelValue', k)
      await flushPromises()
    }
    return { w, pickers, kind }
  }
  const defaultGroup = [{ id: 0, name: 'flows.defaultGroup' }]

  it('binds an account within a group: both picked, the ids sent as numbers', async () => {
    const { w, pickers } = await opened()
    expect(Object.keys(pickers())).toEqual(['group', 'account'])
    // The default group is no group an account is bound within.
    expect(pickers().group.props('extra')).toEqual([])
    expect(pickers().account.props('scope')).toBeUndefined()
    pickers().group.vm.$emit('update:id', '2')
    await flushPromises()
    // The picked group's accounts come first.
    expect(pickers().account.props('scope')).toEqual({ prefer_group_id: '2' })
    pickers().account.vm.$emit('update:id', '12')
    await click(w, 'save')
    expect(put).toHaveBeenCalledWith('/bindings', { group_id: 2, account_id: 12, flow_id: 0, replace: false })
  })

  it('asks for the group and the account it needs', async () => {
    const { w } = await opened()
    await click(w, 'save')
    expect(fieldError(w, 'bindings.col.group')).toBe('bindings.groupRequired')
    expect(fieldError(w, 'bindings.col.account')).toBe('bindings.accountRequired')
    expect(put).not.toHaveBeenCalled()
  })

  it('binds an account in any group with group 0, whatever group was picked before', async () => {
    const { w, pickers, kind } = await opened()
    pickers().group.vm.$emit('update:id', '2')
    await kind('account')
    expect(Object.keys(pickers())).toEqual(['account'])
    expect(pickers().account.props('scope')).toBeUndefined()
    pickers().account.vm.$emit('update:id', '12')
    await click(w, 'save')
    expect(put).toHaveBeenCalledWith('/bindings', { group_id: 0, account_id: 12, flow_id: 0, replace: false })
  })

  it('offers the default group for a group binding only', async () => {
    const { w, pickers, kind } = await opened()
    await kind('group')
    expect(Object.keys(pickers())).toEqual(['group'])
    expect(pickers().group.props('extra')).toEqual(defaultGroup)
    pickers().group.vm.$emit('update:id', '0')
    await click(w, 'save')
    expect(put).toHaveBeenCalledWith('/bindings', { group_id: 0, account_id: 0, flow_id: 0, replace: false })
  })

  it('drops a picked default group when the binding becomes one of an account within a group', async () => {
    const { w, pickers, kind } = await opened()
    await kind('group')
    pickers().group.vm.$emit('update:id', '0')
    await kind('group_account')
    expect(pickers().group.props('id')).toBe('')
    expect(pickers().group.props('extra')).toEqual([])
    pickers().account.vm.$emit('update:id', '12')
    await click(w, 'save')
    expect(fieldError(w, 'bindings.col.group')).toBe('bindings.groupRequired')
    expect(put).not.toHaveBeenCalled()
  })

  it('keeps what an edited binding covers', async () => {
    const { w, pickers } = await opened({ kind: 'group_account', group_id: 2, account_id: 12, flow_id: 0 })
    expect([pickers().group.props('id'), pickers().group.props('disabled')]).toEqual(['2', true])
    expect([pickers().account.props('id'), pickers().account.props('disabled')]).toEqual(['12', true])
    await click(w, 'save')
    expect(put).toHaveBeenCalledWith('/bindings', { group_id: 2, account_id: 12, flow_id: 0, replace: true })
  })

  it('opens empty again after an edit: a new binding takes nothing over', async () => {
    const { w, pickers } = await opened({ kind: 'group', group_id: 0, account_id: 0, flow_id: 0 })
    expect(pickers().group.props('id')).toBe('0')
    await w.setProps({ open: false, binding: null })
    await w.setProps({ open: true })
    await flushPromises()
    expect([pickers().group.props('id'), pickers().account.props('id')]).toEqual(['', ''])
    await click(w, 'save')
    expect(put).not.toHaveBeenCalled()
  })

  describe('an account outside the picked group', () => {
    const warned = (w: ReturnType<typeof mount>) => w.findAllComponents(SAlert).some((a) => a.text() === 'bindings.accountOutsideGroup')
    /** Account 12 is in group 2 only; account 13 is not readable. */
    beforeEach(() => {
      coreGet.mockImplementation(async (path) => {
        if (path === '/accounts/13') throw new ApiError(403, { code: 'permission_denied', message: 'not your account' })
        return { id: 12, group_ids: [2] }
      })
    })
    async function picked(group: string, account: string) {
      const o = await opened()
      o.pickers().group.vm.$emit('update:id', group)
      o.pickers().account.vm.$emit('update:id', account)
      await flushPromises()
      return o
    }

    it('is warned of, and can still be bound', async () => {
      const { w } = await picked('3', '12')
      expect(warned(w)).toBe(true)
      await click(w, 'save')
      expect(put).toHaveBeenCalledWith('/bindings', { group_id: 3, account_id: 12, flow_id: 0, replace: false })
    })

    it('is not warned of once the group is one of the account, nor for an account in any group', async () => {
      const { w, pickers, kind } = await picked('3', '12')
      pickers().group.vm.$emit('update:id', '2')
      await flushPromises()
      expect(warned(w)).toBe(false)
      pickers().group.vm.$emit('update:id', '3')
      await kind('account')
      expect(warned(w)).toBe(false)
    })

    it('is not known of an account the caller may not read', async () => {
      const { w } = await picked('3', '13')
      expect(coreGet).toHaveBeenCalledWith('/accounts/13')
      expect(warned(w)).toBe(false)
    })

    it('is told by the account picked last, whichever answer comes last', async () => {
      let late: (v: unknown) => void = () => {}
      coreGet.mockImplementation((path) => (path === '/accounts/12' ? new Promise((resolve) => (late = resolve)) : Promise.resolve({ id: 14, group_ids: [3] })))
      const { w, pickers } = await picked('3', '12')
      pickers().account.vm.$emit('update:id', '14')
      await flushPromises()
      late({ id: 12, group_ids: [2] })
      await flushPromises()
      expect(warned(w)).toBe(false)
    })
  })
})

describe('the bindings tab', () => {
  it('names the bound accounts through the lookup, asking for those accounts only', async () => {
    bindings = [
      { kind: 'group_account', group_id: 2, account_id: 12, flow_id: 0 },
      { kind: 'account', group_id: 0, account_id: 12, flow_id: 0 },
      { kind: 'account', group_id: 0, account_id: 16, flow_id: 0 },
      { kind: 'group', group_id: 2, account_id: 0, flow_id: 0 }
    ]
    coreGet.mockImplementation(async (path) => (path === '/lookup/accounts' ? { items: [{ id: 12, name: 'claude-main' }], more: false } : { items: [] }))
    const w = mount(BindingsTab, { attachTo: document.body })
    await flushPromises()
    expect(coreGet.mock.calls.filter(([path]) => path === '/lookup/accounts')).toEqual([['/lookup/accounts', { ids: '12,16' }]])
    // An account the lookup does not know keeps its id.
    expect(w.text()).toContain('claude-main #12')
    expect(w.text()).toContain('#16')
  })

  it('previews the binding of the picked group and account', async () => {
    bindings = [{ kind: 'group_account', group_id: 2, account_id: 12, flow_id: 0 }]
    const w = mount(BindingsTab, { attachTo: document.body })
    await flushPromises()
    const [group, account] = w.findAllComponents(EntityPicker)
    expect([group.props('kind'), account.props('kind')]).toEqual(['group', 'account'])
    expect(account.props('scope')).toBeUndefined()
    expect(w.find('.mod-bindings-result').exists()).toBe(false)
    group.vm.$emit('update:id', '2')
    await flushPromises()
    expect(account.props('scope')).toEqual({ prefer_group_id: '2' })
    expect(w.find('.mod-bindings-result').text()).toContain('bindings.previewNone')
    account.vm.$emit('update:id', '12')
    await flushPromises()
    expect(w.find('.mod-bindings-result').text()).toContain('bindings.previewBy')
  })

  it('previews the default group too: what a group without a binding of its own goes by', async () => {
    bindings = [{ kind: 'group_account', group_id: 2, account_id: 12, flow_id: 0 }]
    const w = mount(BindingsTab, { attachTo: document.body })
    await flushPromises()
    const [group, account] = w.findAllComponents(EntityPicker)
    expect(group.props('extra')).toEqual([{ id: 0, name: 'flows.defaultGroup' }])
    group.vm.$emit('update:id', '0')
    account.vm.$emit('update:id', '12')
    await flushPromises()
    // The lookup takes no group 0 to list accounts first by.
    expect(account.props('scope')).toBeUndefined()
    // The binding of account 12 within group 2 is none of another group's.
    expect(w.find('.mod-bindings-result').text()).toBe('bindings.previewNone')
    group.vm.$emit('update:id', '')
    await flushPromises()
    expect(w.find('.mod-bindings-result').exists()).toBe(false)
  })
})

describe('fetchAccountNames', () => {
  it('asks for at most 100 ids a request and leaves out what the lookup does not return', async () => {
    coreGet.mockImplementation(async (_path, query) => {
      const ids = (query as { ids: string }).ids.split(',').map(Number)
      return { items: ids.filter((id) => id % 2 === 0).map((id) => ({ id, name: `a${id}` })) }
    })
    const ids = Array.from({ length: 150 }, (_, i) => i + 1)
    const names = await fetchAccountNames(ids)
    expect(coreGet.mock.calls.map(([path, query]) => [path, (query as { ids: string }).ids.split(',').length])).toEqual([
      ['/lookup/accounts', 100],
      ['/lookup/accounts', 50]
    ])
    expect([names.size, names.get(2), names.get(150), names.get(3)]).toEqual([75, 'a2', 'a150', undefined])
  })

  it('names none when the lookup is refused', async () => {
    coreGet.mockRejectedValue(new ApiError(403, { code: 'permission_denied', message: 'account:read is required' }))
    expect((await fetchAccountNames([1, 2])).size).toBe(0)
    expect(await fetchAccountNames([])).toEqual(new Map())
  })

  it('asks nothing for a caller who may read no account, and for one who may read only its own', async () => {
    const as = (...permissions: string[]) => setHost({ api: { get: coreGet }, permissions: { has: (key: string) => permissions.includes(key) } } as unknown as PluginHost)
    as('plugin.moderation:moderation:manage')
    expect((await fetchAccountNames([1, 2])).size).toBe(0)
    expect(await fetchAccountGroups(1)).toBeNull()
    expect(coreGet).not.toHaveBeenCalled()
    as('account:own:read')
    coreGet.mockResolvedValue({ id: 1, group_ids: [2], items: [{ id: 1, name: 'mine' }] })
    expect((await fetchAccountNames([1, 2])).get(1)).toBe('mine')
    expect(await fetchAccountGroups(1)).toEqual([2])
  })
})

describe('the user lists of the settings', () => {
  const LISTS = ['allowlist_user_ids', 'blocklist_user_ids'] as const

  it('are picked with the entity-select widget and stored as lists of id strings', () => {
    for (const k of LISTS) {
      expect([ui[k]['ui:widget'], ui[k]['ui:options']]).toEqual(['entity-select', { kind: 'user' }])
      expect([schema.properties[k].type, schema.properties[k].items.type, schema.properties[k].maxItems]).toEqual(['array', 'string', 1000])
    }
  })

  it("render the console's widget and save what it picks, with the retired list kept in line", async () => {
    stored = { mode: 'observe', allowlist_user_ids: ['8'], blocklist_user_ids: [] }
    const w = mount(SettingsTab, { attachTo: document.body, props: { page: 'lists' }, global: { provide: { [SCHEMA_WIDGETS_KEY as symbol]: { 'entity-select': EntitySelect } } } })
    await flushPromises()
    const [allow, block] = w.findAllComponents(EntitySelect)
    expect([allow.props('modelValue'), allow.props('multiple')]).toEqual([['8'], true])
    expect([block.props('modelValue'), block.props('multiple')]).toEqual([[], true])

    allow.vm.$emit('update:modelValue', ['8', '9'])
    block.vm.$emit('update:modelValue', ['9'])
    await flushPromises()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(corePut).toHaveBeenCalledTimes(1)
    const sent = (corePut.mock.calls[0][1] as { values: Record<string, unknown> }).values
    expect(sent).toEqual({ mode: 'observe', allowlist_user_ids: ['8', '9'], blocklist_user_ids: ['9'], exempt_user_ids: ['8'] })
  })

  it('refuse more than 1000 users before anything is sent', async () => {
    stored = { mode: 'observe' }
    const w = mount(SettingsTab, { attachTo: document.body, props: { page: 'lists' }, global: { provide: { [SCHEMA_WIDGETS_KEY as symbol]: { 'entity-select': EntitySelect } } } })
    await flushPromises()
    w.findAllComponents(EntitySelect)[0].vm.$emit('update:modelValue', Array.from({ length: 1001 }, (_, i) => String(i + 1)))
    await flushPromises()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(corePut).not.toHaveBeenCalled()
  })
})
