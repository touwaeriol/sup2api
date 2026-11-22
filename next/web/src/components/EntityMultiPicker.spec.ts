import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { ApiError, type Query } from '@sub2api/host'
import { uiMessages } from '@sub2api/ui'
import entity from '@/i18n/locales/en/entity'
import EntityMultiPicker from './EntityMultiPicker.vue'
import EntityPicker from './EntityPicker.vue'
import { resetEntityLookups, type EntityItem } from './entity/lookup'

const mocks = vi.hoisted(() => ({ get: vi.fn(), denied: [] as string[] }))
vi.mock('@sub2api/host', async (original) => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: mocks.get } }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ has: (perm: string[]) => !perm.length || perm.some((p) => !mocks.denied.includes(p)), hasAction: (a: string) => [a, `${a}:own`].some((p) => !mocks.denied.includes(p)) })
}))

enableAutoUnmount(afterEach)
const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { entity, ui: uiMessages.en } } })

const GROUPS: EntityItem[] = [
  { id: 1, name: 'default', status: 'active' },
  { id: 2, name: 'vip', status: 'active' },
  { id: 3, name: 'cc-low', status: 'disabled' }
]
/** The lookup over GROUPS: the given ids, else every group. */
function answer(query: Query = {}) {
  const ids = typeof query.ids === 'string' ? query.ids.split(',') : null
  return { items: GROUPS.filter((g) => !ids || ids.includes(String(g.id))), more: false }
}
const lookups = () =>
  mocks.get.mock.calls.map(([path, query]) => [path, Object.fromEntries(Object.entries(query as Query).filter(([, v]) => v !== undefined))])

/** Mounted as under v-model:ids: what the picker sends comes back as its ids. */
function picker(props: Record<string, unknown> = {}) {
  const w: VueWrapper = mount(EntityMultiPicker, {
    props: { kind: 'group', ...props, 'onUpdate:ids': (ids: string[]) => w.setProps({ ids }) },
    global: { plugins: [i18n] }
  })
  return w
}
async function settle(ms = 250) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}
async function open(w: VueWrapper) {
  await w.get('input').trigger('focus')
  await settle(0)
}
const chips = (w: VueWrapper) => w.findAll('.rounded-full.border').map((c) => c.text())
const rows = (w: VueWrapper) => w.findAll('[role="option"]').map((o) => o.text())
const removeButton = (w: VueWrapper, name: string) => w.get(`button[aria-label="Remove ${name}"]`)

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  resetEntityLookups()
  mocks.denied = []
  mocks.get.mockReset().mockImplementation(async (_path: string, query?: Query) => answer(query))
})
afterEach(() => {
  vi.useRealTimers()
})

