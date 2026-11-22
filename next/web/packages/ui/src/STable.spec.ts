import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import STable from './STable.vue'
import { TABLE_COLUMN_SCOPE_KEY, useTableColumns, type TableColumnState, type TableColumnStorage } from './tableColumns'
import type { TableColumn } from './types'

enableAutoUnmount(afterEach)
afterEach(() => {
  document.body.innerHTML = ''
})

describe('STable', () => {
  const columns = [{ key: 'name', label: 'Name', ellipsis: '8rem' }]
  const rows = [{ id: 1, name: 'one' }, { id: 2, name: 'two' }, { id: 3, name: 'three' }]
  it('selects rows with a select-all header', async () => {
    const w = mount(STable, { props: { columns, rows, selectable: true, selected: [2], rowSelectable: (r: any) => r.id !== 3 } })
    const boxes = w.findAll('input[type="checkbox"]')
    expect((boxes[0].element as HTMLInputElement).indeterminate).toBe(true)
    expect(boxes[3].attributes('disabled')).toBeDefined()
    await boxes[0].trigger('click')
    expect(w.emitted('update:selected')).toEqual([[[1, 2]]])
    await boxes[1].trigger('click')
    expect(w.emitted('update:selected')?.[1]).toEqual([[2, 1]])
  })

  it('emits row-click on Enter for clickable rows and applies rowAttrs / ellipsis titles', async () => {
    const w = mount(STable, { props: { columns, rows, clickable: true, rowAttrs: (r: any) => ({ 'data-testid': `row-${r.id}` }) } })
    const tr = w.get('[data-testid="row-2"]')
    expect(tr.attributes('tabindex')).toBe('0')
    expect(tr.get('td').attributes('title')).toBe('two')
    await tr.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('row-click')?.[0]).toEqual([rows[1]])
  })

  it('lets a grow column take the remaining width on one line, titled with its value', () => {
    const w = mount(STable, { props: { columns: [{ key: 'id', label: 'ID' }, { key: 'name', label: 'Name', grow: true }], rows } })
    expect(w.findAll('th')[1].classes()).toContain('s-table-grow')
    const [id, name] = w.findAll('tbody tr')[0].findAll('td')
    expect(name.classes()).toContain('s-table-grow')
    expect(name.attributes('title')).toBe('one')
    expect(id.classes()).not.toContain('s-table-grow')
  })

  it('renders inside a card with toolbar and header slots', () => {
    const w = mount(STable, { props: { columns, rows, card: true }, slots: { toolbar: '<span class="tb">T</span>', 'head-name': '<b class="hn">N</b>' } })
    expect(w.classes()).toContain('card')
    expect(w.find('.tb').exists()).toBe(true)
    expect(w.find('th .hn').exists()).toBe(true)
  })

  it('labels the expand toggle', async () => {
    const w = mount(STable, { props: { columns, rows, expandable: true } })
    const b = w.get('tbody button')
    expect(b.attributes('aria-expanded')).toBe('false')
    await b.trigger('click')
    expect(b.attributes('aria-expanded')).toBe('true')
  })

  it('lets a cell open its row, without the chevron column and one row at a time when asked', async () => {
    const slots = { 'cell-name': '<button class="open" @click="params.toggleExpand()">{{ params.expanded }}</button>', expand: '<i class="more">{{ params.row.id }}</i>' }
    const w = mount(STable, { props: { columns, rows, expandable: true, expandToggle: false, expandSingle: true }, slots })
    expect(w.findAll('th')).toHaveLength(columns.length)
    expect(w.find('button[aria-expanded]').exists()).toBe(false)
    const open = w.findAll('.open')
    await open[0].trigger('click')
    await open[1].trigger('click')
    expect(open.map(b => b.text())).toEqual(['false', 'true', 'false'])
    expect(w.findAll('.more').map(e => e.text())).toEqual(['2'])
    expect(w.get('.s-table-expand td').attributes('colspan')).toBe(String(columns.length))
    // The row expandSingle closes says so before the one that opens.
    expect(w.emitted('expand')).toEqual([[rows[0], true], [rows[0], false], [rows[1], true]])
    await w.setProps({ expandSingle: false })
    await open[0].trigger('click')
    expect(w.findAll('.more').map(e => e.text())).toEqual(['1', '2'])
    await open[0].trigger('click')
    expect(w.findAll('.more').map(e => e.text())).toEqual(['2'])
  })

  it('gives cell slots a closed row and a toggle that does nothing when rows do not expand', async () => {
    const slots = { 'cell-name': '<button class="open" @click="params.toggleExpand()">{{ params.expanded }}</button>' }
    const w = mount(STable, { props: { columns, rows }, slots })
    await w.findAll('.open')[0].trigger('click')
    expect(w.findAll('.open').map(e => e.text())).toEqual(['false', 'false', 'false'])
    expect(w.emitted('expand')).toBeUndefined()
    // Made expandable later, the row clicked before is still closed.
    await w.setProps({ expandable: true })
    expect(w.findAll('.open').map(e => e.text())).toEqual(['false', 'false', 'false'])
  })

  it('renders an IP column from configuration for any row schema and keeps actions off the row', async () => {
    const column: TableColumn = { key: 'network.remote', label: 'Remote address', format: 'ip', filterable: true }
    const row = { id: 1, network: { remote: '2001:db8::7' } }
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const w = mount(STable, { props: { columns: [column], rows: [row], clickable: true } })
    await w.get('[data-testid="ip-address-filter"]').trigger('click')
    expect(w.emitted('filter')).toEqual([[column, '2001:db8::7', row]])
    expect(w.emitted('row-click')).toBeUndefined()
    await w.get('button[aria-label="Copy"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('2001:db8::7')
    expect(w.emitted('filter')).toHaveLength(1)
    expect(w.emitted('row-click')).toBeUndefined()
    const custom = mount(STable, { props: { columns: [column], rows: [row] }, slots: { 'cell-network.remote': '<b>Custom</b>' } })
    expect(custom.get('td').text()).toBe('Custom')
    expect(custom.find('button').exists()).toBe(false)
  })
})

