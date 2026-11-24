// Mock of the plugin stores and their channels (CONTRACTS §137.6), in the
// shapes of plugin/market: a store is a GitHub repository, a channel one of
// its branches, whose index CI publishes to index-<branch>. Seeded with the
// official store (正式/main default and 测试/develop) and a third-party team
// store (正式/main). The official store cannot be deleted; a new store gets
// the default channel 正式 on the branch it names; each store has one default
// channel, which stays enabled and the default until another takes its place
// and cannot be deleted; deleting a store deletes its channels.
import { fail, noContent, now, on } from './router'
import { invalid } from './common'
import type { Any } from './plugins'

const BRANCH = /^[A-Za-z0-9._-]{1,64}$/
const REPO = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9._-]{1,100}$/
const MAX_NAME = 100
const DEFAULT_CHANNEL = '正式'

interface Store {
  id: number
  name: string
  repo: string
  official: boolean
  created_at: string
}

interface Channel {
  id: number
  store_id: number
  name: string
  branch: string
  enabled: boolean
  is_default: boolean
  created_at: string
}

let storeSeq = 2
let channelSeq = 3
const seeded = '2026-11-24T06:00:00Z'
const stores: Store[] = [
  { id: 1, name: '官方商店', repo: 'touwaeriol/sup2api-plugin', official: true, created_at: seeded },
  { id: 2, name: '团队内部商店', repo: 'ourteam/s2-plugins', official: false, created_at: seeded }
]
const channels: Channel[] = [
  { id: 1, store_id: 1, name: DEFAULT_CHANNEL, branch: 'main', enabled: true, is_default: true, created_at: seeded },
  { id: 2, store_id: 1, name: '测试', branch: 'develop', enabled: true, is_default: false, created_at: seeded },
  { id: 3, store_id: 2, name: DEFAULT_CHANNEL, branch: 'main', enabled: true, is_default: true, created_at: seeded }
]

const storeOf = (c: Channel) => stores.find(s => s.id === c.store_id)!
const view = (c: Channel) => ({ ...c, store_name: storeOf(c).name, url: `https://raw.githubusercontent.com/${storeOf(c).repo}/index-${c.branch}/index.json` })
const created = (body: unknown) => ({ __status: 201, body })
/** GET /market/sources: the official store first, each store's default channel first. */
const listed = (list: Channel[]) =>
  [...list].sort((a, b) => Number(storeOf(b).official) - Number(storeOf(a).official) || a.store_id - b.store_id || Number(b.is_default) - Number(a.is_default) || a.id - b.id)

/** The channel a listing, review or install names (the official store's default when it names none), with its store. */
export function channelOf(sourceId: unknown): (Channel & { store: Store }) | undefined {
  const id = Number(sourceId)
  const c = id ? channels.find(x => x.id === id) : channels.find(x => x.store_id === 1 && x.is_default)
  return c && { ...c, store: storeOf(c) }
}

/** market.validBranch: one git ref name segment. */
const badBranch = (branch: string) => !BRANCH.test(branch) || branch.includes('..') || branch.startsWith('.') || branch.endsWith('.lock')
/** market.validRepo: owner/name on GitHub. */
const badRepo = (repo: string) => {
  const name = repo.split('/')[1] ?? ''
  return !REPO.test(repo) || name === '.' || name === '..' || name.endsWith('.git')
}
const badName = (name: string) => !name || [...name].length > MAX_NAME
const nameInvalid = () => invalid('name', `name must be 1-${MAX_NAME} characters`)
const branchInvalid = () => invalid('branch', 'branch must match ^[A-Za-z0-9._-]{1,64}$ without ".."', 'invalid_format')
const channelTaken = () => fail(409, 'conflict', 'another channel of the store has this name or branch')

function addChannel(store: Store, body: Any, isDefault = false): Any {
  const name = String(body?.name ?? '').trim()
  const branch = String(body?.branch ?? '')
  if (badName(name)) return nameInvalid()
  if (badBranch(branch)) return branchInvalid()
  if (channels.some(c => c.store_id === store.id && (c.branch === branch || c.name === name))) return channelTaken()
  const c = { id: ++channelSeq, store_id: store.id, name, branch, enabled: true, is_default: isDefault, created_at: now() }
  channels.push(c)
  return created(view(c))
}