describe('EntityMultiPicker', () => {
  it('is only the picker while nothing is picked, named "Add <entity>"', async () => {
    const w = picker()
    expect(chips(w)).toEqual([])
    expect(w.get('input').attributes('placeholder')).toBe('Add Account group')
    expect(w.get('input').attributes('aria-label')).toBe('Add Account group')
    expect(mocks.get).not.toHaveBeenCalled()
    expect(picker({ placeholder: 'Groups of the account' }).get('input').attributes('placeholder')).toBe('Groups of the account')
  })

  it('shows the picked ids as "name #id" chips, the names from one lookup, "#id" for an id it does not know', async () => {
    const w = picker({ ids: ['2', '99', '1'] })
    expect(chips(w)).toEqual(['#2', '#99', '#1'])
    await flushPromises()
    expect(lookups()).toEqual([['/lookup/groups', { ids: '2,99,1' }]])
    expect(chips(w)).toEqual(['vip #2', '#99', 'default #1'])
  })

  it('adds the picked entity after the others and stays empty for the next; change follows update:ids', async () => {
    const w = picker({ ids: ['2'] })
    const order: string[] = []
    await w.setProps({ 'onUpdate:ids': (ids: string[]) => (order.push('update'), w.setProps({ ids })), onChange: () => order.push('change') })
    await open(w)
    await w.findAll('[role="option"]')[0].trigger('click')
    expect(w.emitted('update:ids')).toEqual([[['2', '1']]])
    expect(w.emitted('change')).toEqual([[['2', '1']]])
    expect(order).toEqual(['update', 'change'])
    expect(chips(w)).toEqual(['vip #2', 'default #1'])
    expect((w.get('input').element as HTMLInputElement).value).toBe('')
  })

  it('lists only what is not picked yet', async () => {
    const w = picker({ ids: ['2'] })
    await open(w)
    expect(lookups()).toContainEqual(['/lookup/groups', { limit: 21 }])
    expect(rows(w)).toEqual(['default#1', 'cc-lowDisabled#3'])
    await w.findAll('[role="option"]')[1].trigger('click')
    await open(w)
    expect(rows(w)).toEqual(['default#1'])
  })

  it('does not add an id twice', async () => {
    const w = picker({ ids: ['2'] })
    w.getComponent(EntityPicker).vm.$emit('update:id', '2')
    w.getComponent(EntityPicker).vm.$emit('update:id', '')
    expect(w.emitted('update:ids')).toBeUndefined()
    expect(w.emitted('change')).toBeUndefined()
  })

  it('removes a chip by its ×, named after the entity', async () => {
    const w = picker({ ids: ['2', '1'] })
    await flushPromises()
    await removeButton(w, 'vip #2').trigger('click')
    expect(w.emitted('update:ids')).toEqual([[['1']]])
    expect(w.emitted('change')).toEqual([[['1']]])
    expect(chips(w)).toEqual(['default #1'])
  })

  it('says nothing of a change it did not make', async () => {
    const w = picker({ ids: ['2'] })
    await w.setProps({ ids: ['1', '3'] })
    await flushPromises()
    expect(chips(w)).toEqual(['default #1', 'cc-low #3'])
    expect(w.emitted('update:ids')).toBeUndefined()
    expect(w.emitted('change')).toBeUndefined()
  })

  it('stops adding at max and takes it up again below it', async () => {
    const w = picker({ ids: ['2', '1'], max: 2 })
    await flushPromises()
    expect(w.findComponent(EntityPicker).exists()).toBe(false)
    expect(w.text()).not.toContain('—')
    await removeButton(w, 'vip #2').trigger('click')
    expect(w.findComponent(EntityPicker).exists()).toBe(true)
  })

  it('disabled: the chips cannot be removed and nothing can be added', async () => {
    const w = picker({ ids: ['2'], disabled: true })
    await flushPromises()
    expect(chips(w)).toEqual(['vip #2'])
    expect(w.find('button').exists()).toBe(false)
    expect(w.findComponent(EntityPicker).exists()).toBe(false)
    // With nothing picked a dash stands for the empty value.
    expect(picker({ disabled: true }).text()).toBe('—')
  })

  it('passes the lookup props on and names a chip after a page candidate the lookup does not know', async () => {
    const extra = [{ id: 0, name: 'Default group' }]
    const w = picker({ ids: ['0'], mine: true, scope: { prefer_group_id: 4 }, size: 'sm', extra })
    expect(chips(w)).toEqual(['Default group #0'])
    expect(w.getComponent(EntityPicker).props()).toMatchObject({ kind: 'group', id: '', mine: true, scope: { prefer_group_id: 4 }, size: 'sm', extra, exclude: ['0'] })
    await open(w)
    expect(lookups()).toEqual([['/lookup/groups', { limit: 21, mine: true, prefer_group_id: 4 }]])
  })

  it('a model is picked by its name', async () => {
    mocks.get.mockResolvedValue({ items: [{ id: 'gpt-4o', name: 'gpt-4o' }, { id: 'claude-opus-5', name: 'claude-opus-5' }], more: false })
    const w = picker({ kind: 'model', ids: ['gpt-4o'] })
    expect(chips(w)).toEqual(['gpt-4o'])
    await open(w)
    expect(rows(w)).toEqual(['claude-opus-5'])
    await w.findAll('[role="option"]')[0].trigger('click')
    expect(w.emitted('update:ids')).toEqual([[['gpt-4o', 'claude-opus-5']]])
  })

  it('without the lookup an id typed into the text box is added, and the box is empty again', async () => {
    mocks.denied = ['group:read', 'account:group:bind']
    mocks.get.mockRejectedValue(new ApiError(403, { code: 'permission_denied', message: 'group:read is required' }))
    const w = picker({ ids: ['2'] })
    await flushPromises()
    const box = w.get('input')
    expect(w.find('[role="combobox"]').exists()).toBe(false)
    expect(box.attributes('placeholder')).toBe('Add Account group (Account group ID)')
    await box.setValue('7')
    await flushPromises()
    expect(w.emitted('update:ids')).toEqual([[['2', '7']]])
    expect(chips(w)).toEqual(['#2', '#7'])
    expect((box.element as HTMLInputElement).value).toBe('')
    // Neither a picked id nor text is taken.
    await box.setValue('2')
    await box.setValue('vip')
    await flushPromises()
    expect(w.emitted('update:ids')).toHaveLength(1)
    expect((box.element as HTMLInputElement).value).toBe('')
    // The names were asked for once; the refusal is remembered.
    expect(lookups()).toEqual([['/lookup/groups', { ids: '2' }]])
  })

  it('Enter in the text box adds the typed id instead of submitting the form around; an empty box leaves the key alone', async () => {
    mocks.denied = ['group:read', 'account:group:bind']
    const w = picker()
    const box = w.get('input')
    const press = async (init: KeyboardEventInit = {}) => {
      const key = new KeyboardEvent('keydown', { key: 'Enter', cancelable: true, bubbles: true, ...init })
      box.element.dispatchEvent(key)
      await flushPromises()
      return key.defaultPrevented
    }
    expect(await press()).toBe(false)
    // Typed, not yet taken (the box takes its text on change).
    ;(box.element as HTMLInputElement).value = '7'
    await box.trigger('input')
    expect(w.emitted('update:ids')).toBeUndefined()
    // The Enter that ends an input method's composition is part of the typing.
    expect(await press({ isComposing: true })).toBe(false)
    expect(w.emitted('update:ids')).toBeUndefined()
    expect(await press()).toBe(true)
    expect(w.emitted('update:ids')).toEqual([[['7']]])
    expect((box.element as HTMLInputElement).value).toBe('')
    expect(await press()).toBe(false)
  })

  it('a pasted list adds its ids at once, without the ones picked, repeated or not ids, up to max; one word is left to the picker', async () => {
    const w = picker({ ids: ['2'], max: 4 })
    const paste = async (text: string) => {
      const e = Object.assign(new Event('paste', { cancelable: true, bubbles: true }), { clipboardData: { getData: () => text } })
      w.get('input').element.dispatchEvent(e)
      await flushPromises()
      return e.defaultPrevented
    }
    expect(await paste('7')).toBe(false)
    expect(w.emitted('update:ids')).toBeUndefined()
    expect(await paste('3, 2\n7 abc，007;7\r\n9 11')).toBe(true)
    expect(w.emitted('update:ids')).toEqual([[['2', '3', '7', '9']]])
    expect(w.emitted('change')).toEqual([[['2', '3', '7', '9']]])
    expect(w.find('input').exists()).toBe(false)
  })

  it('a pasted list of model names is taken as it is', async () => {
    const w = picker({ kind: 'model' })
    const e = Object.assign(new Event('paste', { cancelable: true, bubbles: true }), { clipboardData: { getData: () => 'gpt-4o,claude-opus-5' } })
    w.get('input').element.dispatchEvent(e)
    await flushPromises()
    expect(w.emitted('update:ids')).toEqual([[['gpt-4o', 'claude-opus-5']]])
  })

  it('leaves Enter to the select: it picks the highlighted row', async () => {
    const w = picker()
    await open(w)
    const key = new KeyboardEvent('keydown', { key: 'Enter', cancelable: true, bubbles: true })
    w.get('input').element.dispatchEvent(key)
    await flushPromises()
    expect(w.emitted('update:ids')).toEqual([[['1']]])
  })
})