describe('table column preferences', () => {
  const columns: TableColumn[] = [
    { key: 'name', label: 'Name', hideable: false },
    { key: 'ip', label: 'Client IP' },
    { key: 'cost', label: 'Cost', defaultHidden: true }
  ]
  const menuItem = (label: string) => Array.from(document.querySelectorAll<HTMLButtonElement>('[role^="menuitem"]'))
    .find(item => item.textContent?.trim() === label)!

  it('selects columns, preserves cell slots and colspan, persists, and resets defaults', async () => {
    localStorage.clear()
    const props = { columns, rows: [{ id: 1, name: 'One', ip: '192.0.2.1' }], columnSettingsKey: 'plugin.example.requests', expandable: true, selectable: true }
    const w = mount(STable, { props, slots: { 'cell-ip': '<b class="ip-cell">Address</b>', expand: '<p>Details</p>' }, attachTo: document.body })
    expect(w.find('.ip-cell').exists()).toBe(true)
    expect(w.findAll('th')).toHaveLength(4)
    await w.get('[data-testid="table-column-settings"]').trigger('click')
    expect(menuItem('Name').disabled).toBe(true)
    menuItem('Client IP').click()
    await nextTick()
    expect(w.find('.ip-cell').exists()).toBe(false)
    expect(menuItem('Client IP').getAttribute('aria-checked')).toBe('false')
    expect(document.querySelector('[role="menu"]')).not.toBeNull()
    menuItem('Client IP').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    expect(document.activeElement).toBe(menuItem('Cost'))
    menuItem('Cost').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    expect(document.activeElement).toBe(w.get('[data-testid="table-column-settings"]').element)
    await w.get('button[aria-label="Expand row"]').trigger('click')
    expect(w.get('.s-table-expand td').attributes('colspan')).toBe('3')
    w.unmount()

    const restored = mount(STable, { props, attachTo: document.body })
    expect(restored.findAll('th')).toHaveLength(3)
    await restored.get('[data-testid="table-column-settings"]').trigger('click')
    menuItem('Restore defaults').click()
    await nextTick()
    expect(restored.findAll('th')).toHaveLength(4)
    expect(menuItem('Cost').getAttribute('aria-checked')).toBe('false')
  })

  it('isolates users and table keys and respects a changing permitted schema', async () => {
    localStorage.clear()
    const scope = ref<string | number | null>(1)
    const key = ref('core.usage.all')
    const schema = ref(columns)
    let state!: TableColumnState
    mount(defineComponent({
      setup() {
        state = useTableColumns(schema, key)
        return () => h('div', state.visibleColumns.value.map(column => column.key).join(','))
      }
    }), { global: { provide: { [TABLE_COLUMN_SCOPE_KEY as symbol]: scope } } })
    state.setVisible('ip', false)
    state.setVisible('cost', true)
    scope.value = 2
    await nextTick()
    expect(state.isVisible('ip')).toBe(true)
    expect(state.isVisible('cost')).toBe(false)
    scope.value = 1
    await nextTick()
    expect(state.isVisible('ip')).toBe(false)
    key.value = 'core.usage.self'
    await nextTick()
    expect(state.isVisible('ip')).toBe(true)
    key.value = 'core.usage.all'
    schema.value = [{ key: 'name', label: 'Translated name', hideable: false }, { key: 'new', label: 'New column' }]
    await nextTick()
    expect(state.visibleColumns.value.map(column => column.key)).toEqual(['name', 'new'])
    expect(state.isVisible('cost')).toBe(false) // stored preferences cannot add a column
    schema.value = columns
    await nextTick()
    expect(state.isVisible('cost')).toBe(true)
  })

  it('handles malformed or unavailable storage and never hides the last visible column', async () => {
    localStorage.clear()
    let state!: TableColumnState
    const mountState = () => mount(defineComponent({
      setup() {
        state = useTableColumns([{ key: 'name', label: 'Name' }, { key: 'ip', label: 'IP' }], 'test')
        return () => h('div')
      }
    }))
    const w = mountState()
    state.setVisible('ip', false)
    state.setVisible('name', false)
    expect(state.visibleColumns.value.map(column => column.key)).toEqual(['name'])
    const storageKey = localStorage.key(0)!
    w.unmount()
    localStorage.setItem(storageKey, 'broken json')
    const reset = mountState()
    expect(state.visibleColumns.value).toHaveLength(2)
    localStorage.setItem(storageKey, '{"test":{"name":false,"ip":false,"unknown":true}}')
    window.dispatchEvent(new StorageEvent('storage', { key: storageKey }))
    expect(state.visibleColumns.value.map(column => column.key)).toEqual(['name'])
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('blocked') },
      setItem: () => { throw new Error('blocked') },
      removeItem: () => { throw new Error('blocked') }
    })
    try {
      state.setVisible('ip', true)
      expect(state.isVisible('ip')).toBe(true)
      reset.unmount()
      mountState()
      expect(state.visibleColumns.value).toHaveLength(2)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('keeps existing tables unchanged until column settings are enabled', () => {
    const w = mount(STable, { props: { columns, rows: [] } })
    expect(w.findAll('th')).toHaveLength(3)
    expect(w.find('[data-testid="table-column-settings"]').exists()).toBe(false)
  })

  it('loads an injected backend in STable and offers retry after a load failure', async () => {
    const storage: TableColumnStorage = {
      getItem: vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue('{"remote":{"ip":false}}'),
      setItem: vi.fn(),
      removeItem: vi.fn()
    }
    const w = mount(STable, { props: { columns, rows: [], columnSettingsKey: 'remote', columnStorage: storage }, attachTo: document.body })
    await flushPromises()
    await w.get('[data-testid="table-column-settings"]').trigger('click')
    expect(document.querySelector('[role="alert"]')?.textContent).toContain('could not be synced')
    expect(menuItem('Client IP').disabled).toBe(true)
    menuItem('Retry').click()
    await flushPromises()
    expect(document.querySelector('[role="alert"]')).toBeNull()
    expect(w.findAll('th')).toHaveLength(1)
  })

  it('serializes asynchronous saves, retries failures, and resets through the caller adapter', async () => {
    const releases: Array<() => void> = []
    const setItem = vi.fn(() => new Promise<void>(resolve => releases.push(resolve)))
    const storage: TableColumnStorage = { getItem: vi.fn(async () => null), setItem, removeItem: vi.fn(async () => {}) }
    let state!: TableColumnState
    mount(defineComponent({
      setup() {
        state = useTableColumns(columns, 'remote', storage)
        return () => h('div')
      }
    }))
    await flushPromises()
    const first = state.setVisible('ip', false)
    const second = state.setVisible('ip', true)
    await flushPromises()
    expect(setItem).toHaveBeenCalledTimes(1)
    expect(state.saving.value).toBe(true)
    releases[0]()
    await first
    await flushPromises()
    expect(setItem).toHaveBeenCalledTimes(2)
    releases[1]()
    await second
    const calls = vi.mocked(storage.setItem).mock.calls
    expect(calls.map(([, value]) => JSON.parse(value))).toEqual([{ remote: { ip: false } }, { remote: { ip: true } }])
    expect(state.saving.value).toBe(false)

    setItem.mockRejectedValueOnce(new Error('save failed'))
    await state.setVisible('ip', false)
    expect(state.isVisible('ip')).toBe(false)
    expect(state.error.value).toBeInstanceOf(Error)
    setItem.mockResolvedValueOnce(undefined)
    await state.retry()
    expect(state.error.value).toBeNull()
    await state.reset()
    expect(storage.removeItem).toHaveBeenCalledWith('s2a.table-columns.v1:standalone')
    expect(state.isVisible('ip')).toBe(true)
  })

  it('discards stale loads and queued writes when the user changes', async () => {
    const scope = ref(1)
    const reads: Array<(raw: string | null) => void> = []
    let releaseWrite!: () => void
    const storage: TableColumnStorage = {
      getItem: vi.fn(() => new Promise<string | null>(resolve => reads.push(resolve))),
      setItem: vi.fn(() => new Promise<void>(resolve => { releaseWrite = resolve })),
      removeItem: vi.fn()
    }
    let state!: TableColumnState
    mount(defineComponent({
      setup() {
        state = useTableColumns(columns, 'remote', storage)
        return () => h('div')
      }
    }), { global: { provide: { [TABLE_COLUMN_SCOPE_KEY as symbol]: scope } } })
    await flushPromises()
    scope.value = 2
    await flushPromises()
    reads[1]('{"remote":{"ip":false}}')
    await flushPromises()
    reads[0]('{"remote":{"ip":true}}')
    await flushPromises()
    expect(state.isVisible('ip')).toBe(false)
    state.setVisible('ip', true)
    state.setVisible('cost', true)
    await flushPromises()
    reads[2]('{"remote":{"ip":false}}')
    await flushPromises()
    expect(storage.setItem).toHaveBeenCalledTimes(1)
    scope.value = 3
    releaseWrite()
    await flushPromises()
    expect(storage.setItem).toHaveBeenCalledTimes(1) // the queued second write belongs to user 2
    reads[3](null)
    await flushPromises()
    expect(state.isVisible('cost')).toBe(false)
    expect(state.isVisible('ip')).toBe(true)
  })

  it('stores table names in one local document and resets only the selected table', () => {
    localStorage.clear()
    let usage!: TableColumnState
    let accounts!: TableColumnState
    mount(defineComponent({
      setup() {
        usage = useTableColumns(columns, 'usage-log')
        accounts = useTableColumns(columns, 'account-list')
        return () => h('div')
      }
    }))
    usage.setVisible('ip', false)
    accounts.setVisible('cost', true)
    expect(localStorage.length).toBe(1)
    const key = localStorage.key(0)!
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual({
      'usage-log': { ip: false },
      'account-list': { cost: true }
    })
    usage.reset()
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual({ 'account-list': { cost: true } })
    accounts.reset()
    expect(localStorage.getItem(key)).toBeNull()
  })

  it('merges writes from separate tables sharing a remote document', async () => {
    let saved: string | null = null
    let release!: () => void
    const storage: TableColumnStorage = {
      getItem: vi.fn(async () => saved),
      setItem: vi.fn(async (_key, value) => {
        if (saved === null) await new Promise<void>(resolve => { release = resolve })
        saved = value
      }),
      removeItem: vi.fn(async () => { saved = null })
    }
    let usage!: TableColumnState
    let accounts!: TableColumnState
    const table = (name: string, expose: (state: TableColumnState) => void) => defineComponent({
      setup() {
        expose(useTableColumns(columns, name, storage))
        return () => h('div')
      }
    })
    mount(table('usage-log', state => { usage = state }))
    mount(table('account-list', state => { accounts = state }))
    await flushPromises()
    const first = usage.setVisible('ip', false)
    const second = accounts.setVisible('cost', true)
    await flushPromises()
    expect(storage.setItem).toHaveBeenCalledTimes(1)
    release()
    await Promise.all([first, second])
    expect(JSON.parse(saved!)).toEqual({
      'usage-log': { ip: false },
      'account-list': { cost: true }
    })
    await usage.reset()
    expect(JSON.parse(saved!)).toEqual({ 'account-list': { cost: true } })
    expect(storage.removeItem).not.toHaveBeenCalled()
    await accounts.reset()
    expect(saved).toBeNull()
    expect(storage.removeItem).toHaveBeenCalledTimes(1)
  })
})