function addStore(body: Any): Any {
  const name = String(body?.name ?? '').trim()
  const repo = String(body?.repo ?? '').trim()
  const branch = String(body?.branch ?? '')
  if (badName(name)) return nameInvalid()
  if (badRepo(repo)) return invalid('repo', 'repo must be owner/name of a GitHub repository', 'invalid_format')
  if (badBranch(branch)) return branchInvalid()
  if (stores.some(s => s.repo === repo)) return fail(409, 'conflict', 'another store has this repository')
  const s = { id: ++storeSeq, name, repo, official: false, created_at: now() }
  stores.push(s)
  addChannel(s, { name: DEFAULT_CHANNEL, branch }, true)
  return created(s)
}

/** market.ChannelPatch.check: the store keeps an enabled default channel. */
function patchRefusal(c: Channel, body: Any): Any | null {
  const enabled = typeof body?.enabled === 'boolean' ? body.enabled : c.enabled
  if (body?.is_default === false && c.is_default) return fail(409, 'conflict', 'the default channel stays the default until another channel is made the default')
  if ((c.is_default || body?.is_default === true) && !enabled) return fail(409, 'conflict', 'the default channel must be enabled')
  return null
}

function updateChannel(c: Channel, body: Any): Any {
  const name = typeof body?.name === 'string' ? body.name.trim() : undefined
  if (name === undefined && typeof body?.enabled !== 'boolean' && typeof body?.is_default !== 'boolean') {
    return fail(400, 'invalid_argument', 'name, enabled or is_default is required')
  }
  if (name !== undefined && badName(name)) return nameInvalid()
  if (name && channels.some(x => x !== c && x.store_id === c.store_id && x.name === name)) return channelTaken()
  const refused = patchRefusal(c, body)
  if (refused) return refused
  if (name) c.name = name
  if (typeof body?.enabled === 'boolean') c.enabled = body.enabled
  if (body?.is_default === true) channels.filter(x => x.store_id === c.store_id).forEach(x => (x.is_default = x === c))
  return view(c)
}

const found = <T extends { id: number }>(list: T[], what: string, id: string, run: (x: T) => Any) => {
  const x = list.find(v => v.id === Number(id))
  return x ? run(x) : fail(404, 'not_found', `${what} not found`)
}

function renameStore(s: Store, body: Any): Any {
  if (typeof body?.name !== 'string') return fail(400, 'invalid_argument', 'name is required')
  const name = body.name.trim()
  if (badName(name)) return nameInvalid()
  s.name = name
  return s
}

// The official store first (plugin/market ListStores).
on('GET', '/market/stores', () => [...stores].sort((a, b) => Number(b.official) - Number(a.official) || a.id - b.id))
on('POST', '/market/stores', req => addStore(req.body))
on('PATCH', '/market/stores/:id', req => found(stores, 'plugin store', req.params.id, s => renameStore(s, req.body)))
on('DELETE', '/market/stores/:id', req =>
  found(stores, 'plugin store', req.params.id, s => {
    if (s.official) return fail(409, 'conflict', 'the official store cannot be deleted')
    stores.splice(stores.indexOf(s), 1)
    for (let i = channels.length - 1; i >= 0; i--) if (channels[i].store_id === s.id) channels.splice(i, 1)
    return noContent()
  })
)

on('GET', '/market/sources', req => listed(channels.filter(c => !req.query.store_id || c.store_id === Number(req.query.store_id))).map(view))
on('POST', '/market/stores/:id/sources', req => found(stores, 'plugin store', req.params.id, s => addChannel(s, req.body)))
on('PATCH', '/market/sources/:id', req => found(channels, 'market channel', req.params.id, c => updateChannel(c, req.body)))
on('DELETE', '/market/sources/:id', req =>
  found(channels, 'market channel', req.params.id, c => {
    if (c.is_default) return fail(409, 'conflict', 'the default channel cannot be deleted; make another channel the default first')
    channels.splice(channels.indexOf(c), 1)
    return noContent()
  })
)
