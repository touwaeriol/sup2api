// The asset library pages: list, search, pages, and the create dialogs over
// the plugin routes (a fake pluginApi records the calls).
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { ApiError, provideHost, type HostContext, type PluginHost } from '@sub2api/host'
import { toastState } from '@sub2api/ui'
import { AssetGroupsPage, AssetsPage } from './pages'
import messages from './messages'
import { setHost } from './host'

type M = Record<string, any>
const en = (key: string) => key.split('.').reduce<any>((o, k) => o?.[k], messages.en) ?? key

let lists: Array<{ path: string; query: M }>
let posts: Array<{ path: string; body: M }>
let listAnswer: (path: string, query: M) => unknown
let postAnswer: (path: string, body: M) => unknown

function fakeHost(granted: string[]) {
  const base = {
    permissions: { superuser: () => false, has: () => false, any: () => false },
    i18n: { t: (k: string) => k, locale: ref('en'), formatDateTime: (v: unknown) => `T(${v})`, formatNumber: (v: unknown) => `N(${v})` }
  } as unknown as HostContext
  provideHost(base)
  const pluginApi = {
    list: async (path: string, query: M) => {
      lists.push({ path, query })
      const r = listAnswer(path, query)
      if (r instanceof Error) throw r
      return r
    },
    post: async (path: string, body: M) => {
      posts.push({ path, body })
      const r = postAnswer(path, body)
      if (r instanceof Error) throw r
      return r
    }
  }
  setHost({ ...base, t: en, pluginApi, can: (p: string) => granted.includes(p) } as unknown as PluginHost)
}

const GROUP_ROW = { id: 7, account_id: 3, account_name: 'ark-main', name: 'logos', title: 'Logos', group_type: 'AIGC', asset_count: 12, index_status: 'ok', upstream_id: 'group-1', updated_at: '2026-11-19T10:00:00Z' }
const ASSET_ROW = { id: 9, account_name: 'ark-main', group_name: 'logos', name: 'cat', asset_type: 'Image', status: 'Active', index_status: 'missing', upstream_id: 'asset-1', updated_at: '' }

beforeEach(() => {
  lists = []
  posts = []
  toastState.items.splice(0)
  listAnswer = (path, q) => ({ items: [path === '/assets' ? ASSET_ROW : GROUP_ROW], page: { page: q.page, page_size: q.page_size, total: 45 } })
  postAnswer = () => ({ id: 8 })
  fakeHost(['assets:manage'])
})
const mounted: VueWrapper[] = []
afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount())
  document.body.innerHTML = ''
  setHost(null)
})

async function page(component: object) {
  const w = mount(component as never, { attachTo: document.body })
  mounted.push(w)
  await flushPromises()
  return w
}
const dialog = () => document.querySelector('[role="dialog"]')
const button = (text: string) => [...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === text) as HTMLButtonElement | undefined
/** The dialog's control a label names. */
function field(label: string) {
  const l = [...dialog()!.querySelectorAll('label')].find((x) => x.textContent?.replace(/\*$/, '').trim() === label)
  if (!l) throw new Error(`no label ${label}`)
  return document.getElementById(l.getAttribute('for')!) as HTMLInputElement | HTMLTextAreaElement
}
async function fill(label: string, value: string) {
  const el = field(label)
  el.value = value
  el.dispatchEvent(new Event('input'))
  el.dispatchEvent(new Event('blur'))
  await flushPromises()
}
async function save() {
  button('Save')!.click()
  await flushPromises()
}

