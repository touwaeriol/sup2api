// Mock of the plugin stores and their channels (CONTRACTS §136): a store is a
// GitHub repository, a channel one of its branches, whose index CI publishes
// to index-<branch>. Seeded with the official store (正式/main default and
// 测试/develop) and a third-party team store (稳定/main). The official store
// cannot be deleted; each store has one default channel, which cannot be
// disabled or deleted; deleting a store deletes its channels.
import { fail, noContent, on } from './router'
import { invalid } from './common'
import type { Any } from './plugins'

const BRANCH = /^[A-Za-z0-9._-]{1,64}$/
const REPO = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9._-]{1,100}$/

interface Store {
  id: number
  name: string
  repo: string
  official: boolean
}

interface Channel {
  id: number
  store_id: number
  name: string
  branch: string
  enabled: boolean
  is_default: boolean
}

let storeSeq = 2
let channelSeq = 3
const stores: Store[] = [
  { id: 1, name: '官方商店', repo: 'touwaeriol/sup2api-plugin', official: true },
  { id: 2, name: '团队内部商店', repo: 'ourteam/s2-plugins', official: false }
]
const channels: Channel[] = [
  { id: 1, store_id: 1, name: '正式', branch: 'main', enabled: true, is_default: true },
  { id: 2, store_id: 1, name: '测试', branch: 'develop', enabled: true, is_default: false },
  { id: 3, store_id: 2, name: '稳定', branch: 'main', enabled: true, is_default: true }
]

const storeOf = (c: Channel) => stores.find(s => s.id === c.store_id)!
const view = (c: Channel) => ({ ...c, url: `https://raw.githubusercontent.com/${storeOf(c).repo}/index-${c.branch}/index.json` })

/** The channel a listing, review or install names (the official store's default when it names none), with its store. */
export function channelOf(sourceId: unknown): (Channel & { store: Store }) | undefined {
  const id = Number(sourceId)
  const c = id ? channels.find(x => x.id === id) : channels.find(x => x.store_id === 1 && x.is_default)
  return c && { ...c, store: storeOf(c) }
}

const badBranch = (branch: string) => !BRANCH.test(branch) || branch.includes('..')

function addChannel(store: Store, body: Any, isDefault = false): Any {
  const name = String(body?.name ?? '').trim()
  const branch = String(body?.branch ?? '').trim()
  if (!name) return invalid('name', 'name is required', 'required')
  if (badBranch(branch)) return invalid('branch', 'not a branch name')
  if (channels.some(c => c.store_id === store.id && c.branch === branch)) return fail(409, 'conflict', `a channel already reads branch ${branch}`)
  const c = { id: ++channelSeq, store_id: store.id, name, branch, enabled: true, is_default: isDefault }
  channels.push(c)
  return view(c)
}

function addStore(body: Any): Any {
  const name = String(body?.name ?? '').trim()
  const repo = String(body?.repo ?? '').trim()
  const branch = String(body?.branch ?? '').trim()
  if (!name) return invalid('name', 'name is required', 'required')
  if (!REPO.test(repo)) return invalid('repo', 'not a GitHub repository (owner/name)')
  if (badBranch(branch)) return invalid('branch', 'not a branch name')
  if (stores.some(s => s.repo.toLowerCase() === repo.toLowerCase())) return fail(409, 'conflict', `a store already reads ${repo}`)
  const s = { id: ++storeSeq, name, repo, official: false }
  stores.push(s)
  addChannel(s, { name: '默认', branch }, true)
  return s
}

function updateChannel(c: Channel, body: Any): Any {
  if (body?.enabled === false && c.is_default) return fail(409, 'conflict', 'the default channel cannot be disabled')
  if (body?.is_default === true && !(body?.enabled ?? c.enabled)) return fail(409, 'conflict', 'a disabled channel cannot be the default')
  if (typeof body?.name === 'string' && body.name.trim()) c.name = body.name.trim()
  if (typeof body?.enabled === 'boolean') c.enabled = body.enabled
  if (body?.is_default === true) channels.filter(x => x.store_id === c.store_id).forEach(x => (x.is_default = x === c))
  return view(c)
}

const found = <T extends { id: number }>(list: T[], what: string, id: string, run: (x: T) => Any) => {
  const x = list.find(v => v.id === Number(id))
  return x ? run(x) : fail(404, 'not_found', `${what} ${id} not found`)
}

on('GET', '/market/stores', () => stores)
on('POST', '/market/stores', req => addStore(req.body))
on('PATCH', '/market/stores/:id', req =>
  found(stores, 'store', req.params.id, s => {
    if (typeof req.body?.name === 'string' && req.body.name.trim()) s.name = req.body.name.trim()
    return s
  })
)
on('DELETE', '/market/stores/:id', req =>
  found(stores, 'store', req.params.id, s => {
    if (s.official) return fail(409, 'conflict', 'the official store cannot be deleted')
    stores.splice(stores.indexOf(s), 1)
    for (let i = channels.length - 1; i >= 0; i--) if (channels[i].store_id === s.id) channels.splice(i, 1)
    return noContent()
  })
)

on('GET', '/market/sources', req => channels.filter(c => !req.query.store_id || c.store_id === Number(req.query.store_id)).map(view))
on('POST', '/market/stores/:id/sources', req => found(stores, 'store', req.params.id, s => addChannel(s, req.body)))
on('PATCH', '/market/sources/:id', req => found(channels, 'channel', req.params.id, c => updateChannel(c, req.body)))
on('DELETE', '/market/sources/:id', req =>
  found(channels, 'channel', req.params.id, c => {
    if (c.is_default) return fail(409, 'conflict', 'the default channel cannot be deleted')
    channels.splice(channels.indexOf(c), 1)
    return noContent()
  })
)