describe('asset groups page', () => {
  it('lists the index with the columns, formats and title of the old table page', async () => {
    const w = await page(AssetGroupsPage)
    expect(lists).toEqual([{ path: '/asset-groups', query: { page: 1, page_size: 20, q: '' } }])
    expect(w.text()).toContain('Ark asset groups')
    expect(w.findAll('th').map((x) => x.text())).toEqual(['#', 'Account', 'Name', 'Title', 'Type', 'Assets', 'Index', 'Ark id', 'Updated'])
    const cells = w.findAll('tbody td').map((x) => x.text())
    expect(cells).toEqual(['7', 'ark-main', 'logos', 'Logos', 'AIGC', 'N(12)', 'ok', 'group-1', 'T(2026-11-19T10:00:00Z)'])
    expect(w.get('tbody .badge').classes()).toContain('badge-success')
  })

  it('searches with q from the first page, and pages', async () => {
    const w = await page(AssetGroupsPage)
    await w.findAll('button').find((b) => b.text() === '2')!.trigger('click')
    await flushPromises()
    expect(lists.at(-1)!.query).toMatchObject({ page: 2 })
    await w.find('input[type="search"]').setValue('log')
    await new Promise((r) => setTimeout(r, 300))
    await flushPromises()
    expect(lists.at(-1)!.query).toEqual({ page: 1, page_size: 20, q: 'log' })
  })

  it('creates a group: the defaults and typed fields are posted, the dialog closes and page 1 reloads', async () => {
    const w = await page(AssetGroupsPage)
    await w.findAll('button').find((b) => b.text() === '2')!.trigger('click')
    await flushPromises()
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    expect(dialog()!.textContent).toContain('New asset group')
    expect((field('Group type') as HTMLInputElement).value).toBe('AIGC')
    await fill('Account', '3')
    await fill('Name', 'logos')
    await fill('Description', 'brand assets')
    const before = lists.length
    await save()
    expect(posts).toEqual([{ path: '/asset-groups', body: { group_type: 'AIGC', account_id: 3, name: 'logos', description: 'brand assets' } }])
    expect(dialog()).toBeNull()
    expect(lists.length).toBeGreaterThan(before)
    expect(lists.at(-1)!.query).toMatchObject({ page: 1 })
  })

  it('local rules stop the save: required, integer id, lengths', async () => {
    const w = await page(AssetGroupsPage)
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    await save()
    expect(posts).toEqual([])
    expect(dialog()).not.toBeNull()
    expect(dialog()!.textContent).toContain('required')
    await fill('Account', '0')
    await fill('Name', 'n'.repeat(129))
    await fill('Title', 't'.repeat(257))
    await fill('Description', 'd'.repeat(1025))
    await fill('Group type', 'g'.repeat(65))
    await save()
    expect(posts).toEqual([])
    await fill('Account', '3')
    await fill('Name', '   ')
    await fill('Title', 't'.repeat(256))
    await fill('Description', 'd'.repeat(1024))
    await fill('Group type', 'g'.repeat(64))
    await save()
    expect(posts).toEqual([])
    await fill('Name', 'n'.repeat(128))
    await save()
    expect(posts).toHaveLength(1)
  })

  it('the account id alone stops the save: an integer of at least 1', async () => {
    const w = await page(AssetGroupsPage)
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    await fill('Name', 'logos')
    await fill('Account', '0')
    await save()
    expect(posts).toEqual([])
    await fill('Account', '1')
    await save()
    expect(posts).toHaveLength(1)
  })

  it('a refused create keeps the dialog open: field errors at the field, others as a toast', async () => {
    const w = await page(AssetGroupsPage)
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    await fill('Account', '3')
    await fill('Name', 'logos')
    postAnswer = () => new ApiError(400, { code: 'invalid_argument', message: 'invalid request', details: { fields: [{ field: 'name', code: 'max_length', message: 'name must be at most 128 characters' }] } })
    await save()
    expect(dialog()!.textContent).toContain('name must be at most 128 characters')
    expect(toastState.items).toEqual([])
    postAnswer = () => new ApiError(409, { code: 'asset_library_disabled', message: 'account 3 has no access key' })
    await save()
    expect(dialog()).not.toBeNull()
    expect(toastState.items.map((t) => t.message)).toEqual(['account 3 has no access key'])
    expect(posts).toHaveLength(2)
  })

  it('without assets:manage: no create button; a refused list says so', async () => {
    fakeHost([])
    listAnswer = () => new ApiError(403, { code: 'forbidden', message: 'permission denied' })
    const w = await page(AssetGroupsPage)
    expect(w.find('[data-testid="resource-create"]').exists()).toBe(false)
    expect(w.find('[data-testid="table-error"]').exists()).toBe(true)
    expect(toastState.items.map((t) => t.message)).toEqual(['permission denied'])
  })
})

describe('assets page', () => {
  it('lists the index with the columns of the old table page; empty cells show a dash', async () => {
    const w = await page(AssetsPage)
    expect(lists[0]).toEqual({ path: '/assets', query: { page: 1, page_size: 20, q: '' } })
    expect(w.text()).toContain('Ark asset library')
    expect(w.findAll('th').map((x) => x.text())).toEqual(['#', 'Account', 'Group', 'Name', 'Type', 'Upstream status', 'Index', 'Ark id', 'Updated'])
    expect(w.findAll('tbody td').map((x) => x.text())).toEqual(['9', 'ark-main', 'logos', 'cat', 'Image', 'Active', 'missing', 'asset-1', '—'])
    // Upstream status and index are badges: a row the upstream lost is a warning.
    expect(w.findAll('tbody .badge').map((b) => b.classes().find((c) => c.startsWith('badge-')))).toEqual(['badge-success', 'badge-warning'])
  })

  it('creates an asset: group id, url and the optional fields', async () => {
    const w = await page(AssetsPage)
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    expect(dialog()!.textContent).toContain('New asset')
    await fill('URL', 'ftp://cdn.example.com/cat.png')
    await fill('Asset group', '7')
    await save()
    expect(posts).toEqual([])
    await fill('URL', 'u'.repeat(2049))
    await save()
    expect(posts).toEqual([])
    await fill('URL', 'tos://bucket/cat.png')
    await fill('Name', 'n'.repeat(129))
    await save()
    expect(posts).toEqual([])
    await fill('Name', 'cat')
    await fill('Asset type', 'Image')
    await save()
    expect(posts).toEqual([{ path: '/assets', body: { url: 'tos://bucket/cat.png', group_id: 7, name: 'cat', asset_type: 'Image' } }])
    expect(dialog()).toBeNull()
  })

  it('each length rule alone stops the save: url at most 2048, asset type at most 64', async () => {
    const w = await page(AssetsPage)
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    await fill('Asset group', '7')
    await fill('URL', 'https://x/' + 'u'.repeat(2039))
    await save()
    expect(posts).toEqual([])
    await fill('URL', 'https://x/' + 'u'.repeat(2038))
    await fill('Asset type', 'a'.repeat(65))
    await save()
    expect(posts).toEqual([])
    await fill('Asset type', 'a'.repeat(64))
    await save()
    expect(posts).toEqual([{ path: '/assets', body: { group_id: 7, url: 'https://x/' + 'u'.repeat(2038), asset_type: 'a'.repeat(64) } }])
  })

  it('a dialog opened again starts empty', async () => {
    const w = await page(AssetsPage)
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    await fill('Name', 'draft')
    button('Cancel')!.click()
    await flushPromises()
    expect(dialog()).toBeNull()
    await w.get('[data-testid="resource-create"]').trigger('click')
    await flushPromises()
    expect((field('Name') as HTMLInputElement).value).toBe('')
  })
})
